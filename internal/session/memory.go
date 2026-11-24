package session

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// MemoryStore 在当前进程内保存会话。零值可以使用。
type MemoryStore struct {
	mu       sync.Mutex
	sessions map[SessionKey]*memorySession
}

type memorySession struct {
	turnLock chan struct{}
	state    atomic.Pointer[Session]
}

var _ SessionStore = (*MemoryStore)(nil)

func (s *MemoryStore) Create(ctx context.Context, key SessionKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, created := s.entry(key, true); !created {
		return ErrSessionExists
	}
	return nil
}

func (s *MemoryStore) Load(ctx context.Context, key SessionKey) (Session, error) {
	if err := ctx.Err(); err != nil {
		return Session{}, err
	}
	entry, _ := s.entry(key, false)
	if entry == nil {
		return Session{}, ErrSessionNotFound
	}
	return entry.snapshot(), nil
}

func (s *MemoryStore) Update(ctx context.Context, key SessionKey, update func(*Session) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, _ := s.entry(key, true)
	select {
	case entry.turnLock <- struct{}{}:
		defer func() { <-entry.turnLock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	next := entry.snapshot()
	if err := update(&next); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	committed := cloneSession(next)
	entry.state.Store(&committed)
	return nil
}

func (s *MemoryStore) entry(key SessionKey, create bool) (*memorySession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.sessions[key]; entry != nil || !create {
		return entry, false
	}
	if s.sessions == nil {
		s.sessions = make(map[SessionKey]*memorySession)
	}
	entry := &memorySession{turnLock: make(chan struct{}, 1)}
	s.sessions[key] = entry
	return entry, true
}

func (entry *memorySession) snapshot() Session {
	if state := entry.state.Load(); state != nil {
		return cloneSession(*state)
	}
	return Session{}
}

func cloneSession(state Session) Session {
	if state.History.Summary != nil {
		summary := *state.History.Summary
		summary.TaskGoals = slices.Clone(summary.TaskGoals)
		summary.UserConstraints = slices.Clone(summary.UserConstraints)
		summary.CompletedSteps = slices.Clone(summary.CompletedSteps)
		summary.PendingTasks = slices.Clone(summary.PendingTasks)
		summary.KeyEvidence = slices.Clone(summary.KeyEvidence)
		state.History.Summary = &summary
	}
	state.History.Turns = slices.Clone(state.History.Turns)
	state.ToolState.Todos = slices.Clone(state.ToolState.Todos)
	for i := range state.History.Turns {
		turn := &state.History.Turns[i]
		turn.ToolMessages = slices.Clone(turn.ToolMessages)
		for j := range turn.ToolMessages {
			message := &turn.ToolMessages[j]
			message.ToolCalls = slices.Clone(message.ToolCalls)
			for k := range message.ToolCalls {
				call := &message.ToolCalls[k]
				call.Arguments = bytes.Clone(call.Arguments)
			}
		}
	}
	return state
}

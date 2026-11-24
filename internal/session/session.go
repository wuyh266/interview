package session

import (
	"context"
	"errors"
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExists   = errors.New("session already exists")
	ErrSessionConflict = errors.New("session changed since it was loaded")
)

// SessionKey 用用户 ID 和会话 ID 定位窗口。继续对话时复用同一个键。
type SessionKey struct {
	UserID    string
	SessionID string
}

type contextKey struct{}

// WithKey 将会话键附加到本次工具调用的上下文。
func WithKey(ctx context.Context, key SessionKey) context.Context {
	return context.WithValue(ctx, contextKey{}, key)
}

// KeyFromContext 读取会话键，默认使用零值会话键。
func KeyFromContext(ctx context.Context) SessionKey {
	key, _ := ctx.Value(contextKey{}).(SessionKey)
	return key
}

type Session struct {
	History   History
	ToolState ToolState
}

// ToolState 保存工具在当前会话中积累的状态。
type ToolState struct {
	Todos []string `json:"todos,omitempty"`
}

type toolStateKey struct{}

// WithToolState 将当前轮次可修改的会话工具状态传给工具。
func WithToolState(ctx context.Context, state *ToolState) context.Context {
	return context.WithValue(ctx, toolStateKey{}, state)
}

func ToolStateFromContext(ctx context.Context) *ToolState {
	state, _ := ctx.Value(toolStateKey{}).(*ToolState)
	return state
}

type SessionStore interface {
	Create(ctx context.Context, key SessionKey) error
	// Load 返回已有会话最近一次提交的快照。
	Load(ctx context.Context, key SessionKey) (Session, error)

	// Update 读取或新建会话，并在隔离副本上执行一次 update。
	// 同一存储实例对同一会话的更新串行。回调成功后原子提交历史和工具状态。
	Update(ctx context.Context, key SessionKey, update func(*Session) error) error
}

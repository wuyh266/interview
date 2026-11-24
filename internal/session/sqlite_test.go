package session

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/tool/protocol"
)

func TestSQLiteSnapshotSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	store := openTestSQLite(t, path)
	key := SessionKey{UserID: "A", SessionID: "window"}
	want := Session{
		History: History{
			CompactedTurns: 1,
			Summary: &TaskState{
				TaskGoals: []string{"计算总额"}, UserConstraints: []string{},
				CompletedSteps: []string{"查找数据"}, PendingTasks: []string{"核对"},
				KeyEvidence: []Evidence{{Source: "calculator", Detail: "42"}},
			},
			Turns: []Turn{{UserInput: "请计算", Answer: "42", ToolMessages: []model.Message{
				{Role: model.RoleAssistant, ToolCalls: []protocol.ToolCall{{
					ID: "call-1", Name: "calculator", Arguments: json.RawMessage(`{"expression":`),
				}}},
				{Role: model.RoleTool, ToolCallID: "call-1", Content: "invalid arguments"},
			}}},
		},
		ToolState: ToolState{Todos: []string{"写周报"}},
	}
	if err := store.Update(ctx, key, func(state *Session) error {
		*state = want
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Create(ctx, key); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("duplicate create returned %v", err)
	}
	ctxCanceled, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := store.Update(ctxCanceled, key, func(state *Session) error {
		state.ToolState.Todos[0] = "canceled todo"
		state.History.Turns[0].Answer = "canceled answer"
		cancel()
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled update returned %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = openTestSQLite(t, path)
	got, err := store.Load(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restored %+v, want %+v", got, want)
	}
}

func TestSQLiteConcurrentStoresCannotOverwriteSession(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	first := openTestSQLite(t, path)
	second := openTestSQLite(t, path)
	key := SessionKey{UserID: "A", SessionID: "window"}
	if err := first.Update(ctx, key, func(state *Session) error {
		state.ToolState.Todos = []string{"stale update"}
		return second.Update(ctx, key, func(latest *Session) error {
			latest.ToolState.Todos = []string{"committed update"}
			return nil
		})
	}); !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("concurrent update returned %v", err)
	}
	state, err := first.Load(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"committed update"}; !reflect.DeepEqual(state.ToolState.Todos, want) {
		t.Fatalf("got %v, want %v", state.ToolState.Todos, want)
	}
}

func openTestSQLite(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	store, err := OpenSQLiteStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

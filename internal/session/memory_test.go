package session

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/tool/protocol"
)

func TestMemoryStoreRollsBackFailedUpdate(t *testing.T) {
	store := &MemoryStore{}
	ctx := context.Background()
	key := SessionKey{UserID: "A", SessionID: "window"}
	if err := store.Update(ctx, key, func(state *Session) error {
		state.History.Summary = &TaskState{
			TaskGoals: []string{"算出结果"}, KeyEvidence: []Evidence{{Source: "calculator", Detail: "42"}},
		}
		state.History.Turns = []Turn{{UserInput: "计算", ToolMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: []protocol.ToolCall{{Name: "calculator", Arguments: json.RawMessage(`{"expression":"6*7"}`)}},
		}}, Answer: "42"}}
		state.ToolState.Todos = []string{"写周报"}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("discard update")
	if err := store.Update(ctx, key, func(state *Session) error {
		turn := &state.History.Turns[0]
		turn.Answer = "changed"
		turn.ToolMessages[0].ToolCalls[0].Arguments[0] = '['
		state.History.Summary.TaskGoals[0] = "changed"
		state.History.Summary.KeyEvidence[0].Detail = "changed"
		state.ToolState.Todos[0] = "changed"
		return failure
	}); !errors.Is(err, failure) {
		t.Fatalf("Update returned %v, want %v", err, failure)
	}

	if err := store.Update(ctx, key, func(state *Session) error {
		if state.History.Summary.TaskGoals[0] != "算出结果" || state.History.Summary.KeyEvidence[0].Detail != "42" {
			t.Errorf("failed update changed the saved task state: %+v", state.History.Summary)
		}
		turn := state.History.Turns[0]
		if turn.Answer != "42" || string(turn.ToolMessages[0].ToolCalls[0].Arguments) != `{"expression":"6*7"}` ||
			state.ToolState.Todos[0] != "写周报" {
			t.Errorf("failed update changed the saved session: %+v", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

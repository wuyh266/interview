package tool

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"example.com/minimal-agent/internal/session"
)

func TestTodoRejectsArgumentsOutsideSchema(t *testing.T) {
	state := session.ToolState{Todos: []string{"保留的待办"}}
	for _, arguments := range []string{
		`null`,
		`{}`,
		`{"action":null}`,
		`{"action":"delete"}`,
		`{"Action":"list"}`,
		`{"action":"add"}`,
		`{"action":"add","text":null}`,
		`{"action":"add","text":123}`,
		`{"action":"add","text":" \t\u3000\u0085\ufeff"}`,
		`{"action":"add","text":"写周报","extra":true}`,
		`{"action":"list","text":"额外参数"}`,
		`{"action":"list","text":null}`,
		`{"action":"add","action":"list"}`,
		`{"action":"list"}{}`,
	} {
		_, err := (TodoTool{}).Execute(context.Background(), &state, json.RawMessage(arguments))
		if !errors.Is(err, ErrInvalidToolArguments) {
			t.Errorf("todo arguments %s returned %v, want ErrInvalidToolArguments", arguments, err)
		}
		if !slices.Equal(state.Todos, []string{"保留的待办"}) {
			t.Fatalf("invalid input %s changed the todo state to %v", arguments, state.Todos)
		}
	}
}

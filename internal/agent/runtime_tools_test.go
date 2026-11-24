package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
	"example.com/minimal-agent/internal/tool/protocol"
)

type toolRequestModel struct{}

func (toolRequestModel) Chat(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	last := request.Messages[len(request.Messages)-1]
	if last.Role == model.RoleTool {
		return model.ChatResponse{Content: last.Content}, nil
	}
	var call protocol.ToolCall
	if err := json.Unmarshal([]byte(last.Content), &call); err != nil {
		return model.ChatResponse{}, err
	}
	return model.ChatResponse{ToolCalls: []protocol.ToolCall{call}}, nil
}

func TestRuntimeInvokesBuiltinTools(t *testing.T) {
	tools, err := tool.NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	defs := tools.Defs()
	if len(defs) != 3 || defs[0].Name != tool.CalculatorToolName || defs[1].Name != tool.SearchToolName || defs[2].Name != tool.TodoToolName {
		t.Fatalf("unexpected built-in definitions: %+v", defs)
	}
	runtime := NewRuntime(toolRequestModel{}, tools, &session.MemoryStore{}, Config{
		MaxIterations: 2,
		ContextLimits: session.ContextLimits{StepTimeout: time.Second},
	})
	runtime.trace = nil
	first := session.SessionKey{UserID: "A", SessionID: "window-1"}
	second := session.SessionKey{UserID: "A", SessionID: "window-2"}
	otherUser := session.SessionKey{UserID: "B", SessionID: "window-1"}
	invoke := func(key session.SessionKey, name, arguments string) string {
		t.Helper()
		call, err := json.Marshal(protocol.ToolCall{ID: "call-1", Name: name, Arguments: json.RawMessage(arguments)})
		if err != nil {
			t.Fatal(err)
		}
		result, err := runtime.Run(context.Background(), RunInput{Session: key, Text: string(call)})
		if err != nil {
			t.Fatal(err)
		}
		return result.Answer
	}
	if output := invoke(first, tool.CalculatorToolName, `{"expression":"0.1 + 0.2"}`); output != "0.3" {
		t.Fatalf("unexpected calculator result %q", output)
	}
	var search tool.SearchResponse
	output := invoke(first, tool.SearchToolName, `{"query":"北京天气 2026-04-01"}`)
	if err := json.Unmarshal([]byte(output), &search); err != nil || !search.Mock || len(search.Results) != 1 {
		t.Fatalf("unexpected search result %q, error %v", output, err)
	}
	if output := invoke(first, tool.TodoToolName, `{"action":"add","text":"  查天气  "}`); output != `{"action":"add","text":"查天气"}` {
		t.Fatalf("unexpected todo add result %q", output)
	}
	if output := invoke(second, tool.TodoToolName, `{"action":"add","text":"写周报"}`); output != `{"action":"add","text":"写周报"}` {
		t.Fatalf("unexpected todo add result %q", output)
	}
	for _, test := range []struct {
		key  session.SessionKey
		want string
	}{
		{key: first, want: `["查天气"]`},
		{key: second, want: `["写周报"]`},
		{key: otherUser, want: `[]`},
	} {
		if output := invoke(test.key, tool.TodoToolName, `{"action":"list"}`); output != test.want {
			t.Errorf("session %+v returned %q, want %q", test.key, output, test.want)
		}
	}
}

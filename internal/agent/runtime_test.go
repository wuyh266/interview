package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
	"example.com/minimal-agent/internal/tool/protocol"
	"example.com/minimal-agent/internal/trace"
)

type chatRoundTripFunc func(*http.Request) (*http.Response, error)

func (f chatRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type calculatorTools struct {
	t *testing.T
}

func (tools calculatorTools) Defs() []protocol.ToolSpec {
	return []protocol.ToolSpec{tool.CalculatorTool{}.Spec()}
}

func (tools calculatorTools) Invoke(_ context.Context, name string, args json.RawMessage) (string, error) {
	tools.t.Helper()
	if name != tool.CalculatorToolName {
		tools.t.Fatalf("unexpected tool invocation: %s %s", name, args)
	}
	switch string(args) {
	case `{"expression":"6*7"}`:
		return "42", nil
	case `{"expression":"7/0"}`:
		return "partial output", errors.New("division by zero")
	default:
		tools.t.Fatalf("unexpected calculator arguments: %s", args)
		return "", nil
	}
}

type deepSeekTestCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func TestRuntimeCompactsHistoryWithoutReasoningBeforeDeepSeekChat(t *testing.T) {
	var calls int
	transport := chatRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		defer request.Body.Close()
		var body struct {
			ResponseFormat map[string]string `json:"response_format"`
			Messages       []model.Message   `json:"messages"`
			Thinking       map[string]string `json:"thinking"`
			Tools          []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		calls++
		content := "完成"
		switch calls {
		case 1:
			if body.Thinking["type"] != "enabled" || len(body.Tools) != 1 || body.ResponseFormat != nil || len(body.Messages) != 1 {
				t.Fatalf("unexpected first chat request: %+v", body)
			}
		case 2:
			if body.Thinking["type"] != "disabled" || len(body.Tools) != 0 || body.ResponseFormat["type"] != "json_object" || len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "撰写报告") {
				t.Fatalf("unexpected compaction request: %+v", body)
			}
			content = testTaskState
		case 3:
			if body.Thinking["type"] != "enabled" || len(body.Tools) != 1 || body.ResponseFormat != nil || len(body.Messages) != 2 || !strings.HasSuffix(body.Messages[0].Content, testTaskState) || body.Messages[1].Content != "继续" {
				t.Fatalf("unexpected follow-up request: %+v", body)
			}
		default:
			t.Fatalf("unexpected model request %d", calls)
		}
		quoted, _ := json.Marshal(content)
		response := `{"choices":[{"finish_reason":"stop","message":{"content":` + string(quoted) + `}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	chatModel, err := model.NewDeepSeekModelWithClient("test-key", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(chatModel, calculatorTools{t: t}, &session.MemoryStore{}, Config{
		MaxIterations: 1,
		ContextLimits: session.ContextLimits{StepTimeout: time.Second},
	})
	for _, input := range []string{"撰写报告", "继续"} {
		if _, err := runtime.Run(context.Background(), RunInput{Text: input}); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 {
		t.Fatalf("got %d model requests, want 3", calls)
	}
	state, err := runtime.RestoreSession(context.Background(), session.SessionKey{})
	if err != nil || state.History.CompactedTurns != 1 || len(state.History.Turns) != 2 || state.History.Turns[0].UserInput != "撰写报告" {
		t.Fatalf("expected the original turn to stay available in the transcript: %+v, %v", state.History, err)
	}
}

func TestRuntimeReturnsNativeToolResultsToDeepSeek(t *testing.T) {
	const toolReasoning = "  Fixture reasoning before tool call.\n"
	const finalReasoning = "Fixture reasoning before final answer."
	calls := []struct {
		id        string
		name      string
		arguments string
		invoked   bool
		output    string
		failure   string
	}{
		{id: "calc-1", name: tool.CalculatorToolName, arguments: `{"expression":"6*7"}`, invoked: true, output: "42"},
		{id: "calc-2", name: tool.CalculatorToolName, arguments: "bad json", failure: tool.ErrInvalidToolArguments.Error()},
		{id: "calc-3", name: tool.CalculatorToolName, arguments: `{"expression":"7/0"}`, invoked: true, output: "partial output", failure: "division by zero"},
		{id: "weather-1", name: "weather", arguments: `{}`, failure: `tool "weather" is unavailable`},
	}
	nativeCalls := make([]deepSeekTestCall, 0, len(calls))
	for _, call := range calls {
		nativeCall := deepSeekTestCall{ID: call.id, Type: "function"}
		nativeCall.Function.Name = call.name
		nativeCall.Function.Arguments = call.arguments
		nativeCalls = append(nativeCalls, nativeCall)
	}
	encodedCalls, err := json.Marshal(nativeCalls)
	if err != nil {
		t.Fatal(err)
	}
	expectedSpec := tool.CalculatorTool{}.Spec()
	expectedSchema, err := json.Marshal(expectedSpec.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	requestCount := 0
	transport := chatRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		defer request.Body.Close()

		var body struct {
			Messages []struct {
				Role             string             `json:"role"`
				Content          string             `json:"content"`
				ReasoningContent string             `json:"reasoning_content"`
				ToolCalls        []deepSeekTestCall `json:"tool_calls"`
				ToolCallID       string             `json:"tool_call_id"`
			} `json:"messages"`
			Tools []struct {
				Type     string            `json:"type"`
				Function protocol.ToolSpec `json:"function"`
			} `json:"tools"`
			ToolChoice string            `json:"tool_choice"`
			Thinking   map[string]string `json:"thinking"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Thinking["type"] != "enabled" || body.ToolChoice != "auto" || len(body.Tools) != 1 || body.Tools[0].Type != "function" {
			t.Fatalf("unexpected tool request: %+v", body)
		}
		definition := body.Tools[0].Function
		if definition.Name != expectedSpec.Name || definition.Description != expectedSpec.Description || !bytes.Equal(definition.Parameters, expectedSchema) {
			t.Fatalf("unexpected tool definitions: %+v", body.Tools)
		}

		var response string
		switch requestCount {
		case 1:
			if len(body.Messages) != 1 || body.Messages[0].Role != model.RoleUser || body.Messages[0].Content != "6*7 等于多少？" {
				t.Fatalf("unexpected first messages: %+v", body.Messages)
			}
			reasoning, _ := json.Marshal(toolReasoning)
			response = `{"choices":[{"finish_reason":"tool_calls","message":{"content":null,"reasoning_content":` + string(reasoning) + `,"tool_calls":` + string(encodedCalls) + `}}]}`
		case 2:
			if len(body.Messages) != 2+len(calls) {
				t.Fatalf("expected user, assistant and %d tool messages, got %+v", len(calls), body.Messages)
			}
			assistant := body.Messages[1]
			if assistant.Role != model.RoleAssistant || assistant.ReasoningContent != toolReasoning || len(assistant.ToolCalls) != len(calls) {
				t.Fatalf("assistant does not carry the original calls: %+v", assistant)
			}
			for i, call := range calls {
				if assistant.ToolCalls[i] != nativeCalls[i] {
					t.Errorf("unexpected assistant tool call %d: %+v", i, assistant.ToolCalls[i])
				}
				content := call.output
				if call.failure != "" {
					failure, _ := json.Marshal(map[string]string{"error": call.failure})
					content = string(failure)
				}
				result := body.Messages[2+i]
				if result.Role != model.RoleTool || result.ToolCallID != call.id || result.Content != content {
					t.Errorf("unexpected tool result for %s: %+v", call.id, result)
				}
			}
			reasoning, _ := json.Marshal(finalReasoning)
			response = `{"choices":[{"finish_reason":"stop","message":{"content":"答案是 42。","reasoning_content":` + string(reasoning) + `}}]}`
		case 3:
			if len(body.Messages) != 4+len(calls) {
				t.Fatalf("restored request lost message history: %+v", body.Messages)
			}
			assistant := body.Messages[1]
			answer := body.Messages[len(body.Messages)-2]
			if assistant.ReasoningContent != toolReasoning || answer.Role != model.RoleAssistant || answer.Content != "答案是 42。" || answer.ReasoningContent != finalReasoning {
				t.Fatalf("restored request lost reasoning: %+v", body.Messages)
			}
			response = `{"choices":[{"finish_reason":"stop","message":{"content":"刚才的答案是 42。"}}]}`
		default:
			t.Fatalf("unexpected model request %d", requestCount)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})

	chatModel, err := model.NewDeepSeekModelWithClient("test-key", &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	store, err := session.OpenSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	config := Config{
		MaxIterations: 2,
		ContextLimits: session.ContextLimits{StepTimeout: time.Second},
	}
	runtime := NewRuntime(chatModel, calculatorTools{t: t}, store, config)
	var logs bytes.Buffer
	recorder, ok := runtime.trace.(trace.LogTraceRecorder)
	if !ok {
		t.Fatalf("unexpected default trace recorder: %T", runtime.trace)
	}
	recorder.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	runtime.trace = recorder

	key := session.SessionKey{UserID: "user-1", SessionID: "session-1"}
	result, err := runtime.Run(ctx, RunInput{
		Session: key,
		Text:    "6*7 等于多少？",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Answer != "答案是 42。" || result.ReasoningContent != finalReasoning || requestCount != 2 {
		t.Fatalf("got %+v after %d requests", result, requestCount)
	}
	if result.RunID == "" {
		t.Fatal("run did not return an ID")
	}

	decoder := json.NewDecoder(&logs)
	for _, expected := range calls {
		var entry struct {
			Level      string    `json:"level"`
			RunID      string    `json:"run_id"`
			UserID     string    `json:"user_id"`
			SessionID  string    `json:"session_id"`
			Iteration  int       `json:"iteration"`
			ToolCallID string    `json:"tool_call_id"`
			ToolName   string    `json:"tool_name"`
			Arguments  string    `json:"arguments"`
			Invoked    *bool     `json:"invoked"`
			StartedAt  time.Time `json:"started_at"`
			DurationMS *int64    `json:"duration_ms"`
			Output     string    `json:"output"`
			Error      string    `json:"error"`
		}
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry.RunID != result.RunID || entry.UserID != "user-1" || entry.SessionID != "session-1" ||
			entry.Iteration != 1 || entry.ToolCallID != expected.id || entry.ToolName != expected.name {
			t.Errorf("unexpected trace identifiers for %s: %+v", expected.id, entry)
		}
		if entry.StartedAt.IsZero() || entry.DurationMS == nil || *entry.DurationMS < 0 {
			t.Errorf("unexpected trace timing for %s: %+v", expected.id, entry)
		}
		level := "INFO"
		if expected.failure != "" {
			level = "WARN"
		}
		if entry.Level != level || entry.Arguments != expected.arguments ||
			entry.Invoked == nil || *entry.Invoked != expected.invoked ||
			entry.Output != expected.output || entry.Error != expected.failure {
			t.Errorf("unexpected trace outcome for %s: %+v", expected.id, entry)
		}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		t.Errorf("expected exactly %d tool trace entries, got %v", len(calls), err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := session.OpenSQLiteStore(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	runtime = NewRuntime(chatModel, calculatorTools{t: t}, reopened, config)
	followUp, err := runtime.Run(ctx, RunInput{Session: key, Text: "刚才答案是多少？"})
	if err != nil || followUp.Answer != "刚才的答案是 42。" || requestCount != 3 {
		t.Fatalf("follow-up after reopening SQLite returned %+v, %v after %d requests", followUp, err, requestCount)
	}
}

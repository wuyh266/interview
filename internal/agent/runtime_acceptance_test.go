package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
	"example.com/minimal-agent/internal/tool/protocol"
)

func TestRuntimeHandlesPlainAndToolFollowUps(t *testing.T) {
	tools, err := tool.NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	defs := tools.Defs()
	first := model.Message{Role: model.RoleUser, Content: "每箱装 12 件，请记住。"}
	firstAnswer := model.Message{Role: model.RoleAssistant, Content: "记住了，每箱 12 件。"}
	plainFollowUp := model.Message{Role: model.RoleUser, Content: "刚才说每箱装几件？"}
	plainAnswer := model.Message{Role: model.RoleAssistant, Content: "每箱 12 件。"}
	toolFollowUp := model.Message{Role: model.RoleUser, Content: "按刚才的数量算 3 箱，把总数记到待办里。"}
	const toolAnswer = "3 箱共 36 件，已添加待办。"
	calculate := protocol.ToolCall{
		ID: "calc-1", Name: tool.CalculatorToolName, Arguments: json.RawMessage(`{"expression":"12 * 3"}`),
	}
	addTodo := protocol.ToolCall{
		ID: "todo-1", Name: tool.TodoToolName, Arguments: json.RawMessage(`{"action":"add","text":"准备 36 件货"}`),
	}
	toolTurn := []model.Message{
		toolFollowUp,
		{Role: model.RoleAssistant, ToolCalls: []protocol.ToolCall{calculate}},
		{Role: model.RoleTool, ToolCallID: calculate.ID, Content: "36"},
		{Role: model.RoleAssistant, ToolCalls: []protocol.ToolCall{addTodo}},
		{Role: model.RoleTool, ToolCallID: addTodo.ID, Content: `{"action":"add","text":"准备 36 件货"}`},
	}
	plainHistory := []model.Message{first, firstAnswer, plainFollowUp, plainAnswer}
	steps := []struct {
		messages []model.Message
		response model.ChatResponse
	}{
		{[]model.Message{first}, model.ChatResponse{Content: firstAnswer.Content}},
		{plainHistory[:3], model.ChatResponse{Content: plainAnswer.Content}},
		{append(plainHistory, toolTurn[:1]...), model.ChatResponse{ToolCalls: []protocol.ToolCall{calculate}}},
		{append(plainHistory, toolTurn[:3]...), model.ChatResponse{ToolCalls: []protocol.ToolCall{addTodo}}},
		{append(plainHistory, toolTurn...), model.ChatResponse{Content: toolAnswer}},
	}
	requestCount := 0
	chatModel := historyTestModel(func(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
		if requestCount >= len(steps) {
			t.Fatalf("unexpected request after final answer: %+v", request)
		}
		step := steps[requestCount]
		requestCount++
		if request.JSONResponse || !reflect.DeepEqual(request.Messages, step.messages) {
			t.Fatalf("request %d messages = %+v, want %+v", requestCount, request.Messages, step.messages)
		}
		if !reflect.DeepEqual(request.Tools, defs) {
			t.Fatalf("request %d tools = %+v, want %+v", requestCount, request.Tools, defs)
		}
		return step.response, nil
	})
	runtime := NewRuntime(chatModel, tools, &session.MemoryStore{}, Config{
		MaxIterations: 3,
		ContextLimits: session.ContextLimits{StepTimeout: time.Second},
	})
	runtime.trace = nil
	ctx := context.Background()
	key := session.SessionKey{UserID: "A", SessionID: "window-1"}
	for _, turn := range []struct {
		input, answer string
		requests      int
	}{
		{first.Content, firstAnswer.Content, 1},
		{plainFollowUp.Content, plainAnswer.Content, 2},
		{toolFollowUp.Content, toolAnswer, 5},
	} {
		result, err := runtime.Run(ctx, RunInput{Session: key, Text: turn.input})
		if err != nil || result.Answer != turn.answer || requestCount != turn.requests {
			t.Fatalf("input %q: got %+v, %v after %d total requests, want %q after %d", turn.input, result, err, requestCount, turn.answer, turn.requests)
		}
	}
	state, err := runtime.RestoreSession(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.ToolState.Todos, []string{"准备 36 件货"}) {
		t.Fatalf("unexpected saved todos: %v", state.ToolState.Todos)
	}
	if len(state.History.Turns) != 3 || !reflect.DeepEqual(state.History.Turns[2].ToolMessages, toolTurn[1:]) {
		t.Fatalf("tool exchanges were not saved in order: %+v", state.History.Turns)
	}
}

func TestLiveDeepSeekChoosesCalculator(t *testing.T) {
	if os.Getenv("AGENT_TEST_LIVE_DEEPSEEK") != "1" {
		t.Skip("set AGENT_TEST_LIVE_DEEPSEEK=1 and DEEPSEEK_API_KEY to call DeepSeek")
	}
	chatModel, err := model.NewDeepSeekModel(os.Getenv("DEEPSEEK_API_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	tools, err := tool.NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(chatModel, tools, &session.MemoryStore{}, Config{
		MaxIterations: 5,
		ContextLimits: session.ContextLimits{StepTimeout: 30 * time.Second},
	})
	runtime.trace = nil
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	key := session.SessionKey{UserID: "live-test", SessionID: "calculator"}
	const product = "1219326311370217952237463801111263526900"
	result, err := runtime.Run(ctx, RunInput{
		Session: key,
		Text:    "请调用合适的工具精确计算 12345678901234567890 * 98765432109876543210，最终答复只写十进制整数。",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Answer, product) {
		t.Errorf("answer = %q, want the exact product %s", result.Answer, product)
	}
	state, err := runtime.RestoreSession(ctx, key)
	if err != nil || len(state.History.Turns) != 1 {
		t.Fatalf("failed to restore the completed turn: %+v, %v", state, err)
	}
	calculatorCalls := make(map[string]bool)
	hasReasoning := result.ReasoningContent != ""
	var exactCalculation bool
	for _, message := range state.History.Turns[0].ToolMessages {
		hasReasoning = hasReasoning || message.ReasoningContent != ""
		for _, call := range message.ToolCalls {
			if call.Name == tool.CalculatorToolName {
				calculatorCalls[call.ID] = true
			}
		}
		if message.Role == model.RoleTool && calculatorCalls[message.ToolCallID] && message.Content == product {
			exactCalculation = true
		}
	}
	if !exactCalculation {
		t.Error("DeepSeek did not select a calculator call that returned the exact product")
	}
	if !hasReasoning {
		t.Error("DeepSeek did not return any reasoning_content in the turn")
	}
}

func TestRuntimeRollsBackIncompleteToolTurn(t *testing.T) {
	const stepTimeout = 50 * time.Millisecond
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "iteration limit", err: ErrIterationLimit},
		{name: "step timeout", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := session.OpenSQLiteStore(ctx, filepath.Join(t.TempDir(), "sessions.sqlite3"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			tools, err := tool.NewBuiltinTools()
			if err != nil {
				t.Fatal(err)
			}
			addTodo := protocol.ToolCall{
				ID: "todo-1", Name: tool.TodoToolName, Arguments: json.RawMessage(`{"action":"add","text":"临时待办"}`),
			}
			failedSteps := 0
			chatModel := historyTestModel(func(stepCtx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
				last := request.Messages[len(request.Messages)-1]
				if last.Role == model.RoleUser && last.Content == "先保存这条对话。" {
					return model.ChatResponse{Content: "已收到。"}, nil
				}
				failedSteps++
				switch failedSteps {
				case 1:
					return model.ChatResponse{ToolCalls: []protocol.ToolCall{addTodo}}, nil
				case 2:
					if last.Role != model.RoleTool || last.ToolCallID != addTodo.ID || last.Content != `{"action":"add","text":"临时待办"}` {
						t.Fatalf("expected a successful todo write before the failure, got %+v", last)
					}
					if test.err == context.DeadlineExceeded {
						if _, ok := stepCtx.Deadline(); !ok {
							t.Fatal("model step has no deadline")
						}
						select {
						case <-stepCtx.Done():
							return model.ChatResponse{}, stepCtx.Err()
						case <-time.After(2 * time.Second):
							t.Fatal("model step did not time out")
						}
					}
					return model.ChatResponse{ToolCalls: []protocol.ToolCall{{
						ID: "todo-2", Name: tool.TodoToolName, Arguments: json.RawMessage(`{"action":"list"}`),
					}}}, nil
				default:
					t.Fatalf("unexpected model step %d", failedSteps)
					return model.ChatResponse{}, nil
				}
			})
			runtime := NewRuntime(chatModel, tools, store, Config{
				MaxIterations: 2,
				ContextLimits: session.ContextLimits{StepTimeout: stepTimeout},
			})
			runtime.trace = nil
			key := session.SessionKey{UserID: "A", SessionID: "window-1"}
			if _, err := runtime.Run(ctx, RunInput{Session: key, Text: "先保存这条对话。"}); err != nil {
				t.Fatal(err)
			}
			before, err := runtime.RestoreSession(ctx, key)
			if err != nil {
				t.Fatal(err)
			}
			result, err := runtime.Run(ctx, RunInput{Session: key, Text: "添加临时待办。"})
			if !errors.Is(err, test.err) || result.Answer != "" || failedSteps != 2 {
				t.Fatalf("incomplete turn returned %+v, %v after %d steps, want %v after 2 steps", result, err, failedSteps, test.err)
			}
			after, err := runtime.RestoreSession(ctx, key)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("incomplete turn changed persisted history or todos: before %+v, after %+v, error %v", before, after, err)
			}
		})
	}
}

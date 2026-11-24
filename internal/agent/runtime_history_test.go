package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
	"example.com/minimal-agent/internal/tool/protocol"
)

type historyTestModel func(context.Context, model.ChatRequest) (model.ChatResponse, error)

func (m historyTestModel) Chat(ctx context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	return m(ctx, request)
}

const testTaskState = `{"task_goals":["完成报告"],"user_constraints":["使用中文"],"completed_steps":["核对数据"],"pending_tasks":["撰写结论"],"key_evidence":[{"source":"calculator","detail":"2+3=5"}]}`

func TestRuntimeKeepsLatestTwentyTurns(t *testing.T) {
	const (
		limit = 20
		total = 23
	)
	for _, configured := range []int{0, 50} {
		t.Run(fmt.Sprintf("configured_%d", configured), func(t *testing.T) {
			var current int
			var compactions int
			chatModel := historyTestModel(func(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
				if request.JSONResponse {
					var input struct {
						PreviousState *session.TaskState `json:"previous_state"`
						OlderMessages []model.Message    `json:"older_messages"`
					}
					if len(request.Tools) != 0 || len(request.Messages) != 2 {
						t.Fatalf("unexpected compression request: %+v", request)
					}
					if err := json.Unmarshal([]byte(request.Messages[1].Content), &input); err != nil {
						t.Fatal(err)
					}
					compactions++
					want := []model.Message{
						{Role: model.RoleUser, Content: fmt.Sprintf("question %d", compactions)},
						{Role: model.RoleAssistant, Content: fmt.Sprintf("answer %d", compactions)},
					}
					if !reflect.DeepEqual(input.OlderMessages, want) || (input.PreviousState == nil) != (compactions == 1) {
						t.Fatalf("compaction %d got %+v", compactions, input)
					}
					if input.PreviousState != nil && input.PreviousState.KeyEvidence[0].Detail != "2+3=5" {
						t.Fatalf("compaction did not receive the prior evidence: %+v", input.PreviousState)
					}
					return model.ChatResponse{Content: testTaskState}, nil
				}

				messages := request.Messages
				if current > limit {
					if messages[0].Role != model.RoleSystem || !strings.HasSuffix(messages[0].Content, testTaskState) {
						t.Fatalf("turn %d did not receive task state: %v", current, messages)
					}
					messages = messages[1:]
				}
				var want []model.Message
				for previous := max(1, current-limit+1); previous < current; previous++ {
					want = append(want,
						model.Message{Role: model.RoleUser, Content: fmt.Sprintf("question %d", previous)},
						model.Message{Role: model.RoleAssistant, Content: fmt.Sprintf("answer %d", previous)},
					)
				}
				want = append(want, model.Message{Role: model.RoleUser, Content: fmt.Sprintf("question %d", current)})
				if !reflect.DeepEqual(messages, want) {
					t.Fatalf("turn %d sent %v, want %v", current, request.Messages, want)
				}
				return model.ChatResponse{Content: fmt.Sprintf("answer %d", current)}, nil
			})
			runtime := NewRuntime(chatModel, nil, &session.MemoryStore{}, Config{
				MaxIterations: 1,
				ContextLimits: session.ContextLimits{MaxHistoryTurns: configured, StepTimeout: time.Second},
			})
			ctx := context.Background()
			key := session.SessionKey{UserID: "A", SessionID: "window"}
			for current = 1; current <= total; current++ {
				input := RunInput{Session: key, Text: fmt.Sprintf("question %d", current)}
				result, err := runtime.Run(ctx, input)
				if err != nil || result.Answer != fmt.Sprintf("answer %d", current) {
					t.Fatalf("turn %d returned %+v, error %v", current, result, err)
				}
			}
			if compactions != total-limit {
				t.Fatalf("compacted %d times, want %d", compactions, total-limit)
			}
			if err := runtime.sessions.Update(ctx, key, func(state *session.Session) error {
				turns := state.History.Turns
				if len(turns) != limit || turns[0].UserInput != "question 4" || turns[limit-1].Answer != "answer 23" {
					t.Errorf("stored turns do not match the last %d completed turns: %v", limit, turns)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeCompactsWhenCurrentToolOutputFillsContext(t *testing.T) {
	const budget = 2000
	oldInput := strings.Repeat("a", 1290)
	todoText := strings.Repeat("待", 180)
	arguments, err := json.Marshal(tool.TodoArgs{Action: tool.TodoAdd, Text: todoText})
	if err != nil {
		t.Fatal(err)
	}
	call := protocol.ToolCall{ID: "todo-1", Name: tool.TodoToolName, Arguments: arguments}
	var compactions int
	chatModel := historyTestModel(func(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
		last := request.Messages[len(request.Messages)-1]
		if request.JSONResponse {
			compactions++
			if !strings.Contains(last.Content, oldInput) || strings.Contains(last.Content, todoText) {
				t.Fatalf("compaction received the wrong turns: %s", last.Content)
			}
			return model.ChatResponse{Content: testTaskState}, nil
		}
		if size := len(model.MarshalMessages(request.Messages)); size > budget {
			t.Fatalf("sent %d message bytes with a budget of %d", size, budget)
		}
		switch last.Content {
		case oldInput:
			return model.ChatResponse{Content: "已记录"}, nil
		case "添加待办":
			if compactions != 0 || request.Messages[0].Content != oldInput {
				t.Fatalf("compacted before the tool result filled the budget: %+v", request.Messages)
			}
			return model.ChatResponse{ToolCalls: []protocol.ToolCall{call}}, nil
		case string(arguments):
			if compactions != 1 || len(request.Messages) != 4 || request.Messages[0].Role != model.RoleSystem {
				t.Fatalf("unexpected compacted prompt: %+v", request.Messages)
			}
			if !reflect.DeepEqual(request.Messages[2].ToolCalls, []protocol.ToolCall{call}) || last.Role != model.RoleTool || last.ToolCallID != call.ID {
				t.Fatalf("tool call and result were not replayed as a pair: %+v", request.Messages)
			}
		case "新窗口":
			if len(request.Messages) != 1 {
				t.Fatalf("new session received another session's state: %+v", request.Messages)
			}
		}
		return model.ChatResponse{Content: "完成"}, nil
	})
	tools, err := tool.NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(chatModel, tools, &session.MemoryStore{}, Config{
		MaxIterations: 2,
		ContextLimits: session.ContextLimits{MaxContextBytes: budget, StepTimeout: time.Second},
	})
	runtime.trace = nil
	ctx := context.Background()
	first := session.SessionKey{UserID: "A", SessionID: "first"}
	second := session.SessionKey{UserID: "A", SessionID: "second"}
	for _, input := range []RunInput{{Session: first, Text: oldInput}, {Session: first, Text: "添加待办"}, {Session: second, Text: "新窗口"}} {
		if _, err := runtime.Run(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	state, err := runtime.RestoreSession(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if state.History.Summary == nil || state.History.CompactedTurns != 1 || len(state.History.Turns) != 2 || state.History.Turns[0].UserInput != oldInput {
		t.Fatalf("compression did not retain the transcript: %+v", state.History)
	}
}

func TestRuntimeKeepsHistoryWhenCompactionOrAnswerFails(t *testing.T) {
	answerFailure := errors.New("answer failed")
	var attempt int
	var compressed int
	chatModel := historyTestModel(func(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
		if request.JSONResponse {
			compressed++
			if attempt == 1 {
				return model.ChatResponse{Content: `{"task_goals":[]}`}, nil
			}
			return model.ChatResponse{Content: testTaskState}, nil
		}
		if attempt == 2 {
			return model.ChatResponse{}, answerFailure
		}
		return model.ChatResponse{Content: "ok"}, nil
	})
	runtime := NewRuntime(chatModel, nil, &session.MemoryStore{}, Config{
		MaxIterations: 1,
		ContextLimits: session.ContextLimits{MaxHistoryTurns: 1, MaxContextBytes: 2000, StepTimeout: time.Second},
	})
	ctx := context.Background()
	input := RunInput{Text: "原始任务"}
	if _, err := runtime.Run(ctx, input); err != nil {
		t.Fatal(err)
	}
	before, err := runtime.RestoreSession(ctx, input.Session)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		text string
		err  error
	}{
		{text: "第一次续聊"},
		{text: "第二次续聊", err: answerFailure},
		{text: strings.Repeat("字", 1000), err: session.ErrContextLimit},
	} {
		attempt++
		input.Text = test.text
		if _, err := runtime.Run(ctx, input); err == nil || (test.err != nil && !errors.Is(err, test.err)) {
			t.Fatalf("attempt %d returned %v, want failure %v", attempt, err, test.err)
		}
		after, err := runtime.RestoreSession(ctx, input.Session)
		if err != nil || !reflect.DeepEqual(after, before) {
			t.Fatalf("attempt %d changed the stored history: %+v, err %v", attempt, after, err)
		}
	}
	if compressed != 2 {
		t.Fatalf("sent %d compaction requests, want 2", compressed)
	}
}

func TestRuntimeReplaysToolHistoryWithinItsSession(t *testing.T) {
	tools, err := tool.NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	calls := []protocol.ToolCall{
		{ID: "calc-1", Name: tool.CalculatorToolName, Arguments: json.RawMessage(`{"expression":"2+3"}`)},
		{ID: "calc-2", Name: tool.CalculatorToolName, Arguments: json.RawMessage(`{"expression":"4*5"}`)},
	}
	toolTurn := []model.Message{
		{Role: model.RoleUser, Content: "算两道题"},
		{Role: model.RoleAssistant, Content: "分别计算两道题", ToolCalls: calls},
		{Role: model.RoleTool, ToolCallID: "calc-1", Content: "5"},
		{Role: model.RoleTool, ToolCallID: "calc-2", Content: "20"},
	}
	modelFailure := errors.New("mock model failure")
	chatModel := historyTestModel(func(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
		last := request.Messages[len(request.Messages)-1]
		switch last.Content {
		case "算两道题":
			return model.ChatResponse{Content: toolTurn[1].Content, ToolCalls: calls}, nil
		case "20":
			if !reflect.DeepEqual(request.Messages, toolTurn) {
				t.Fatalf("unexpected messages after tool calls: %v", request.Messages)
			}
			return model.ChatResponse{Content: "结果是 5 和 20"}, nil
		case "失败的请求":
			return model.ChatResponse{}, modelFailure
		case "刚才第一个结果是多少":
			want := append(toolTurn,
				model.Message{Role: model.RoleAssistant, Content: "结果是 5 和 20"}, last)
			if !reflect.DeepEqual(request.Messages, want) {
				t.Fatalf("follow-up sent %v, want %v", request.Messages, want)
			}
			return model.ChatResponse{Content: "5"}, nil
		case "另一个窗口":
			if len(request.Messages) != 1 {
				t.Fatalf("new session received another session's history: %v", request.Messages)
			}
			return model.ChatResponse{Content: "新会话"}, nil
		}
		return model.ChatResponse{}, fmt.Errorf("unexpected message %q", last.Content)
	})
	runtime := NewRuntime(chatModel, tools, &session.MemoryStore{}, Config{
		MaxIterations: 2,
		ContextLimits: session.ContextLimits{StepTimeout: time.Second},
	})
	runtime.trace = nil
	first := session.SessionKey{UserID: "A", SessionID: "window-1"}
	second := session.SessionKey{UserID: "A", SessionID: "window-2"}
	for _, test := range []struct {
		input  RunInput
		answer string
		err    error
	}{
		{input: RunInput{Session: first, Text: "算两道题"}, answer: "结果是 5 和 20"},
		{input: RunInput{Session: first, Text: "失败的请求"}, err: modelFailure},
		{input: RunInput{Session: second, Text: "另一个窗口"}, answer: "新会话"},
		{input: RunInput{Session: first, Text: "刚才第一个结果是多少"}, answer: "5"},
	} {
		result, err := runtime.Run(context.Background(), test.input)
		if !errors.Is(err, test.err) || result.Answer != test.answer {
			t.Fatalf("input %q returned %+v, error %v, want answer %q and error %v", test.input.Text, result, err, test.answer, test.err)
		}
	}
}

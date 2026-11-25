package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"example.com/minimal-agent/internal/tool/protocol"
)

type deepSeekTestTransport func(*http.Request) (*http.Response, error)

func (transport deepSeekTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	defer request.Body.Close()
	return transport(request)
}

func TestDeepSeekParsesFinalAnswerAndRejectsInvalidResponses(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		body      string
		answer    string
		reasoning string
		wantErr   string
	}{
		{
			name:      "final answer",
			body:      `{"choices":[{"finish_reason":"stop","message":{"content":"每箱 12 件。","reasoning_content":"Fixture reasoning"}}]}`,
			answer:    "每箱 12 件。",
			reasoning: "Fixture reasoning",
		},
		{
			name:    "provider rate limit",
			status:  http.StatusTooManyRequests,
			body:    `{"error":{"message":"Rate limit reached"}}`,
			wantErr: "DeepSeek HTTP 429: Rate limit reached",
		},
		{
			name:    "provider error without JSON",
			status:  http.StatusBadGateway,
			body:    "Bad gateway",
			wantErr: "DeepSeek HTTP 502",
		},
		{
			name:    "broken JSON",
			body:    `{"choices":[`,
			wantErr: "decode DeepSeek response",
		},
		{
			name:    "no choices",
			body:    `{"choices":[]}`,
			wantErr: "DeepSeek returned no choices",
		},
		{
			name:    "truncated answer",
			body:    `{"choices":[{"finish_reason":"length","message":{"content":"partial"}}]}`,
			wantErr: `DeepSeek did not finish normally: "length"`,
		},
		{
			name:    "blank answer",
			body:    `{"choices":[{"finish_reason":"stop","message":{"content":" \t"}}]}`,
			wantErr: "DeepSeek returned an empty answer",
		},
		{
			name:    "missing tool calls",
			body:    `{"choices":[{"finish_reason":"tool_calls","message":{"content":null}}]}`,
			wantErr: "DeepSeek finished with tool_calls but returned no calls",
		},
		{
			name:    "tool call without ID",
			body:    `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"type":"function","function":{"name":"calculator","arguments":"{}"}}]}}]}`,
			wantErr: "DeepSeek returned an invalid tool call",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			status := test.status
			if status == 0 {
				status = http.StatusOK
			}
			client := &http.Client{Transport: deepSeekTestTransport(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			chat, err := NewDeepSeekModelWithClient("test-key", client)
			if err != nil {
				t.Fatal(err)
			}
			response, err := chat.Chat(context.Background(), ChatRequest{
				Messages: []Message{{Role: RoleUser, Content: "每箱几件？"}},
			})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Errorf("got error %v, want %q", err, test.wantErr)
				}
			} else if err != nil {
				t.Errorf("unexpected model error: %v", err)
			}
			if response.Content != test.answer || response.ReasoningContent != test.reasoning || len(response.ToolCalls) != 0 {
				t.Errorf("got response %+v, want content %q, reasoning %q and no tool calls", response, test.answer, test.reasoning)
			}
		})
	}
}

func TestDeepSeekContinuesToolCallWithMissingReasoning(t *testing.T) {
	client := &http.Client{Transport: deepSeekTestTransport(func(request *http.Request) (*http.Response, error) {
		var body struct {
			Thinking map[string]string `json:"thinking"`
			Tools    []json.RawMessage `json:"tools"`
			Messages []deepSeekMessage `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Thinking["type"] != "disabled" || len(body.Tools) != 1 || len(body.Messages) != 5 {
			t.Fatalf("unexpected fallback request: %+v", body)
		}
		if body.Messages[1].ReasoningContent != "" || len(body.Messages[3].ToolCalls) != 1 || body.Messages[4].ToolCallID != "calc-1" {
			t.Fatalf("fallback did not preserve the tool exchange: %+v", body.Messages)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"stop","message":{"content":"42"}}]}`)),
		}, nil
	})}
	chat, err := NewDeepSeekModelWithClient("test-key", client)
	if err != nil {
		t.Fatal(err)
	}
	response, err := chat.Chat(context.Background(), ChatRequest{
		Tools: []protocol.ToolSpec{{Name: "calculator", Description: "Calculate", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{
			{Role: RoleUser, Content: "记住 6 和 7。"},
			{Role: RoleAssistant, Content: "记住了。", ReasoningContent: "Previous reasoning."},
			{Role: RoleUser, Content: "用计算器乘起来。"},
			{Role: RoleAssistant, ToolCalls: []protocol.ToolCall{{ID: "calc-1", Name: "calculator", Arguments: json.RawMessage(`{"expression":"6*7"}`)}}},
			{Role: RoleTool, ToolCallID: "calc-1", Content: "42"},
		},
	})
	if err != nil || response.Content != "42" || response.ReasoningContent != "" {
		t.Fatalf("unexpected fallback response: %+v, %v", response, err)
	}
}

package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"example.com/minimal-agent/internal/tool/protocol"
)

const deepSeekURL = "https://api.deepseek.com/chat/completions"
const deepSeekName = "deepseek-flash"

type deepSeekMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []deepSeekCall `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type deepSeekCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function deepSeekFunction `json:"function"`
}

type deepSeekFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // DeepSeek 用字符串传输 JSON 实参。
}

type deepSeekTool struct {
	Type     string            `json:"type"`
	Function protocol.ToolSpec `json:"function"`
}

type DeepSeekModel struct {
	apiKey string
	client *http.Client
}

var _ Model = (*DeepSeekModel)(nil) //此处用来检查是否实现model接口，防止chat和几口接口不匹配

func NewDeepSeekModel(apiKey string) (*DeepSeekModel, error) {
	return NewDeepSeekModelWithClient(apiKey, nil)
}

// NewDeepSeekModelWithClient 使用指定的 HTTP 客户端。client 为空时使用默认超时配置。
func NewDeepSeekModelWithClient(apiKey string, client *http.Client) (*DeepSeekModel, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("DEEPSEEK_API_KEY is empty")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &DeepSeekModel{apiKey: apiKey, client: client}, nil
}

func (m *DeepSeekModel) Chat(ctx context.Context, request ChatRequest) (ChatResponse, error) {
	messages := encodeDeepSeekMessages(request.Messages)
	thinking := "enabled"
	if request.JSONResponse {
		thinking = "disabled"
	} else if len(request.Tools) > 0 {
		// 上游可能在一次工具调用中省略思考文本，此时仍需提交工具结果。
		for _, message := range messages {
			if message.Role == RoleAssistant && message.ReasoningContent == "" {
				thinking = "disabled"
				break
			}
		}
	}
	body := map[string]any{
		"model":      deepSeekName,
		"messages":   messages,
		"thinking":   map[string]string{"type": thinking},
		"stream":     false,
		"max_tokens": 1024,
	}
	if thinking == "enabled" {
		body["reasoning_effort"] = "low"
		body["max_tokens"] = 8192
	} else {
		for i := range messages {
			messages[i].ReasoningContent = ""
		}
	}
	if request.JSONResponse {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	if len(request.Tools) > 0 {
		tools := make([]deepSeekTool, 0, len(request.Tools))
		for _, spec := range request.Tools {
			tools = append(tools, deepSeekTool{Type: "function", Function: spec})
		}
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("encode DeepSeek request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepSeekURL, bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, fmt.Errorf("create DeepSeek request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.client.Do(req)
	if err != nil {
		return ChatResponse{}, fmt.Errorf("call DeepSeek: %w", err)
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if decoder.Decode(&failure) == nil && failure.Error.Message != "" {
			return ChatResponse{}, fmt.Errorf("DeepSeek HTTP %d: %s", resp.StatusCode, failure.Error.Message)
		}
		return ChatResponse{}, fmt.Errorf("DeepSeek HTTP %d", resp.StatusCode)
	}

	var completion struct {
		Choices []struct {
			FinishReason string          `json:"finish_reason"`
			Message      deepSeekMessage `json:"message"`
		} `json:"choices"`
	}

	if err := decoder.Decode(&completion); err != nil {
		return ChatResponse{}, fmt.Errorf("decode DeepSeek response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return ChatResponse{}, errors.New("DeepSeek returned no choices")
	}

	choice := completion.Choices[0]
	if choice.FinishReason != "stop" && choice.FinishReason != "tool_calls" {
		return ChatResponse{}, fmt.Errorf("DeepSeek did not finish normally: %q", choice.FinishReason)
	}
	if choice.FinishReason == "tool_calls" && len(choice.Message.ToolCalls) == 0 {
		return ChatResponse{}, errors.New("DeepSeek finished with tool_calls but returned no calls")
	}
	if len(choice.Message.ToolCalls) == 0 && strings.TrimSpace(choice.Message.Content) == "" {
		return ChatResponse{}, errors.New("DeepSeek returned an empty answer")
	}

	response := ChatResponse{
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
	}
	for _, call := range choice.Message.ToolCalls {
		if call.Type != "function" || strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Function.Name) == "" {
			return ChatResponse{}, errors.New("DeepSeek returned an invalid tool call")
		}
		response.ToolCalls = append(response.ToolCalls, protocol.ToolCall{
			ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
		})
	}
	return response, nil
}

func encodeDeepSeekMessages(messages []Message) []deepSeekMessage {
	encoded := make([]deepSeekMessage, 0, len(messages))
	for _, message := range messages {
		item := deepSeekMessage{
			Role:             message.Role,
			Content:          message.Content,
			ReasoningContent: message.ReasoningContent,
			ToolCallID:       message.ToolCallID,
		}
		for _, call := range message.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, deepSeekCall{
				ID:   call.ID,
				Type: "function",
				Function: deepSeekFunction{
					Name: call.Name, Arguments: string(call.Arguments),
				},
			})
		}
		encoded = append(encoded, item)
	}
	return encoded
}

// MarshalMessages 使用 Chat Completions 的消息格式，保留工具参数的原始字符串。
func MarshalMessages(messages []Message) []byte {
	data, _ := json.Marshal(encodeDeepSeekMessages(messages)) // 消息编码只含字符串及其集合。
	return data
}

// UnmarshalMessages 恢复 MarshalMessages 编码的历史消息。
func UnmarshalMessages(data []byte) ([]Message, error) {
	var encoded []deepSeekMessage
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil, err
	}
	messages := make([]Message, len(encoded))
	for i, item := range encoded {
		message := &messages[i]
		message.Role = item.Role
		message.Content = item.Content
		message.ReasoningContent = item.ReasoningContent
		message.ToolCallID = item.ToolCallID
		for _, call := range item.ToolCalls {
			message.ToolCalls = append(message.ToolCalls, protocol.ToolCall{
				ID: call.ID, Name: call.Function.Name, Arguments: json.RawMessage(call.Function.Arguments),
			})
		}
	}
	return messages, nil
}

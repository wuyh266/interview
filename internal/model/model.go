package model

import (
	"context"

	"example.com/minimal-agent/internal/tool/protocol"
)

// Model 根据消息和工具定义执行一次非流式对话。
type Model interface {
	Chat(ctx context.Context, request ChatRequest) (ChatResponse, error)
}

type ChatRequest struct {
	Messages     []Message
	Tools        []protocol.ToolSpec
	JSONResponse bool // 要求模型返回一个 JSON 对象。
}

type ChatResponse struct {
	Content          string
	ReasoningContent string
	ToolCalls        []protocol.ToolCall
}

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

type Message struct {
	Role             string
	Content          string
	ReasoningContent string              // Assistant 消息中模型原样返回的思考文本。
	ToolCalls        []protocol.ToolCall // Assistant 发出的工具调用。
	ToolCallID       string              // Tool 消息所对应的调用 ID。
}

package protocol

import "encoding/json"

type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolExchange 将一次调用与其结果配对，供模型在下一次决策时读取。
type ToolExchange struct {
	Call   ToolCall   `json:"call"`
	Result ToolResult `json:"result"`
}

type ToolResult struct {
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"` // 面向模型的工具失败信息。
	Truncated bool   `json:"truncated,omitempty"`
}

package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool/protocol"
)

// TodoTool 在当前会话中添加或列出待办。
type TodoTool struct{}

func (TodoTool) Spec() protocol.ToolSpec {
	return protocol.ToolSpec{
		Name:        TodoToolName,
		Description: "管理当前会话的待办。add 添加一条 text，list 列出会话的待办。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": {"type": "string", "enum": ["add", "list"]},
				"text": {
					"type": "string",
					"pattern": "` + nonBlankToolStringPattern + `",
					"description": "待办内容。add 时必填。"
				}
			},
			"required": ["action"],
			"additionalProperties": false,
			"anyOf": [
				{"properties": {"action": {"enum": ["add"]}}, "required": ["text"]},
				{"properties": {"action": {"enum": ["list"]}}, "additionalProperties": false}
			]
		}`),
	}
}

func (TodoTool) Execute(ctx context.Context, state *session.ToolState, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if state == nil {
		return "", errors.New("todo requires session state")
	}
	fields, err := decodeToolFields(arguments, "action", "text")
	if err != nil {
		return "", err
	}
	action, err := readToolString(fields, "action")
	if err != nil {
		return "", err
	}

	switch TodoAction(action) {
	case TodoAdd:
		text, err := readToolString(fields, "text")
		if err != nil {
			return "", err
		}
		text = strings.TrimFunc(text, isToolSpace)
		receipt, err := json.Marshal(TodoArgs{Action: TodoAdd, Text: text})
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		state.Todos = append(state.Todos, text)
		return string(receipt), nil
	case TodoList:
		if _, exists := fields["text"]; exists {
			return "", fmt.Errorf("%w: list accepts only action", ErrInvalidToolArguments)
		}
		items := state.Todos
		if items == nil {
			items = []string{}
		}
		result, err := json.Marshal(items)
		return string(result), err
	default:
		return "", fmt.Errorf("%w: unsupported todo action %q", ErrInvalidToolArguments, action)
	}
}

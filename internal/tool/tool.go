package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode"

	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool/protocol"
)

// ErrInvalidToolArguments 表示实参不符合工具的参数约定。
var ErrInvalidToolArguments = errors.New("invalid tool arguments")

// Tools 提供本次运行可用的工具定义和执行入口。
type Tools interface {
	Defs() []protocol.ToolSpec
	// Invoke 执行指定工具，并按该工具的参数约定校验实参。
	Invoke(ctx context.Context, name string, arguments json.RawMessage) (string, error)
}

type Tool interface {
	Spec() protocol.ToolSpec
	// Execute 按参数 Schema 校验实参，并仅在成功时更新会话内的工具状态。
	Execute(ctx context.Context, state *session.ToolState, arguments json.RawMessage) (string, error)
}

type ToolRegistry interface {
	// Register 校验工具定义，工具名称重复时返回错误。
	Register(tool Tool) error
	Specs() []protocol.ToolSpec
	Lookup(name string) (Tool, bool)
}

const (
	CalculatorToolName = "calculator"
	SearchToolName     = "search"
	TodoToolName       = "todo"
)

type CalculatorArgs struct {
	Expression string `json:"expression"`
}

type SearchArgs struct {
	Query string `json:"query"`
}

type TodoAction string

const (
	TodoAdd  TodoAction = "add"
	TodoList TodoAction = "list"
)

type TodoArgs struct {
	Action TodoAction `json:"action"`
	Text   string     `json:"text,omitempty"` // add 时必填。
}

// nonBlankToolStringPattern 是经 JSON 转义的正则，与 isToolSpace 使用相同的空白字符集合。
const nonBlankToolStringPattern = `[^\\t-\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028-\\u2029\\u202f\\u205f\\u3000\\ufeff]`

func isToolSpace(r rune) bool {
	return unicode.IsSpace(r) || r == '\ufeff'
}

func decodeToolFields(arguments json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%w: expected a JSON object", ErrInvalidToolArguments)
	}
	fields := make(map[string]json.RawMessage, len(allowed))
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: read field name: %v", ErrInvalidToolArguments, err)
		}
		name, ok := token.(string)
		if !ok || !slices.Contains(allowed, name) {
			return nil, fmt.Errorf("%w: unexpected field %q", ErrInvalidToolArguments, token)
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate field %q", ErrInvalidToolArguments, name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("%w: read field %q: %v", ErrInvalidToolArguments, name, err)
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, fmt.Errorf("%w: expected a closing brace", ErrInvalidToolArguments)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: unexpected data after the object", ErrInvalidToolArguments)
	}
	return fields, nil
}

func readToolString(fields map[string]json.RawMessage, name string) (string, error) {
	raw, exists := fields[name]
	if !exists {
		return "", fmt.Errorf("%w: missing field %q", ErrInvalidToolArguments, name)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimFunc(value, isToolSpace) == "" {
		return "", fmt.Errorf("%w: %q must be a non-blank string", ErrInvalidToolArguments, name)
	}
	return value, nil
}

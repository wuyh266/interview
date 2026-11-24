package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"example.com/minimal-agent/internal/tool/protocol"
)

// ErrInvalidDecision 表示模型输出无法解析为一条有效决策。
var ErrInvalidDecision = errors.New("invalid model decision")

// DecisionParser 解析原先由模型在文本中输出的 JSON 决策。
type DecisionParser interface {
	Parse(output []byte) (Decision, error)
}

type Decision struct {
	Summary     string             `json:"summary,omitempty"`
	ToolCall    *protocol.ToolCall `json:"tool_call,omitempty"`
	FinalAnswer *string            `json:"final_answer,omitempty"`
}

// JSONDecisionParser 将原始 JSON 解析为一条有效的模型决策。
type JSONDecisionParser struct{}

var _ DecisionParser = JSONDecisionParser{}

func (JSONDecisionParser) Parse(output []byte) (Decision, error) {
	// RawMessage 保留字段是否出现的信息，包括值为 null 的字段。
	var fields struct {
		Summary     string          `json:"summary"`
		FinalAnswer json.RawMessage `json:"final_answer"`
		ToolCall    json.RawMessage `json:"tool_call"`
	}
	if err := json.Unmarshal(output, &fields); err != nil {
		return Decision{}, fmt.Errorf("%w: decode JSON: %w", ErrInvalidDecision, err)
	}

	hasFinalAnswer := fields.FinalAnswer != nil
	hasToolCall := fields.ToolCall != nil
	if hasFinalAnswer == hasToolCall {
		return Decision{}, fmt.Errorf("%w: expected exactly one of final_answer or tool_call", ErrInvalidDecision)
	}

	decision := Decision{Summary: fields.Summary}
	if hasFinalAnswer {
		if err := json.Unmarshal(fields.FinalAnswer, &decision.FinalAnswer); err != nil {
			return Decision{}, fmt.Errorf("%w: decode final_answer: %w", ErrInvalidDecision, err)
		}
		if decision.FinalAnswer == nil || strings.TrimSpace(*decision.FinalAnswer) == "" {
			return Decision{}, fmt.Errorf("%w: final_answer must be a non-blank string", ErrInvalidDecision)
		}
		return decision, nil
	}

	if err := json.Unmarshal(fields.ToolCall, &decision.ToolCall); err != nil {
		return Decision{}, fmt.Errorf("%w: decode tool_call: %w", ErrInvalidDecision, err)
	}
	call := decision.ToolCall
	if call == nil || (call.Name == "" && call.Arguments == nil) {
		return Decision{}, fmt.Errorf("%w: tool_call is empty", ErrInvalidDecision)
	}
	if strings.TrimSpace(call.Name) == "" {
		return Decision{}, fmt.Errorf("%w: tool_call.name must be a non-blank string", ErrInvalidDecision)
	}
	arguments := bytes.TrimSpace(call.Arguments)
	if len(arguments) == 0 || arguments[0] != '{' {
		return Decision{}, fmt.Errorf("%w: tool_call.arguments must be a JSON object", ErrInvalidDecision)
	}
	return decision, nil
}

package model

import (
	"errors"
	"testing"
)

func TestJSONDecisionParserFinalAnswer(t *testing.T) {
	output := []byte(`{"summary":"直接回答","final_answer":"  结果是 42\n"}`)
	decision, err := JSONDecisionParser{}.Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	if decision.FinalAnswer == nil || *decision.FinalAnswer != "  结果是 42\n" {
		t.Fatalf("Parse(%s) did not preserve final_answer", output)
	}
	if decision.Summary != "直接回答" || decision.ToolCall != nil {
		t.Errorf("unexpected final answer decision: %+v", decision)
	}
}

func TestJSONDecisionParserToolCall(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		arguments string
	}{
		{name: "with arguments", toolName: "calculator", arguments: `{"expression":"6 * 7"}`},
		{name: "empty arguments object", toolName: "ping", arguments: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := []byte(`{"tool_call":{"name":"` + test.toolName + `","arguments":` + test.arguments + `}}`)
			decision, err := JSONDecisionParser{}.Parse(output)
			if err != nil {
				t.Fatal(err)
			}
			if decision.ToolCall == nil || decision.FinalAnswer != nil {
				t.Fatalf("expected a tool call decision, got %+v", decision)
			}
			if decision.ToolCall.Name != test.toolName || string(decision.ToolCall.Arguments) != test.arguments {
				t.Errorf("unexpected tool call %q with arguments %s", decision.ToolCall.Name, decision.ToolCall.Arguments)
			}
		})
	}
}

func TestJSONDecisionParserRejectsInvalidDecisions(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "both fields", output: `{"final_answer":"42","tool_call":{"name":"calculator"}}`},
		{name: "both fields with null answer", output: `{"final_answer":null,"tool_call":{"name":"calculator"}}`},
		{name: "both fields with null tool call", output: `{"final_answer":"42","tool_call":null}`},
		{name: "both fields missing", output: `{"summary":"等待输入"}`},
		{name: "empty answer", output: `{"final_answer":""}`},
		{name: "blank answer", output: `{"final_answer":" \t\n\u3000"}`},
		{name: "null answer", output: `{"final_answer":null}`},
		{name: "null tool call", output: `{"tool_call":null}`},
		{name: "empty tool call object", output: `{"tool_call":{}}`},
		{name: "empty tool call string", output: `{"tool_call":""}`},
		{name: "non-string answer", output: `{"final_answer":42}`},
		{name: "non-object tool call", output: `{"tool_call":"calculator"}`},
		{name: "missing tool name", output: `{"tool_call":{"arguments":{}}}`},
		{name: "blank tool name", output: `{"tool_call":{"name":" \t\u3000","arguments":{}}}`},
		{name: "non-string tool name", output: `{"tool_call":{"name":42,"arguments":{}}}`},
		{name: "missing tool arguments", output: `{"tool_call":{"name":"calculator"}}`},
		{name: "null tool arguments", output: `{"tool_call":{"name":"calculator","arguments":null}}`},
		{name: "non-object tool arguments", output: `{"tool_call":{"name":"calculator","arguments":["6 * 7"]}}`},
		{name: "malformed JSON", output: `{"final_answer":"42"`},
	}
	parser := JSONDecisionParser{}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := parser.Parse([]byte(test.output))
			if !errors.Is(err, ErrInvalidDecision) {
				t.Fatalf("Parse(%s) error = %v, want ErrInvalidDecision", test.output, err)
			}
			if decision != (Decision{}) {
				t.Errorf("expected an empty decision, got %+v", decision)
			}
		})
	}
}

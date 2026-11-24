package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCalculatorExactArithmetic(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		want       string
	}{
		{name: "decimal addition", expression: "0.1 + 0.2", want: "0.3"},
		{name: "long decimal", expression: "9007199254740993.000000000000000001 + 0.000000000000000002", want: "9007199254740993.000000000000000003"},
		{name: "thousand-digit integer", expression: strings.Repeat("9", 1000) + "+1", want: "1" + strings.Repeat("0", 1000)},
		{name: "large multiplication", expression: "1e600 * 1e400", want: "1" + strings.Repeat("0", 1000)},
		{name: "signs and parentheses", expression: "-(2 + .5) * -4.", want: "10"},
		{name: "operator precedence", expression: "2 + 3 * 4", want: "14"},
		{name: "left associative division", expression: "8 / 4 / 2", want: "1"},
		{name: "scientific notation", expression: "1.25e3 * 4E-4", want: "0.5"},
		{name: "leading decimal zero", expression: "009 + 1", want: "10"},
		{name: "finite division", expression: "1 / 40", want: "0.025"},
		{name: "repeating division", expression: "2 / 12", want: "1/6"},
		{name: "exact intermediate fraction", expression: "(1 / 3) * 3", want: "1"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			arguments, err := json.Marshal(CalculatorArgs{Expression: test.expression})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (CalculatorTool{}).Execute(context.Background(), nil, arguments)
			if err != nil || result != test.want {
				t.Fatalf("calculator returned %q, %v, want %q", result, err, test.want)
			}
		})
	}
}

func TestCalculatorRejectsInvalidArguments(t *testing.T) {
	for _, arguments := range []string{
		`{}`,
		`{"expression": 2}`,
		`{"expression":" "}`,
		`{"expression":"1 / (2 - 2)"}`,
		`{"expression":"(1 + 2"}`,
		`{"expression":"1 2"}`,
		`{"expression":"1\u0000"}`,
		`{"expression":"1e9999999999"}`,
		`{"expression":"1e8192 * 1e8192 * 1e8192"}`,
		`{"expression":"` + strings.Repeat("(", maxCalculatorDepth+1) + "1" + strings.Repeat(")", maxCalculatorDepth+1) + `"}`,
	} {
		_, err := (CalculatorTool{}).Execute(context.Background(), nil, json.RawMessage(arguments))
		if !errors.Is(err, ErrInvalidToolArguments) {
			t.Errorf("calculator arguments %s returned %v, want ErrInvalidToolArguments", arguments, err)
		}
	}
}

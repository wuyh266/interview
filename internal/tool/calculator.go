package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"

	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool/protocol"
)

const (
	maxCalculatorInputBytes = 8192
	maxCalculatorExponent   = 8192
	maxCalculatorDepth      = 128
	maxCalculatorBits       = 65536
)

var calculatorNumber = regexp.MustCompile(`^([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?`)

// CalculatorTool 使用任意精度有理数计算四则运算。
type CalculatorTool struct{}

func (CalculatorTool) Spec() protocol.ToolSpec {
	return protocol.ToolSpec{
		Name:        CalculatorToolName,
		Description: "精确计算 +、-、*、/ 表达式。支持括号、大整数、小数和科学计数法，循环小数返回精确分数。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"expression": {"type": "string", "pattern": "` + nonBlankToolStringPattern + `"}
			},
			"required": ["expression"],
			"additionalProperties": false
		}`),
	}
}

func (CalculatorTool) Execute(ctx context.Context, _ *session.ToolState, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fields, err := decodeToolFields(arguments, "expression")
	if err != nil {
		return "", err
	}
	expression, err := readToolString(fields, "expression")
	if err != nil {
		return "", err
	}
	if len(expression) > maxCalculatorInputBytes {
		return "", fmt.Errorf("%w: expression exceeds %d bytes", ErrInvalidToolArguments, maxCalculatorInputBytes)
	}

	parser := calculatorParser{remaining: expression}
	value, err := parser.expression(1, 0)
	if err != nil {
		return "", err
	}
	if parser.remaining != "" {
		return "", fmt.Errorf("%w: unexpected input at byte %d", ErrInvalidToolArguments, len(expression)-len(parser.remaining)+1)
	}
	if digits, exact := value.FloatPrec(); exact {
		return value.FloatString(digits), nil
	}
	return value.RatString(), nil
}

type calculatorParser struct {
	remaining string
}

func (p *calculatorParser) peek() byte {
	p.remaining = strings.TrimLeftFunc(p.remaining, isToolSpace)
	if p.remaining == "" {
		return 0
	}
	return p.remaining[0]
}

func calculatorPrecedence(operator byte) int {
	switch operator {
	case '+', '-':
		return 1
	case '*', '/':
		return 2
	default:
		return 0
	}
}

func (p *calculatorParser) expression(minPrecedence, depth int) (*big.Rat, error) {
	left, err := p.operand(depth)
	if err != nil {
		return nil, err
	}
	for {
		operator := p.peek()
		precedence := calculatorPrecedence(operator)
		if precedence < minPrecedence {
			return left, nil
		}
		p.remaining = p.remaining[1:]
		right, err := p.expression(precedence+1, depth)
		if err != nil {
			return nil, err
		}
		switch operator {
		case '+':
			left.Add(left, right)
		case '-':
			left.Sub(left, right)
		case '*':
			left.Mul(left, right)
		case '/':
			if right.Sign() == 0 {
				return nil, fmt.Errorf("%w: division by zero", ErrInvalidToolArguments)
			}
			left.Quo(left, right)
		}
		if max(left.Num().BitLen(), left.Denom().BitLen()) > maxCalculatorBits {
			return nil, fmt.Errorf("%w: calculation exceeds %d bits", ErrInvalidToolArguments, maxCalculatorBits)
		}
	}
}

func (p *calculatorParser) operand(depth int) (*big.Rat, error) {
	negative := false
	for sign := p.peek(); sign == '+' || sign == '-'; sign = p.peek() {
		if sign == '-' {
			negative = !negative
		}
		p.remaining = p.remaining[1:]
	}

	var value *big.Rat
	var err error
	if p.peek() == '(' {
		if depth >= maxCalculatorDepth {
			return nil, fmt.Errorf("%w: parentheses exceed depth %d", ErrInvalidToolArguments, maxCalculatorDepth)
		}
		p.remaining = p.remaining[1:]
		value, err = p.expression(1, depth+1)
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, fmt.Errorf("%w: missing closing parenthesis", ErrInvalidToolArguments)
		}
		p.remaining = p.remaining[1:]
	} else {
		value, err = p.number()
		if err != nil {
			return nil, err
		}
	}
	if negative {
		value.Neg(value)
	}
	return value, nil
}

func (p *calculatorParser) number() (*big.Rat, error) {
	literal := calculatorNumber.FindString(p.remaining)
	if literal == "" {
		return nil, fmt.Errorf("%w: expected a number or parenthesis", ErrInvalidToolArguments)
	}
	if exponentIndex := strings.IndexAny(literal, "eE"); exponentIndex >= 0 {
		exponent, err := strconv.Atoi(literal[exponentIndex+1:])
		if err != nil || exponent < -maxCalculatorExponent || exponent > maxCalculatorExponent {
			return nil, fmt.Errorf("%w: exponent must be between %d and %d", ErrInvalidToolArguments, -maxCalculatorExponent, maxCalculatorExponent)
		}
	}
	value, ok := new(big.Rat).SetString(literal)
	if !ok {
		return nil, fmt.Errorf("%w: invalid decimal number", ErrInvalidToolArguments)
	}
	p.remaining = p.remaining[len(literal):]
	return value, nil
}

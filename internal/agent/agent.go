package agent

import (
	"context"
	"errors"

	"example.com/minimal-agent/internal/session"
)

// ErrIterationLimit 表示当前用户轮次耗尽了模型调用次数。
var ErrIterationLimit = errors.New("agent iteration limit reached")

// Agent 每次 Run 接收一条新用户消息并产出一轮最终答复。
type Agent interface {
	Run(ctx context.Context, input RunInput) (RunResult, error)
}

type RunInput struct {
	Session session.SessionKey
	Text    string
}

type RunResult struct {
	RunID            string
	Answer           string
	ReasoningContent string
}

// Config 中的各项上限均为正整数。
type Config struct {
	MaxIterations int
	ContextLimits session.ContextLimits
}

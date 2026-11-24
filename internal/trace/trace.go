package trace

import (
	"context"
	"log/slog"
	"time"

	"example.com/minimal-agent/internal/session"
)

// ToolTrace 记录一次工具调用尝试，包括执行前的校验失败。
type ToolTrace struct {
	RunID      string
	Session    session.SessionKey
	Iteration  int
	ToolCallID string
	ToolName   string
	Arguments  string // 模型传入的参数原文，可以包含无效 JSON。
	Invoked    bool   // 是否进入了 Tools.Invoke。
	StartedAt  time.Time
	Duration   time.Duration
	Output     string // Tools.Invoke 返回的原始输出。
	Error      string
}

type TraceRecorder interface {
	RecordTool(ctx context.Context, trace ToolTrace)
}

// LogTraceRecorder 将工具调用写入 slog。零值使用默认日志记录器。
type LogTraceRecorder struct {
	Logger *slog.Logger
}

func (r LogTraceRecorder) RecordTool(ctx context.Context, trace ToolTrace) {
	logger := r.Logger
	if logger == nil {
		logger = slog.Default()
	}

	level := slog.LevelInfo
	if trace.Error != "" {
		level = slog.LevelWarn
	}
	logger.LogAttrs(ctx, level, "agent tool attempt",
		slog.String("run_id", trace.RunID),
		slog.String("user_id", trace.Session.UserID),
		slog.String("session_id", trace.Session.SessionID),
		slog.Int("iteration", trace.Iteration),
		slog.String("tool_call_id", trace.ToolCallID),
		slog.String("tool_name", trace.ToolName),
		slog.String("arguments", trace.Arguments),
		slog.Bool("invoked", trace.Invoked),
		slog.Time("started_at", trace.StartedAt),
		slog.Int64("duration_ms", trace.Duration.Milliseconds()),
		slog.String("output", trace.Output),
		slog.String("error", trace.Error),
	)
}

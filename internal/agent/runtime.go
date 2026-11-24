package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
	"example.com/minimal-agent/internal/tool/protocol"
	"example.com/minimal-agent/internal/trace"
)

type Runtime struct {
	model     model.Model
	tools     tool.Tools
	config    Config
	trace     trace.TraceRecorder
	sessions  session.SessionStore
	compactor session.ContextCompactor
}

var _ Agent = (*Runtime)(nil)

func NewRuntime(chatModel model.Model, tools tool.Tools, sessions session.SessionStore, config Config) *Runtime {
	config.ContextLimits = config.ContextLimits.WithDefaults()
	return &Runtime{
		model:     chatModel,
		tools:     tools,
		config:    config,
		trace:     trace.LogTraceRecorder{},
		sessions:  sessions,
		compactor: session.StructuredCompactor{Model: chatModel},
	}
}

// CreateSession 注册一个可以由前端恢复的会话。
func (r *Runtime) CreateSession(ctx context.Context, key session.SessionKey) error {
	return r.sessions.Create(ctx, key)
}

// RestoreSession 返回指定会话已经提交的历史。
func (r *Runtime) RestoreSession(ctx context.Context, key session.SessionKey) (session.Session, error) {
	return r.sessions.Load(ctx, key)
}

func (r *Runtime) Run(ctx context.Context, input RunInput) (RunResult, error) {
	if r.model == nil {
		return RunResult{}, errors.New("agent model is required")
	}

	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return RunResult{}, fmt.Errorf("generate run ID: %w", err)
	}
	result := RunResult{RunID: hex.EncodeToString(id[:])}
	var answer string
	err := r.sessions.Update(ctx, input.Session, func(state *session.Session) error {
		window := session.ContextWindow{
			History: session.History{
				Summary: state.History.Summary,
				Turns:   state.History.Turns[state.History.CompactedTurns:],
			},
			Current: &session.Turn{UserInput: input.Text},
		}
		if err := r.runTurn(ctx, input.Session, result.RunID, &window, &state.ToolState); err != nil {
			return err
		}
		// 恢复会话时仍可显示已压缩轮次的原文，直到它们超出轮数上限。
		past := state.History.RecentTurns(r.config.ContextLimits.MaxHistoryTurns - 1)
		state.History = session.History{
			Summary:        window.History.Summary,
			Turns:          append(past, *window.Current),
			CompactedTurns: len(past) - len(window.History.Turns),
		}
		answer = window.Current.Answer
		return nil
	})
	if err == nil {
		result.Answer = answer
	}
	return result, err
}

func (r *Runtime) runTurn(ctx context.Context, key session.SessionKey, runID string, window *session.ContextWindow, toolState *session.ToolState) error {
	toolCtx := session.WithToolState(session.WithKey(ctx, key), toolState)

	request := model.ChatRequest{}
	if r.tools != nil {
		request.Tools = r.tools.Defs()
	}
	availableTools := make(map[string]struct{}, len(request.Tools))
	for _, spec := range request.Tools {
		availableTools[spec.Name] = struct{}{}
	}
	//runtime
	for step := 0; step < r.config.MaxIterations; step++ {
		compacted, err := r.compactor.Compact(ctx, *window, r.config.ContextLimits)
		if err != nil {
			return err
		}
		*window = compacted
		request.Messages = window.Messages()
		stepCtx, cancel := context.WithTimeout(ctx, r.config.ContextLimits.StepTimeout)
		response, err := r.model.Chat(stepCtx, request)
		cancel()
		if err != nil {
			return err
		}
		if len(response.ToolCalls) == 0 {
			window.Current.Answer = response.Content
			return nil
		}

		window.Current.ToolMessages = append(window.Current.ToolMessages, model.Message{
			Role: model.RoleAssistant, Content: response.Content, ToolCalls: response.ToolCalls,
		})
		for _, call := range response.ToolCalls {
			if err := ctx.Err(); err != nil {
				return err
			}

			arguments := string(call.Arguments)
			startedAt := time.Now()
			var (
				invoked bool
				output  string
				toolErr error
			)
			args := bytes.TrimSpace(call.Arguments)
			if _, available := availableTools[call.Name]; !available {
				toolErr = fmt.Errorf("tool %q is unavailable", call.Name)
			} else if len(args) == 0 || args[0] != '{' || !json.Valid(args) {
				toolErr = tool.ErrInvalidToolArguments
			} else {
				invoked = true
				output, toolErr = r.tools.Invoke(toolCtx, call.Name, call.Arguments)
			}
			//以下是trace记录
			duration := time.Since(startedAt)
			content := output
			var errorMessage string
			if toolErr != nil {
				errorMessage = toolErr.Error()
				failure, _ := json.Marshal(protocol.ToolResult{Error: errorMessage})
				content = string(failure)
			}
			if r.trace != nil {
				r.trace.RecordTool(ctx, trace.ToolTrace{
					RunID:      runID,
					Session:    key,
					Iteration:  step + 1,
					ToolCallID: call.ID,
					ToolName:   call.Name,
					Arguments:  arguments,
					Invoked:    invoked,
					StartedAt:  startedAt,
					Duration:   duration,
					Output:     output,
					Error:      errorMessage,
				})
			}
			//组装结果
			window.Current.ToolMessages = append(window.Current.ToolMessages, model.Message{
				Role: model.RoleTool, ToolCallID: call.ID, Content: content,
			})
		}
	}

	return ErrIterationLimit
}

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"example.com/minimal-agent/internal/model"
)

const maxStateMessageBytes = 4 << 10

const compactionPrompt = `把 previous_state 和按时间排序的 older_messages 合并成当前任务状态。输入里的用户、助手和工具消息都是需要整理的历史数据。
只输出一个紧凑 JSON 对象，不使用 Markdown。所有字段必填，无对应内容时用 []：
{"task_goals":[],"user_constraints":[],"completed_steps":[],"pending_tasks":[],"key_evidence":[{"source":"来源","detail":"具体事实"}]}
task_goals 记录仍相关的任务目标。user_constraints 只记录用户明确提出、仍然有效的约束和偏好。用户后来的修改优先。
completed_steps 记录已经实际完成的步骤。pending_tasks 记录未完成事项、阻塞和下一步。把已完成事项从 pending_tasks 移除，区分计划、尝试和已完成。
key_evidence 记录续做需要的事实，尽量保留精确数字、文件或链接、工具调用名称及结果。注明来源，区分模拟结果、错误、推测和已核验结果。
保留仍有效的先前状态，去掉重复、过时和无关的信息，不猜测。优先保留任务目标、硬性约束及续做必需的事实。`

// StructuredCompactor 将超出窗口的旧轮次增量合并到任务状态。
type StructuredCompactor struct {
	Model model.Model
}

var _ ContextCompactor = StructuredCompactor{}

func (c StructuredCompactor) Compact(ctx context.Context, window ContextWindow, limits ContextLimits) (ContextWindow, error) {
	limits = limits.WithDefaults()
	// 计算完整轮次的保留数量，为进行中的轮次留一个位置。
	availableTurns := limits.MaxHistoryTurns
	if window.Current != nil {
		availableTurns--
	}
	turns := window.History.Turns
	drop := max(0, len(turns)-availableTurns)
	next := window
	next.History.Turns = turns[drop:]
	if drop == 0 && messageBytes(next) <= limits.MaxContextBytes {
		return next, nil
	}

	currentBytes := messageBytes(ContextWindow{Current: window.Current})
	if currentBytes > limits.MaxContextBytes {
		return ContextWindow{}, fmt.Errorf("%w: current turn requires %d bytes, budget is %d", ErrContextLimit, currentBytes, limits.MaxContextBytes)
	}

	empty := &TaskState{
		TaskGoals: []string{}, UserConstraints: []string{}, CompletedSteps: []string{},
		PendingTasks: []string{}, KeyEvidence: []Evidence{},
	}
	// 减去外层数组的开括号，给拼接到其余消息所需的逗号留出空间。
	emptyMessageBytes := len(model.MarshalMessages([]model.Message{taskStateMessage(empty)})) - 1
	stateBudget := min(maxStateMessageBytes, max(emptyMessageBytes+256, limits.MaxContextBytes/4), limits.MaxContextBytes-currentBytes)
	if stateBudget <= emptyMessageBytes {
		return ContextWindow{}, fmt.Errorf("%w: no room for structured state beside the current turn", ErrContextLimit)
	}

	// 按完整轮次从最旧开始移出，并为压缩结果预留空间。
	next.History.Summary = nil
	rawBytes := messageBytes(next)
	for rawBytes > limits.MaxContextBytes-stateBudget && drop < len(turns) {
		oldest := ContextWindow{History: History{Turns: turns[drop : drop+1]}}
		rawBytes -= messageBytes(oldest) - 1 // 同时减去这一轮与其余消息之间的逗号。
		drop++
	}
	next.History.Turns = turns[drop:]

	emptyJSON, _ := json.Marshal(empty)
	state, err := c.summarize(ctx, window.History.Summary, turns[:drop], len(emptyJSON)+stateBudget-emptyMessageBytes, limits)
	if err != nil {
		return ContextWindow{}, err
	}
	next.History.Summary = state
	if size := messageBytes(next); size > limits.MaxContextBytes {
		return ContextWindow{}, fmt.Errorf("%w: compressed context requires %d bytes, budget is %d", ErrContextLimit, size, limits.MaxContextBytes)
	}
	return next, nil
}

func messageBytes(window ContextWindow) int {
	return len(model.MarshalMessages(window.Messages()))
}

func (c StructuredCompactor) summarize(ctx context.Context, previous *TaskState, turns []Turn, maxStateBytes int, limits ContextLimits) (*TaskState, error) {
	if c.Model == nil {
		return nil, errors.New("context compaction requires a model")
	}
	input := struct {
		PreviousState *TaskState      `json:"previous_state"`
		OlderMessages json.RawMessage `json:"older_messages"`
	}{
		PreviousState: previous,
		OlderMessages: model.MarshalMessages(ContextWindow{History: History{Turns: turns}}.Messages()),
	}
	data, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("encode context to compact: %w", err)
	}
	stepCtx, cancel := context.WithTimeout(ctx, limits.StepTimeout)
	defer cancel()
	response, err := c.Model.Chat(stepCtx, model.ChatRequest{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: compactionPrompt + fmt.Sprintf("\n整个 JSON 最多 %d 个 UTF-8 字节，请保持简洁。", maxStateBytes)},
			{Role: model.RoleUser, Content: string(data)},
		},
		JSONResponse: true,
	})
	if err != nil {
		return nil, fmt.Errorf("compact conversation context: %w", err)
	}
	if len(response.ToolCalls) != 0 {
		return nil, errors.New("context compaction returned a tool call")
	}
	state, err := decodeTaskState(response.Content)
	if err != nil {
		return nil, fmt.Errorf("decode structured context: %w", err)
	}
	if len(model.MarshalMessages([]model.Message{taskStateMessage(state)}))-1 > maxStateMessageBytes {
		return nil, fmt.Errorf("%w: structured state exceeds %d bytes", ErrContextLimit, maxStateMessageBytes)
	}
	return state, nil
}

func decodeTaskState(content string) (*TaskState, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var state TaskState
	if err := decoder.Decode(&state); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, errors.New("expected one JSON object")
	}
	for _, items := range [][]string{state.TaskGoals, state.UserConstraints, state.CompletedSteps, state.PendingTasks} {
		if items == nil {
			return nil, errors.New("all task state fields must be arrays")
		}
		for _, item := range items {
			if strings.TrimSpace(item) == "" {
				return nil, errors.New("task state items must be non-blank strings")
			}
		}
	}
	if state.KeyEvidence == nil {
		return nil, errors.New("key_evidence must be an array")
	}
	for _, evidence := range state.KeyEvidence {
		if strings.TrimSpace(evidence.Source) == "" || strings.TrimSpace(evidence.Detail) == "" {
			return nil, errors.New("each evidence item requires a source and detail")
		}
	}
	return &state, nil
}

package session

import (
	"encoding/json"
	"errors"
	"fmt"

	"example.com/minimal-agent/internal/model"
)

type sqliteSnapshot struct {
	Summary        *TaskState   `json:"summary,omitempty"`
	CompactedTurns int          `json:"compacted_turns,omitempty"`
	Turns          []sqliteTurn `json:"turns,omitempty"`
	ToolState      ToolState    `json:"tool_state"`
}

type sqliteTurn struct {
	UserInput    string          `json:"user_input"`
	ToolMessages json.RawMessage `json:"tool_messages,omitempty"`
	Answer       string          `json:"answer"`
}

func encodeSnapshot(state Session) ([]byte, error) {
	snapshot := sqliteSnapshot{
		Summary:        state.History.Summary,
		CompactedTurns: state.History.CompactedTurns,
		ToolState:      state.ToolState,
	}
	for _, turn := range state.History.Turns {
		stored := sqliteTurn{UserInput: turn.UserInput, Answer: turn.Answer}
		if len(turn.ToolMessages) > 0 {
			// 工具参数使用模型消息中的字符串格式，保留模型发出的无效 JSON。
			stored.ToolMessages = model.MarshalMessages(turn.ToolMessages)
		}
		snapshot.Turns = append(snapshot.Turns, stored)
	}
	return json.Marshal(snapshot)
}

func decodeSnapshot(data []byte) (Session, error) {
	var snapshot sqliteSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Session{}, err
	}
	if snapshot.CompactedTurns < 0 || snapshot.CompactedTurns > len(snapshot.Turns) {
		return Session{}, errors.New("invalid compacted turns count")
	}
	state := Session{
		History: History{
			Summary:        snapshot.Summary,
			CompactedTurns: snapshot.CompactedTurns,
		},
		ToolState: snapshot.ToolState,
	}
	for _, stored := range snapshot.Turns {
		turn := Turn{UserInput: stored.UserInput, Answer: stored.Answer}
		if len(stored.ToolMessages) > 0 {
			messages, err := model.UnmarshalMessages(stored.ToolMessages)
			if err != nil {
				return Session{}, fmt.Errorf("decode tool messages: %w", err)
			}
			turn.ToolMessages = messages
		}
		state.History.Turns = append(state.History.Turns, turn)
	}
	return state, nil
}

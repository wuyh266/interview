package tool

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool/protocol"
)

const mockSearchResultLimit = 5

// SearchTool 在本地模拟语料中搜索资料。
type SearchTool struct{}

type SearchResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	URL     string `json:"url"`
}

type SearchResponse struct {
	Query   string         `json:"query"`
	Mock    bool           `json:"mock"`
	Results []SearchResult `json:"results"`
}

func (SearchTool) Spec() protocol.ToolSpec {
	return protocol.ToolSpec{
		Name:        SearchToolName,
		Description: "搜索本地模拟资料，包含天气示例、Go、Agent 和工作文档。每次返回最多 5 条带 mock 标记的结果。",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {"type": "string", "pattern": "` + nonBlankToolStringPattern + `"}
			},
			"required": ["query"],
			"additionalProperties": false
		}`),
	}
}

func (SearchTool) Execute(ctx context.Context, _ *session.ToolState, arguments json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	fields, err := decodeToolFields(arguments, "query")
	if err != nil {
		return "", err
	}
	query, err := readToolString(fields, "query")
	if err != nil {
		return "", err
	}
	query = strings.TrimFunc(query, isToolSpace)
	words := strings.FieldsFunc(strings.ToLower(query), isToolSpace)
	phrase := strings.Join(words, " ")

	type match struct {
		result SearchResult
		score  int
	}
	var matches []match
	for _, doc := range mockSearchDocuments {
		if score := doc.score(words, phrase); score > 0 {
			matches = append(matches, match{result: doc.result, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].score > matches[j].score })

	count := min(len(matches), mockSearchResultLimit)
	response := SearchResponse{Query: query, Mock: true, Results: make([]SearchResult, 0, count)}
	for _, match := range matches[:count] {
		response.Results = append(response.Results, match.result)
	}
	output, err := json.Marshal(response)
	return string(output), err
}

func (d searchDocument) score(words []string, phrase string) int {
	score := 0
	for _, word := range words {
		switch {
		case strings.Contains(d.title, word):
			score += 2
		case strings.Contains(d.searchable, word):
			score++
		default:
			return 0
		}
	}
	if strings.Contains(d.title, phrase) {
		score += len(words)
	}
	return score
}

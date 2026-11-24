package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestMockSearchFindsEveryFixture(t *testing.T) {
	if len(mockSearchDocuments) < 500 {
		t.Fatalf("got %d mock documents, want at least 500", len(mockSearchDocuments))
	}
	seen := make(map[string]bool, len(mockSearchDocuments))
	for _, doc := range mockSearchDocuments {
		if seen[doc.result.ID] {
			t.Fatalf("duplicate mock result ID %q", doc.result.ID)
		}
		seen[doc.result.ID] = true
		t.Run(doc.result.ID, func(t *testing.T) {
			query := "  " + strings.ToUpper(doc.result.Title) + "  "
			response := runSearch(t, query)
			if !response.Mock || len(response.Results) == 0 || response.Results[0] != doc.result {
				t.Fatalf("search %q returned %+v, want first result %+v", query, response, doc.result)
			}
		})
	}
}

func TestMockSearchQueries(t *testing.T) {
	for _, test := range []struct {
		query       string
		firstPrefix string
		count       int
	}{
		{query: "Beijing   weather", firstPrefix: "北京天气", count: mockSearchResultLimit},
		{query: "GO CONTEXT", firstPrefix: "Go Context", count: mockSearchResultLimit},
		{query: "武汉 2026-04-30", firstPrefix: "武汉天气 2026-04-30", count: 1},
		{query: "项目周报", firstPrefix: "项目周报", count: mockSearchResultLimit},
		{query: "不存在的一条模拟记录", count: 0},
	} {
		response := runSearch(t, test.query)
		if !response.Mock || response.Results == nil || len(response.Results) != test.count {
			t.Fatalf("search %q returned %+v, want %d mock results", test.query, response, test.count)
		}
		if test.count > 0 && !strings.HasPrefix(response.Results[0].Title, test.firstPrefix) {
			t.Errorf("search %q first title is %q, want prefix %q", test.query, response.Results[0].Title, test.firstPrefix)
		}
	}
	if _, err := (SearchTool{}).Execute(context.Background(), nil, json.RawMessage(`{"query":"\u3000"}`)); !errors.Is(err, ErrInvalidToolArguments) {
		t.Errorf("blank search returned %v, want ErrInvalidToolArguments", err)
	}
}

func runSearch(t *testing.T, query string) SearchResponse {
	t.Helper()
	arguments, err := json.Marshal(SearchArgs{Query: query})
	if err != nil {
		t.Fatal(err)
	}
	output, err := (SearchTool{}).Execute(context.Background(), nil, arguments)
	if err != nil {
		t.Fatal(err)
	}
	var response SearchResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

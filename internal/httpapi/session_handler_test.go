package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/minimal-agent/internal/agent"
	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
	"example.com/minimal-agent/internal/tool/protocol"
)

type todoModel struct{}

const todoReasoning = "已收到待办工具结果。"

func (todoModel) Chat(_ context.Context, request model.ChatRequest) (model.ChatResponse, error) {
	last := request.Messages[len(request.Messages)-1]
	if last.Role == model.RoleTool {
		return model.ChatResponse{Content: last.Content, ReasoningContent: todoReasoning}, nil
	}
	args := tool.TodoArgs{Action: tool.TodoList}
	if text, add := strings.CutPrefix(last.Content, "添加待办 "); add {
		args = tool.TodoArgs{Action: tool.TodoAdd, Text: text}
	}
	arguments, err := json.Marshal(args)
	if err != nil {
		return model.ChatResponse{}, err
	}
	return model.ChatResponse{ToolCalls: []protocol.ToolCall{{
		ID: "todo-1", Name: tool.TodoToolName, Arguments: arguments,
	}}}, nil
}

func sqliteSessionAPI(t *testing.T, path string) (http.Handler, *session.SQLiteStore) {
	t.Helper()
	store, err := session.OpenSQLiteStore(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	tools, err := tool.NewBuiltinTools()
	if err != nil {
		t.Fatal(err)
	}
	runtime := agent.NewRuntime(todoModel{}, tools, store, agent.Config{
		MaxIterations: 2,
		ContextLimits: session.ContextLimits{StepTimeout: time.Second},
	})
	return NewHandler(runtime), store
}

func TestSessionAPIIsolatesAndRestoresWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "会话 #1", "sessions.sqlite3")
	handler, store := sqliteSessionAPI(t, path)
	browser := newBrowser(t)
	browser.Transport = handlerTransport{handler}
	other := newBrowser(t)
	other.Transport = handlerTransport{handler}
	base := "http://agent.test" + sessionsPath

	var first, second, foreign sessionView
	requestAPI(t, browser, http.MethodPost, base, "", http.StatusCreated, &first)
	requestAPI(t, browser, http.MethodPost, base, "", http.StatusCreated, &second)
	requestAPI(t, other, http.MethodPost, base, "", http.StatusCreated, &foreign)
	if !validToken(first.SessionID) || !validToken(second.SessionID) || !validToken(foreign.SessionID) ||
		first.SessionID == second.SessionID || first.SessionID == foreign.SessionID || second.SessionID == foreign.SessionID {
		t.Fatalf("unexpected created session IDs: %q, %q, %q", first.SessionID, second.SessionID, foreign.SessionID)
	}
	if first.Turns == nil || len(first.Turns) != 0 {
		t.Fatalf("new session did not return an empty turns array: %+v", first)
	}
	firstURL := base + "/" + first.SessionID
	secondURL := base + "/" + second.SessionID
	var firstReply, secondReply messageView
	requestAPI(t, browser, http.MethodPost, firstURL+"/messages", `{"text":"添加待办 查天气"}`, http.StatusOK, &firstReply)
	requestAPI(t, browser, http.MethodPost, secondURL+"/messages", `{"text":"添加待办 写周报"}`, http.StatusOK, &secondReply)
	if firstReply.SessionID != first.SessionID || firstReply.RunID == "" || firstReply.Answer != `{"action":"add","text":"查天气"}` ||
		secondReply.SessionID != second.SessionID || secondReply.Answer != `{"action":"add","text":"写周报"}` ||
		firstReply.ReasoningContent != todoReasoning || secondReply.ReasoningContent != todoReasoning {
		t.Fatalf("unexpected tool replies: %+v, %+v", firstReply, secondReply)
	}

	// 关闭原存储，建立新的 Runtime 和工具注册表，再恢复原会话。
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, _ := sqliteSessionAPI(t, path)
	transport := handlerTransport{reopened}
	browser.Transport = transport
	other.Transport = transport
	reconnected := &http.Client{Jar: browser.Jar, Transport: transport, Timeout: 5 * time.Second}
	for _, window := range []struct {
		url   string
		added string
		id    string
	}{
		{url: firstURL, added: "查天气", id: first.SessionID},
		{url: secondURL, added: "写周报", id: second.SessionID},
	} {
		var restored sessionView
		requestAPI(t, reconnected, http.MethodGet, window.url, "", http.StatusOK, &restored)
		if restored.SessionID != window.id || len(restored.Turns) != 1 || restored.Turns[0].UserInput != "添加待办 "+window.added ||
			restored.Turns[0].ReasoningContent != todoReasoning {
			t.Fatalf("unexpected history for %s: %+v", window.id, restored)
		}
		var reply messageView
		requestAPI(t, reconnected, http.MethodPost, window.url+"/messages", `{"text":"查看待办"}`, http.StatusOK, &reply)
		if reply.Answer != `["`+window.added+`"]` {
			t.Fatalf("unexpected todos for %s: %s", window.id, reply.Answer)
		}
	}

	requestAPI(t, other, http.MethodGet, firstURL, "", http.StatusNotFound, nil)
	requestAPI(t, other, http.MethodPost, firstURL+"/messages", `{"text":"查看待办"}`, http.StatusNotFound, nil)
	requestAPI(t, browser, http.MethodPost, firstURL+"/messages", `{"text":"查看待办","user_id":"forged"}`, http.StatusBadRequest, nil)
	requestAPI(t, browser, http.MethodPost, firstURL+"/messages", `{"text":"查看待办"}{}`, http.StatusBadRequest, nil)
}

func TestSessionAPIRestoresBothFirstWindows(t *testing.T) {
	handler := NewHandler(agent.NewRuntime(nil, nil, &session.MemoryStore{}, agent.Config{}))
	origin := &url.URL{Scheme: "http", Host: "example.test", Path: sessionsPath}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for range 2 {
		// 两个首次建窗请求都在浏览器收到第一个 Cookie 之前发出。
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, origin.String(), nil))
		response := recorder.Result()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("first-window creation returned %d", response.StatusCode)
		}
		cookies := response.Cookies()
		if len(cookies) != 2 || cookies[0].MaxAge != ownerCookieAge || cookies[1].MaxAge != ownerCookieAge {
			t.Fatalf("expected persistent browser and session cookies, got %v", cookies)
		}
		jar.SetCookies(origin, cookies)
		paths = append(paths, response.Header.Get("Location"))
		response.Body.Close()
	}
	for _, path := range paths {
		target := &url.URL{Scheme: origin.Scheme, Host: origin.Host, Path: path}
		request := httptest.NewRequest(http.MethodGet, target.String(), nil)
		for _, cookie := range jar.Cookies(target) {
			request.AddCookie(cookie)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("restore %s returned %d: %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

type handlerTransport struct{ handler http.Handler }

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	transport.handler.ServeHTTP(recorder, request)
	return recorder.Result(), nil
}

func newBrowser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, Timeout: 5 * time.Second}
}

func requestAPI(t *testing.T, browser *http.Client, method, target, body string, status int, output any) {
	t.Helper()
	request, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := browser.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		message, _ := io.ReadAll(response.Body)
		t.Fatalf("%s %s returned %d, want %d: %s", method, target, response.StatusCode, status, message)
	}
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			t.Fatal(err)
		}
	}
}

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"example.com/minimal-agent/internal/agent"
	"example.com/minimal-agent/internal/httpapi"
	"example.com/minimal-agent/internal/model"
	"example.com/minimal-agent/internal/session"
	"example.com/minimal-agent/internal/tool"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	serve := len(args) == 1 && args[0] == "--serve"
	question := strings.TrimSpace(strings.Join(args, " "))
	if question == "" {
		fmt.Fprintln(os.Stderr, `用法：go run ./cmd "你的问题"`)
		fmt.Fprintln(os.Stderr, `启动会话接口：go run ./cmd --serve`)
		return 1
	}

	chatModel, err := model.NewDeepSeekModel(os.Getenv("DEEPSEEK_API_KEY"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建模型失败：", err)
		return 1
	}

	tools, err := tool.NewBuiltinTools()
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建工具失败", err)
		return 1
	}
	ctx := context.Background()
	dbPath := strings.TrimSpace(os.Getenv("AGENT_SQLITE_PATH"))
	if dbPath == "" {
		dbPath = "data/sessions.sqlite3"
	}
	store, err := session.OpenSQLiteStore(ctx, dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "打开会话数据库失败：", err)
		return 1
	}
	defer store.Close()

	runtime := agent.NewRuntime(chatModel, tools, store, agent.Config{
		MaxIterations: 5,
		ContextLimits: session.ContextLimits{
			StepTimeout: 60 * time.Second,
		},
	})
	if serve {
		mux := http.NewServeMux()
		mux.Handle("/api/", httpapi.NewHandler(runtime))
		mux.Handle("/", http.FileServer(http.Dir("frontend/dist")))
		server := &http.Server{
			Addr:              "127.0.0.1:8080",
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		fmt.Fprintln(os.Stderr, "会话接口监听 http://"+server.Addr)
		if err := server.ListenAndServe(); err != nil {
			fmt.Fprintln(os.Stderr, "会话接口启动失败", err)
			return 1
		}
		return 0
	}

	result, err := runtime.Run(ctx, agent.RunInput{
		Session: session.SessionKey{UserID: "local", SessionID: "cli"},
		Text:    question,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "运行失败：", err)
		return 1
	}

	fmt.Println(result.Answer)
	return 0
}

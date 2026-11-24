# Minimal Agent

一个用 Go、React 和 SQLite 实现的多会话 Agent。Go 后端接入 DeepSeek，由模型决定直接回答还是调用计算器、本地搜索和待办工具。关闭进程后，同一个会话仍可从 SQLite 恢复。

## 运行方式

需要 Go 1.22 或更高版本、DeepSeek API Key。运行 Web 界面还需要 Node.js 20.19+（20.x）或 22.12+，以及 npm。以下命令从仓库根目录执行，使用 PowerShell。

```powershell
$env:DEEPSEEK_API_KEY = '替换为你的 DeepSeek API Key'
npm --prefix frontend ci
npm --prefix frontend run build
go run ./cmd --serve
```

打开 [http://127.0.0.1:8080](http://127.0.0.1:8080)。Go 服务同时提供会话 API 和 `frontend/dist` 中的静态页面。前端开发时可保持 Go 服务运行，在另一个终端执行 `npm --prefix frontend run dev`，然后打开 Vite 输出的地址。

也可以直接从命令行运行，无需安装前端依赖。两次命令使用同一个 CLI 会话 `local/cli`，适合验证保存和续聊。

```powershell
go run ./cmd '用 todo 工具添加一条待办：复习 SQLite 持久化'
go run ./cmd '用 todo 工具列出当前会话的待办'
```

| 环境变量 | 用途 | 默认值 |
| --- | --- | --- |
| `DEEPSEEK_API_KEY` | Go 后端请求 DeepSeek 时使用的 API Key | 必填 |
| `AGENT_SQLITE_PATH` | 会话数据库路径，数据库和父目录会在首次启动时创建 | `data/sessions.sqlite3` |
| `AGENT_API_ORIGIN` | Vite 开发服务器转发 `/api` 请求的目标 | `http://127.0.0.1:8080` |

运行后端测试使用 `go test ./...`。

## 系统设计

| 模块 | 职责 |
| --- | --- |
| [`cmd`](cmd/main.go) | 组装模型、工具注册表、SQLite 会话仓库和 Runtime，提供 CLI 与 HTTP 启动入口 |
| [`frontend`](frontend/) | 提供会话列表、新建会话、消息发送和历史恢复。浏览器本地存储保存会话索引、标题和更新时间 |
| [`internal/httpapi`](internal/httpapi/session_handler.go) | 提供会话接口，使用浏览器 Cookie 验证会话归属 |
| [`internal/agent`](internal/agent/runtime.go) | 组织 Agent 循环、限制迭代次数、校验并执行工具调用、提交成功完成的轮次 |
| [`internal/model`](internal/model/deepseek_model.go) | 请求 DeepSeek Chat Completions，解析最终答复 `content` 和原生 `tool_calls` |
| [`internal/tool`](internal/tool/tool_registry.go) | 注册工具的名称、描述和参数 JSON Schema，按名称分派执行 |
| [`internal/session`](internal/session/) | 读写 SQLite 会话快照，构建模型消息并压缩超出预算的历史 |

每条用户消息触发一次 `Runtime.Run`。每次模型调用会收到对话消息与已注册工具的 Schema。模型直接回复时，Runtime 保存最终答复并结束本轮。模型请求工具时，Runtime 保存含 `tool_calls` 的 `assistant` 消息，再把各工具的结果或错误作为 `tool` 消息追加，使用 `tool_call_id` 标明对应的调用，然后再次请求模型。默认最多执行 5 次循环中的模型调用，每次调用超时 60 秒。触发历史压缩时会额外调用一次模型处理摘要，摘要调用使用同样的超时设置。

| 工具 | 参数示例 | 行为 |
| --- | --- | --- |
| `calculator` | `{"expression":"(12.5 + 7.5) / 4"}` | 计算支持括号的四则表达式 |
| `search` | `{"query":"Redis"}` | 在代码内置的 mock 文档中搜索 |
| `todo` | `{"action":"add","text":"复习 SQLite"}` 或 `{"action":"list"}` | 添加或列出当前会话的待办 |

| HTTP 接口 | 用途 |
| --- | --- |
| `POST /api/sessions` | 创建会话，返回 `session_id` 和空的 `turns` |
| `GET /api/sessions/{sessionID}` | 恢复已保存的用户消息和最终答复 |
| `POST /api/sessions/{sessionID}/messages` | 接收 `{"text":"..."}`，运行 Agent 并返回 `session_id`、`run_id` 和 `answer` |

SQLite 的 `sessions` 表以 `(user_id, session_id)` 为主键。同一浏览器新建的会话有各自的历史、摘要和待办。进程内按会话串行执行轮次，写入时通过 `revision` 检查并发更新，避免覆盖已提交的结果。

## Memory 的召回时机与放置方式

### 什么时候召回

1. **打开已有会话时**，页面调用恢复接口，把 SQLite 中仍保留的用户消息和答复显示到界面。这个请求只读取数据库。
2. **发送一条新消息时**，`Runtime.Run` 通过 `SessionStore.Update` 按会话主键读取快照。它取出已保存的结构化摘要和未压缩的历史轮次，附上当前用户输入，构建上下文窗口。每次续聊都会按会话 ID 读取已有记忆。
3. **每次循环调用模型前**，`StructuredCompactor` 检查轮次和消息大小。默认最多保留 20 个原始轮次，包含正在处理的这一轮。对话消息序列化后的预算为 128 KiB，工具定义另行传递。超出预算时，从最旧的完整轮次开始移出原文，由模型把这些轮次与旧摘要合并为新的结构化摘要。
4. **模型调用 `todo` 时**，工具从当前会话的 `ToolState.Todos` 读取或更新待办。待办列表通过 `todo list` 的工具结果进入上下文，历史轮次和摘要中也可能保留此前提到的待办内容。

### 放在模型输入的哪里

| 顺序 | 消息角色 | 内容 |
| --- | --- | --- |
| 1 | `system` | 会话已有摘要时，在消息首部放入结构化状态的 JSON。摘要记录任务目标、用户约束、已完成步骤、待办事项和带来源的关键证据 |
| 2 | `user`、`assistant`、`tool` | 按时间顺序放入未压缩的完整历史轮次，每轮包含用户输入、可能出现的工具调用及结果、最终答复 |
| 3 | `user` | 本轮用户输入 |
| 4 | `assistant`、`tool` | 如果本轮已执行工具，接着放入工具调用及其结果，再请求模型作答 |

例如，新会话的第一条消息要求添加待办时，首次模型请求由该 `user` 消息开始。如果模型选择 `todo add`，下一次请求会在这条 `user` 消息后附上工具调用和结果。以后同一会话继续聊天时，这段交互会作为历史输入，直到它被合并进位于开头的 `system` 摘要。

### 保存在数据库的哪里

轮次成功结束后，摘要、原始轮次（含工具消息）、已压缩轮次计数 `compacted_turns` 和工具状态作为 JSON 写入 `sessions.state`。已进入摘要的原始轮次在保留期内仍可供页面展示。模型输入使用 `compacted_turns` 跳过这段原文，避免同一段历史被重复传入。运行失败时，本轮的对话和待办改动不会提交。

相关实现位于 [`Runtime.Run`](internal/agent/runtime.go)、[`ContextWindow.Messages`](internal/session/context.go)、[`StructuredCompactor`](internal/session/compactor.go) 和 [SQLite 快照编解码](internal/session/sqlite_snapshot.go)。

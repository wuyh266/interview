# AI Prompt 与问题解决记录

本文整理 Minimal Agent 开发过程中影响实现的几次人机问答。保留能确认的提问原文，合并多轮追问时标为「提问整理」。解法和验证结果以当前代码为准。


## 1. 工具怎么选，参数错误怎么处理

**提问整理**

> 至少实现 calculator、search（可 mock）和一个自定义工具。工具要有名称、描述和参数 Schema，由 LLM 决定是否调用。执行后还要能继续循环，并留下 Trace。

**处理过程**

启动时把 `calculator`、`search`、`todo` 注册到 `Registry`。Runtime 通过 `Defs()` 取得定义，DeepSeek 请求携带 `tools` 和 `tool_choice: "auto"`。后端根据返回的工具名查注册表，检查参数是否为合法的 JSON 对象，再由各工具校验必填字段和类型。

工具失败时，Runtime 按原调用的 `tool_call_id` 回传错误，模型可以据此调整参数或向用户解释。Trace 可以用 `run_id` 串起同一次运行的工具尝试，记录调用编号、轮次、参数、是否执行、耗时、输出和错误。

相关实现 · [工具注册表](../internal/tool/tool_registry.go) · [工具 Trace](../internal/trace/trace.go) · [原生工具调用测试](../internal/agent/runtime_test.go)

## 2. 两个窗口如何隔离，之前的 mock 数据存在哪里

**用户提问**

> 我不希望再内存里面进行对话的记忆，我希望你能够用sqlite作为一个持久化的记忆

> 那之前mock的数据存放在哪里

**处理过程**

运行入口接入 `SQLiteStore`，默认数据库为 `data/sessions.sqlite3`。`sessions` 表以 `(user_id, session_id)` 为联合主键，`state` 保存历史、摘要和工具状态。同一浏览器的「查天气」与「写周报」窗口有各自的历史和待办。进程内同一会话的更新串行执行，写入时用 `revision` 防止覆盖其他进程已经提交的结果。模型超时或迭代耗尽时，失败轮次的历史和待办改动均不提交。

`search` 使用 `search_mock.go` 在进程启动时构建的本地模拟文档。会话的用户输入、回复和待办状态保存在 SQLite。

相关实现 · [SQLite 会话存储](../internal/session/sqlite.go) · [SQLite 快照](../internal/session/sqlite_snapshot.go) · [mock 搜索数据](../internal/tool/search_mock.go) · [双窗口恢复测试](../internal/httpapi/session_handler_test.go)

## 3. 续聊时什么时候读取记忆，怎么放进上下文

**提问整理**

> 给我讲解 `r.sessions.Update(...)` 这段代码。纯对话追问和带工具的追问怎么做？用户输入、工具执行结果和 Agent 思考过程，哪些放进 context 更合适？

**处理过程**

每次续聊时，`Runtime.Run` 根据会话标识读取 SQLite 快照。它用 `Turns[CompactedTurns:]` 取尚未压缩的原始轮次，将本次输入放进 `Current`。模型收到的消息顺序是摘要（`system`）、未压缩的历史、本次用户消息、当前轮产生的助手工具调用和工具结果。请求通过 `tool_call_id` 保留每次调用及结果的对应关系。读取历史的恢复接口只加载会话快照。

验收用例先让 Agent 记住「每箱 12 件」，再追问「刚才说每箱装几件」，最后要求按原数量计算 3 箱并记录待办。测试检查传给模型的消息和最终写入的「准备 36 件货」，分别覆盖纯对话追问和工具追问。

相关实现 · [组装上下文窗口](../internal/agent/runtime.go) · [构建消息顺序](../internal/session/context.go) · [追问验收测试](../internal/agent/runtime_acceptance_test.go)

## 4. 长对话压缩要留下什么

**用户提问**

> 为这个项目实现长上下文压缩，要求策略是结构化状态，单独记录任务目标、用户约束、已完成步骤、待办和关键证据

**处理过程**

`StructuredCompactor` 检查轮次数和序列化消息的字节数。默认最多容纳 20 个原始轮次（含当前轮），消息预算为 128 KiB。超限时优先移出最旧的完整轮次，调用模型把它们与已有摘要合并。压缩请求使用独立的 `compactionPrompt`，要求输出包含 `task_goals`、`user_constraints`、`completed_steps`、`pending_tasks` 和 `key_evidence` 的 JSON，关键证据还要带来源。

摘要作为 `system` 消息放在后续对话开头。SQLite 通过 `CompactedTurns` 标记哪些原文已经合并进摘要。那些原始对话在保留期限内仍可用于恢复历史，下一次模型请求从标记后开始取原文，避免重复传入。

相关实现 · [结构化压缩及提示词](../internal/session/compactor.go) · [默认上下文限制](../internal/session/context.go) · [历史压缩测试](../internal/agent/runtime_history_test.go)



## 5. DeepSeek 的思考过程怎么提取和回传

**用户提问**

> 思考过程如何提取呢

> 改成可以提取思考过程的

**处理过程**

普通请求默认启用思考模式，从非流式响应的 `choices[0].message.reasoning_content` 读取文本。若一条助手消息含工具调用，Runtime 把思考文本和 `tool_calls` 存在同一条 `assistant` 消息中，紧接着追加 `tool` 消息并继续请求。最终答复及其思考文本也一起写入 SQLite，供重启后的续聊恢复。发送消息和恢复会话的 HTTP 响应在有内容时会返回最终答复的 `reasoning_content`，前端会话页目前展示答复正文。

旧轮次缺少该字段时，续聊会先将相关历史合并进摘要。若模型在当前工具轮次省略思考字段，适配器会关闭该次续接请求的思考模式，让工具结果仍能提交给模型。结构化压缩请求也使用关闭思考的 JSON 模式。

相关实现 · [提取与回传](../internal/model/deepseek_model.go) · [保存工具轮次](../internal/agent/runtime.go) · [会话接口](../internal/httpapi/session_handler.go)

## 8. 最终怎么验收

**用户要求**

> 构建测试用例，来测试以上功能

| 场景 | 检查结果 |
| --- | --- |
| 直接答复、纯对话追问、连续工具调用 | 检查模型收到的消息顺序，并验证计算器结果可继续用于写待办。 |
| 会话隔离、重启恢复 | 两个窗口的原文和待办分别恢复，另一个浏览器不能读取它们。 |
| 历史压缩 | 超限后旧轮次进入摘要，工具调用和结果保持配对，压缩失败时保留此前提交的状态。 |
| 异常与 Trace | 覆盖参数错误、上游限流、模型超时和迭代上限，检查失败轮次回滚及日志字段。 |
| 思考内容 | 模拟 DeepSeek 响应，检查提取、工具回传和 SQLite 重启续聊。 |

离线验证使用 `go test -count=1 ./...`，并运行了 `go vet ./...`。真实 DeepSeek 联调曾因执行环境无法解析 `api.deepseek.com` 中止，在线工具选择和字段回传仍待真实服务验证。

相关用例 · [Runtime 验收](../internal/agent/runtime_acceptance_test.go) · [DeepSeek 响应解析](../internal/model/deepseek_model_test.go) · [工具注册](../internal/tool/tool_registry_test.go)

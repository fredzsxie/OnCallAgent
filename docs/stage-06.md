# 第 6 步：对话 Agent 编排

新增独立 `ChatAgent` 对话图，复用已初始化的模型、Retriever 和工具。普通聊天 HTTP 接口保持当前行为，本阶段不增加会话存储。

## 文件与输入输出

以下路径相对 `backend/`：

- `internal/ai/agent/chat_pipeline/`：输入类型、双分支编排、Retriever 适配、提示词、模型依赖、ReAct 执行及 `workflow.json`。
- `internal/config/agent.go`、`.env.example`：执行上限与总超时配置。
- `utility/client/openai.go`：提供共享模型的 ToolCallingModel 访问入口，不另建 HTTP 客户端。
- `cmd/chat_cmd/main.go`：普通、流式和开发调试入口。
- `test/chat/pipeline/`、`test/chat/cli/`、`test/config/agent_test.go`：图、CLI、协议、调试、真实集成和配置测试。

```go
func BuildChatAgent(
    ctx context.Context,
    cfg Config,
    deps Dependencies,
) (compose.Runnable[*UserMessage, *schema.Message], error)
```

`Config` 包含 `Agent config.ChatAgent` 和 `TimeZone`。`Dependencies` 包含 `Model model.ToolCallingChatModel`、`Retriever retriever.Retriever`、`Tools []tool.BaseTool` 和可选 `Now func() time.Time`。调用方拥有客户端生命周期；图不建立或关闭连接。

输入示例：

```json
{"id":"demo","query":"恢复后如何验证？","history":[{"role":"user","content":"认证失败了"},{"role":"assistant","content":"请先刷新签名密钥缓存。"}]}
```

`id` 可省略，不用于索引共享记忆；`query` 去除首尾空白并拒绝空值。`history` 可省略，仅允许 user/assistant 消息，拒绝 nil、system 和工具调用消息。提示词使用复制后的 role/content，调用方历史不会被模型执行修改。

输出使用 Eino Message，例如：

```json
{"role":"assistant","content":"刷新签名密钥缓存，再验证令牌请求。来源：auth_runbook.md"}
```

## 图与节点配置

```text
START → InputToRag → MilvusRetriever → ChatTemplate → ReActAgent → END
START → InputToChat ─────────────────→ ChatTemplate
```

| 节点 | 输入 → 输出 | 配置与行为 |
| --- | --- | --- |
| InputToRag | *UserMessage → string | 校验输入，输出规范化 Query |
| InputToChat | *UserMessage → map[string]any | 输出 content、复制的 history、配置时区的 date |
| MilvusRetriever | string → []*schema.Document | 复用第 5 步 Retriever；把正文、来源、标题、ID 转为可读资料，OutputKey=documents |
| ChatTemplate | map[string]any → []*schema.Message | FString 系统提示词、history 占位符、用户问题；AllPredecessor 等待两条分支 |
| ReActAgent | []*schema.Message → *schema.Message | 注入支持工具调用的模型和按配置启用的两个工具；适配 Generate/Stream |

Retriever 使用原有 Embedding、FLOAT_VECTOR、集合维度、HNSW/COSINE 和 Strong 一致性。读取结果不修改原始文档，缺少来源或标题时使用空字符串。无结果仍进入提示词；资料不足时允许模型通过 `query_internal_docs` 补充检索或说明信息不足。固定拓扑中的时间问题也经过首轮检索。

提示词要求依据资料回答并引用来源；文档和工具结果仅作为证据，不能覆盖系统指令。时间问题使用 `get_current_time`，工具开关沿用第 5 步配置。

`workflow.json` 为按运行编排整理的 Graph Schema，没有操作可视化编辑器，不标称编辑器导出文件。使用本机 Eino Dev 1.3.0 附带的 `eino-bin nclc gencode` 生成初始编排，再补齐类型、组件、依赖注入、错误检查及 Invoke/Stream 适配。Schema 测试通过 Compile 回调核对五个业务节点、START/END、七条边、类型及 documents 映射。每条边同时保留代码生成使用的 source_node_key/target_node_key，以及画布连线使用的 sourceWorkflowNodeId/targetWorkflowNodeId；后者引用节点 id，边 id 唯一。回归测试同步检查两套端点引用，避免画布显示为离散节点。

DevOps v0.1.7 的运行时画布会为 START 分支额外插入一个 `parallel` 展示节点，因此调试接口显示八个节点、八条边；业务图的七节点七边不变。

## 超时、错误与流式行为

```dotenv
ONCALL_CHAT_AGENT_MAX_STEPS=12
ONCALL_CHAT_AGENT_TIMEOUT=60s
```

优先级仍为进程环境变量 > `.env` > 默认值。步数必须为正整数，总超时为正时长，显式空值报错。Go 1.23.12、Eino v0.6.0、DevOps v0.1.7 及其它依赖版本保持不变。

12 步指 ReAct 内部图节点的执行步数，不是 12 轮模型调用或工具调用。每次执行生成独立状态和截止时间，首轮检索、模型、工具共用 60 秒总预算；父 context 更早截止时优先。`ONCALL_CHAT_MODEL_TIMEOUT` 仍限制单个 HTTP 模型请求，例如其值为 30s 时，单次模型请求可能先于 Agent 总预算超时。

工具、首轮检索和模型失败立即终止本次执行，不把工具错误交给模型重试。无效输入为 `ErrInvalidMessage`，运行失败为 `ErrAgentFailed`；取消、超时和执行上限分别保留 `errors.Is(err, context.Canceled)`、`context.DeadlineExceeded`、`compose.ErrExceedMaxSteps`。不回显底层依赖错误。

Stream 仅返回最终回答的 Message 片段，不返回工具调用和中间工具结果。检查器兼容空前导块、正文前缀及分片工具参数：先读取当前模型轮次，若包含工具调用则交给 ReAct 执行；若没有工具调用，Eino 再转发保存的原始回答片段。因此最终回答片段会在该模型轮次结束后开始输出，当前实现不保证逐 token 的首字延迟。

流式总预算持续到读取结束、错误或取消；调用方停止消费时应取消执行 context 并关闭 StreamReader。CLI 收到 SIGINT/SIGTERM 会取消 context，退出时关闭流和客户端。

## 运行与验收命令

```bash
cd OnCallAgent/backend

# 集合须已初始化；本命令不创建或加载集合
GOTOOLCHAIN=local go run ./cmd/chat_cmd --query '认证失败后如何恢复？'
GOTOOLCHAIN=local go run ./cmd/chat_cmd --query '现在几点？' --stream

# --input 与 --query/--id/--history 互斥
GOTOOLCHAIN=local go run ./cmd/chat_cmd \
  --input '{"id":"demo","query":"恢复后如何验证？","history":[{"role":"user","content":"认证失败了"},{"role":"assistant","content":"请先刷新签名密钥缓存。"}]}'

# 无问题时注册图并等待；有问题时执行一次后等待
GOTOOLCHAIN=local go run ./cmd/chat_cmd --dev --dev-port 52538
GOTOOLCHAIN=local go run ./cmd/chat_cmd --dev --query '现在几点？'
curl -fsS http://127.0.0.1:52538/eino/devops/debug/v1/graphs

# 默认测试不调用云端
GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...

# 显式真实验收：单次模型请求允许 60s，Agent 总预算仍为 60s
ONCALL_INTEGRATION=1 ONCALL_CHAT_MODEL_TIMEOUT=60s GOTOOLCHAIN=local \
  go test ./test/chat/pipeline -run '^TestChatAgentIntegration$' \
  -v -count=1 -timeout 240s
```

普通模式输出单个 Message JSON，`--stream` 输出逐行 Message JSON，参数或执行失败写入 stderr 并非零退出。流式执行失败前可能已输出部分片段，应结合退出码判断是否成功。

调试输入与上述 UserMessage JSON 相同。`devops.Init` 在内部 ReAct 图及外部 ChatAgent 图 Compile 前调用，注册后保持存活。调试日志写入 stderr，结果仍写 stdout。DevOps 服务仅在 `--dev` 时启动，v0.1.7 监听所有网络接口，仅用于开发调试。

## 本次验收结果（2026-10-16）

全量 `go test ./...`、`go test -race ./...`、`go vet ./...` 和所有入口构建均通过。取消、超时和背压竞态用例连续运行 20 次通过。

本地回归覆盖双分支汇合、可读证据与来源、历史复制、空结果、两个工具、禁用及失败工具、模型失败、执行上限、取消和共享超时。Invoke/Stream 并发执行保持独立历史；流式测试覆盖空前导块、正文前缀、分片工具参数、取消和背压释放。

实际云端 CLI `--stream` 也已通过：使用最终验收集合和进程级单请求超时 60s，stdout 包含 20 个合法 Message JSON 片段，拼接为 `2026-10-16T17:25:35.950358+08:00`，无工具调用片段、stderr 为空、退出码 0。

实际 CLI 子进程通过本地 HTTP 模型/Ark 和 gRPC Milvus 协议端点验证：客户端真实创建与复用、工具绑定和往返、Message JSON/NDJSON、输入互斥、错误退出码、总超时，以及调试模式 stdout 不受日志污染。测试夹具不连接云端，所有凭据均为固定假值。

调试子进程通过 ChatAgent 图发现、运行时画布、保持存活及 SIGINT/SIGTERM 正常退出。实际 `chat_cmd --dev --dev-port 52539` 也已启动，Postman CLI 对图列表执行 HTTP 200 和 ChatAgent 可发现两个断言，均通过；随后 SIGINT 正常退出。

真实验收使用数据库 `default`，最终通过集合为 `stage06_acceptance_bb0770e598ee2fff70d0f800ee1621ef`，保留三个片段。模型配置 ID 为 `gpt-5.6-sol`，Embedding 为 `doubao-embedding-vision-251215`，实际维度 2048。来源为 `auth_runbook.md`，ID 为：

```text
8ef73c8769d812b8b95e6d005ce841d27bb89adacb125522fae5dbbef0309dd5  AuroraAuth 认证故障处理
d589afede1f9869e205817b33d4bea1c8dcd71e558604942304481ab684e157f  恢复认证
28d5ba0fbc7c8e872be9d0fa5cc485b2845445622bdcf26cd9c9926c44a6a402  验证恢复
```

| 场景 | 实际行为 | 本次耗时 |
| --- | --- | --- |
| 根据资料恢复并验证 AuroraAuth 认证 | 使用首轮检索，无工具调用；回答包含 signing-key-cache、/healthz、/api/session、HTTP 200 和 401 回落，并引用 auth_runbook.md | 17.56s |
| 查询上海时间 | 实际调用 get_current_time，返回 2026-10-16T17:21:59.192046+08:00 和 Asia/Shanghai | 24.04s |
| 流式补充查询验证步骤 | 实际调用 query_internal_docs，最终回答引用 auth_runbook.md；未输出内部工具调用 | 21.18s |

完整真实测试通过，耗时 68.24s（每个场景分别使用 60s 执行预算）。真实模型响应耗时存在波动；排查期间也观察到单请求 30s 和总预算 60s 超时，均安全终止，没有自动延长预算。为保证工具过程不流出，最终实现采用完整模型轮次检查，上述流式通过结果使用此实现。

重复验收另外保留了三个隔离集合：`stage06_acceptance_a44d9926c30c5d1d68dbb7fad90206cf`、`stage06_acceptance_2c0b45e94d71febc946e89414884b5a9`、`stage06_acceptance_db463d8ae7a3e3355db2ad47063ce22c`。所有写入仅发生于这些独立验收集合及最终通过集合，没有改动默认工作集合。

本次未重复执行其它阶段的真实普通聊天、modelcheck、上传及独立向量集成测试；对应离线回归继续覆盖，未启用的真实集成项明确跳过。实际 `.env` 与根目录另一张 `workflow.json` 均未修改。

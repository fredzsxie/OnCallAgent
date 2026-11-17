# 日志与排障

[文档索引](README.md) · [配置](configuration.md) · [API](api.md)

## 格式与输出

日志由项目独立实现，使用标准库格式化与文件输出，统一为易读文本。正文按“毫秒时间、四字符级别、代码位置、组件、事件、字段”排列，位置相对 `backend/`。以下行号和请求 ID 为示意：

```text
2026-11-17 08:00:00.125 [INFO] utility/middleware/logging.go:90 -> [HTTP] 请求开始 rid[example-request] method[POST] route[/api/chat]
2026-11-17 08:00:02.250 [INFO] internal/service/chat/chat.go:61 -> [Chat] 对话执行完成 rid[example-request] mode[invoke] history_committed[true] outcome[success] output_bytes[5] elapsed[2.12s]
2026-11-17 08:00:02.255 [INFO] utility/middleware/logging.go:125 -> [HTTP] 请求完成 rid[example-request] method[POST] route[/api/chat] code[200] elapsed[2.13s] outcome[success] req[{"Id":"demo","Question":"hello"}] rsp[{"data":{"answer":"hello"},"message":"OK"}] req_bytes[32] rsp_bytes[42]
2026-11-17 08:01:00.255 [ERRO] utility/middleware/logging.go:125 -> [HTTP] 请求未完成 rid[example-stream] method[POST] route[/api/chat_stream] code[200] elapsed[60s] outcome[timeout] error_type[timeout] req[{"Id":"demo","Question":"hello"}] rsp[<stream>] req_bytes[32] rsp_bytes[147]
```

时间使用进程本地时区并包含毫秒，耗时按量级显示 ms/s。代码位置表示日志从哪里发出，不是自动推断异常最初发生处；无法解析时为 `unknown:0`。普通与 `-trimpath` 构建均支持。直接 defer 在编译器优化下可能定位到执行它的 return 行。

Web 输出至 stderr 和每日文件 `log/oncall-YYYY-MM-DD.log`；从 `backend/` 启动时即为 `backend/log/`。CLI 运行日志默认只写文件，stdout/stderr 的业务协议不变。终端交互设备可为级别着色，文件与重定向输出无颜色；`NO_COLOR=1` 关闭终端颜色。

启动时 INFO 级别打印真实 API 路由表，含 METHOD、ROUTE、HANDLER 三列；仅标题显示代码位置，表格保留多行。请求日志保持单行，字段中的换行、右方括号、反斜杠和控制字符转义，避免伪造日志行。

## 请求关联

HTTP 请求沿用合法的 `X-Request-ID`，缺失或非法时生成 UUID，写入请求和响应 Header，以 `rid[...]` 贯穿本进程的服务、图、模型与工具日志。合法 ID 最长 128 字符，仅含英文字母、数字及 `-_.:`。CLI 每次执行生成 `run[...]`。

仅在 context 已有有效 trace ID 时附加 `trace[...]`。应用没有主动创建 span、解析 W3C 请求头、向上游传播追踪 Header 或导出链路；框架迁移后不保证自动生成 trace。客户端仅增加过程日志，查询、认证、超时与重试行为保持不变。

在 `backend/` 执行：

```bash
curl -i http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"Id":"log-demo","Question":"认证失败后如何恢复？"}'

# 用响应头中的完整 ID 替换示例值
rg -F 'rid[example-request]' log/
rg '\[ERRO\]|\[WARN\]' log/
rg 'code\[401\]|code\[503\]|error_type\[timeout\]' log/
rg '请求完成|请求未完成|运维分析完成|工具执行完成' log/
tail -f log/oncall-*.log
```

### 字段含义

| 字段 | 含义 |
| --- | --- |
| `code` | **真实 HTTP 数值状态码**；未收到响应时不填写，不用于业务错误分类 |
| `error_type` | 安全错误类别，如 timeout、cancelled、authentication_failed、unavailable |
| `outcome` | 请求终止结果，如 success、failed、timeout、cancelled |
| `status` | 运维事件的 completed/failed 等语义状态，不重复表示 HTTP 状态码 |
| `headers_elapsed` | 收到上游响应头的耗时，不代表完整生成结束 |
| `elapsed` | 当前事件所覆盖操作的耗时 |
| `req`、`rsp` | HTTP 终态中的请求/响应预览，以及 Tool 开始/完成时的实际 JSON 输入/结果预览；共用脱敏与限长规则 |
| `req_bytes`、`rsp_bytes` | 业务实际读取的请求字节数、底层 writer 成功接收的响应字节数；不是预览长度，也不保证等于 Content-Length 或客户端实收数量 |
| `source_hash`、`session_hash` | 内部来源或会话的稳定摘要，便于关联而不输出存储路径 |
| `agent`、`iteration`、`sequence`、`tool`、`count` | 运维步骤、工具及适用的结果数量 |
| `step_id`、`parent_step_id`、`duration_ms` | 关联步骤及父步骤，以及服务端计算的终态耗时；进度日志不记录可能含模型正文的 summary |
| `error_code` | 运维步骤的安全错误类别，来自结构化步骤错误；与数值 HTTP `code` 不同 |
| `output_bytes`、`history_committed` | 聊天输出或工具结果的字节数，以及聊天历史是否提交；流式聊天累计成功交付回调的正文片段 |
| `report_bytes`、`tool_failures`、`evidence_limited` | 运维报告的返回字节数、工具失败数及证据是否受限；失败时不返回报告，report_bytes 为 0 |
| `timestamp`、`timezone` | 时间工具实际返回的时间戳和时区，仅该工具记录这些结果字段 |
| `target`、`method`、`role` | 实际上游 HTTP URL、方法及调用角色；URL 移除用户信息、片段并脱敏敏感查询参数 |
| `response_bytes` | 调用方实际读取的上游响应字节数；被拒绝且未读取的响应可以为 0 |

SSE 开始后的失败保留 `code[200]`，通过 outcome/error_type 表明实际结果。API 错误响应和 CLI 错误 JSON 中的字符串 `code` 仍沿用原契约，与日志字段含义不同。

### HTTP 正文预览

中间件随业务读取请求，保留原始 body 的读取和关闭行为；响应在写出后仅计入成功写出的字节，同时覆盖 `Write` 和 `WriteString`。每侧 JSON 缓存最多 **8 KiB + 1 字节（8193 字节）**，额外一字节只用于识别超限。这个限制不截断业务请求或响应。

仅 `application/json` 和 `+json` 媒体类型进入正文缓存，支持 Content-Type 参数。请求需正常到达 EOF，或实际读取量等于已知 Content-Length，且没有非 EOF 读取错误；响应写出失败或短写时不视为完整。完整、有效 UTF-8 且不超过 **8 KiB** 的 JSON 才会解析并递归脱敏，支持嵌套对象和数组。敏感键忽略大小写、连字符和下划线，匹配 Authorization、Cookie、password、secret、token、api_key、private_key、access_key、signature 等片段及独立的 key，因此也覆盖 accessToken、clientSecret 和 X-Api-Key；值替换为 `[REDACTED]`。

脱敏后重新序列化的 JSON，以及字段转义后的显示内容，都必须不超过 **8 KiB**。超出任一上限即改为标记，不输出未脱敏前缀或半截 JSON，因此不会切断 UTF-8 字符。该上限针对每个正文预览，不是整条日志行的长度限制。

| 标记 | 含义 |
| --- | --- |
| `<truncated>` | 已读取/写出的正文超过 8 KiB，或脱敏、转义后的预览超限；不输出正文前缀 |
| `<invalid>` | 完整缓存不构成有效 UTF-8 JSON，包括畸形 JSON 或尾随额外数据 |
| `<incomplete>` | 请求未读完、读取失败，或响应写出失败/短写；即使已捕获前缀恰好可解析也不输出 |
| `<stream>` | `text/event-stream` 响应只计字节，不缓存事件正文 |
| `<multipart>` | multipart 请求及其响应均不缓存正文，包括上传接口的 JSON 响应 |
| `<omitted>` | Content-Type 缺失、无法解析或不是上述 JSON/流式/上传类型 |
| `<empty>` | JSON 类型、已确认完整且实际没有正文 |

HTTP 当前输入仅在中间件终态 `req` 中记录一次，控制器不再重复打印 Id、Question 或请求流；SSE 的 JSON 请求也遵循同一规则，流式响应正文不记录。聊天、检索和运维 CLI 仍完整记录一次当前 Id/Question 或 query，并转义换行等字符，不套用 HTTP 预览上限。

## 工具与依赖请求

以下过程在默认 INFO 级别可见，并沿用同一次调用的 `rid` 或 `run`。HTTP 入口的 `req/rsp` 与工具的 `req/rsp` 通过组件名称区分：

| 组件 | 记录内容 |
| --- | --- |
| `Tool` | 开始时记录工具名和实际 JSON 入参 `req`；成功时记录结果 `rsp`、字节数和耗时；失败记录安全 `error_type` |
| `Milvus` | `Search`、`Query`、`HybridSearch` 的集合、过滤表达式、输出字段、TopK/分页、ANN 参数与一致性；BM25/混合检索记录实际文本；完成时记录数量与耗时 |
| `Prom`、`Model`、`Embed` | 实际 HTTP 方法、脱敏目标 URL、角色、响应状态、收到响应头的耗时，以及响应读取结束时的字节数与总耗时 |

例如，行号与数据为示意：

```text
2026-11-17 08:00:00.130 [INFO] internal/ai/tools/logging.go:23 -> [Tool] 开始执行工具 rid[example-request] tool[query_internal_docs] req[{"query":"cache errors"}]
2026-11-17 08:00:00.140 [INFO] utility/client/milvus.go:143 -> [Milvus] 发起查询 rid[example-request] operation[HybridSearch] collection[knowledge_v2] filter[document_id == "doc-a"] query[cache errors] topk[20] limit[3] rrf_k[60] consistency[Strong]
2026-11-17 08:00:00.150 [INFO] utility/client/logging.go:43 -> [Prom] 发起上游请求 rid[example-request] role[alerts] method[GET] target[https://metrics.example.test/api/v1/alerts]
2026-11-17 08:00:00.180 [INFO] utility/client/logging.go:120 -> [Prom] 上游响应读取结束 rid[example-request] role[alerts] method[GET] target[https://metrics.example.test/api/v1/alerts] code[200] response_bytes[41] outcome[eof] elapsed[30ms]
```

Milvus 日志展示真实 SDK 参数与过滤表达式；数值向量仅记录数量、维度。Prometheus 当前工具实际调用 `GET /api/v1/alerts`，配置的基础路径会保留在 `target` 中。

工具 JSON 预览同样最多 8 KiB，超限仅输出 `<truncated>`；大结果仍原样返回，但日志跳过用于摘要的全量 JSON 解析，保留字节数和耗时。工具结果中的文档片段、告警标签和注释会按此规则进入日志。

上游 HTTP 日志只观察调用方正常读取的响应，不提前读取正文或额外消费模型流。`outcome[eof]` 表示响应体已读完，`closed` 表示已关闭；它们不表示业务成功，需结合数值 `code` 与工具结果判断。未建立响应的连接失败只记录安全错误类别和耗时，不填写 HTTP 状态码。认证 Header、模型提示词、Embedding 文本和数值向量不进入这些 HTTP 过程日志。

按一次请求查看全部过程：

```bash
rg -F 'rid[example-request]' log/
rg -F -e '[Tool]' -e '[Milvus]' -e '[Prom]' log/
```

## 日志级别、留存与内容

INFO 记录启动、图编译、关键业务结果与请求边界；DEBUG 增加节点、批次、连接关闭和健康检查；WARN 记录校验拒绝、取消或可恢复工具错误；ERROR 记录终止执行的错误。调整级别后重启服务：

```bash
# 在 backend/ 执行
ONCALL_LOG_LEVEL=debug GOTOOLCHAIN=local go run .
```

默认保留 7 个日历日（今天及之前六天），在启动和跨日写入时仅清理当前目录中严格匹配本模块命名的过期常规文件。启动检查日志目录可写性，运行期间写入失败会向 Web stderr 输出一次安全提示，不改变业务响应。

**普通 JSON 请求和响应的限长预览会进入日志**，可能包含当前问题、模型回答、运维报告、文档内容或工具详情；上述敏感键会递归脱敏。上传正文、multipart 响应及 SSE 事件正文不进入 HTTP 预览。工具边界额外记录实际 JSON 输入与结果的限长脱敏预览，Milvus 边界记录实际查询参数。运维步骤日志仍以字节数、数量、状态和父子步骤为主；上游原始错误、模型提示词与聊天历史保持省略。

JSON 和 URL 参数按键名脱敏；查询文本、过滤表达式以及任意自然语言内的敏感信息无法自动识别。CLI 当前输入仍完整记录。请据实际数据要求管理日志权限、备份和留存。`rid` 用于本地关联，被动 `trace` 读取与外部客户端行为见上文，不提供自动跨服务追踪。

## 常见问题

| 现象 | 检查方向 |
| --- | --- |
| Base URL 校验失败 | 去掉完整 completion/embedding 端点、账号密码、query 或片段，按提供方填写 API 基础地址 |
| `.env` 已填写却未生效 | 检查工作目录、已导出的同名变量及显式空值；Embedding 两个密钥变量还有名称选择优先级 |
| 模型 ID 不存在或 401/403 | 使用提供方已授权的实际 ID；运行 modelcheck，查看上游数值 code 和安全 error_type |
| Web 启动前失败，连 health 也不可用 | 检查启动日志 stage、Milvus 连接、已有集合及模型配置；health 不是独立于启动装配的服务 |
| 集合不存在、无索引或未加载 | 显式执行 init_knowledge；不匹配时更换独立集合或检查模型，不删除已有数据来绕过错误 |
| 无检索结果 | 检查上传是否完成、逻辑来源、TopK 和阈值；用 `--score-threshold -1` 查看邻近结果，区分空结果与查询失败 |
| SSE connected 后长时间无正文 | 首轮检索、工具或完整模型轮次仍在执行；结合日志耗时判断，不能只按 HTTP 200 判断成功 |
| SSE 末尾没有 done | 检查 error 事件、取消、代理断开或写出失败；不要将部分正文作为完成回答 |
| 刷新后能看到记录，但模型不记得 | localStorage 是展示历史；后端 TTL、重启或多实例可能导致上下文失效 |
| 上传 413/415/503 | 分别核对大小限制、文本格式、入库依赖；保存/入库阶段以 data.stage 区分 |
| AI Ops 只有心跳、长时间没有新 progress | 心跳仅说明传输可写；结合当前阶段的模型耗时、总预算和请求日志判断任务状态 |
| AI Ops 超时或证据受限 | 分别检查单次模型、工具和总预算；工具失败可继续，模型或总预算失败终止；无告警不等于健康 |
| 路由表没有打印 | 确认日志级别允许 INFO、正在查看 Web stderr/文件，CLI 不注册 HTTP 路由 |

复现错误时先记录请求 ID、HTTP 状态和安全类别，再通过[本地模拟](development.md#postman-本地模拟)隔离依赖问题。不要为排查而将密钥或原始上游正文加入日志。

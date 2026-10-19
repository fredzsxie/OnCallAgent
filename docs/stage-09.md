# 第 9 步：Prometheus 告警与 CLS 日志工具

新增两个默认关闭的运维工具，使用现有工具注册模块和 ChatAgent。配置继续仅从 `.env` 与进程环境读取，优先级为进程环境变量 > `.env` > 默认值。实际 `.env` 与根目录 `workflow.json` 保留，未新增业务配置 YAML。

## 修改文件

| 路径（仓库根目录起） | 职责 |
| --- | --- |
| `backend/internal/config/operations.go`、`retrieval.go` | 工具开关、地址、认证、预算和日志默认参数校验 |
| `backend/internal/service/operations/` | 安全错误分类、结构化结果、日志输入与时间转换 |
| `backend/utility/client/{tool_http,prometheus,log_mcp,operations}.go` | HTTP 连接复用、认证、MCP 生命周期及按需装配 |
| `backend/internal/ai/tools/{operations,query_metrics_alerts,query_log,registry}.go` | Eino 适配、严格 JSON 输入、注册和 Schema |
| `backend/main.go`、`backend/cmd/{chat_cmd,recall_cmd}/main.go` | 复用并集中关闭新增客户端 |
| `backend/cmd/tools_cmd/main.go` | 独立列表、MCP 检查与工具调用 |
| `backend/test/config/operations_test.go`、`backend/test/operations/` | 配置、真实本地 HTTP/MCP 客户端、CLI 与显式真实验收 |
| `backend/test/chat/pipeline/operations_test.go` | 真实图的 Invoke/Stream 工具调用及失败传播 |
| 既有工具、检索、对话集成测试 | 更新显式 Registry 依赖注入签名 |
| `backend/.env.example`、`go.mod`、`go.sum` | 配置示例和锁定依赖 |
| 根 README、本阶段文档 | 启动、契约及验收记录 |

## 配置

两个开关均默认为 `false`：

- `ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED`
- `ONCALL_TOOLS_QUERY_LOG_ENABLED`

| 环境变量 | 默认值 / 说明 |
| --- | --- |
| `ONCALL_PROMETHEUS_BASE_URL` | `http://127.0.0.1:9090`，追加 `/api/v1/alerts` |
| `ONCALL_PROMETHEUS_TIMEOUT` | `15s` |
| `ONCALL_PROMETHEUS_AUTH_TYPE` | `none`，可选 `basic`、`bearer` |
| `ONCALL_PROMETHEUS_USERNAME` / `PASSWORD` | Basic 用户名、密码，变量共用前缀 `ONCALL_PROMETHEUS_` |
| `ONCALL_PROMETHEUS_BEARER_TOKEN` | Bearer Token，默认空 |
| `ONCALL_LOG_MCP_URL` | 默认空，填完整 Streamable HTTP 入口，例如 `https://日志网关/mcp` |
| `ONCALL_LOG_MCP_TIMEOUT` | `15s`，初始化和单次调用各有独立预算 |
| `ONCALL_LOG_MCP_AUTH_TYPE` | `none`，可选 `basic`、`bearer`、`header` |
| `ONCALL_LOG_MCP_USERNAME` / `PASSWORD` | Basic 用户名、密码，共用前缀 `ONCALL_LOG_MCP_` |
| `ONCALL_LOG_MCP_BEARER_TOKEN` | Bearer Token，默认空 |
| `ONCALL_LOG_MCP_HEADER_NAME` / `HEADER_VALUE` | 自定义认证 Header 名和值，共用前缀 `ONCALL_LOG_MCP_` |
| `ONCALL_LOG_REGION` / `ONCALL_LOG_TOPIC_ID` | 默认空，允许调用输入覆盖 |
| `ONCALL_LOG_LOOKBACK` | `15m`，最小 1ms |
| `ONCALL_LOG_LIMIT` | `20`，范围 1–1000 |

布尔值、时长和数量的显式空值报错。只加载启用或当前操作需要的外部配置，禁用 CLS 时空 URL 不影响启动；离线列定义无需有效外部地址和凭据。启用后检查 http(s) 地址、正超时及对应认证字段；地址禁止内嵌用户名密码、query、fragment。认证模式不可混填，Header 禁止换行和覆盖协议字段。认证值仅从环境配置读取，模型输入不能指定地址或认证。

腾讯云 SecretId/SecretKey 应配置在 CLS MCP 服务端；本应用配置的是 MCP 入口认证。只有 Streamable HTTP，无 SSE/stdio 回退。Streamable HTTP 的响应允许协议内的 `text/event-stream`，这不等于使用旧 SSE 传输。

## 工具定义

`tools_cmd --list-tools` 输出按开关启用的工具及完整输入/输出 JSON Schema；`--mcp-tools` 输出远端工具定义并校验 `SearchLog`。远端其它工具不直接注册给 Agent。

### query_prometheus_alerts

输入 `{}`，实际发送 `GET <base_url>/api/v1/alerts`。输出示例：

```json
{"source":"http://127.0.0.1:9090/api/v1/alerts","alerts":[{"labels":{"alertname":"HighErrorRate","instance":"node-a"},"annotations":{"summary":"错误率升高"},"state":"firing","activeAt":"2026-10-19T12:00:00Z","value":"2"}]}
```

每个告警实例原样保留，不按名称去重。无数据为 `alerts:[]`。HTTP 错误、Prometheus `status:error`、非法 JSON、缺失数组和不符合契约的告警结构均报错。连接池复用，不在每次调用创建客户端。

### query_log

```json
{"query":"level:ERROR","region":"ap-guangzhou","topic_id":"主题ID","start_time":"2026-10-19T12:00:00+08:00","end_time":"2026-10-19T12:15:00+08:00","limit":20}
```

`query` 必填且去除首尾空白；地域、主题先取配置，再由输入覆盖，不猜测缺失值。起止时间均不传时取单次时钟读数对应的最近 LOOKBACK；否则必须成对提供带时区的 RFC3339，开始早于结束。单主题、单次查询，不自动翻页，不自动生成 CQL。

映射到官方 `SearchLog` 的 `Region`、`TopicId`、`Query`、`From`、`To`、`Limit`；From/To 为 Unix 毫秒。输出包含最终条件和提供方记录：

```json
{"request":{"region":"ap-guangzhou","topic_id":"主题ID","query":"level:ERROR","from":1792382400000,"to":1792383300000,"limit":20},"records":[{"Time":1792382400000,"Source":"node-a","LogJson":{"level":"ERROR","message":"认证失败"}}]}
```

记录保留提供方字段、日志时间和来源，不把 SQL 分析结果强制转换成普通日志。优先读取结构化数组或 `{"records":[...]}`，否则读取官方返回的单个 JSON 文本数组。只接受记录对象；空结果是 `records:[]`。截断文本、多块非预期内容、格式错误及 `isError` 均不当作空结果。成功结果仅编码一次。

## 生命周期与错误

`utility/client.OpenOperations` 只创建启用的客户端。MCP 完成 Start → Initialize → tools/list → SearchLog Schema 检查后复用；初始化失败立即关闭，退出集中 Close。请求和认证状态按单次调用隔离，认证同样应用到关闭会话的 DELETE。响应读取上限为 8 MiB。

工具构造函数只接收窄接口，不创建连接。Registry 显式接收 Retriever、时钟、告警与日志依赖；Web、chat_cmd 和 recall_cmd 更新装配。独立运维调用无需模型、Embedding 或 Milvus；仅列定义不建立外部连接。

单工具默认 15 秒，同时遵循更早的父 context 截止时间；ChatAgent 仍受原有总预算约束。SIGINT/SIGTERM 取消 CLI 操作并关闭客户端。启用后告警、日志结果会作为工具结果发送给当前聊天模型。

| 错误 code | 含义 | retryable |
| --- | --- | --- |
| `invalid_input` | JSON、字段、查询条件无效 | false |
| `authentication_failed` | HTTP 401/403 或可识别的 CLS 认证错误码 | false |
| `timeout` | 工具或父 context 截止 | true |
| `cancelled` | 调用取消 | false |
| `invalid_response` | 格式错误或不兼容的 SearchLog Schema | false |
| `upstream_error` | 上游 HTTP/API/工具失败 | true |
| `invalid_configuration` | 地址或认证等配置无效 | false |

`errors.Is` 可识别公共分类以及 `context.Canceled`、`context.DeadlineExceeded`。错误不返回原始依赖正文、URL、认证值或堆栈。CLI 另区分 `disabled_tool`、`unknown_tool`；失败向 stderr 写安全 JSON，退出非零：

```json
{"error":{"tool":"query_log","code":"authentication_failed","message":"authentication failed","retryable":false}}
```

## 启动与手工验收

在 `backend/` 执行，先在环境中提供有效地址和相应认证：

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/tools_cmd --list-tools

ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
GOTOOLCHAIN=local go run ./cmd/tools_cmd --tool query_prometheus_alerts --input '{}'

ONCALL_TOOLS_QUERY_LOG_ENABLED=true \
GOTOOLCHAIN=local go run ./cmd/tools_cmd --mcp-tools

ONCALL_TOOLS_QUERY_LOG_ENABLED=true \
GOTOOLCHAIN=local go run ./cmd/tools_cmd --tool query_log \
  --input '{"query":"level:ERROR","region":"ap-guangzhou","topic_id":"实际主题ID","limit":20}'

# Web：启用相应开关并提供配置后，使用同一套 ChatAgent 工具
GOTOOLCHAIN=local go run .
```

列表、MCP 检查、工具调用三个模式互斥；`--input` 仅配合 `--tool`。普通结果只输出 JSON，空数组退出码为 0。独立 CLI 不初始化知识集合。

## 自动验收与结果（2026-10-19）

从仓库根目录执行：

```bash
GOTOOLCHAIN=local go -C backend test ./...
GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
node --test backend/test/frontend/*.test.mjs
```

以上均通过。前端 Node 测试 15 项通过、1 项真实集成测试按开关跳过。全量 Go 回归包含普通聊天、SSE、上传、已有图、检索、模型检查和 CLI。

本地 HTTP/MCP 服务测试实际 SDK：认证四种模式、15 秒配置预算及更早截止、取消、空数组、同名告警实例、非法 JSON、上游失败、握手、Schema 不兼容清理、日志毫秒转换、结构化/SQL 结果、关闭会话、并发请求认证隔离和客户端复用。真实 ChatAgent 配合脚本模型验证 Invoke/Stream 调用新增工具以及失败终止。禁用工具不连接，时间/列表/运维模式按需初始化。

Postman 本地模拟另开终端运行：

```bash
postman simulation run backend/test/operations/postman/stage09.sim.yaml \
  --port 4909 --no-history --output ndjson
```

```bash
ONCALL_OPERATIONS_SIM_URL=http://127.0.0.1:4909 \
GOTOOLCHAIN=local go -C backend test ./test/operations \
  -run '^TestPostmanOperationsCLI$' -v -count=1
```

16 个 CLI 场景全部通过，覆盖 Prometheus 正常/空结果/认证/非法 JSON/503/慢响应，以及 MCP 列表、日志普通/空/SQL、认证、截断内容、上游错误、超时、工具 Schema 不匹配。测试运行真实 tools_cmd 与 MCP SDK，模拟仅替代上游服务；不据此声称云端可用。停止模拟用 Ctrl+C。Postman YAML 仅为测试资产。

模拟契约依据官方定义构建；未登录 Postman，未执行需要账号的远端 mock 归属检索，也未发布云端资源。日志查询条件以官方工具定义核对，不依赖推测字段。

### 显式真实联调

在 `backend/` 执行，测试读取 `.env`，保留进程变量优先级：

```bash
ONCALL_INTEGRATION=1 ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
GOTOOLCHAIN=local go test ./test/operations -run '^TestPrometheusIntegration$' -v -count=1

# 先提供有效 MCP URL、入口认证、ONCALL_LOG_REGION、ONCALL_LOG_TOPIC_ID
# ONCALL_LOG_INTEGRATION_QUERY 可覆盖测试查询，默认 *
ONCALL_INTEGRATION=1 ONCALL_TOOLS_QUERY_LOG_ENABLED=true \
GOTOOLCHAIN=local go test ./test/operations -run '^TestLogMCPIntegration$' -v -count=1
```

真实 Prometheus 查询、CLS tools/list 和一次真实日志查询：**未执行**。目前仅有 Prometheus 本机默认地址，缺少可用 MCP 地址、地域、主题和凭据，不能使用占位值宣称通过。真实测试仅查询，不写日志；只记录计数与查询时间，不打印业务日志或凭据。

## 依赖与协议依据

新增并锁定 `github.com/mark3labs/mcp-go v0.42.0`，其 Go 要求为 1.23；已用 Go 1.23.12 编译验收。新增间接依赖 `invopop/jsonschema v0.13.0`、`spf13/cast v1.7.1`、`yosida95/uritemplate/v3 v3.0.2`。cast 的最小版本约束使 `kr/pretty` 从 v0.3.0 升至 v0.3.1、`rogpeppe/go-internal` 从 v1.8.1 升至 v1.9.0；固定旧版本会导致 MCP SDK 被降级，因此保留这两项必要升级。GoFrame、Eino、Embedding 和 Milvus 版本未变，所有版本由 go.mod/go.sum 锁定。

- [Prometheus alerts API](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts)
- [腾讯云 CLS MCP 接入说明](https://cloud.tencent.com/document/product/614/118699)
- [CLS SearchLog 工具定义](https://github.com/Tencent/cls-mcp-server/blob/main/src/tools/log-search.ts)
- [CLS 输入字段定义](https://github.com/Tencent/cls-mcp-server/blob/main/src/constants.ts)
- [mcp-go v0.42.0](https://github.com/mark3labs/mcp-go/tree/v0.42.0)

# 第 9 步：Prometheus 告警与 CLS 日志工具

新增两个默认关闭的运维工具，使用现有工具注册模块和 ChatAgent。配置继续仅从 `.env` 与进程环境读取，优先级为进程环境变量 > `.env` > 默认值。实际 `.env` 与根目录 `workflow.json` 保留，未新增业务配置 YAML。

## 修改文件

| 路径（仓库根目录起） | 职责 |
| --- | --- |
| `backend/internal/config/operations.go`、`retrieval.go` | 工具开关、地址、认证、预算和日志默认参数校验 |
| `backend/internal/service/operations/` | 安全错误分类、结构化结果、日志输入与时间转换 |
| `backend/utility/client/{tool_http,prometheus,log_mcp,operations}.go` | Prometheus HTTP 连接复用、CLS stdio 子进程生命周期及按需装配 |
| `backend/internal/ai/tools/{operations,query_metrics_alerts,query_log,registry}.go` | Eino 适配、严格 JSON 输入、注册和 Schema |
| `backend/main.go`、`backend/cmd/{chat_cmd,recall_cmd}/main.go` | 复用并集中关闭新增客户端 |
| `backend/cmd/tools_cmd/main.go` | 独立列表、MCP 检查与工具调用 |
| `backend/test/config/operations_test.go`、`backend/test/operations/` | 配置、本地 HTTP/stdio MCP 客户端、CLI 与显式真实验收 |
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
| `ONCALL_LOG_MCP_COMMAND` | `npx` |
| `ONCALL_LOG_MCP_ARGS_JSON` | `'["-y","cls-mcp-server@1.2.0"]'` |
| `ONCALL_LOG_MCP_TIMEOUT` | `15s`，初始化和单次调用各有独立预算 |
| `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` | 启用日志工具时必填，由本地 `.env` 提供 |
| `TENCENTCLOUD_API_BASE_HOST` | 可选；VPC 内可设为 `internal.tencentcloudapi.com` |
| `ONCALL_TIME_ZONE` | `Asia/Shanghai`，作为 MCP 子进程的 `TZ` |
| `ONCALL_LOG_REGION` / `ONCALL_LOG_TOPIC_ID` | 默认空，允许调用输入覆盖 |
| `ONCALL_LOG_LOOKBACK` | `15m`，最小 1ms |
| `ONCALL_LOG_LIMIT` | `20`，范围 1–1000 |

布尔值、时长和数量的显式空值报错。只加载启用或当前操作需要的外部配置；日志工具关闭时，不要求 SecretId/SecretKey，也不启动子进程。启用后校验命令、参数 JSON、正超时及密钥。参数作为数组传给进程，不通过 shell 拼接。

官方服务通过 `npx -y cls-mcp-server@1.2.0` 使用 stdio；运行 Go 后端的主机需安装 Node.js/npm，首次启动需能从 npm 获取固定版本。腾讯云 SecretId/SecretKey 由 OnCallAgent 从本地 `.env` 读取并传给 CLS MCP。子进程只收到 PATH、HOME、`TRANSPORT=stdio`、CLS 凭据、可选 API Host 和时区，不继承聊天模型或 Milvus 环境变量。[腾讯 CLS MCP Server](https://github.com/Tencent/cls-mcp-server) [npm 包元数据](https://github.com/Tencent/cls-mcp-server/blob/main/package.json)

## 工具定义

`tools_cmd --list-tools` 输出按开关启用的工具及完整输入/输出 JSON Schema；`--mcp-tools` 启动配置的 stdio 进程，输出工具定义并校验 `SearchLog`。服务端其它工具不直接注册给 Agent。

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

`utility/client.OpenOperations` 只创建启用的客户端。MCP 完成子进程启动 → Initialize → tools/list → SearchLog Schema 检查后复用；初始化失败立即关闭，退出集中 Close。响应和错误走 stdio 协议，不暴露子进程诊断内容。

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
cd OnCallAgent
GOTOOLCHAIN=local go -C backend test ./...
GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
node --test backend/test/frontend/*.test.mjs
```

以上均通过。前端 Node 测试 15 项通过、1 项真实集成测试按开关跳过。全量 Go 回归包含普通聊天、SSE、上传、已有图、检索、模型检查和 CLI。

本地 HTTP fixture 验证 Prometheus SDK 请求、none/Basic/Bearer 认证、错误与空告警；本地 stdio 子进程使用真实 MCP SDK 验证握手、工具列表和 SearchLog 调用、Schema 不兼容、错误退出、超时/取消和关闭。测试确认只把 CLS 所需密钥传给子进程，不会继承聊天模型/Milvus 凭据。日志条件到毫秒的转换、结构化记录和 SQL 结果由服务及工具测试覆盖。真实 ChatAgent 配合脚本模型验证 Invoke/Stream 调用新增工具以及失败终止。禁用工具不连接，时间/列表/运维模式按需初始化。

Postman 本地模拟另开终端运行：

```bash
cd OnCallAgent
postman simulation run backend/test/operations/postman/stage09.sim.yaml \
  --port 4909 --no-history --output ndjson
```

```bash
cd OnCallAgent
ONCALL_OPERATIONS_SIM_URL=http://127.0.0.1:4909 \
GOTOOLCHAIN=local go -C backend test ./test/operations \
  -run '^TestPostmanOperationsCLI$' -v -count=1
```

6 个 Prometheus CLI 场景全部通过，覆盖正常/空结果/认证/非法 JSON/503/慢响应。CLS 使用独立的本地 stdio MCP 子进程 fixture，不再通过 HTTP mock 模拟。停止模拟用 Ctrl+C。Postman YAML 仅为测试资产。

模拟契约依据官方定义构建；未登录 Postman，未执行需要账号的远端 mock 归属检索，也未发布云端资源。日志查询条件以官方工具定义核对，不依赖推测字段。

### 显式真实联调

在 `backend/` 执行，测试读取 `.env`，保留进程变量优先级：

```bash
cd OnCallAgent/backend
ONCALL_INTEGRATION=1 ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
GOTOOLCHAIN=local go test ./test/operations -run '^TestPrometheusIntegration$' -v -count=1

# 先在 backend/.env 提供 Tencent SecretId/SecretKey、地域和 TopicId
# ONCALL_LOG_INTEGRATION_QUERY 可覆盖测试查询，默认 *
ONCALL_INTEGRATION=1 ONCALL_TOOLS_QUERY_LOG_ENABLED=true \
GOTOOLCHAIN=local go test ./test/operations -run '^TestLogMCPIntegration$' -v -count=1
```

真实 Prometheus 查询、CLS tools/list 和一次真实日志查询：**未执行**。本机配置已填写 Prometheus 地址并启用告警工具；日志工具保持禁用，Tencent SecretId/SecretKey 尚未配置。真实测试仅查询，不写日志；只记录计数与查询时间，不打印业务日志或凭据。

## 依赖与协议依据

新增并锁定 `github.com/mark3labs/mcp-go v0.42.0`，其 Go 要求为 1.23；已用 Go 1.23.12 编译验收。新增间接依赖 `invopop/jsonschema v0.13.0`、`spf13/cast v1.7.1`、`yosida95/uritemplate/v3 v3.0.2`。cast 的最小版本约束使 `kr/pretty` 从 v0.3.0 升至 v0.3.1、`rogpeppe/go-internal` 从 v1.8.1 升至 v1.9.0；固定旧版本会导致 MCP SDK 被降级，因此保留这两项必要升级。GoFrame、Eino、Embedding 和 Milvus 版本未变，所有版本由 go.mod/go.sum 锁定。

- [Prometheus alerts API](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts)
- [腾讯云 CLS MCP 接入说明](https://cloud.tencent.com/document/product/614/118699)
- [CLS SearchLog 工具定义](https://github.com/Tencent/cls-mcp-server/blob/main/src/tools/log-search.ts)
- [CLS 输入字段定义](https://github.com/Tencent/cls-mcp-server/blob/main/src/constants.ts)
- [mcp-go v0.42.0](https://github.com/mark3labs/mcp-go/tree/v0.42.0)

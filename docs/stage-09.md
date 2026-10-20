# 第 9 步：Prometheus 告警工具

`query_prometheus_alerts` 是默认关闭的只读运维工具。启用后，它加入 Web、聊天 CLI 和运维 Agent 共用的工具注册表；配置从进程环境变量或 `backend/.env` 读取，进程环境变量优先。独立的 `tools_cmd` 可列出工具 Schema 或调用指定工具。

## 配置与调用

| 环境变量 | 默认值 / 说明 |
| --- | --- |
| `ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED` | `false` |
| `ONCALL_PROMETHEUS_BASE_URL` | `http://127.0.0.1:9090`，调用时追加 `/api/v1/alerts` |
| `ONCALL_PROMETHEUS_TIMEOUT` | `15s`，同时遵循更早的请求截止时间 |
| `ONCALL_PROMETHEUS_AUTH_TYPE` | `none`，也支持 `basic`、`bearer` |
| `ONCALL_PROMETHEUS_USERNAME` / `ONCALL_PROMETHEUS_PASSWORD` | Basic 认证字段 |
| `ONCALL_PROMETHEUS_BEARER_TOKEN` | Bearer Token |

仅启用时校验并创建 Prometheus HTTP 客户端。地址必须是无凭据、查询串和片段的 HTTP(S) URL；超时须为正数，认证类型及字段必须匹配。客户端连接池在进程内复用并在退出时关闭。列出 Schema、调用时间工具或其他未启用的工具时，不建立 Prometheus 连接。

输入为 `{}`，实际发送 `GET <base_url>/api/v1/alerts`。每个告警实例原样保留，不按名称去重；无告警返回 `alerts:[]`。示例：

```json
{"source":"http://127.0.0.1:9090/api/v1/alerts","alerts":[{"labels":{"alertname":"HighErrorRate","instance":"node-a"},"annotations":{"summary":"错误率升高"},"state":"firing","activeAt":"2026-10-19T12:00:00Z","value":"2"}]}
```

HTTP 401/403 映射为 `authentication_failed`；超时与取消分别为 `timeout`、`cancelled`；非法 JSON、缺失告警数组和不符合契约的告警结构为 `invalid_response`；其他上游故障为 `upstream_error`。错误不回显响应正文、认证值或完整 URL。CLI 失败向 stderr 输出安全 JSON 并以非零状态退出；禁用或不存在的工具分别返回 `disabled_tool`、`unknown_tool`。

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/tools_cmd --list-tools
ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
  GOTOOLCHAIN=local go run ./cmd/tools_cmd --tool query_prometheus_alerts --input '{}'
```

`--list-tools` 输出启用工具的输入和输出 JSON Schema，不建立外部连接。`--tool` 与 `--list-tools` 互斥；`--input` 仅配合 `--tool`。独立告警调用无需聊天模型、Embedding 或 Milvus。

## 验证

```bash
cd OnCallAgent
GOTOOLCHAIN=local go -C backend test ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
node --test backend/test/frontend/*.test.mjs
```

本地 HTTP fixture 覆盖无认证、Basic、Bearer、空告警、认证失败、非法响应、上游故障、超时和取消；真实 ChatAgent 图覆盖调用与失败传播。默认测试不访问云端。

Postman 本地模拟可复用六个告警 CLI 场景：

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

显式真实联调读取 `backend/.env`，只查询告警，不写入数据：

```bash
cd OnCallAgent/backend
ONCALL_INTEGRATION=1 ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
  GOTOOLCHAIN=local go test ./test/operations -run '^TestPrometheusIntegration$' -v -count=1
```

本次清理后，后端全量 Go 测试、`go vet`、构建及前端 Node 测试通过；本地 Postman 告警模拟 6 个场景通过，确认请求到达 `/api/v1/alerts`。本地模拟结果不能代替真实 Prometheus 查询。接口依据：[Prometheus alerts API](https://prometheus.io/docs/prometheus/latest/querying/api/#alerts)。

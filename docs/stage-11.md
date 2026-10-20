# 第 11 步：运维接口与报告页面

新增同步运维 HTTP 接口与独立 AI Ops 面板。沿用 Go 1.23.12、现有依赖、原生前端和第 10 步 ADK；实际 .env 与根目录 workflow.json 保留。CLI 与 HTTP 共用 AIOpsService，不增加任务队列、轮询或运维 SSE。

## 修改文件

| 文件 / 目录 | 内容 |
| --- | --- |
| `backend/internal/service/aiops/aiops.go` | 固定任务、共享执行流程、结果副本、安全错误及部分 detail |
| `backend/api/chat/v1/chat.go`、`internal/controller/chat/chat_v1_ai_ops.go` | 空任务请求、result/detail 响应与独立注入控制器 |
| `backend/main.go`、`cmd/ai_ops_cmd/main.go` | Web 启动时装配共享客户端与 Agent，CLI 调用同一服务 |
| `backend/utility/middleware/middleware.go` | 限定在 AIOps 的错误映射 |
| `backend/internal/ai/agent/plan_execute_replan/{types,executor}.go` | 可选数值 count、格式错误保护 |
| `frontend/{index.html,styles.css,app.js,aiops.mjs,server.mjs}` | 面板、状态、协议、取消、防重复与代理预算 |
| `backend/test/ops/{http_test,http_integration_test,agent_test}.go` | 服务/实际 HTTP/ADK、取消、计数、显式真实验收 |
| `backend/test/frontend/` | Node 测试、扩展本地服务、stage11.sim.yaml、ops.collection.json |
| `backend/test/ops/postman/model/default.js` | 复用模型模拟，增加通过测试 URL 选择的页面场景 |
| 根 README、前端 README、本阶段文档 | 启动、契约、人工清单与结果 |

## 服务与接口

```text
POST /api/ai_ops → AIOpsController → AIOpsService → 共享 ADK Agent
ai_ops_cmd --query ... ────────────→ AIOpsService → 同一业务流程
```

Web 在启动时加载既有 Ops 配置并创建规划/执行模型，复用已经初始化的 Retriever、Registry 和外部工具客户端；每个请求独立执行，退出集中关闭客户端。CLI 保留必填 --query、report/detail stdout 和可选 JSONL；服务接收同一个窄 Agent 接口与超时，保留事件回调。

请求为 `{}`（也兼容空请求体），固定检查当前告警、时间、相关手册及可用日志；不接收自定义 Id/Question，不猜测地域/主题，不执行修复。存在请求体时要求 application/json 和空 JSON 对象，畸形 JSON、数组、null 或额外字段返回 400，不调用 Agent。GoFrame 不解析无字段请求的 body，因此控制器负责这层请求提取校验，业务执行留在 service。

成功格式：

```json
{"message":"OK","data":{"result":"## 告警\n\n...","detail":[{"sequence":1,"timestamp":"2026-10-20T10:00:00+08:00","iteration":1,"agent":"Executor","type":"tool","tool":"query_prometheus_alerts","status":"completed","summary":"alerts=0","count":0}]}}
```

result 是程序确认过的五部分 Markdown 最终报告。detail 沿用事件结构，可选 count 为有效工具数组的数量；不通过报告文本或 summary 推测。告警工具的最新事件必须成功且 count=0 才显示无活动告警。格式错误/缺失数组、失败或未执行的查询不产生有效空告警计数。

单工具失败但 Agent 最终成功，HTTP 仍返回 200，detail 保留 failed 事件，页面显示“分析完成，存在证据限制”。无告警不代表系统健康；有效告警结果缺失时也显示证据限制。

终止错误没有 result，返回安全 code 和已完成 detail：

```json
{"message":"context deadline exceeded","data":{"code":"timeout","detail":[]}}
```

| 情况 | HTTP / code |
| --- | --- |
| 请求无效 | 400 / invalid_input |
| 父 context 取消且连接仍可写 | 408 / cancelled |
| 单模型或任务超时 | 504 / timeout |
| 执行/模型错误、计划或执行步数上限、无效报告 | 502 / execution_failed、plan_limit、executor_limit、invalid_report |
| 服务未初始化、内部错误 | 500 / initialization_failed、internal_error |

错误类型保留 errors.Is 判断能力，不透出 provider 正文、密钥、堆栈。结果和错误中的事件/count 返回副本。聊天、上传和 SSE 沿用原有响应及错误规则。

## 页面与代理

导航中的聊天和 AI Ops 共用页面操作锁。运维面板显示任务说明、开始/取消、状态和计时，收到同步结果后才显示完整报告与默认折叠的详情；计时不表示实时节点进度。Markdown 使用现有 Marked/DOMPurify；详情和错误只写纯文本。

运行期间禁用提交、聊天、上传、新建/切换会话及面板切换。取消通过 AbortController 关闭上游并立即恢复操作；请求标识阻止迟到结果污染下一次分析。新分析清空旧结果；错误只展示本次部分详情，不自动重试。

面板切换保留页面内的最后报告和原聊天内容；刷新后报告清空，聊天浏览器记录仍按原规则恢复。报告不写 localStorage 或会话记忆。

开发代理 `ONCALL_API_PROXY_TIMEOUT_MS` 默认为 210000，须为正整数。它是上游空闲预算，长于默认 180s 运维总预算；更慢的自定义后端预算须同步调整代理。Nginx 示例使用 210s，外部部署本次未执行。

## 启动与接口验收

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run .

# 另一终端
cd OnCallAgent/frontend
ONCALL_API_ORIGIN=http://127.0.0.1:6872 ONCALL_API_PROXY_TIMEOUT_MS=210000 ./start.sh

# 请求真实配置的服务；可能调用当前启用的模型及工具
curl -i --max-time 210 -X POST http://127.0.0.1:6872/api/ai_ops \
  -H 'Content-Type: application/json' -d '{}'
```

## 可复现的本地完整链路

以下命令从仓库根目录在不同终端运行，不使用真实云端凭据：

```bash
cd OnCallAgent
postman simulation run backend/test/frontend/stage11.sim.yaml \
  --port 4911 --no-history --output ndjson
```

```bash
cd OnCallAgent
ONCALL_FRONTEND_HELPER_READY=/tmp/oncall-stage11-url \
ONCALL_CHAT_SIM_BASE_URL=http://127.0.0.1:4911/chat/v1 \
ONCALL_OPS_SIM_BASE_URL=http://127.0.0.1:4911 \
GOTOOLCHAIN=local go -C backend test ./test/frontend \
  -run '^TestFrontendServerHelper$' -v -count=1 -timeout 40m
```

```bash
cd OnCallAgent
ONCALL_API_ORIGIN="$(cat /tmp/oncall-stage11-url)" ./frontend/start.sh
```

```bash
cd OnCallAgent
postman collection run backend/test/frontend/ops.collection.json \
  --env-var base_url=http://127.0.0.1:8080 \
  --env-var helper_url="$(cat /tmp/oncall-stage11-url)"
```

本地服务使用实际 GoFrame、AIOpsService、ADK、模型 SDK、时间工具和 Prometheus HTTP 客户端。文档来自 MemoryStore，日志为内存 fixture，不能据此验证语义向量或真实 CLS；既有独立 stdio 协议测试继续回归。模型与 Prometheus mock 复用原有本地契约，未发布到 Postman 云端，需登录的远端资源归属检索未执行。

仅测试服务暴露 `/__test/ops-scenario`，接收 normal、empty、limited、failure、timeout、slow、unsafe。可用 curl 手动选择，再点击页面按钮；生产服务不注册该端点。Ctrl+C 停止三个进程，模拟变更后需重启加载。

```bash
curl -sS -X POST "$(cat /tmp/oncall-stage11-url)/__test/ops-scenario" \
  -H 'Content-Type: application/json' -d '{"scenario":"limited"}'
```

## 人工页面清单

| 操作 | 预期 |
| --- | --- |
| AI Ops → 开始分析 | 显示运行和计时，完成后出现五部分报告 |
| 运行时再提交、切换、上传 | 控件禁用，不触发第二次操作 |
| empty 场景 | 数值 count=0，提示未发现活动告警及健康判断限制 |
| limited 场景 | 200 报告保留，提示证据受限，详情记录日志失败 |
| failure / timeout | 不展示旧报告，分别提示依赖失败 / 超时，保留部分详情 |
| slow → 取消 → normal → 重试 | 立即恢复控件，旧请求迟到结果不覆盖新结果 |
| 展开详情，键盘 Enter | 默认折叠，可用键盘展开，内容为纯文本 |
| unsafe 场景 | Markdown 表格保留，脚本、图片、iframe、危险链接被过滤 |
| 切回聊天 / 再切回 AI Ops | 原聊天与当前报告分别保留，不混入聊天消息 |
| 刷新 | 报告清空，聊天历史仍按原规则恢复 |
| 390px 移动端 | 导航可开合、报告可滚动，无横向页面溢出 |

## 自动与真实验收记录（2026-10-20）

```bash
cd OnCallAgent
node --test backend/test/frontend/*.test.mjs
GOTOOLCHAIN=local go -C backend test ./...
GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
```

- 后端全量、竞态、静态检查及全部入口构建通过；前端 20 项通过，1 项既有云端测试按开关跳过。
- Postman 经实际开发代理调用真实 GoFrame/ADK：14 请求、17 断言通过。模拟日志核对了实际模型 POST 和 Prometheus GET。
- 浏览器验证正常/无告警/证据受限、运行锁、取消、超时、失败恢复、折叠与键盘、聊天保留和刷新清空。1280×900 桌面和 390×844 移动端检查完成；危险元素和链接计数为 0，表格保留。
- 真实 .env 服务成功启动于独立验收端口 6874；一次只读 HTTP 分析返回 502、execution_failed、1 条部分事件，未生成完整云端报告。安全失败映射可用，真实成功报告验收未通过；未更改超时预算或重复重试。
- 第 10 步两次模型超时记录保留；CLS 缺少凭据的真实查询未执行。本次不写入、不初始化知识集合，外部 Nginx 部署未执行。

显式真实 HTTP 验收：先启动使用真实配置的服务，再执行：

```bash
cd OnCallAgent
ONCALL_INTEGRATION=1 ONCALL_AIOPS_INTEGRATION_URL=http://127.0.0.1:6872 \
GOTOOLCHAIN=local go -C backend test ./test/ops \
  -run '^TestAIOpsHTTPIntegration$' -v -count=1 -timeout 220s
```

测试只记录 HTTP 状态、安全 code、事件数及工具概要，不打印完整业务报告。不得把本地模拟报告视为真实告警排查结论。

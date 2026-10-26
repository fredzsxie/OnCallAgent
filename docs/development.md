# 开发、测试与贡献

[文档索引](README.md) · [架构](architecture.md) · [API](api.md)

## 开发环境

使用 Go 1.23.12；所有 Go 命令指定 `GOTOOLCHAIN=local`，依赖由 `backend/go.mod` 与 `backend/go.sum` 锁定。前端开发与 Node 测试使用 Node.js 20+，无需 npm 安装或构建。

后端代码在 `backend/`，前端在 `frontend/`，全部测试在 `backend/test/`。真实服务使用自己的 `.env`；测试中的固定假凭据只适用于本地 fixture，不是可用的云端配置。

## 默认本地验证

在仓库根目录执行，显式关闭真实集成：

```bash
ONCALL_INTEGRATION=0 GOTOOLCHAIN=local go -C backend test ./...
ONCALL_INTEGRATION=0 GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
ONCALL_INTEGRATION=0 node --test backend/test/frontend/*.test.mjs
```

默认测试会启动本机 HTTP/gRPC 服务和 CLI 子进程；需要可写临时目录、Go 缓存及本地监听权限，但不要求真实云端凭据。测试包括：

| 范围 | 主要验证 |
| --- | --- |
| 配置与客户端 | .env 优先级、显式空值、校验、实际 SDK 协议、认证、超时与取消 |
| 知识库 | UTF-8/格式/大小、稳定 ID、真实图执行、Upsert、批次失败重试与旧版本保护 |
| 检索与工具 | TopK、阈值、无结果与失败区分、COSINE/HNSW、工具 Schema 与单次 JSON 编码 |
| 对话与记忆 | 双分支汇合、工具调用、Invoke/Stream、会话隔离、完整轮次、TTL 与并发 |
| HTTP 与 SSE | 控制器、中间件、状态码、事件顺序、多行正文、取消、超时与资源释放 |
| 运维 | 实际 ADK、计划调整、报告结构、工具失败后继续、两类执行上限、CLI/HTTP 共用流程 |
| 前端与日志 | SSE 分块、存储恢复、代理、AI Ops 状态、请求关联、调用位置、单行转义与留存 |

针对改动选择相关测试；完整验证命令用于发布前或跨模块改动。日志还可执行：

```bash
GOTOOLCHAIN=local go -C backend test -trimpath ./test/logger
```

## Postman 本地模拟

仓库已有模拟模型与 Prometheus，可复用同一组服务验证真实控制器、图、客户端和 CLI 调用。此路径不使用云端凭据，也不替代真实模型或数据库验证。

需要支持 `simulation` 命令的 Postman CLI。第一终端，在仓库根目录启动：

```bash
postman simulation run backend/test/frontend/stage11.sim.yaml \
  --port 4963 --output ndjson
```

第二终端，在仓库根目录执行：

```bash
ONCALL_LOGGING_SIM_URL=http://127.0.0.1:4963/chat \
ONCALL_OPERATIONS_SIM_URL=http://127.0.0.1:4963 \
ONCALL_OPS_SIM_URL=http://127.0.0.1:4963 \
GOTOOLCHAIN=local go -C backend test \
  ./test/logger ./test/operations ./test/ops \
  -run TestPostman -count=1 -v
```

模拟覆盖聊天和流式失败、告警空结果/认证失败/格式错误/超时，以及运维计划调整、模型失败和执行上限。以模拟进程的 request 事件确认调用到达依赖；仅有测试启动或监听事件不能证明端到端通过。

### 浏览器完整链路

保留上面的模拟进程。另一终端在仓库根目录启动测试专用 GoFrame 服务：

```bash
ONCALL_FRONTEND_HELPER_READY=/tmp/oncall-frontend-ready \
ONCALL_CHAT_SIM_BASE_URL=http://127.0.0.1:4963/chat/v1 \
ONCALL_OPS_SIM_BASE_URL=http://127.0.0.1:4963 \
GOTOOLCHAIN=local go -C backend test ./test/frontend \
  -run '^TestFrontendServerHelper$' -count=1 -timeout 0 -v
```

等待该进程将新地址写入 ready 文件，再在另一终端、仓库根目录执行：

```bash
ONCALL_API_ORIGIN="$(cat /tmp/oncall-frontend-ready)" ./frontend/start.sh
```

该服务使用实际控制器和 Agent 图、本地模型 SDK 及 MemoryStore，文件上传后可检索；测试专用场景控制入口只存在于 helper 中，生产服务没有这些接口。ready 文件不是凭据。验证结束后分别向测试服务和模拟进程发送 SIGINT/SIGTERM；若通过 `go test` 启动，应确保测试子进程也退出。

## 显式真实集成

真实验证由 `ONCALL_INTEGRATION=1` 单独开启，可能产生模型费用、创建集合或保留验收记录。执行前确认目标配置和测试行为，不直接对全部测试全局开启。

| 测试 | 外部行为 |
| --- | --- |
| `TestKnowledgeIntegration` | 在配置集合初始化并保留一条向量记录；建议先覆盖为独立集合 |
| `TestPipelineIntegration`、`TestUploadIntegration` | 在配置集合写入独立来源的验收片段；上传还验证更新和失败重试 |
| `TestRetrievalIntegration`、`TestChatAgentIntegration` | 创建独立验收集合并保留文档，验证召回或模型工具调用 |
| `TestChatMemoryIntegration` | 只读已准备好的聊天验收集合；需显式配置 `ONCALL_CHAT_INTEGRATION_COLLECTION`，要求 `stage06_acceptance_` 前缀 |
| `TestPrometheusIntegration` | 只读实际 `/api/v1/alerts`，要求工具启用 |
| `TestOpsIntegration` | 使用实际模型和启用工具完成只读排查，不执行修复 |
| `TestAIOpsHTTPIntegration` | 调用指定运行服务的运维 HTTP 接口 |

例如，在仓库根目录检查实际 Prometheus：

```bash
ONCALL_INTEGRATION=1 ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
GOTOOLCHAIN=local go -C backend test ./test/operations \
  -run '^TestPrometheusIntegration$' -count=1 -v
```

验证已运行服务的运维接口：

```bash
ONCALL_INTEGRATION=1 ONCALL_AIOPS_INTEGRATION_URL=http://127.0.0.1:6872 \
GOTOOLCHAIN=local go -C backend test ./test/ops \
  -run '^TestAIOpsHTTPIntegration$' -count=1 -v -timeout 240s
```

前端真实集成还要求 `ONCALL_FRONTEND_INTEGRATION_URL` 指向运行中的前端代理；会上传唯一标记文件并留下验收记录。详见[测试源文件](../backend/test/frontend/integration.test.mjs)，不要将它与纯协议单元测试混淆。

### 如何记录验证结果

报告执行命令、环境/版本、实际通过与跳过项、保留数据和已知限制；不提交密钥、完整日志正文或真实业务报告。默认测试通过不能推导为云端通过，目录查询通过不能推导为所有模型工具调用均兼容。

已有真实 Embedding/Milvus 及部分聊天验证记录，但完整云端运维分析曾受模型超时影响，不能据此声明全面兼容或生产可用。重构文档不会重新认证这些外部环境；新环境应独立执行需要的验证，未执行项明确标注。

## 参与贡献

1. 在 [Issues](https://github.com/fredzsxie/OnCallAgent/issues) 中描述问题或改进目标；报告版本、最小复现、HTTP 状态和安全错误类别，不上传实际 `.env`。
2. Fork 仓库，创建独立主题分支。维护者在 `codex/dev` 集成；本地自动化分支使用 `codex/` 前缀。
3. 保持控制器轻薄，业务放入 `internal/service`，外部客户端集中装配。核心逻辑仅添加必要英文注释，API、配置和图变化同步更新文档与 Schema。
4. 执行相关验证后提交。使用 **`type(scope): subject`**，按独立功能或修复划分粒度，例如 `fix(sse): preserve cancellation`、`docs(api): clarify stream errors`。
5. 提交 Pull Request，说明问题、最终行为、验证结果和限制；不要求为简单文档修改重复执行云端验收。

不提交密钥、日志、上传数据、构建产物或个人环境路径。项目当前尚未指定许可证；不要在贡献说明或文档中擅自声明项目授权，第三方依赖许可证保持原样。

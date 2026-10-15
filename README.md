# OnCallAgent

OnCallAgent 是基于 Go 的智能运维 Agent 应用。当前实现 GoFrame 服务骨架与基于 Eino 的普通聊天，后续按需加入会话记忆、知识库和工具调用。

固定使用 Go **1.23.12**、GoFrame **v2.7.1**、Eino **v0.6.0**、Eino OpenAI 模型扩展 **v0.1.1**。所有 Go 命令设置 `GOTOOLCHAIN=local`，避免自动切换工具链。依赖版本由 `backend/go.mod` 和 `backend/go.sum` 锁定。

```text
backend/
├── main.go                  # 启动时创建一次模型客户端并注入 ChatService
├── api/{health,chat}/v1/    # 请求和响应定义
├── internal/
│   ├── config/             # 配置读取、环境变量覆盖和校验
│   ├── controller/         # 接收 HTTP 请求，调用 logic
│   └── logic/              # 健康检查与 ChatService
├── utility/
│   ├── client/             # 模型客户端接口、Eino OpenAI 适配器
│   └── middleware/         # message/data 统一响应及状态码
├── manifest/config/        # GoFrame 配置
└── test/                   # 所有测试文件和本地协议测试
```

后续前端代码放在根目录 `frontend/`，当前没有前端页面或运行时 mock 模式。

## 配置与启动

在 `backend/manifest/config/config.yaml` 中填写模型 API 基础地址和实际模型 ID，或通过环境变量覆盖。密钥只接受环境变量：

```bash
cd OnCallAgent/backend
export ONCALL_CHAT_MODEL_BASE_URL='https://你的服务商地址/v1'
export ONCALL_CHAT_MODEL_MODEL='服务商提供的实际模型ID'
export ONCALL_CHAT_MODEL_API_KEY='你的密钥'
export ONCALL_CHAT_MODEL_TIMEOUT='30s'
GOTOOLCHAIN=local go run .
```

Base URL 通常以 `/v1` 结尾，具体以服务商为准；不要填写完整的 `/chat/completions` 地址。API Key 不允许放在 YAML 中，程序也不会自动加载 `.env`。`backend/.env.example` 列出所需变量。未提供有效配置时服务会在启动前报错。

| YAML 字段 | 环境变量 | 默认值 |
| --- | --- | --- |
| `server.port` | `ONCALL_SERVER_PORT` | 6872 |
| `chat_model.base_url` | `ONCALL_CHAT_MODEL_BASE_URL` | 必填 |
| `chat_model.model` | `ONCALL_CHAT_MODEL_MODEL` | 必填 |
| 不接受 YAML 密钥 | `ONCALL_CHAT_MODEL_API_KEY` | 必填 |
| `chat_model.timeout` | `ONCALL_CHAT_MODEL_TIMEOUT` | 30s |

环境变量显式覆盖 YAML，包括空值；空的必填字段、非法端口、非正数或无法解析的超时都会被拒绝。端口范围为 1–65535，超时支持 `750ms`、`30s` 等格式。

模型客户端在启动时创建一次，多个请求复用同一个 Eino ChatModel 和 HTTP 连接池。每次请求传递原始 context，再附加配置中的超时；退出后关闭空闲模型连接。

## curl 验收

```bash
curl -i http://127.0.0.1:6872/api/health
curl -i http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  --data '{"Id":"session-1","Question":"你好，请简单介绍一下自己。"}'
```

聊天成功返回 HTTP 200，响应格式如下：

```json
{"message":"OK","data":{"answer":"模型生成的回答"}}
```

`Id` 和 `Question` 必须为非空字符串。当前是单轮聊天，`Id` 为后续会话记忆保留，同一 Id 的请求尚不会携带历史消息。

| 情况 | HTTP 状态 |
| --- | --- |
| 请求合法、模型回答成功 | 200 |
| 缺少 Id/Question，或仅含空白 | 400 |
| 模型接口报错或没有有效文本回答 | 502 |
| 模型调用超时 | 504 |
| 请求 context 被取消且仍能写回响应 | 408 |

错误也使用 `message/data`，不会回显模型原始错误、密钥或提交的问题。`GET /api/health` 只报告服务存活，不调用模型；当前没有 `/api/ready`。

## 测试与构建

以下命令在 `backend/` 中执行，自动测试不需要真实模型凭据：

```bash
GOTOOLCHAIN=local go test -timeout 60s ./test/...
GOTOOLCHAIN=local go test -race -timeout 90s ./test/...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build -o bin/oncallagent .
```

测试使用临时本地 HTTP 端点验证实际 Eino 适配器发送的模型 ID、认证头与请求内容，以及错误响应、超时和取消传播。这些属于本地协议测试；未提供真实模型连接信息和凭据时，真实集成测试标记为未执行。

## Git 约定

在 `codex/dev` 开发，每项改动验收后提交到本地。提交使用 **`type(scope): subject`**，例如 `feat(chat): add ordinary chat endpoint`、`test(chat): verify request cancellation`。按独立功能或修复划分范围，同一阶段可以有多条提交。

实现说明见 `docs/stage-01.md` 和 `docs/stage-02.md`。

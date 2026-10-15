# 阶段 2：普通聊天接口

## 请求契约

```http
POST /api/chat
Content-Type: application/json

{"Id":"session-1","Question":"你好"}
```

成功返回 HTTP 200：`{"message":"OK","data":{"answer":"..."}}`。

当前只发送本轮问题，不添加历史、检索或工具调用。`Id` 为会话标识字段，暂不参与模型消息生成。

## 调用关系

启动时：

```text
config.Load
  → client.NewOpenAI：创建 Eino ChatModel 和可复用 HTTP 连接池
  → chat.NewChatService(model)：注入模型客户端接口
  → controller/chat.NewV1(service)：注入聊天业务
  → GoFrame Group / Bind / Run
```

请求时：

```text
ChatReq(Id, Question)
  → ControllerV1.Chat(ctx, req)
  → ChatService.Chat(ctx, id, question)
  → ModelClient.Generate(ctx, question)
  → context.WithTimeout + Eino ChatModel.Generate
  → OpenAI 兼容 POST /chat/completions
  → ChatRes(answer) → message/data
```

控制器只处理 HTTP 适配，业务校验放在 `internal/logic/chat`，Eino 和外部连接集中在 `utility/client`。模型在启动时创建，未放进控制器或逐次请求创建。

配置超时同时约束 HTTP 客户端和单次调用的 context。上游调用继承请求取消和更早的 deadline。服务商错误不直接传给浏览器，避免原始错误包含密钥或问题内容。

## 配置与版本

`chat_model.base_url`、`chat_model.model`、`chat_model.timeout` 从 GoFrame 配置读取，再显式应用对应 `ONCALL_CHAT_MODEL_*` 环境变量。API Key 只从 `ONCALL_CHAT_MODEL_API_KEY` 读取；YAML 的 `chat_model.api_key` 字段会被拒绝。

新增 Eino `v0.6.0` 和 OpenAI 模型扩展 `v0.1.1`，使用锁定版本的 `NewChatModel`、`ChatModelConfig.HTTPClient` 和 `Generate` API。GoFrame 保持 `v2.7.1`，Go 保持 `1.23.12`。未引入要求 Go 1.24 的 `eino-examples`。

## 验收范围

`test/config` 验证配置加载、环境变量覆盖、模型必填字段、超时和 YAML 密钥拒绝。`test/chat` 启动本地协议端点和实际 GoFrame 路由，验证请求/响应契约、模型 ID、Bearer 认证、客户端复用、输入校验、502/504 及从 HTTP 请求到模型请求的取消传播。

完整启动、模型配置和 curl 命令见根目录 README。没有提供真实模型连接信息或凭据，本次不执行真实服务商集成，密钥仅从环境变量读取。

普通聊天部分验收（2026-10-15）：配置与聊天协议测试、`go test -race`、`go vet ./...`、Go 1.23.12 构建均通过。HTTP 200/400/502/504、模型请求参数、复用和取消传播均已验证。

## 独立模型检查

运行 `GOTOOLCHAIN=local go run ./cmd/modelcheck`，只读取模型配置，不启动 GoFrame HTTP 服务，也不要求 server.port 可用。

检查流程是：`LoadChatModel → NewOpenAI → GET /models → 配置 ID 精确匹配 → Eino Generate → 输出 JSON`。检查的编排逻辑位于 `internal/logic/modelcheck`，模型目录的 HTTP 请求集中在 `utility/client/models.go`，命令入口只负责配置、客户端生命周期和输出。

只有模型目录包含目标 ID，且聊天请求返回有效文本，才报告两个检查项成功。目录接口不受支持时明确报错；不会根据“你是什么模型”的文字回答推测 ID。模型列表和聊天探测共享命令总超时。

`test/modelcheck` 覆盖目录与实际调用都成功、ID 不匹配、空目录、非法 JSON、401、404、目录超时，以及目录正常但聊天失败。

模型检查部分验收（2026-10-15）：全部配置、聊天、模型检查测试及竞态检测通过，静态检查和两个命令的构建通过。使用临时本地 OpenAI 兼容协议端点运行构建产物，验证了独立检查成功、错误 ID 非零退出、Web 连续两次聊天成功、health 不触发模型调用、缺少密钥拒绝启动及 SIGTERM 正常退出。检查命令在 `ONCALL_SERVER_PORT` 非法时仍能独立运行。所有验收进程已清理。

真实服务商的模型 ID、鉴权和聊天接口集成测试：**未执行（未提供连接信息及凭据）**。模型检查功能已通过本地协议测试，不能将这些结果表述为真实模型已连接。

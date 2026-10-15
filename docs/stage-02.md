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

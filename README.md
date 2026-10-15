# OnCallAgent

OnCallAgent 是基于 Go 的智能运维 Agent 应用。当前只建立一个可以启动的 GoFrame 服务，后续按需加入 Eino、模型和 Milvus。

使用 Go **1.23.12**、GoFrame **v2.7.1**。后端及测试都放在 `backend/`，后续前端放在根目录 `frontend/`。

```text
backend/
├── main.go                  # 创建服务、绑定路由、启动 GoFrame
├── api/health/v1/           # 请求和响应定义
├── internal/
│   ├── config/             # 读取、覆盖和校验端口
│   ├── controller/health/  # 处理 HTTP 请求
│   └── logic/health/       # 健康检查逻辑
├── utility/middleware/     # message/data 统一响应
├── manifest/config/        # GoFrame 配置
└── test/config/            # 配置测试
```

启动：

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run .
```

验收：

```bash
curl -i http://127.0.0.1:6872/api/health
```

预期返回 HTTP 200：

```json
{"message":"OK","data":{"status":"alive"}}
```

默认端口来自 `backend/manifest/config/config.yaml` 的 `server.port`，缺省为 6872。可显式通过环境变量覆盖：

```bash
ONCALL_SERVER_PORT=7872 GOTOOLCHAIN=local go run .
```

端口必须为 1–65535 的整数；空的环境变量也会报错。配置文件由 GoFrame 读取，环境变量覆盖由 `internal/config` 显式实现。`.env.example` 列出环境变量示例，程序不自动读取 `.env`。

测试和构建（在 `backend/` 执行）：

```bash
GOTOOLCHAIN=local go test ./test/...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build -o bin/oncallagent .
```

当前只有 `/api/health`，没有 mock 服务、外部客户端、依赖管理器或 `/api/ready`。模型、Embedding、Milvus、工具配置及连接会在对应阶段加入；密钥届时通过环境变量提供。真实外部服务集成测试未执行。

开发分支为 `codex/dev`。每一步验收后按合适粒度提交到本地，使用 `feat:`、`fix:`、`docs:`、`test:`、`chore:` 等提交类型。

调用关系和实现说明见 `docs/stage-01.md`。

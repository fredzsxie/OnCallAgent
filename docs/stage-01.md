# 阶段 1：最小 GoFrame 服务

本阶段建立可运行的 GoFrame 服务，采用独立后端目录并使用 Go 1.23.12。

## 调用关系

```text
main.go
  → config.Load：读取配置并覆盖端口
  → g.Server：创建 GoFrame 服务
  → Group("/api")：设置路由前缀和响应中间件
  → Bind(health.NewV1())：绑定控制器
  → SetPort / Run：设置端口并启动

GET /api/health
  → ControllerV1.Health
  → healthservice.Health
  → ResponseMiddleware
  → {"message":"OK","data":{"status":"alive"}}
```

## 模块职责

- `main.go` 使用 `g.Server()`、`Group`、`Bind`、`SetPort`、`Run`。
- `api` 使用 `g.Meta` 声明接口；路径 `/health` 与分组 `/api` 组合为 `/api/health`。
- `internal/controller` 处理请求，`internal/service` 返回业务结果。
- `utility/middleware` 通过 `ResponseMiddleware` 统一返回 `message/data`，业务错误使用 HTTP 500、参数错误使用 HTTP 400。
- `internal/config` 只负责当前用到的端口配置与校验，环境变量优先于 YAML，YAML 优先于默认值。

没有创建 mock、客户端管理器、依赖注入容器或自定义服务生命周期封装。当前没有外部依赖，不实现 ready 检查；以后需要哪个组件，再增加哪个组件。

## 验收

在 `backend/` 执行 README 中的启动、测试和构建命令。服务启动后访问 `/api/health` 应返回 HTTP 200；修改 YAML 端口或设置 `ONCALL_SERVER_PORT` 后，应在新端口响应。非法端口应在启动前报错。

所有测试文件集中在 `backend/test/`。配置测试覆盖默认端口、YAML 端口、环境变量覆盖和非法端口。外部服务真实集成测试未执行。

本次验收（2026-10-15）：Go 1.23.12 下配置测试、`go vet ./...` 和构建均通过。构建产物在默认端口、环境变量端口及 YAML 端口下的 `/api/health` 均返回 HTTP 200，`/api/ready` 返回 404；SIGTERM 后正常退出。非法端口在启动前被拒绝，验收进程已清理。

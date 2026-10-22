# OnCallAgent 文档

[返回项目首页](../README.md)

本文档描述当前代码的功能、配置和运行方式。首次使用请从[快速开始](../README.md#快速开始)进入；详细指南按主题组织，不要求了解项目开发过程。

## 阅读路径

| 你想完成的任务 | 建议阅读 |
| --- | --- |
| 启动服务、上传文档并提问 | [快速开始](../README.md#快速开始) → [配置](configuration.md) → [前端说明](../frontend/README.md) |
| 将接口接入其他应用 | [HTTP API](api.md) → [部署](deployment.md) |
| 单独检查模型、检索或运维工具 | [CLI](cli.md) → [日志与排障](observability.md) |
| 理解或扩展 Agent | [架构](architecture.md) → [开发与测试](development.md) |
| 部署到服务器 | [配置](configuration.md) → [部署](deployment.md) → [日志与排障](observability.md) |

## 主题指南

- [架构](architecture.md)：模块分层、KnowledgeIndexing、ChatAgent、运维 Agent，以及数据与并发边界。
- [配置](configuration.md)：后端环境变量、默认值、校验、模型继承、工具开关和前端公开配置。
- [HTTP API](api.md)：存活检查、聊天、SSE、上传和运维分析的现有契约。
- [CLI](cli.md)：七个独立入口、输入输出、依赖要求与 Eino Dev 调试。
- [部署](deployment.md)：二进制运行、静态前端、同源代理、超时和访问控制。
- [开发与测试](development.md)：默认测试、本地模拟、显式真实集成、验证范围和贡献流程。
- [日志与排障](observability.md)：日志格式、请求关联、文件留存与常见问题。

## 约定

- 示例中的“仓库根目录”指克隆后的 `OnCallAgent/`；其余命令在代码块前注明执行目录。
- 密钥和实际连接信息由自己的环境提供，不复制他人的部署配置；配置模板为 [backend/.env.example](../backend/.env.example)。
- API 定义、配置加载器与锁定依赖是当前行为的依据；Markdown 示例用于说明，不代表目标环境的真实返回结果。
- 默认测试、本地模拟和云端集成是不同的验证层次。未执行的真实验证不描述为通过。

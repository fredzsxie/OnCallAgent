# OnCallAgent Web 工作台

[项目首页](../README.md) · [配置](../docs/configuration.md) · [API](../docs/api.md) · [部署](../docs/deployment.md)

原生 HTML、CSS、JavaScript 页面，提供聊天、知识文件上传和独立 AI Ops 报告面板。无需 npm 安装或构建；Markdown 依赖随仓库提供，不依赖运行时 CDN。

## 启动

要求 Node.js **20+** 和支持 Fetch、ReadableStream、AbortController、localStorage 的现代浏览器。先按[快速开始](../README.md#快速开始)准备并启动后端，再从仓库根目录执行：

```bash
./frontend/start.sh
```

打开 **http://127.0.0.1:8080**。自定义开发后端和端口：

```bash
ONCALL_API_ORIGIN=http://127.0.0.1:6872 \
ONCALL_FRONTEND_PORT=8080 \
ONCALL_API_PROXY_TIMEOUT_MS=210000 ./frontend/start.sh
```

启动脚本从自身目录运行，不读取后端 `.env`。浏览器始终请求同源 `/api`，开发服务负责转发上传和 SSE；不要直接以 `file://` 打开页面。生产静态托管与 Nginx 示例见[部署指南](../docs/deployment.md#静态前端与同源反向代理)。

## 使用与数据边界

- **聊天**：默认流式，可切换普通模式。Enter 发送、Shift+Enter 换行；输入法候选确认不会误发送。生成期间禁止重复发送和切换，停止按钮通过 AbortController 取消请求。
- **上传**：支持 txt/md/markdown，默认 10 MiB。成功后展示真实保存文件名、大小、来源及片段数，再进行提问。文件进入共享知识库，不是当前会话附件；服务端路径不作为下载链接。
- **历史**：新建会话产生独立 Id，打开旧记录沿用原 Id。localStorage 保存展示正文和完成状态，不将历史重新发送到后端；清理浏览器记录不清理后端记忆或知识文件。
- **上下文**：后端默认记忆 3 轮、成功提交后 30 分钟过期，重启清空。页面只能提示可能失效，不能准确检测；实际行为以服务配置为准。
- **失败恢复**：停止、错误或缺失 done 的回答标为“未完成”。不自动重试，可手动重新发送；网络中断时无法确认服务端是否已提交本轮。损坏、禁用或容量不足的存储会提示并降级为当前页面内使用。
- **AI Ops**：发送 `{}` 执行固定分析，显示已用时间、报告和默认折叠的纯文本详情。仅依据成功告警事件的数值 count=0 判断无活动告警。分析期间禁止重复提交和其他冲突操作，可取消；报告仅保留在当前页面，不写入聊天历史或 localStorage。

助手与运维报告先经 Marked 解析，再经 DOMPurify 白名单过滤；用户输入、错误和详情均为纯文本。默认不加载消息图片，过滤脚本、事件属性、SVG、iframe、表单和不安全链接；库失效时降级为纯文本。第三方版本与许可证见 [vendor/README.md](vendor/README.md)。

公开的 `config.js` 只配置浏览器行为，例如上传大小预校验；不能放密钥。前端不提供身份认证，访问控制需由部署环境提供。

## 验证

在仓库根目录执行：

```bash
ONCALL_INTEGRATION=0 node --test backend/test/frontend/*.test.mjs
```

默认测试覆盖 SSE 分块/中文/换行、JSON 与错误事件、缺失 done、取消、存储恢复、AI Ops 状态和代理。真实浏览器手工检查：

- 普通/流式聊天，多轮及跨模式续聊；不同 Id 不混用上下文。
- 上传后提问、重复上传、格式错误和超限提示。
- 新建与恢复会话、刷新恢复、未完成消息标记、清除本地记录。
- 停止后恢复控件、网络失败后手动重试、旧会话上下文提示。
- AI Ops 成功、无告警、证据受限、超时、取消后重试及折叠详情。
- Markdown 安全过滤、多行代码、中文输入法、移动端布局、键盘焦点及滚动。

真实 GoFrame + Graph + 模拟依赖的浏览器链路和显式云端测试见[开发与测试](../docs/development.md)。单元测试、本地模拟和真实浏览器/云端验证应分别记录，不互相替代。

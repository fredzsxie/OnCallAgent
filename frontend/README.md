# OnCallAgent 前端

原生 HTML、CSS、JavaScript 聊天工作台。无需 npm install 或构建，运行时资源均在本目录。开发服务器要求 Node.js 20+，页面使用现代浏览器的 Fetch、ReadableStream、AbortController 和 localStorage。

## 启动

先启动后端：

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run .
```

另一终端启动前端，打开 http://127.0.0.1:8080：

```bash
cd OnCallAgent/frontend
./start.sh

# 后端地址和本地前端端口可覆盖，不读取或修改 backend/.env
ONCALL_API_ORIGIN=http://127.0.0.1:6872 ONCALL_FRONTEND_PORT=8080 ./start.sh
```

脚本可以从任意工作目录调用。开发服务只监听 127.0.0.1，只提供明确列出的页面资源，并把 `/api` 代理到启动时指定的 HTTP(S) origin。API origin 不包含 `/api`、账号密码、query 或 fragment。上传和 SSE 双向透传，不缓存完整响应；客户端断开会关闭上游。代理空闲超时 120 秒，连接错误返回安全 JSON。

页面始终请求同源 `/api`，无需给后端增加 CORS。不要用 file:// 直接打开页面，也不要用只提供静态文件且没有 API 代理的服务器联调。

## 使用与接口

- 默认流式聊天，可切换普通模式；Enter 发送，Shift+Enter 换行，输入法候选确认期间不发送。
- 两种模式分别调用 `POST /api/chat` 和 `POST /api/chat_stream`，仅提交 `Id`、`Question`。普通成功读取 `message/data.answer`，SSE 仅在有效 done 后确认成功。
- 生成时可停止，停止和错误会保留标记为“未完成”的展示记录，不自动重试。重新发送按钮将问题放回输入框，由用户决定是否提交；网络异常时无法确认后端是否已提交本轮。
- 上传使用 `POST /api/upload` 的 multipart 字段 `file`；支持 txt、md、markdown，默认前端限制 10 MiB。可修改公开的 `config.js` 中 `uploadMaxBytes` 与服务端限制配合，服务端始终做最终校验。此文件不能放密钥。
- 上传成功展示实际保存文件名、实际字节数、来源和片段数；文件进入共享知识库，不是某会话专有的附件。上传期间先等待入库完成再提问。
- 会话列表仅为浏览器展示记录。新建会话生成新 Id，恢复旧记录沿用原 Id，不将本地消息重新发送给模型。

localStorage 使用版本化键 `oncallagent.history.v1`，保存会话、标题、正文、完成状态和最近成功时间；刷新前未完成的消息恢复为“未完成”。存储损坏、被禁用或容量不足时提示并降级为当前页面内使用，后续保存可能覆盖损坏数据。清除记录需确认，只清理当前浏览器，不清除后端记忆或知识文件。不同浏览器、不同 origin 的记录不共享；本阶段不做多标签页记录合并。

后端默认只记忆最近 3 轮完整对话，成功提交后 30 分钟过期，进程重启清空；实际值以服务配置为准。重新打开旧会话或默认 30 分钟未成功提交时，页面提醒上下文可能失效。现有接口不提供会话状态或重启标识，前端无法准确检测；需要时补充背景或新建会话。

SSE 使用 fetch POST、流式 UTF-8 解码和完整事件边界解析，支持 connected/message/error/done、CRLF/LF、跨块事件、注释及多行 data。仅追加 message.content；非法 JSON、异常 EOF 或 error 不标记成功。停止会 abort 请求并取消 reader。后端仍会先确认模型轮次再输出正文，connected 后等待检索或生成属于正常行为。

助手 Markdown 经本地 Marked 解析、DOMPurify 白名单过滤后生成 DOM；用户消息始终使用纯文本。禁用脚本、表单、样式、SVG、iframe 和图片，仅允许明确的 HTTP(S) 链接；外部链接带 noopener/noreferrer。依赖缺失或渲染失败时降级纯文本。依赖版本、许可证和 SHA-256 见 [vendor/README.md](vendor/README.md)。

## 同源部署

将本目录静态资源复制到部署目录；不需要部署开发用的 Node 服务器。Nginx 示例：

```nginx
server {
    listen 80;
    server_name oncall.example.com;
    root /srv/oncallagent/frontend;
    index index.html;
    client_max_body_size 11m;

    location / {
        try_files $uri $uri/ =404;
        add_header X-Content-Type-Options nosniff;
    }
    location ~ /\. { deny all; }
    location ~ ^/(server\.mjs|start\.sh|README\.md)$ { deny all; }

    location /api/ {
        proxy_pass http://127.0.0.1:6872;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 120s;
        proxy_send_timeout 120s;
    }
}
```

部署时使用 HTTPS，并按后端上传配置同步调整代理大小和超时。静态部署仅复制 index.html、styles.css、app.js、config.js、四个前端模块及 vendor 运行库，许可证可随包附带；不要复制仓库或 backend/.env。部署示例本次未在外部服务器执行。

## 验收

从仓库根目录运行：

```bash
node --test backend/test/frontend/*.test.mjs
GOTOOLCHAIN=local go -C backend test ./...
GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
```

页面验收清单、Postman 本地完整链路、显式云端验收命令和实际结果见 [第 8 步文档](../docs/stage-08.md)。

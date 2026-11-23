# 部署

[文档索引](README.md) · [配置](configuration.md) · [日志与排障](observability.md)

项目提供 Go 后端与静态前端，不附带数据库部署或一键容器编排。先准备可访问的 Milvus、聊天模型和 Ark Embedding，再部署应用。以下是单进程部署方式；不会自动获得多实例共享会话或跨进程写入锁。

## 本地开发

在仓库根目录，按[快速开始](../README.md#快速开始)准备 `backend/.env` 后执行：

```bash
cd backend
GOTOOLCHAIN=local go run ./cmd/init_knowledge
GOTOOLCHAIN=local go run .
```

另一终端，在仓库根目录执行：

```bash
cd frontend
ONCALL_API_ORIGIN=http://127.0.0.1:6872 \
ONCALL_FRONTEND_PORT=8080 \
ONCALL_API_PROXY_TIMEOUT_MS=210000 ./start.sh
```

页面始终访问同源 `/api`；开发 Node 服务转发 API 请求，不缓存 SSE，浏览器断开时关闭上游连接。它仅监听 `127.0.0.1`，只暴露白名单静态资源，不提供仓库或后端配置文件。不要使用 `file://` 打开页面，也不要以没有 `/api` 转发的静态服务器替代联调代理。

## 构建与运行后端

在仓库根目录执行：

```bash
cd backend
GOTOOLCHAIN=local go build -o bin/oncallagent .
GOTOOLCHAIN=local go build -o bin/init_knowledge ./cmd/init_knowledge

# 在目标环境首次运行，或检查并加载已有集合
./bin/init_knowledge
./bin/oncallagent
```

二进制从当前工作目录加载 `.env`。若用服务管理器运行，应显式设置工作目录、配置注入和数据目录权限；不要依赖开发机器的路径。也可以完全通过进程环境提供配置。

初始化命令可能创建集合、索引并加载，且会调用真实 Embedding；已有结构不匹配时保留数据。Web 不会替代这一步。默认后端端口为 6872；未配置 `ONCALL_SERVER_HOST` 时监听所有网卡。同机 Nginx 部署应设置 `ONCALL_SERVER_HOST=127.0.0.1`，仅在环回地址监听后端，避免与网关的同号公网端口冲突。

SIGINT/SIGTERM 触发停止接收新连接，最多等待 **20 秒**让活动请求完成，再关闭共享客户端；超过等待上限会关闭剩余连接，因此长时间 SSE 可能中断。服务管理器的强制退出宽限期应长于这段等待时间。应用日志默认在当前工作目录的 `log/`，上传文件默认在 `data/knowledge/`，SQLite 文档目录默认在同一目录的 `catalog.sqlite`。关闭前端标签页或代理连接不等于服务停止。

## 静态前端与同源反向代理

生产环境可直接托管前端静态资源，无需运行开发 Node 服务。部署 `index.html`、CSS、JavaScript 模块、公开的 `config.js` 和 `vendor/` 运行时资源，保留第三方许可证；不部署后端 `.env`、测试目录或仓库元数据。

下面是 Nginx 的站点片段示例，路径、域名和证书应按目标环境替换。`/srv/oncallagent/frontend` 仅为示例路径，不是项目要求。

```nginx
server {
    listen 80;
    server_name oncall.example.com;
    root /srv/oncallagent/frontend;
    index index.html;

    # 为默认 10 MiB 文件限制预留 multipart 开销
    client_max_body_size 12m;

    location / {
        try_files $uri $uri/ =404;
    }

    location ~ /\. {
        deny all;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:6872;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Connection "";
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 210s;
        proxy_send_timeout 210s;
    }
}
```

`proxy_pass` 不附加尾部路径，保留 `/api/...`。实际对外站点应配置 HTTPS 与访问控制；上述片段演示静态文件和 API 转发，不构成完整网关配置。部署后可先执行 `nginx -t` 检查自己的完整配置。

### 协调超时与上传限制

| 层级 | 默认值 | 含义 |
| --- | --- | --- |
| 聊天模型 | 30s | 单次请求预算 |
| ChatAgent | 60s | 对话总预算，含会话锁等待 |
| Prometheus | 15s | 单工具请求预算 |
| 运维 Agent | 180s | 运维任务预算 |
| Node 开发代理 | 210000ms | 上游连接空闲超时 |
| Nginx 示例 | 210s | 代理读取/发送空闲超时 |

较短的模型或工具超时仍可能先触发。提高运维总预算时，也要调整代理、负载均衡和调用方超时；代理空闲超时不是保证任务成功的绝对总时限。SSE 的 HTTP 200 只代表连接已经开始，应继续检查 error/done。

上传大小限制需要协调后端 `ONCALL_UPLOAD_MAX_BYTES`、浏览器 `config.js` 和网关 body 限制；网关应为 multipart 留出开销。服务端是文件格式、实际字节数和内容校验的最终依据。

## 数据与访问边界

- 持久保存 Milvus 数据及上传版本文件；应用不提供备份、知识文件下载、回滚或旧磁盘文件自动回收接口。
- 会话记忆在重启后清空，浏览器记录仍可能存在；多实例之间不共享会话或来源锁。需要多实例时应先设计共享记忆及写入协调。
- 当前没有内置认证、权限、租户隔离或限流。聊天 Id 不应作为身份凭证；上传知识对所有使用同一集合的会话可见。
- 模型会接收必要的当前问题、历史、文档片段和工具结果；启用 Prometheus 后，告警信息可能作为工具结果交给模型。应选择符合自己数据要求的模型端点。
- HTTP 日志仅缓存每侧最多 8 KiB + 1 字节，完整且有效的 JSON 经字段脱敏后输出，序列化及转义后的预览各限 8 KiB；普通响应可能包含回答、报告或文档内容。上传请求及其响应、SSE 响应正文不记录；CLI 仍完整记录一次当前问题或查询。实际读取/写出字节计数独立于预览上限。详见[日志规则](observability.md#http-正文预览)，请为日志和配置设置适当权限与留存策略。
- 不在公网部署 Eino Dev 调试入口，也不把 `backend/` 作为静态目录。

## 部署后检查

```bash
curl -i http://127.0.0.1:6872/api/health
curl -i http://oncall.example.com/api/health
```

再按 [API 指南](api.md)验证普通聊天、SSE、上传后提问和运维分析。检查页面请求是否始终为同源 `/api`，SSE 是否收到 done，超时是否能展示安全错误，以及响应 `X-Request-ID` 是否能关联到日志。真实依赖的成功情况需在目标环境记录，不以本地模拟结果替代。


## 知识库 v2 持久化和切换

原文目录和 SQLite 文档目录需要持久化。SQLite 使用 WAL，备份时停止写入并关闭进程，或使用 SQLite 一致性备份机制。当前按单进程、单写入者部署；不要同时运行多个 Web/知识 CLI 写同一目录。

已有旧集合先按[迁移指南](knowledge.md#从旧集合迁移)预检查，再在禁写窗口创建目标集合并重新索引。迁移及验证期间停止旧 Web 和 CLI 写入，通过网络规则或暂不切换公共代理限制新服务入口。核对目录、正文、关键查询及更新后，成对切换集合与目录配置并恢复写入。验证窗口保留旧应用和旧集合；恢复写入后的回滚还需要保存并补录新内容。

静态部署包含新增的 knowledge.mjs 和 knowledge-view.mjs；代理需要转发知识接口的 GET、POST、PUT 以及 multipart 请求。实际验收结果见[知识库验收记录](knowledge-acceptance.md)。

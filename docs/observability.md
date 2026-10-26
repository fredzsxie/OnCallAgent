# 日志与排障

[文档索引](README.md) · [配置](configuration.md) · [API](api.md)

## 格式与输出

日志复用 GoFrame `glog`，统一为易读文本，不使用 JSONL。正文按“时间、级别、组件、代码位置、事件、字段”排列；组件不填充空格，位置相对 `backend/`。以下行号和请求 ID 为示意：

```text
2026-01-01 08:00:00.125 +08:00 INFO  [HTTP](utility/middleware/logging.go:38) --> 请求开始 | req=example-request method=POST route=/api/chat
2026-01-01 08:00:00.130 +08:00 INFO  [Chat](internal/controller/chat/chat_v1_chat.go:11) --> 用户请求 | req=example-request Id=demo Question="第一行\n第二行"
2026-01-01 08:00:02.255 +08:00 INFO  [HTTP](utility/middleware/logging.go:72) --> 请求完成 | req=example-request method=POST route=/api/chat code=200 elapsed=2.13s outcome=success
2026-01-01 08:01:00.255 +08:00 ERROR [HTTP](utility/middleware/logging.go:72) --> 请求未完成 | req=example-stream method=POST route=/api/chat_stream code=200 elapsed=60s outcome=timeout error_type=timeout
```

时间包含毫秒和进程本地时区，耗时按量级显示 ms/s。代码位置表示日志从哪里发出，不是自动推断异常最初发生处；无法解析时为 `unknown:0`。普通与 `-trimpath` 构建均支持。直接 defer 在编译器优化下可能定位到执行它的 return 行。

Web 输出至 stderr 和每日文件 `log/oncall-YYYY-MM-DD.log`；从 `backend/` 启动时即为 `backend/log/`。CLI 运行日志默认只写文件，stdout/stderr 的业务协议不变。终端交互设备可为级别着色，文件与重定向输出无颜色；`NO_COLOR=1` 关闭终端颜色。

启动时 INFO 级别打印真实 API 路由表，含 ADDRESS、METHOD、ROUTE、HANDLER、MIDDLEWARE 五列；仅标题显示代码位置，表格保留多行。请求日志保持单行，字段中的换行、引号、反斜杠和控制字符转义，避免伪造日志行。

## 请求关联

每次 HTTP 请求由服务生成完整 `X-Request-ID`，以 `req=...` 贯穿服务、图、模型与工具；客户端同名 Header 不覆盖服务端 ID。CLI 每次执行生成 `run=...`；context 中已有有效 trace ID 时附加 `trace=...`，不表示项目自动部署了链路采集系统。

在 `backend/` 执行：

```bash
curl -i http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"Id":"log-demo","Question":"认证失败后如何恢复？"}'

# 用响应头中的完整 ID 替换示例值
rg 'req=example-request' log/
rg 'ERROR|WARN' log/
rg 'code=401|code=503|error_type=timeout' log/
rg '用户请求|用户检索|用户上传' log/
tail -f log/oncall-*.log
```

### 字段含义

| 字段 | 含义 |
| --- | --- |
| `code` | **真实 HTTP 数值状态码**；未收到响应时不填写，不用于业务错误分类 |
| `error_type` | 安全错误类别，如 timeout、cancelled、authentication_failed、unavailable |
| `outcome` | 请求终止结果，如 success、failed、timeout、cancelled |
| `status` | 运维事件的 completed/failed 等语义状态，不重复表示 HTTP 状态码 |
| `headers_elapsed` | 收到上游响应头的耗时，不代表完整生成结束 |
| `elapsed` | 当前事件所覆盖操作的耗时 |
| `source_hash`、`session_hash` | 内部来源或会话的稳定摘要，便于关联而不输出存储路径 |
| `agent`、`iteration`、`sequence`、`tool`、`count` | 运维步骤、工具及适用的结果数量 |

SSE 开始后的失败保留 `code=200`，通过 outcome/error_type 表明实际结果。API 错误响应和 CLI 错误 JSON 中的字符串 `code` 仍沿用原契约，与日志字段含义不同。

## 日志级别、留存与内容

INFO 记录启动、图编译、关键业务结果与请求边界；DEBUG 增加节点、批次、连接关闭和健康检查；WARN 记录校验拒绝、取消或可恢复工具错误；ERROR 记录终止执行的错误。调整级别后重启服务：

```bash
# 在 backend/ 执行
ONCALL_LOG_LEVEL=debug GOTOOLCHAIN=local go run .
```

默认保留 7 个日历日（今天及之前六天），在启动和跨日写入时仅清理当前目录中严格匹配本模块命名的过期常规文件。启动检查日志目录可写性，运行期间写入失败会向 Web stderr 输出一次安全提示，不改变业务响应。

**当前用户输入会进入日志**：聊天/SSE 记录一次完整解析后的 Id、Question；运维记录固定请求 `{}` 及实际任务；CLI 记录当前问题或查询；上传记录原始文件名、大小和格式。解析失败时不倾倒原始请求流。

不记录环境密钥、认证 Header、上传文件正文、聊天历史、模型提示词、模型回答、文档正文、完整工具结果或思维链；不直接输出上游原始错误。请根据用户输入的实际敏感程度管理日志权限、备份和留存。

## 常见问题

| 现象 | 检查方向 |
| --- | --- |
| Base URL 校验失败 | 去掉完整 completion/embedding 端点、账号密码、query 或片段，按提供方填写 API 基础地址 |
| `.env` 已填写却未生效 | 检查工作目录、已导出的同名变量及显式空值；Embedding 两个密钥变量还有名称选择优先级 |
| 模型 ID 不存在或 401/403 | 使用提供方已授权的实际 ID；运行 modelcheck，查看上游数值 code 和安全 error_type |
| Web 启动前失败，连 health 也不可用 | 检查启动日志 stage、Milvus 连接、已有集合及模型配置；health 不是独立于启动装配的服务 |
| 集合不存在、无索引或未加载 | 显式执行 init_knowledge；不匹配时更换独立集合或检查模型，不删除已有数据来绕过错误 |
| 无检索结果 | 检查上传是否完成、逻辑来源、TopK 和阈值；用 `--score-threshold -1` 查看邻近结果，区分空结果与查询失败 |
| SSE connected 后长时间无正文 | 首轮检索、工具或完整模型轮次仍在执行；结合日志耗时判断，不能只按 HTTP 200 判断成功 |
| SSE 末尾没有 done | 检查 error 事件、取消、代理断开或写出失败；不要将部分正文作为完成回答 |
| 刷新后能看到记录，但模型不记得 | localStorage 是展示历史；后端 TTL、重启或多实例可能导致上下文失效 |
| 上传 413/415/503 | 分别核对大小限制、文本格式、入库依赖；保存/入库阶段以 data.stage 区分 |
| AI Ops 超时或证据受限 | 分别检查单次模型、工具和总预算；工具失败可继续，模型或总预算失败终止；无告警不等于健康 |
| 路由表没有打印 | 确认日志级别允许 INFO、正在查看 Web stderr/文件，CLI 不注册 HTTP 路由 |

复现错误时先记录请求 ID、HTTP 状态和安全类别，再通过[本地模拟](development.md#postman-本地模拟)隔离依赖问题。不要为排查而将密钥或原始上游正文加入日志。

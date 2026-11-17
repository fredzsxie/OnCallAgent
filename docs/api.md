# HTTP API

[文档索引](README.md) · [配置](configuration.md) · [部署](deployment.md)

默认基础地址为 `http://127.0.0.1:6872`，统一前缀 `/api`。本页命令在仓库根目录执行，要求实际服务已启动；调用聊天、上传和运维接口可能访问所配置的真实依赖。

## 通用约定

普通接口返回 JSON 对象 `{"message":"...","data":...}`，成功 message 为 `OK`。错误通过 HTTP 状态码和安全消息表达，data 可能为 `null` 或错误详情。应用不回显上游原始错误、密钥或模型提示词。

所有请求体的总大小限制为 `ONCALL_UPLOAD_MAX_BYTES + 1 MiB`，默认 11 MiB；已知 Content-Length 和 chunked 请求均受限，超限返回 413。上传文件本身仍按 `ONCALL_UPLOAD_MAX_BYTES` 单独校验。

服务通过 `X-Request-ID` 响应头返回沿用或生成的请求 ID，可用于[关联日志](observability.md#请求关联)。目前没有内置身份认证；`Id` 不是身份凭证，知识库也不按 Id 隔离。公开访问应由网关或反向代理提供访问控制。

| 方法 | 路径 | 用途 |
| --- | --- | --- |
| GET | `/api/health` | 服务存活 |
| POST | `/api/chat` | 普通对话 |
| POST | `/api/chat_stream` | SSE 对话 |
| POST | `/api/upload` | 文件保存并入库 |
| POST | `/api/ai_ops` | 固定任务运维分析，完整 JSON 响应 |
| POST | `/api/ai_ops_stream` | 固定任务运维分析，实时 SSE 进度 |
| GET | `/api/knowledge/documents` | 分页文档目录 |
| GET | `/api/knowledge/documents/{id}` | 文档正文与元数据 |
| GET | `/api/knowledge/documents/{id}/chunks` | 当前版本切片 |
| POST | `/api/knowledge/documents` | 上传新文档及元数据 |
| PUT | `/api/knowledge/documents/{id}` | 指定预期版本更新文件 |
| POST | `/api/knowledge/search` | 向量、BM25 或混合检索 |

当前没有 `/api/ready`、会话删除接口或知识文件下载接口。

## 存活检查

```bash
curl -i http://127.0.0.1:6872/api/health
```

```json
{"message":"OK","data":{"status":"alive"}}
```

该接口不执行模型或数据库探测。Web 启动前仍需完成依赖初始化；响应存活不意味着依赖始终可用。

## 普通聊天

请求为 JSON，`Id`、`Question` 均为必填字符串，去除首尾空白后不能为空。Id 区分大小写。

```bash
curl -i http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"Id":"demo","Question":"请记住我的值班代号是 Aurora。"}'
```

```json
{"message":"OK","data":{"answer":"你的值班代号是 Aurora。"}}
```

示例回答仅用于说明结构，实际文本由模型生成。普通与 SSE 接口复用同一个 ChatAgent 和会话记忆，默认保存最近 3 轮完整对话，成功提交后 30 分钟过期。相同 Id 串行，等待锁也占用总预算；不同 Id 可并行。

| 情况 | HTTP | message 示例 |
| --- | --- | --- |
| 成功 | 200 | `OK` |
| 缺少参数、空白输入或非法请求 | 400 | `invalid request` |
| 请求取消且连接仍可写 | 408 | `request cancelled` |
| Agent、模型或执行上限失败 | 502 | `chat agent execution failed` |
| 调用或总预算超时 | 504 | `model request timed out` |
| 内部错误 | 500 | `internal server error` |

通常错误 data 为 `null`。失败或取消不保存部分回答；网络中断时调用方无法仅凭断开判断服务端是否已提交，应避免自动重试造成重复轮次。

## SSE 聊天

相同 JSON 请求，使用 **POST + fetch 流**，不适用只能发起 GET 的原生 EventSource 用法。

```bash
curl -N http://127.0.0.1:6872/api/chat_stream \
  -H 'Content-Type: application/json' \
  -d '{"Id":"demo","Question":"我的值班代号是什么？"}'
```

参数错误或写出能力初始化失败，在 SSE 开始前返回普通 HTTP JSON；参数验证通过后先发送 connected，再等待会话锁和模型 Stream 初始化。响应头包含 `Content-Type: text/event-stream; charset=utf-8`、`Cache-Control: no-cache` 和禁用代理缓冲头。

成功事件示例：

```text
event: connected
data: {"status":"connected"}

event: message
data: {"content":"你的值班代号是 Aurora。\n请继续描述故障。"}

event: done
data: {"status":"completed"}

```

SSE 开始后的失败示例：

```text
event: error
data: {"message":"chat agent execution failed"}

```

- 每个事件以空行结束，data 是 JSON；message 只通过 `content` 提供正文。按事件边界解析，不能把一次网络读取等同于一个事件。
- 成功顺序为 connected → 零到多个 message → done；error 后关闭，不再发送 done。流尾不会追加统一 JSON。
- SSE 开始后 HTTP 状态保持 200；必须以有效 done 判断成功，异常 EOF、error 或解析失败都不能当作成功。
- connected 仅表示流已建立。后端先检查完整模型轮次，因此可能等待一段时间才出现正文；不输出内部工具调用。
- 使用 AbortController 取消请求，并释放 reader。服务端传递取消、关闭上游流；单次写出最多阻塞 5 秒，还受请求剩余预算限制。
- 仅在正常结束、完整文本非空且所有消息及 done 写出成功后提交历史。

## 上传并入库

使用 `multipart/form-data`，字段名固定为 `file`。浏览器使用 FormData 时不要手动设置 multipart Content-Type。

```bash
curl -i http://127.0.0.1:6872/api/upload \
  -F 'file=@backend/test/retriever/testdata/auth_runbook.md'
```

成功响应结构示例：

```json
{
  "message": "OK",
  "data": {
    "fileName": "<source-hash>_<content-hash>.md",
    "filePath": "<实际保存路径>",
    "fileSize": 1024,
    "source": "auth_runbook.md",
    "contentHash": "<sha256>",
    "chunkCount": 3
  }
}
```

文件名、大小、路径、hash 和片段数以真实响应为准，上例为结构示意。路径是保存信息，不是下载 URL；前端不将服务器路径转换为可点击链接。

支持 `.txt`、`.md`、`.markdown`，默认上限 10 MiB。服务端拒绝空白内容、非法 UTF-8、NUL 字节、不支持格式及超限文件。重复上传同一逻辑来源和内容使用稳定 ID；同名更新会在新片段全部确认后清理旧版本，文件名不是租户隔离边界。

| 情况 | HTTP | data |
| --- | --- | --- |
| 成功 | 200 | 实际文件信息和 chunkCount |
| 缺少文件、空文件或非法文本 | 400 | `null` |
| 请求取消 | 408 | `null` |
| 文件过大 | 413 | `null` |
| 不支持的格式 | 415 | `null` |
| 保存或文件信息读取失败 | 500 | `{"stage":"save","retryable":true}` |
| Graph 入库失败 | 503 | `{"stage":"ingest","retryable":true}` |
| Graph 入库超时 | 504 | `{"stage":"ingest","retryable":true}` |

入库失败示例：

```json
{"message":"knowledge ingestion failed; retry the upload","data":{"stage":"ingest","retryable":true}}
```

失败响应不返回有效 chunkCount。保存失败不调用图；入库失败保留版本文件和旧向量以便重试，但可能已写入部分新片段，见[入库一致性](architecture.md#知识入库knowledgeindexing)。

## 运维分析

请求只接受固定任务的空对象 `{}`，不接受聊天 Id、Question 或自定义 query。自定义任务使用 [ai_ops_cmd](cli.md#运维分析)。

```bash
curl -i --max-time 210 http://127.0.0.1:6872/api/ai_ops \
  -H 'Content-Type: application/json' -d '{}'
```

成功响应示例：

```json
{
  "message": "OK",
  "data": {
    "result": "## 告警\n...\n## 证据\n...\n## 根因候选\n...\n## 处理建议\n...\n## 未确认事项\n...",
    "detail": [
      {
        "sequence": 4,
        "timestamp": "2026-01-01T08:00:00+08:00",
        "iteration": 1,
        "agent": "Executor",
        "type": "tool",
        "tool": "query_prometheus_alerts",
        "status": "completed",
        "summary": "告警查询完成",
        "count": 0
      }
    ]
  }
}
```

`result` 是最终五部分 Markdown 报告；`detail` 是执行及工具结果概要，`tool`、数值 `count` 仅在适用时出现。报告不写入聊天历史。

工具失败但仍生成有效报告时返回 200，detail 保留失败事件，页面提示证据受限。只有最近一次告警工具事件成功且 `count=0`，页面才显示“无活动告警”；未查询、禁用或失败不表示无告警。

| 情况 | HTTP | data.code 示例 |
| --- | --- | --- |
| 请求不合法 | 400 | `invalid_input` |
| 请求取消且可写回 | 408 | `cancelled` |
| 模型/任务失败、无效报告、计划或执行上限 | 502 | `execution_failed`、`invalid_report`、`plan_limit`、`executor_limit` |
| 单次模型或总预算超时 | 504 | `timeout` |
| 初始化或内部错误 | 500 | `initialization_failed`、`internal_error` |

```json
{"message":"context deadline exceeded","data":{"code":"timeout","detail":[]}}
```

失败时可能包含已完成的部分 detail，不返回有效 result。这里的字符串 `data.code` 是 API 错误契约；它与应用日志中仅表示数值 HTTP 状态码的 `code` 不同。

### 结构化步骤与工具结果

`/api/ai_ops`、运维 SSE 和 CLI 共用扩展后的 Event。原字段保持，新增字段均可选；事件条数随实际生命周期变化，不应硬编码。

| 字段 | 含义 |
| --- | --- |
| `stepId`、`parentStepId` | 本次运行内唯一的逻辑步骤 ID、所属步骤 ID；同一次调用的 started 与终态共享 ID |
| `title` | 实际计划步骤或可读工具名称 |
| `startedAt`、`finishedAt` | RFC3339Nano 起止时间，finishedAt 仅终态存在 |
| `durationMs` | 服务端计算的非负毫秒耗时，终态允许为 0 |
| `input` | 白名单调用参数，目前为手册工具的 query；无参数工具省略 |
| `output` | 计划列表、工具证据或执行步骤的工具结果汇总 |
| `error` | 安全错误的 code、中文 message、retryable；不会返回上游错误原文 |

`status` 为 started、completed、failed 或 cancelled。Runner 的流程终止事件可不含 stepId；页面仅将具有步骤语义的事件合并成时间线。父步骤包含模型与工具等待时间，不能将父子耗时相加，也不能用相邻事件的时间差推算工具耗时。并行调用同名工具仍有不同 stepId。

output 按工具提供：

- 时间：timestamp、timezone、unix_seconds。
- 告警：source、alerts，每条保留 labels、annotations、state、activeAt、value；不合并相同名称的不同实例。
- 文档：query、mode、scoreType、documents，包含 title、source、sectionPath、version、score、scoreType、excerpt。得分保持原始评分语义，不是概率。
- 规划：steps；执行完成：tools 汇总每次调用的工具、状态、摘要和适用的 count。

每次结果最多附带 20 条明细，count 为截断前的完整数量，output.truncated 标明列表截断。每段 excerpt 最多 500 个字符（含省略号），文档自己的 truncated 标明片段截断。其他文本和标签集合也有长度边界，凭据字段隐藏，来源 URL 去除用户信息和查询串。证据以纯文本展示，不输出原始 JSON、模型内部消息或隐藏推理。

### 运维实时进度

请求仍为固定任务的 `{}`，使用 POST + fetch 流：

```bash
curl -N --max-time 210 http://127.0.0.1:6872/api/ai_ops_stream \
  -H 'Content-Type: application/json' -d '{}'
```

参数或写入能力初始化错误在流开始前返回原 HTTP JSON 错误。成功建立流后返回 HTTP 200，响应头为 `Content-Type: text/event-stream; charset=utf-8`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`。

| 事件 | data |
| --- | --- |
| connected | `{"status":"connected"}`，在等待规划模型前发送 |
| progress | 一条完整 Event，sequence 从 1 连续递增；按 stepId 合并开始和结束 |
| done | `{"status":"completed","result":"五部分 Markdown 报告","detail":[...]}` |
| error | `{"message":"安全错误","data":{"code":"timeout","detail":[...]}}` |

流每 10 秒发送 `: heartbeat` 注释帧；它不是业务步骤，也不表示任务有新进展。进度事件实时发送，done.detail 是同一次运行已发送事件的最终快照。终态只有一个，之后不追加业务事件。

只有收到有效 done 才确认成功。HTTP 200、部分 progress、异常 EOF 或网络断开都不代表分析完成；error 保留部分 detail，不返回有效报告。单工具失败可继续生成证据受限报告。取消或关闭页面会断开请求并取消上游；不自动重试，不支持断线续传、刷新恢复或后台执行。运维和聊天共享串行 SSE 写入器，单次写入最多等待 5 秒；传输损坏后不拼接错误帧。

## 知识库接口

知识库目录、正文和切片分别通过 `GET /api/knowledge/documents`、`GET /api/knowledge/documents/{id}`、`GET /api/knowledge/documents/{id}/chunks` 获取，支持 `page`、`pageSize` 分页。目录支持 `q` 标题查询及 `docType`、`domain`、`service`、`tags`、`alertName` 过滤。

`POST /api/knowledge/search` 接收 `query`、`mode`（dense/bm25/hybrid）、`topK`（1–100，默认 10）和 `filters`，返回命中片段、来源、文档版本及 `scoreType`（cosine/bm25/rrf）。`denseMinScore` 仅允许用于 dense。

`POST /api/knowledge/documents` 与 `PUT /api/knowledge/documents/{id}` 接收 multipart `file` 和可选 JSON 字符串 `metadata`。新建可传逻辑 `source`；更新必须传 `expectedVersion`，来源保持原值。来源或版本冲突返回 409，文档不存在返回 404。显式 metadata 字段覆盖 Markdown front matter；省略字段使用文件值或默认值。


知识检索成功响应中的 documents 包含 id、documentId、version、chunkIndex、title、source、docType、sectionPath、content、score、scoreType，顶层另有 query、mode、scoreType、durationMs。目录及切片分页返回 items、total、page、pageSize。文档详情包含 activeVersion、latestVersion、updatedAt、chunkCount、indexOnly、status 和可用的 content；新接口不返回服务端原文路径。

知识参数错误返回 400，格式错误 415，超限 413，目录不存在 404，来源或版本冲突 409，检索依赖失败 502，入库失败 503。首次失败文档可用相同内容和元数据重试新建；更新未发布文档时仍必须显式提交 expectedVersion 字段，其值可以为空字符串。

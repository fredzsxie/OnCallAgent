# OnCallAgent

OnCallAgent 是基于 Go 的智能运维 Agent 应用。当前实现 GoFrame 服务骨架、基于 Eino 的普通聊天，豆包 Embedding、Milvus 向量存取，以及知识文件上传、KnowledgeIndexing 图编排、知识检索、时间/内部文档工具及 ChatAgent 对话编排、进程内会话记忆、SSE 对话接口和原生聊天前端。

固定使用 Go **1.23.12**、GoFrame **v2.7.1**、Eino **v0.6.0**、Eino OpenAI 模型扩展 **v0.1.1**。所有 Go 命令设置 `GOTOOLCHAIN=local`，避免自动切换工具链。依赖版本由 `backend/go.mod` 和 `backend/go.sum` 锁定。

```text
backend/
├── main.go                  # 启动时创建并注入共享客户端和服务
├── cmd/                     # modelcheck、init_knowledge、knowledge_cmd、recall_cmd、chat_cmd 独立命令
├── api/{health,chat,upload}/v1/    # 请求和响应定义
├── internal/
│   ├── ai/
│   │   ├── agent/          # 各 Agent 的 xxx_pipeline 编排与组件适配
│   │   ├── retriever/      # 复用 Store 的检索组件
│   │   └── tools/          # 时间、内部文档工具和注册模块
│   ├── config/             # 配置读取、环境变量覆盖和校验
│   ├── controller/         # 接收 HTTP 请求，调用 service
│   └── service/            # 健康检查、ChatService 和知识库向量服务
├── utility/
│   ├── client/             # 聊天、Ark Embedding 和 Milvus 客户端
│   └── middleware/         # message/data 统一响应及状态码
└── test/                   # 所有测试文件和本地协议测试
```

当前及后续两个 Agent 的编排代码、组件适配和 Graph Schema 统一放在 `backend/internal/ai/agent/xxx_pipeline/`。当前知识入库图为 `knowledge_index_pipeline/`；共享知识库业务逻辑继续放在 `internal/service/knowledge/`，测试继续放在 `backend/test/`。

前端代码位于根目录 `frontend/`，包含普通/流式聊天、停止生成、上传、新建会话与浏览器历史。所有测试继续放在 `backend/test/`；本地模型模拟仅用于验收，不是生产服务的运行模式。

## 配置与启动

在 `backend/.env` 中填写 `backend/.env.example` 列出的聊天、Embedding 和 Milvus 配置。首次启动前显式初始化集合：

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/init_knowledge
GOTOOLCHAIN=local go run .
```

配置统一通过 `.env` 或进程环境变量提供：

```bash
cd OnCallAgent/backend
export ONCALL_CHAT_MODEL_BASE_URL='https://你的服务商地址/v1'
export ONCALL_CHAT_MODEL_MODEL='服务商提供的实际模型ID'
export ONCALL_CHAT_MODEL_API_KEY='你的密钥'
export ONCALL_CHAT_MODEL_TIMEOUT='30s'
GOTOOLCHAIN=local go run .
```

Base URL 通常以 `/v1` 结尾，具体以服务商为准；不要填写完整的 `/chat/completions` 地址。密钥只通过 `.env` 或进程环境提供。未提供有效配置时服务会在启动前报错。

Web 服务和独立模型检查命令都会在读取配置前自动加载 `.env`：优先使用当前工作目录的 `.env`，不存在时查找 `backend/.env`，只加载第一份存在的文件。文件不存在时允许仅通过进程环境配置；文件存在但语法错误时明确报错。`.env` 已由 `.gitignore` 排除。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_SERVER_PORT` | 6872 |
| `ONCALL_CHAT_MODEL_BASE_URL` | 必填 |
| `ONCALL_CHAT_MODEL_MODEL` | 必填 |
| `ONCALL_CHAT_MODEL_API_KEY` | 必填 |
| `ONCALL_CHAT_MODEL_TIMEOUT` | 30s |

配置优先级为：**已有进程环境变量 > `.env` > 默认值**。`.env` 不覆盖已导出的变量，包括空值；空的必填字段、非法端口、非正数或无法解析的超时都会被拒绝。端口范围为 1–65535，超时支持 `750ms`、`30s` 等格式。

模型客户端在启动时创建一次，多个请求复用同一个 Eino ChatModel 和 HTTP 连接池。每次请求传递原始 context，再附加配置中的超时；退出后关闭空闲模型连接。

## 独立模型检查

配置好上述环境变量后，无需启动 Web 服务即可执行：

```bash
GOTOOLCHAIN=local go run ./cmd/modelcheck
```

命令先通过 `GET <base_url>/models` 查询服务商返回的实际模型 ID，确认配置 ID 精确匹配，再通过与聊天服务相同的 Eino 客户端发起一次聊天。成功输出 JSON，并以状态 0 退出：

```json
{"configured_model":"实际模型ID","available_models":["实际模型ID"],"model_id_verified":true,"chat_completion_available":true}
```

错误 ID 会提示服务商返回的可用 ID；鉴权失败、超时或聊天失败均以非零状态退出。整个检查受 `chat_model.timeout` 约束。若服务商不支持 `/models`，命令会明确报告无法验证模型目录；此时仍可用下面的 curl 单独验证聊天接口，不能将目录检查失败当成聊天一定不可用。

检查依据是服务商 API 的模型目录及配置 ID 的成功调用，不依据模型文本中的自报身份。网关内部如何将 ID 映射到模型由服务商负责。

## curl 验收

```bash
curl -i http://127.0.0.1:6872/api/health
curl -i http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  --data '{"Id":"session-1","Question":"你好，请简单介绍一下自己。"}'
```

聊天成功返回 HTTP 200，响应格式如下：

```json
{"message":"OK","data":{"answer":"模型生成的回答"}}
```

`Id` 和 `Question` 必须为非空字符串。普通和流式接口共享 ChatAgent 和按 Id 隔离的记忆，默认携带最近 3 轮完整对话，成功对话后 30 分钟过期。

| 情况 | HTTP 状态 |
| --- | --- |
| 请求合法、模型回答成功 | 200 |
| 缺少 Id/Question，或仅含空白 | 400 |
| 模型接口报错或没有有效文本回答 | 502 |
| 模型调用超时 | 504 |
| 请求 context 被取消且仍能写回响应 | 408 |

错误也使用 `message/data`，不会回显模型原始错误、密钥或提交的问题。`GET /api/health` 只报告服务存活，不调用模型；当前没有 `/api/ready`。

## 测试与构建

以下命令在 `backend/` 中执行，自动测试不需要真实模型凭据：

```bash
GOTOOLCHAIN=local go test -timeout 60s ./test/...
GOTOOLCHAIN=local go test -race -timeout 90s ./test/...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build -o bin/oncallagent .
GOTOOLCHAIN=local go build -o bin/modelcheck ./cmd/modelcheck
```

测试使用临时本地 HTTP 端点验证实际 Eino 适配器发送的模型 ID、认证头与请求内容，以及错误响应、超时和取消传播。这些属于本地协议测试；未提供真实模型连接信息和凭据时，真实集成测试标记为未执行。

## Git 约定

在 `codex/dev` 开发，每项改动验收后提交到本地。提交使用 **`type(scope): subject`**，例如 `feat(chat): add ordinary chat endpoint`、`test(chat): verify request cancellation`。按独立功能或修复划分范围，同一阶段可以有多条提交。

仅在核心逻辑中使用必要的英文注释，解释配置优先级、context 与超时传递、错误处理等设计原因；普通方法与直观判断不逐一注释。后续新增或修改核心逻辑时同步维护。

实现说明见 `docs/stage-01.md`、`docs/stage-02.md` 、`docs/stage-03.md` 和 `docs/stage-04.md`。

## Milvus 与 Embedding

通过 Eino Ark Embedding **v0.1.2** 接入 `doubao-embedding-vision-251215`，文本使用 `/embeddings/multimodal` 接口。Milvus SDK 固定 **v2.4.2**，已验证云端 Milvus **v2.4.1**；所有依赖版本保持不变。

配置统一写在 `backend/.env`，示例见 `backend/.env.example`。已有进程环境变量优先，显式空值不回退到默认值。独立初始化不要求聊天模型凭据。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_EMBEDDING_BASE_URL` | `https://ark.cn-beijing.volces.com/api/v3` |
| `ONCALL_EMBEDDING_MODEL` | `doubao-embedding-vision-251215` |
| `ONCALL_EMBEDDING_API_KEY` 或 `ARK_API_KEY` | 必填，显式 ONCALL 变量优先 |
| `ONCALL_EMBEDDING_TIMEOUT` | `30s` |
| `ONCALL_MILVUS_ADDRESS` | `localhost:19530` |
| `ONCALL_MILVUS_DATABASE` | `default` |
| `ONCALL_MILVUS_COLLECTION` | `oncall_knowledge` |
| `ONCALL_MILVUS_USERNAME` / `ONCALL_MILVUS_PASSWORD` | 同时提供，或同时省略 |
| `ONCALL_MILVUS_TLS` | `false`，启用后使用系统 CA 校验证书 |
| `ONCALL_MILVUS_CONNECT_TIMEOUT` | `5s` |
| `ONCALL_MILVUS_TIMEOUT` | `60s` |

Base URL 填 API 根路径，不包含完整的 Embedding 端点。Milvus 地址填 `host:port`。当前仅支持用户名密码和基本 TLS，不提供 Token、自定义 CA 或证书域名配置。向量维度从模型实际输出读取，不单独配置。

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/init_knowledge
```

初始化会探测实际向量维度，检查已有集合的 schema 与向量索引，创建缺失集合/索引并加载。数据库必须预先存在。已有结构不匹配时返回具体差异，保留数据；匹配时可重复执行。

集合保留四个字段：`id`（VARCHAR 主键，256 字节）、`content`（VARCHAR，65535 字节）、`metadata`（JSON）和 `vector`（FLOAT_VECTOR，实际维度）。索引为 HNSW/COSINE（M=16、efConstruction=128），检索显式使用 COSINE 与 Strong 一致性，ef=max(64, topK)。

`utility/client` 负责创建 Ark 和 Milvus 客户端，调用方复用并关闭。`internal/service/knowledge` 保留 Eino `Store/Retrieve` 接口：Upsert 写入文档，检索支持 TopK 和分数阈值。Store 构造函数读取已有集合，不创建或加载集合。Web 服务通过 `/api/upload` 写入知识库；`/api/chat` 和 `/api/chat_stream` 共享 ChatAgent 与会话记忆。更换 Embedding 模型时应使用新集合，避免混用向量空间。

三个独立检查/验收命令已移除，真实服务验证通过显式开启的集成测试完成：

```bash
# 默认测试仅使用本地 HTTP/gRPC 端点，真实集成测试自动跳过。
GOTOOLCHAIN=local go test -race -timeout 90s ./test/...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...

# 显式调用豆包和 Milvus；初始化集合并保留一条带验收标记的文档。
ONCALL_INTEGRATION=1 GOTOOLCHAIN=local go test -v -count=1 -timeout 120s \
  ./test/knowledge -run '^TestKnowledgeIntegration$'
```

集成测试读取 `backend/.env`，保留已有进程变量，核对现有集合 ID 未改变，再验证真实向量写入、检索、metadata 和 COSINE 得分。每次执行会新增并保留一条 `source=knowledge_integration` 的记录。没有配置真实凭据时，不启用该测试；不能把本地测试通过表述为云端验证通过。

实现与验收记录见 `docs/stage-03.md`。

## 知识上传

`POST /api/upload` 接收一个 `file` 文件，支持 `.txt`、`.md`、`.markdown`，编码为 UTF-8。来源使用原始文件名，区分大小写；相同文件名的重复上传使用稳定片段 ID，修改内容后先写入完整新版本再清理旧片段。保存文件使用 hash 名称，旧文件保留。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_FILE_DIR` | `./data/knowledge`，解析为绝对路径 |
| `ONCALL_UPLOAD_MAX_BYTES` | 10485760（10 MiB） |
| `ONCALL_KNOWLEDGE_CHUNK_SIZE` | 2000 UTF-8 字节，允许 4–65535 |
| `ONCALL_KNOWLEDGE_CHUNK_OVERLAP` | 200 字节，非负且小于块大小 |
| `ONCALL_KNOWLEDGE_BATCH_SIZE` | 16，必须为正 |

KnowledgeService 校验并原子保存文件后，构造指向实际保存路径的 `document.Source`，调用启动时编译并复用的 KnowledgeIndexing Graph。Loader 通过调用选项接收原始逻辑来源，切分、Embedding、批量写入和旧版本清理统一在图内完成。

切分识别 Markdown 标题，忽略代码围栏中的标题符号，块和重叠均不拆断字符，不跨标题重叠。元数据保留来源、标题、序号、片段与文档 hash、文档 ID 和版本。批量大小不改变片段 ID。

```bash
printf '# 值班手册\n检查健康状态，定位错误日志。\n' > /tmp/oncall-stage04.md
curl -i -F 'file=@/tmp/oncall-stage04.md;filename=stage04-manual.md' http://127.0.0.1:6872/api/upload
# 重复上传：保存路径、hash、数量相同，Milvus 片段 ID 集合不变。
curl -i -F 'file=@/tmp/oncall-stage04.md;filename=stage04-manual.md' http://127.0.0.1:6872/api/upload
```

成功数据为 `fileName`（安全保存名）、`filePath`（真实绝对路径）、`fileSize`（实际字节数）、`source`、`contentHash`、`chunkCount`。响应仍为 `message/data`。无效或空文档返回 400，超限 413，格式不支持 415；保存失败返回 500，`data={"stage":"save","retryable":true}`；Graph 入库或旧版本清理失败返回 503，超时返回 504，均带 `data={"stage":"ingest","retryable":true}`。请求取消返回 408。错误响应不返回成功文件信息或 chunkCount，底层错误不会回显。

同一来源在单进程内串行处理，不支持多个进程同时更新同一来源。失败期间允许暂时混合新旧片段；全部新批次确认前不会清理旧版本。完整约定与验收说明见 [第 4.2 步](docs/stage-04-2.md)。

```bash
# 显式连接云端，验证上传、重复、更新和分批失败后的重试；保留带标记的验收来源。
ONCALL_INTEGRATION=1 GOTOOLCHAIN=local go test -v -count=1 -timeout 120s \
  ./test/upload -run '^TestUploadIntegration$'
```

## KnowledgeIndexing 图编排

图契约为 `document.Source → []string`，拓扑固定为 `START → FileLoader → MarkdownSplitter → MilvusIndexer → END`。Embedding 由 Store 内部调用，客户端由命令入口复用和关闭。

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --root ./test/knowledge/pipeline/testdata --file runbook.md
GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --dev --root ./test/knowledge/pipeline/testdata --dev-port 52538
```

普通模式输出 JSON ID 数组后退出；调试模式可不指定文件，图注册后等待 SIGINT/SIGTERM。调试输入为 `{"URI":"runbook.md"}`。图配置见 [Graph Schema](backend/internal/ai/agent/knowledge_index_pipeline/workflow.json)，节点配置、幂等规则和验收结果见 [第 4.1 步](docs/stage-04-1.md)。

## 知识检索与基础工具

`internal/ai/retriever` 注入现有 Store，复用同一 Embedding、集合维度、FLOAT_VECTOR 转换和 HNSW/COSINE 搜索。默认 TopK 为 3，最低相似度为 0.5；无命中返回 `documents:[]`，查询失败返回错误。结果包含正文、来源、标题和相似度。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_RETRIEVAL_TOP_K` | 3，范围 1–16384 |
| `ONCALL_RETRIEVAL_SCORE_THRESHOLD` | 0.5，有限数，范围 [-1,1] |
| `ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED` | true |
| `ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED` | true |
| `ONCALL_TIME_ZONE` | Asia/Shanghai |

`query_internal_docs` 输入 `{"query":"问题"}`，返回 `{"query":"问题","documents":[...]}`；`get_current_time` 输入 `{}`，返回含 UTC 偏移的 `timestamp`、IANA `timezone` 和 `unix_seconds`。工具按配置注册并复用依赖，HTTP 聊天通过 ChatAgent 使用这些工具。

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/recall_cmd --query '服务认证失败后如何恢复？'
GOTOOLCHAIN=local go run ./cmd/recall_cmd --query '服务认证失败后如何恢复？' --top-k 3 --score-threshold 0.5
GOTOOLCHAIN=local go run ./cmd/recall_cmd --list-tools
GOTOOLCHAIN=local go run ./cmd/recall_cmd --tool get_current_time --input '{}'
GOTOOLCHAIN=local go run ./cmd/recall_cmd --tool query_internal_docs --input '{"query":"服务认证失败后如何恢复？"}'
```

列工具定义和调用时间工具不要求云端凭据。检索 CLI 不隐式初始化集合，配置错误或依赖失败会非零退出；无结果正常退出。完整工具输入输出、验收集合与实际得分见 [第 5 步](docs/stage-05.md)。


## ChatAgent 对话编排

独立对话图输入 `*UserMessage`（`id`、`query`、`history`），输出 `*schema.Message`。问题分别进入知识检索和对话参数分支，ChatTemplate 等待两条分支完成，再交给支持工具调用的 ReAct Agent。Agent 使用已有时间和内部文档工具；普通及 SSE 接口在启动时编译并共享该图。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_CHAT_AGENT_MAX_STEPS` | 12，ReAct 内部图执行步数 |
| `ONCALL_CHAT_AGENT_TIMEOUT` | 60s，首轮检索、模型与工具共享总预算 |

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/chat_cmd --query '认证失败后如何恢复？'
GOTOOLCHAIN=local go run ./cmd/chat_cmd --query '现在几点？' --stream
GOTOOLCHAIN=local go run ./cmd/chat_cmd --input '{"id":"demo","query":"恢复后如何验证？","history":[{"role":"user","content":"认证失败了"},{"role":"assistant","content":"请先刷新签名密钥缓存。"}]}'
GOTOOLCHAIN=local go run ./cmd/chat_cmd --dev --dev-port 52538
```

普通模式输出 Message JSON；流式模式输出逐行 Message JSON。为兼容先输出正文再调用工具的模型，检查器会先确认当前模型轮次是否包含工具调用，确认是最终回答后再转发片段，因此回答片段会在该模型轮次完成后开始输出。调试模式可不传问题，注册后等待 SIGINT/SIGTERM；DevOps 日志写入 stderr。独立 CLI 每次执行状态独立，历史仍显式传入；HTTP 接口通过下述记忆服务管理历史。

图与节点配置见 [Graph Schema](backend/internal/ai/agent/chat_pipeline/workflow.json)，完整契约、测试与真实验收记录见 [第 6 步](docs/stage-06.md)。


## 会话记忆与 SSE

`POST /api/chat` 调用 Invoke，保持 `message/data.answer`；`POST /api/chat_stream` 调用同一 Agent 的 Stream。两个接口都接收 `Id`、`Question`，同 Id 的请求串行执行，不同 Id 并行。成功后保存 user/assistant 完整消息对，失败、取消及写出失败不保存本轮。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_CHAT_MEMORY_MAX_ROUNDS` | 3 |
| `ONCALL_CHAT_MEMORY_TTL` | 30m，从最近一次成功对话计算 |

记忆仅存在于当前进程，服务重启后清空，多实例不共享。读取返回副本，不续期；过期记录按访问清理。现有 Agent 总超时也覆盖等待同会话锁的时间。

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run .

curl -sS http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"Id":"demo","Question":"请记住我的值班代号是 Aurora。"}'
curl -N http://127.0.0.1:6872/api/chat_stream \
  -H 'Content-Type: application/json' \
  -d '{"Id":"demo","Question":"我的值班代号是什么？"}'
```

SSE 成功事件顺序为 `connected → message... → done`，失败为 `connected → error`。data 始终为 JSON，例如 `{"content":"第一行\n第二行"}`；结束后不追加统一 JSON。参数错误在流开始前仍返回 HTTP 400 JSON。connected 在等待锁和模型初始化前发出，回答片段保持第 6 步的模型轮次确认行为。

完整事件定义、并发和过期规则、Postman 本地模拟命令及真实验收结果见 [第 7 步](docs/stage-07.md)。

## 聊天前端（第 8 步）

后端启动后，另开终端运行：

```bash
cd OnCallAgent/frontend
./start.sh
# 打开 http://127.0.0.1:8080

# 自定义开发后端 origin 和前端端口
ONCALL_API_ORIGIN=http://127.0.0.1:6872 ONCALL_FRONTEND_PORT=8080 ./start.sh
```

开发服务要求 Node.js 20+，无需 npm install；浏览器始终访问同源 `/api`，由开发代理转发，部署时改用同源反向代理。Markdown 库本地锁定为 Marked 18.1.1 和 DOMPurify 3.4.17；不使用运行时 CDN。公开的 `frontend/config.js` 可调整上传大小提示，默认 10 MiB，服务端最终校验。

默认流式，可切换普通模式；Enter 发送、Shift+Enter 换行，生成中可停止。localStorage 保存的是浏览器展示记录，旧会话恢复原 Id，不能恢复过期或重启丢失的模型上下文；未完成回答有明确标记。上传文件进入共享知识库。

```bash
# 仓库根目录：默认前端测试不调用云端
node --test backend/test/frontend/*.test.mjs
```

文件说明、部署示例见 [前端 README](frontend/README.md)；页面验收清单、本地 Postman 与真实集成命令、实际结果见 [第 8 步文档](docs/stage-08.md)。

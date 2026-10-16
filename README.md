# OnCallAgent

OnCallAgent 是基于 Go 的智能运维 Agent 应用。当前实现 GoFrame 服务骨架、基于 Eino 的普通聊天，以及豆包 Embedding 和 Milvus 向量存取，后续按需加入会话记忆、知识库问答和工具调用。

固定使用 Go **1.23.12**、GoFrame **v2.7.1**、Eino **v0.6.0**、Eino OpenAI 模型扩展 **v0.1.1**。所有 Go 命令设置 `GOTOOLCHAIN=local`，避免自动切换工具链。依赖版本由 `backend/go.mod` 和 `backend/go.sum` 锁定。

```text
backend/
├── main.go                  # 启动时创建一次模型客户端并注入 ChatService
├── cmd/                     # 模型检查、向量诊断、集合初始化和验收命令
├── api/{health,chat}/v1/    # 请求和响应定义
├── internal/
│   ├── config/             # 配置读取、环境变量覆盖和校验
│   ├── controller/         # 接收 HTTP 请求，调用 service
│   └── service/            # 健康检查、ChatService 和知识库向量服务
├── utility/
│   ├── client/             # 聊天、Ark Embedding 和 Milvus 客户端
│   └── middleware/         # message/data 统一响应及状态码
└── test/                   # 所有测试文件和本地协议测试
```

后续前端代码放在根目录 `frontend/`，当前没有前端页面或运行时 mock 模式。

## 配置与启动

在 `backend/.env` 中填写 `backend/.env.example` 列出的变量，然后启动：

```bash
cd OnCallAgent/backend
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

`Id` 和 `Question` 必须为非空字符串。当前是单轮聊天，`Id` 为后续会话记忆保留，同一 Id 的请求尚不会携带历史消息。

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

实现说明见 `docs/stage-01.md`、`docs/stage-02.md` 和 `docs/stage-03.md`。

## Milvus 与 Embedding

当前支持云端 Milvus 2.4.x，以及豆包 `doubao-embedding-vision-251215` 的文本向量化。使用 Eino Ark Embedding **v0.1.2**、Milvus Go SDK **v2.4.2**；Go 1.23.12、GoFrame 和 Eino 版本保持不变。

配置示例见 `backend/.env.example`。将实际地址和密钥填入已忽略的 `backend/.env`，不要直接覆盖已有的聊天配置。下面的命令只读取所需的配置，不需要聊天模型凭据。

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_EMBEDDING_BASE_URL` | `https://ark.cn-beijing.volces.com/api/v3` |
| `ONCALL_EMBEDDING_MODEL` | `doubao-embedding-vision-251215` |
| `ONCALL_EMBEDDING_API_KEY` 或 `ARK_API_KEY` | 必填 |
| `ONCALL_EMBEDDING_TIMEOUT` | `30s` |
| `ONCALL_MILVUS_ADDRESS` | `localhost:19530` |
| `ONCALL_MILVUS_DATABASE` | `default` |
| `ONCALL_MILVUS_COLLECTION` | `oncall_knowledge` |
| `ONCALL_MILVUS_TLS` | `false` |
| `ONCALL_MILVUS_CONNECT_TIMEOUT` | `5s` |
| `ONCALL_MILVUS_TIMEOUT` | `60s` |
| `ONCALL_MILVUS_USERNAME` / `ONCALL_MILVUS_PASSWORD` | 不认证 |

所有配置直接从环境变量读取，包括显式空值。`ONCALL_EMBEDDING_API_KEY` 若存在就优先于 `ARK_API_KEY`，显式空值会校验失败。向量维度由模型实际返回值确定。Base URL 填 API 根路径，不能包含完整的 `/embeddings/multimodal` 端点。Milvus 地址填 `host:port`，TLS 通过独立开关启用；使用系统 CA 验证服务端证书，不支持自定义 CA、证书域名或双向 TLS。

```bash
cd OnCallAgent/backend

# 调用真实模型，输出实际维度。
GOTOOLCHAIN=local go run ./cmd/embedding_check

# 只读诊断；集合尚不存在、结构不兼容或未加载时以非零状态退出。
GOTOOLCHAIN=local go run ./cmd/milvus_check

# 显式创建集合、向量索引并加载；数据库必须事先存在。
GOTOOLCHAIN=local go run ./cmd/init_knowledge
GOTOOLCHAIN=local go run ./cmd/milvus_check

# 写入一条带唯一 ID 的验收记录，再用相同文本进行真实向量检索。
GOTOOLCHAIN=local go run ./cmd/knowledge_check
```

专用集合字段为 `id`（VARCHAR 主键，256 字节，手动 ID）、`content`（VARCHAR，65535 字节）、`metadata`（JSON）和 `vector`（FLOAT_VECTOR，实际探测维度）。使用 HNSW/COSINE 索引（M=16、efConstruction=128），检索显式使用 COSINE 和 HNSW search param（ef 至少为 64 和 topK 中的较大值）。写入使用 Upsert，相同 ID 更新已有文档；检索使用 Strong 一致性。

`milvus_check` 输出服务版本、SDK 版本、schema、索引参数/状态、集合加载状态及差异，不创建、不加载、不写入。`init_knowledge` 先探测模型维度；已有集合的 schema 或向量索引不兼容时输出差异并停止，不删除或重建集合。兼容集合允许补建缺失索引和加载，重复运行不会重建已有集合和索引。更换 Embedding 模型时应使用新集合，即使新旧模型维度相同，也不能混用向量空间。

`knowledge_check` 成功返回 `matched=true`、维度和 COSINE 得分；同时验证 ID、内容、来源 metadata，并要求得分至少 0.99。每次执行会保留一条 `source=knowledge_check` 的验收记录；不清空集合。

连接工厂在 `utility/client`，集合检查、初始化和 Eino Indexer/Retriever 适配层在 `internal/service/knowledge`。命令在 `internal/command/knowledge` 统一管理客户端创建和关闭。当前 Web 服务继续提供普通聊天，知识库尚未参与 `/api/chat`。

新增测试位于 `backend/test/{config,embedding,milvus,knowledge}`。构建所有命令可执行 `GOTOOLCHAIN=local go build ./...`；测试与竞态检测沿用前述命令。真实验收结果和版本依据见 `docs/stage-03.md`。

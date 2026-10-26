# 配置

[文档索引](README.md) · [快速开始](../README.md#快速开始) · [CLI](cli.md)

后端配置来自进程环境变量和 `.env`，不使用 YAML 配置。可复制 [backend/.env.example](../backend/.env.example) 作为起点；实际 `.env` 已被 Git 忽略，不应提交到仓库。

## 加载与优先级

**已有进程环境变量 > `.env` > 代码默认值**。

入口先查找当前工作目录的 `.env`，不存在时查找 `backend/.env`，只加载第一份存在的文件。没有文件时允许完全使用进程环境；文件存在但不可读或语法错误时明确失败。相对文件与日志目录均相对进程工作目录，建议从 `backend/` 启动后端。

```bash
# 仓库根目录：首次创建配置，不覆盖已有文件
if [ ! -e backend/.env ]; then
  cp backend/.env.example backend/.env
fi

# 在 backend/ 中临时覆盖一个配置，不写回 .env
cd backend
ONCALL_SERVER_PORT=6873 GOTOOLCHAIN=local go run .
```

已存在的空环境变量也不会被 `.env` 覆盖。必填字段、布尔值、数字、时长等显式为空会校验失败；可选认证字段允许为空。时长使用 Go 格式，如 `750ms`、`30s`、`30m`，必须为正。不要在 Base URL 中嵌入账号密码、查询参数或片段。

## 服务与聊天模型

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_SERVER_PORT` | `6872` | 整数，1–65535 |
| `ONCALL_CHAT_MODEL_BASE_URL` | 无，必填 | OpenAI 兼容 API 基础地址，不含 `/chat/completions` |
| `ONCALL_CHAT_MODEL_MODEL` | 无，必填 | 服务商实际可调用的模型 ID |
| `ONCALL_CHAT_MODEL_API_KEY` | 无，必填 | 模型密钥 |
| `ONCALL_CHAT_MODEL_TIMEOUT` | `30s` | 单次模型请求超时 |

聊天和运维模型需要支持工具调用。Base URL 常以 `/v1` 结尾，但以实际提供方为准。例如 `https://your-provider.example/v1`，不要填成完整的聊天请求端点。`modelcheck` 可检查模型目录和一次调用；部分网关不提供 `/models`，详见 [CLI](cli.md#模型检查)。

## Embedding 与 Milvus

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_EMBEDDING_BASE_URL` | `https://ark.cn-beijing.volces.com/api/v3` | Ark API 基础地址，不含 `/embeddings` |
| `ONCALL_EMBEDDING_MODEL` | `doubao-embedding-vision-251215` | 使用多模态文本端点的模型 ID |
| `ARK_API_KEY` | 无 | Embedding 密钥，与聊天密钥无自动关联 |
| `ONCALL_EMBEDDING_API_KEY` | 未设置 | 存在时优先于 `ARK_API_KEY`，即使值为空 |
| `ONCALL_EMBEDDING_TIMEOUT` | `30s` | 一次 EmbedStrings 调用预算 |
| `ONCALL_MILVUS_ADDRESS` | `localhost:19530` | `host:port`，不含 URL scheme |
| `ONCALL_MILVUS_DATABASE` | `default` | 已存在的数据库 |
| `ONCALL_MILVUS_COLLECTION` | `oncall_knowledge` | 知识集合 |
| `ONCALL_MILVUS_USERNAME` | 空 | 与密码同时提供或同时省略 |
| `ONCALL_MILVUS_PASSWORD` | 空 | Milvus 密码 |
| `ONCALL_MILVUS_TLS` | `false` | TLS 开关，使用 SDK 标准 TLS 配置 |
| `ONCALL_MILVUS_CONNECT_TIMEOUT` | `5s` | 建立连接预算 |
| `ONCALL_MILVUS_TIMEOUT` | `60s` | 初始化或存取操作预算 |

环境变量优先级按同名变量计算；`ONCALL_EMBEDDING_API_KEY` 与 `ARK_API_KEY` 是两个不同名称，前者只要存在就被选择。若希望使用进程中的 `ARK_API_KEY`，应移除 `.env` 中显式配置的 `ONCALL_EMBEDDING_API_KEY`，而不是把它留空。

维度从实际向量输出确定，不配置预期维度。已有集合必须与实际模型输出、FLOAT_VECTOR、HNSW/COSINE 匹配；更换 Embedding 模型时，即使维度相同也不能混用不同向量空间。请使用独立集合重新入库，项目不会自动迁移已有向量。

## 文件与检索

| 环境变量 | 默认值 | 校验 / 行为 |
| --- | --- | --- |
| `ONCALL_FILE_DIR` | `./data/knowledge` | 保存上传的版本文件；内部规范化为绝对路径 |
| `ONCALL_UPLOAD_MAX_BYTES` | `10485760` | 正整数，默认 10 MiB；服务端做最终校验 |
| `ONCALL_KNOWLEDGE_CHUNK_SIZE` | `2000` | 4–65535，UTF-8 字节数 |
| `ONCALL_KNOWLEDGE_CHUNK_OVERLAP` | `200` | 非负，且小于 chunk size |
| `ONCALL_KNOWLEDGE_BATCH_SIZE` | `16` | 正整数，每批写入片段数 |
| `ONCALL_RETRIEVAL_TOP_K` | `3` | 1–16384 |
| `ONCALL_RETRIEVAL_SCORE_THRESHOLD` | `0.5` | 有限数，范围 [-1,1] |

切分大小按字节计算，不是字符数或 token 数。Markdown 标题作为片段标题；来源与版本用于稳定 ID 和旧版本清理。阈值 `-1` 可用于观察未按分数过滤的邻近结果，不代表所有结果都相关。

## 对话与会话记忆

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_CHAT_AGENT_MAX_STEPS` | `12` | 正整数；Eino ReAct 内部图执行步数，不是工具调用轮数 |
| `ONCALL_CHAT_AGENT_TIMEOUT` | `60s` | 等待会话锁及整次对话执行的总预算 |
| `ONCALL_CHAT_MEMORY_MAX_ROUNDS` | `3` | 正整数；完整 user/assistant 对数 |
| `ONCALL_CHAT_MEMORY_TTL` | `30m` | 从最近成功提交起计算；读取和失败不续期 |

模型、工具自身的较短超时仍生效，父请求更早截止时优先。浏览器默认以 30 分钟提示上下文可能失效，但接口不提供实际会话过期状态；该提示不能确认后端真实状态。

## 工具与 Prometheus

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED` | `true` | 注册 `query_internal_docs` |
| `ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED` | `true` | 注册 `get_current_time` |
| `ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED` | `false` | 注册 `query_prometheus_alerts` |
| `ONCALL_TIME_ZONE` | `Asia/Shanghai` | 有效 IANA 时区，可用 `UTC`，不可用隐式 `Local` |
| `ONCALL_PROMETHEUS_BASE_URL` | `http://127.0.0.1:9090` | 请求时追加 `/api/v1/alerts`，可含路径前缀 |
| `ONCALL_PROMETHEUS_TIMEOUT` | `15s` | 单工具请求预算 |
| `ONCALL_PROMETHEUS_AUTH_TYPE` | `none` | `none`、`basic` 或 `bearer` |
| `ONCALL_PROMETHEUS_USERNAME` | 空 | Basic 用户名 |
| `ONCALL_PROMETHEUS_PASSWORD` | 空 | Basic 密码 |
| `ONCALL_PROMETHEUS_BEARER_TOKEN` | 空 | Bearer Token |

认证组合必须一致：`none` 不接受非空认证字段；`basic` 只填写用户名和密码；`bearer` 只填写 Token。认证值不能含换行。工具启用且本次操作需要它时才加载连接配置、创建客户端；列工具定义不连接外部服务。

Web 即使关闭内部文档工具，固定聊天拓扑仍需要首轮 Retriever，因此不能据此省略 Embedding/Milvus。独立运维 CLI 关闭文档工具后可以不初始化向量依赖。

## 运维 Agent 与模型继承

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_OPS_MAX_ITERATIONS` | `8` | 正整数；Executor → Replanner 循环次数 |
| `ONCALL_OPS_EXECUTOR_MAX_STEPS` | `12` | 正整数；每次执行计划步骤时的模型生成上限 |
| `ONCALL_OPS_TIMEOUT` | `180s` | 运维任务总预算；CLI 还覆盖依赖初始化 |

规划与重规划使用 `ONCALL_OPS_PLANNER_*`，执行使用 `ONCALL_OPS_EXECUTOR_*`。两个前缀均接受以下后缀：

| 后缀 | 未设置时的来源 |
| --- | --- |
| `_BASE_URL` | `ONCALL_CHAT_MODEL_BASE_URL` |
| `_MODEL` | `ONCALL_CHAT_MODEL_MODEL` |
| `_API_KEY` | `ONCALL_CHAT_MODEL_API_KEY` |
| `_TIMEOUT` | `ONCALL_CHAT_MODEL_TIMEOUT`，其未设置时为 `30s` |

逐字段合并后校验；专用字段显式为空会失败，不回退到聊天配置。独立运维 CLI 若提供完整的两套专用模型配置，则无需普通聊天模型凭据。Web 还要构造 ChatAgent，仍需有效的聊天模型配置。

## 日志与前端

| 后端环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_LOG_LEVEL` | `info` | `debug`、`info`、`warn`、`error` |
| `ONCALL_LOG_FILE_ENABLED` | `true` | Web 同时输出 stderr；CLI 运行日志仅写文件 |
| `ONCALL_LOG_DIR` | `./log` | 非空可写目录，相对工作目录 |
| `ONCALL_LOG_RETENTION_DAYS` | `7` | 1–36500，保留今天及之前六天的日志 |

`NO_COLOR=1` 关闭交互终端颜色。详情见[日志与排障](observability.md)。

| 前端开发环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ONCALL_API_ORIGIN` | `http://127.0.0.1:6872` | 仅 HTTP(S) origin，不含 `/api`、凭据、query 或 fragment |
| `ONCALL_FRONTEND_PORT` | `8080` | 开发服务监听 `127.0.0.1` |
| `ONCALL_API_PROXY_TIMEOUT_MS` | `210000` | 正整数；代理空闲超时，应大于后端任务预算 |

前端启动脚本不读取后端 `.env`。页面始终请求同源 `/api`，生产部署由反向代理完成转发。[frontend/config.js](../frontend/config.js) 中的 `uploadMaxBytes` 是公开的浏览器预校验配置，应与后端大小限制协调，不能放入密钥。

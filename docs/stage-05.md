# 第 5 步：检索组件与基础工具

提供复用现有 Store 的 Milvus Retriever、内部文档与时间工具，以及独立的 `recall_cmd`。普通聊天保持当前单轮行为，工具注册模块可供后续 Agent 注入。

## 文件与配置

以下路径相对 `backend/`：

- `internal/config/retrieval.go`、`.env.example`：检索与工具配置及校验。
- `internal/ai/retriever/retriever.go`：Eino Retriever 薄封装、结构化结果与错误分类。
- `internal/ai/tools/query_internal_docs.go`、`get_current_time.go`、`registry.go`：两个工具、按配置注册、输入输出定义及按名调用。
- `cmd/recall_cmd/main.go`：普通检索、工具定义查看、工具调用。
- `internal/service/knowledge/store.go`：底层默认 TopK 统一为 3；写入、向量转换、搜索和客户端创建方式保持不变。
- `test/config/`、`test/knowledge/`、`test/retriever/`、`test/tools/`、`test/recall/`：默认测试、CLI 子进程测试和显式真实验收。

Go 固定 1.23.12；Eino v0.6.0、Ark Embedding、Milvus SDK 和其它依赖版本保持不变。实际 `.env` 未修改，新增示例：

```dotenv
ONCALL_RETRIEVAL_TOP_K=3
ONCALL_RETRIEVAL_SCORE_THRESHOLD=0.5
ONCALL_TOOLS_QUERY_INTERNAL_DOCS_ENABLED=true
ONCALL_TOOLS_GET_CURRENT_TIME_ENABLED=true
ONCALL_TIME_ZONE=Asia/Shanghai
```

优先级为进程环境变量 > `.env` > 默认值。TopK 为 1–16384；阈值必须为有限数且位于 [-1,1]。布尔值必须可解析，时区必须是有效的显式 IANA 名称（也支持 UTC），不使用依赖运行主机的 `Local`。标准库 tzdata 随程序提供时区数据。显式空配置报错。

## 检索接口和数据流

`NewMilvusRetriever(base, cfg)` 接收已初始化的 Eino Retriever，实际运行注入 `knowledge.Store`。客户端由 CLI 创建、复用并在退出时关闭；Retriever 和工具不创建连接。

- `Retrieve(ctx, query, opts...)` 满足 Eino Retriever 接口，返回 `[]*schema.Document`，保留全部 metadata 和 `Document.Score()`。
- `Search(ctx, query)` 返回结构化结果，工具与 CLI 共用此方法。
- 查询去除首尾空白，空查询在调用后端前拒绝；配置默认值可被调用方 TopK、阈值选项覆盖。索引、Embedder、子索引和 DSL 覆盖仍被拒绝。
- 保持同一 Embedding、实际集合维度、数值 FLOAT_VECTOR 转换、HNSW/COSINE、Strong 一致性和 `ef=max(64,TopK)`。维度不匹配时不会执行 Milvus Search。
- 按相似度降序返回最多 TopK 个片段，保留分数等于阈值的命中。结构化结果的来源或标题缺失时返回空字符串。

结构化结果示例：

```json
{
  "query": "认证失败如何恢复？",
  "documents": [
    {
      "id": "稳定片段ID",
      "content": "刷新签名密钥缓存，再验证令牌请求。",
      "source": "auth_runbook.md",
      "title": "恢复认证",
      "score": 0.777262
    }
  ]
}
```

无命中为 `{"query":"...","documents":[]}`，不是错误。空查询为 `ErrInvalidQuery`；依赖、维度或无效结果等查询失败为 `ErrQueryFailed`。底层依赖错误被隐藏，取消/超时继续支持 `errors.Is(err, context.Canceled/DeadlineExceeded)`。

COSINE 分数表示向量相似程度，不是正确答案的概率。0.5 是初始配置，实际数据变化后可通过配置调整；`--score-threshold -1` 可查看未过滤邻近结果，帮助判断阈值。

## 工具输入输出与注册

| 工具 | 输入 | 输出 |
| --- | --- | --- |
| `query_internal_docs` | `{"query":"非空问题"}` | 上述检索结果对象；无命中时 documents 为空数组，失败返回工具错误 |
| `get_current_time` | `{}` | timestamp、timezone、unix_seconds |

时间输出示例（使用固定测试时钟）：

```json
{"timestamp":"2026-10-16T08:00:00.123456789+08:00","timezone":"Asia/Shanghai","unix_seconds":1792108800}
```

时间工具单次调用读取一次时钟，按配置时区生成 RFC3339Nano 时间（UTC 使用 Z），允许构造时注入时钟函数。

`NewRegistry(cfg, retriever, clock)` 按两个开关构造启用工具；`Tools()` 返回 Eino `[]tool.BaseTool`，`Invoke(ctx,name,input)` 按名称执行，未知和禁用工具分别报错。仅启用时间工具时允许 Retriever 为空，启用文档工具时要求已初始化的 Retriever。

`Definitions(cfg)` 从与实际工具相同的 Go 输入输出类型生成 Schema，无需连接云端。工具通过 Eino v0.6.0 的工具辅助函数构造，`InvokableRun` 返回 JSON 对象字符串，CLI 以 JSON 原文输出，不会再次编码为带转义的字符串。工具函数不调用 `log.Fatal`。

## 运行与验收命令

```bash
cd OnCallAgent/backend

# 不要求 Embedding、Milvus 或聊天凭据
GOTOOLCHAIN=local go run ./cmd/recall_cmd --list-tools
GOTOOLCHAIN=local go run ./cmd/recall_cmd --tool get_current_time --input '{}'

# 读取现有 .env，集合需已初始化；检索 CLI 不会隐式创建或加载集合
GOTOOLCHAIN=local go run ./cmd/recall_cmd --query '服务认证失败后如何恢复？'
GOTOOLCHAIN=local go run ./cmd/recall_cmd --query '服务认证失败后如何恢复？' --top-k 3 --score-threshold 0.5
GOTOOLCHAIN=local go run ./cmd/recall_cmd --tool query_internal_docs --input '{"query":"服务认证失败后如何恢复？"}'

# 默认测试不调用云端
GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...

# 显式真实验收：创建并保留独立集合，不修改默认工作集合
ONCALL_INTEGRATION=1 GOTOOLCHAIN=local go test -v -count=1 -timeout 120s \
  ./test/retriever -run '^TestRetrievalIntegration$'
```

CLI 的普通检索、列定义和工具调用模式互斥。`--input` 仅用于工具调用；检索参数仅用于普通检索或文档工具。CLI 参数覆盖本次进程的有效检索配置，不写回 `.env`。普通检索不受文档工具开关限制；时间工具只初始化时间依赖。

标准输出仅为 JSON。无结果退出码为 0；非法参数、禁用/未知工具和依赖失败非零退出，错误写入 stderr。SIGINT/SIGTERM 会取消正在进行的连接或查询并执行客户端关闭。

## 本次验收结果（2026-10-16）

全量默认测试、全量竞态检测、`go vet` 和所有入口构建通过。覆盖默认值、环境变量优先级、显式空值及非法配置、TopK/阈值覆盖、阈值边界、空结果、取消和超时、向量数值转换、COSINE/HNSW 参数、维度不匹配、工具 Schema/JSON、时区/时钟、工具启用组合、CLI 模式与 SIGTERM 取消。

真实验收使用：

- 数据库 `default`，保留集合 `stage05_acceptance_7806ad0abc810a1ebfe6cfe0664dfee4`。
- 模型 `doubao-embedding-vision-251215`，实际维度 2048，FLOAT_VECTOR + HNSW/COSINE。
- 文档 `test/retriever/testdata/auth_runbook.md`，通过知识入库 Graph 写入三个标题片段；TopK=3、阈值=0.5。

| 问题 | 过滤后命中 | 实际 COSINE 分数 |
| --- | --- | --- |
| AuroraAuth 密钥轮转后出现401，如何刷新签名密钥缓存恢复认证？ | 3 | 0.835120、0.746768、0.614688 |
| 介绍如何用黄油制作法式可颂的烘焙温度和面团折叠方法。 | 0 | 未过滤邻近分数为 0.130839、0.124676、0.119807 |
| AuroraAuth 密钥轮转后令牌签名校验失败，应该怎么恢复，并如何确认认证已经恢复？ | 3 | 0.778572、0.777262、0.711854；包含“恢复认证”和“验证恢复” |

来源均为 `auth_runbook.md`，三个稳定 ID 为：

```text
8ef73c8769d812b8b95e6d005ce841d27bb89adacb125522fae5dbbef0309dd5  AuroraAuth 认证故障处理
d589afede1f9869e205817b33d4bea1c8dcd71e558604942304481ab684e157f  恢复认证
28d5ba0fbc7c8e872be9d0fa5cc485b2845445622bdcf26cd9c9926c44a6a402  验证恢复
```

同一 Retriever 注入文档工具后，每个问题的工具返回 ID 集合与直接检索一致。实际 CLI 也验证了配置覆盖、文档工具 ID 一致、无结果正常退出、TopK=1 与阈值=-1，以及坏云端配置不影响时间工具/列定义。Embedding 指向不可达本地端口时，CLI 非零退出、stdout 为空、stderr 仅包含安全的检索失败信息。

上述实际 CLI 可通过进程环境指定该验收集合复现，例如：

```bash
cd OnCallAgent/backend
ONCALL_MILVUS_COLLECTION=stage05_acceptance_7806ad0abc810a1ebfe6cfe0664dfee4 \
GOTOOLCHAIN=local go run ./cmd/recall_cmd \
  --query 'AuroraAuth 密钥轮转后令牌签名校验失败，应该怎么恢复，并如何确认认证已经恢复？'
```

本次未重复执行其它阶段的真实聊天、上传和独立向量集成测试；对应默认测试中的显式集成项为跳过。没有改动实际 `.env`、默认工作集合或根目录另一张 `workflow.json`。

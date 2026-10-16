# Milvus 与 Embedding 接入

## 实现

采用 Eino 官方 Ark Embedding v0.1.2，通过 `/embeddings/multimodal` 向 `doubao-embedding-vision-251215` 提交纯文本。客户端在每个命令开始时创建一次并复用，退出时关闭连接池。每次调用继承 context 和更早的 deadline，再附加配置超时；关闭自动重试以便诊断。实际输出必须是维度一致、可转换为 float32 的有限非零向量，不补齐、不截断、不转成二进制向量。

配置由 GoFrame 读取，再显式应用环境变量。优先级为进程环境、`.env`、YAML、默认值。密钥与 Milvus 认证只接受环境变量；`ARK_API_KEY` 可提供豆包凭据，显式设置的 `ONCALL_EMBEDDING_API_KEY` 优先。完整配置表与验收命令见根目录 README。

Milvus 通过单独工厂建立连接，支持指定地址、数据库、集合、用户名/密码或 Token、TLS、CA、证书 server name、连接超时与操作超时。每个命令持有并关闭自身的客户端；没有自动建库或随 Web 启动建集合的行为。

`internal/service/knowledge` 直接使用 SDK，满足 Eino v0.6.0 的 `indexer.Indexer` 和 `retriever.Retriever` 接口。Store 使用 Upsert 写入文档，支持自动生成字符串 ID；Retrieve 支持 topK 与分数阈值。集合和 Embedder 由构造函数传入，不使用全局连接变量或运行时 mock 模式。

| 字段 | 类型与约束 |
| --- | --- |
| `id` | VARCHAR 主键，max_length=256，auto_id=false |
| `content` | VARCHAR，max_length=65535 |
| `metadata` | JSON，保存来源等字段 |
| `vector` | FLOAT_VECTOR，dim=实际 Embedding 输出维度 |

禁用动态字段。索引为 HNSW/COSINE，M=16、efConstruction=128；查询使用 FLOAT_VECTOR、COSINE、HNSW SearchParam，ef=max(64, topK)，并显式使用 Strong 一致性，使完成的写入对后续检索可见。

`milvus_check` 只读版本、schema、各字段索引及状态、加载状态；未就绪时仍输出 JSON 报告，并非零退出。`init_knowledge` 先调用真实模型确定维度，再检查现有结构。已有 schema 或向量索引不匹配时报告差异，保留全部数据；结构兼容时允许补建缺失索引和加载。数据库不存在时报告失败，由操作者预先创建。

`embedding_check` 独立探测模型，不要求 Milvus 或聊天模型配置。`knowledge_check` 要求集合已初始化，通过同一个 Eino 适配层写入一条记录并进行 COSINE 检索，核对 ID、内容、来源和分数；验收记录保留，重复执行会新增记录。

## 版本依据

- Go 固定 1.23.12，GoFrame v2.7.1、Eino v0.6.0、聊天模型扩展 v0.1.1 保持不变。
- Milvus SDK 锁定 `github.com/milvus-io/milvus-sdk-go/v2 v2.4.2`。官方兼容表将 2.4.x 对应至 2.4 系列 SDK，本项目选择该系列最后的补丁版；其 go.mod 要求 Go 1.17，可使用当前工具链。该仓库已归档，保留它是为了配合现有 Milvus 2.4.1，后续升级服务端时重新评估 SDK。
- Ark Embedding 锁定 v0.1.2，其 go.mod 要求 Go 1.23.0、Eino v0.6.0，支持多模态 API。新增此组件是因为当前模型使用多模态端点，常规 `/embeddings` 客户端不适用。
- Ark 使用的 Volcengine SDK v1.2.30，以及所需 x/sync v0.16.0、x/sys v0.33.0 等传递依赖由 go.mod/go.sum 锁定。没有自动升级 Go 工具链。

依据：[Milvus SDK 兼容表](https://github.com/milvus-io/milvus-sdk-go)、[SDK v2.4.2 go.mod](https://github.com/milvus-io/milvus-sdk-go/blob/v2.4.2/go.mod)、[Eino Ark 组件](https://github.com/cloudwego/eino-ext/tree/main/components/embedding/ark)、[豆包向量化接口](https://docs.volcengine.com/docs/ark/vectorization)。

## 验收结果（2026-10-16）

真实服务验收已执行：

- Milvus 返回版本 **v2.4.1**，SDK **v2.4.2** 成功完成版本、schema、索引、加载状态读取及向量写入/检索。
- 模型 `doubao-embedding-vision-251215` 实际返回 **2048 维**。
- `default.oncall_knowledge` 已显式初始化为上述 schema，向量索引状态 Finished，加载状态 loaded。
- 验收记录 ID：`check_b8a4920d5d00efc38e62168043837bf2`。
- 检索得分 **0.9988304376602173**，`matched=true`，来源 metadata 已核对，记录保留。得分受真实模型输出影响，不要求恰好为 1。

自动测试覆盖配置覆盖和 YAML 密钥拒绝、实际 Ark 组件请求格式、维度与数值校验、超时取消、SDK 数据库与认证 metadata、TLS 连接，以及集合初始化幂等性、结构不匹配时不修改、FLOAT_VECTOR/COSINE 写入与检索。

云端认证与云端 TLS 集成：**未执行**，当前实例使用无认证、TLS=false；对应客户端行为通过本地 gRPC 协议测试验证。真实普通聊天模型调用不在本阶段验收范围。

最终本地验收：`GOTOOLCHAIN=local go test -race -timeout 90s ./test/...` 全部通过；`GOTOOLCHAIN=local go vet ./...` 和 `GOTOOLCHAIN=local go build ./...` 均通过。`init_knowledge` 在已存在的匹配集合上再次运行成功，未重建集合或索引；最终 `milvus_check` 返回 `ready=true`。

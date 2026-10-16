# Milvus 与 Embedding

## 核心流程

客户端集中在 `utility/client`：Ark `NewEmbedder` 配置 Base URL、模型、密钥、HTTP 客户端与多模态 API 类型；Milvus `NewClient` 配置地址、数据库、用户名密码、TLS 和连接超时。调用方创建一次、复用并关闭，所有调用继承 context 与超时。

Embedding 校验实际返回数量、维度与有限非零向量；SDK 写入前将 float64 数值转换为 float32。当前模型为 `doubao-embedding-vision-251215`，只提交文本，使用 `/embeddings/multimodal`，实际输出 2048 维。

知识库业务集中在 `internal/service/knowledge`，保留三个入口：

- `Init(ctx, db, emb, cfg)`：探测实际维度，检查已有结构，创建缺失集合/索引并加载，返回维度。只有 `init_knowledge` 命令或显式集成测试调用初始化。
- `NewStore(ctx, db, emb, cfg)`：从现有 schema 读取维度并校验结构、索引，不创建或加载集合。
- `Store/Retrieve`：满足 Eino Indexer/Retriever 接口，Upsert 文档，检索支持 TopK 与分数阈值，返回内容、来源 metadata 和得分。

集合固定为 VARCHAR 主键 `id`（256 字节）、VARCHAR `content`（65535 字节）、JSON `metadata`、FLOAT_VECTOR `vector`（实际维度），禁用动态字段和 auto_id。索引统一 HNSW/COSINE（M=16、efConstruction=128），查询显式指定 COSINE、HNSW SearchParam 和 Strong 一致性。

已有 schema、维度或索引不匹配时返回差异，不删除或重建已有集合。初始化可补建缺失索引；Store 构造函数要求已存在匹配索引。数据库需要预先创建，更换模型应使用新集合。

## 配置与精简范围

运行配置仅使用 `.env` 或进程环境，优先级为 **已有进程环境 > `.env` > 默认值**。显式空值接受校验，不隐式回退。示例见 `backend/.env.example`，变量表和运行命令见根目录 README。

保留基本用户名密码、TLS 开关和超时。移除双配置源、Token、自定义 CA、证书域名和预期维度配置；删除独立诊断报告、版本判断、三个检查/验收命令和通用命令分发层。第二阶段的 `modelcheck` 保留，Web 普通聊天契约不变。

Go 仍为 1.23.12、GoFrame v2.7.1、Eino v0.6.0、Ark Embedding v0.1.2、Milvus SDK v2.4.2，依赖版本未变。Volcengine SDK v1.2.30 由直接依赖改为间接依赖，继续由 go.mod/go.sum 锁定。

## 测试

默认测试不调用云端。配置测试覆盖默认值、环境变量与 `.env` 优先级、空值和无效端口/地址/超时；本地 HTTP 测试验证实际 Ark 组件的请求格式、鉴权、维度、取消和超时；gRPC 测试验证数据库、用户名密码和 TLS 对不可信证书的拒绝。知识库测试覆盖初始化幂等性、结构不匹配时不修改数据、FLOAT_VECTOR 写入、COSINE 检索及 metadata。

真实验收使用 `ONCALL_INTEGRATION=1` 显式开启 `TestKnowledgeIntegration`。测试读取真实配置，验证现有集合初始化不会改变集合 ID，通过同一 Eino 适配层写入并检索一条唯一记录，核对内容、来源和得分至少 0.99。验收记录保留，来源标记为 `knowledge_integration`。

## 本次验收（2026-10-16）

- `go test -race -timeout 90s ./test/...`、`go vet ./...`、`go build ./...` 全部通过，普通聊天和 modelcheck 的本地回归通过。
- 从仓库根目录启动实际服务，自动加载 `backend/.env`，health 返回 HTTP 200；临时进程已关闭。
- 独立 `init_knowledge` 命令仅凭 `.env` 再次运行成功，返回 dimension=2048、ready=true。
- 显式真实集成测试通过：Milvus **v2.4.1**、模型 **doubao-embedding-vision-251215**、维度 **2048**，已有集合 ID 保持不变。
- 保留验收记录 `check_33c7fe5ff82d0440c1a8a97493fc069e`，来源为 `knowledge_integration`，COSINE 检索得分约 **0.99952555**，内容和 metadata 核对通过。

云端认证/TLS 集成：**未执行**，当前实例采用无认证、TLS=false；对应用户名密码与拒绝不可信证书的行为通过本地 gRPC 测试。普通聊天真实模型调用不在本次验收范围。

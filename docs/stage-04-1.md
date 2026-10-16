# 第 4.1 步：KnowledgeIndexing 图编排

新增独立知识入库图，输入 `document.Source`，输出按片段序号排列的 `[]string`。上传接口与目录导入继续使用原有入口。

```text
START → FileLoader → MarkdownSplitter → MilvusIndexer → END
```

## 文件与契约

- `backend/internal/ai/agent/knowledge_index_pipeline/`：`config.go`、`loader.go`、`transformer.go`、`indexer.go`、`orchestration.go` 和 `workflow.json`。
- `backend/internal/service/knowledge/ingestion.go`：共享来源校验、读取、同来源锁和批量入库核心；`service.go` 复用这些函数。
- `backend/cmd/knowledge_cmd/main.go`：普通命令与开发调试入口，创建并关闭 Embedding、Milvus 客户端。
- `backend/test/knowledge/pipeline/`：图执行、Schema 一致性、调试服务、显式云端集成测试及 `testdata/runbook.md`。

```go
func BuildKnowledgeIndexing(
    ctx context.Context,
    cfg knowledge_index_pipeline.Config,
    store knowledge.DocumentStore,
) (compose.Runnable[document.Source, []string], error)
```

`Config` 提供 `Root` 和 `Knowledge config.Knowledge`。所有节点注册、边连接及 Compile 错误均向调用方返回。图内不创建客户端，也不关闭调用方注入的 Store。

`knowledge_index_pipeline/workflow.json` 是按编排代码整理的 Graph Schema，本阶段按最终要求不操作可视化编辑器，也不将它标为编辑器导出文件。已使用 Eino Dev 1.3.0 附带的 `eino-bin nclc gencode` 实际生成初始 Go 编排，再补齐依赖注入、构造函数及错误检查。Schema 中三个组件均标记为本地自定义适配组件；没有 Redis/OpenAI 组件或密钥。根目录的另一张 `workflow.json` 保留。

Agent 编排目录统一使用 `backend/internal/ai/agent/xxx_pipeline/`，后续两个 Agent 同样遵循此约定。共享业务逻辑保留在 `internal/service/knowledge/`；测试和 CLI 路径不变，因此启动与验收命令不变。

## 节点配置清单

| 节点 | Eino 类型、输入与输出 | 配置及行为 |
| --- | --- | --- |
| FileLoader | Loader：`document.Source → []*schema.Document` | `Config.Root`、`Knowledge.MaxUploadBytes`；只读根目录内常规 UTF-8 txt/md/markdown 文件，拒绝越界和子路径符号链接，不保存上传副本 |
| MarkdownSplitter | DocumentTransformer：`[]*schema.Document → []*schema.Document` | `Knowledge.ChunkSize/ChunkOverlap`；直接调用现有 `knowledge.Split`，保留标题和稳定 ID 算法 |
| MilvusIndexer | Indexer：`[]*schema.Document → []string` | 注入 `DocumentStore`、`Knowledge.BatchSize`；共享批量确认和旧版本清理逻辑，Embedding 由 Store 内部调用 |

`Root` 默认当前目录，来源默认统一为根目录内的相对路径并使用 `/`。上传通过 `WithLogicalSource(source)` LoaderOption 定向传给 `FileLoader`，在保留物理路径检查的前提下覆盖逻辑来源；该选项不改变图的输入输出或拓扑，独立 CLI 未传选项时仍使用相对路径。同一文件的绝对 URI、相对 URI 使用相同来源和 ID。原始字节的 SHA-256 用于 `document_hash`；片段保留 `source`、`title`、`chunk_index`、`content_hash`、`document_id` 和 `version`。切分参数变化会更新版本，批量大小变化不会更新版本。

上传使用独立的进程级来源锁覆盖保存与 Graph 调用；Indexer 在所有图实例间共享同来源写入锁，避免上传重入同一把锁。新版本所有批次的返回 ID 都确认一致后，才调用 `DeleteOld` 清理同来源旧版本。写入或清理失败时保留旧版本；使用相同内容重试可收敛。空白、空文件、非法 UTF-8、NUL 内容不进入 Store。该锁只协调当前进程，多进程并发更新同一来源需要后续增加跨进程协调。

## 启动与调试

沿用 `backend/.env`：进程环境变量优先于 `.env`，配置由现有加载器校验。集合须已通过 `init_knowledge` 初始化，知识图不会隐式创建或重建集合。

```bash
cd OnCallAgent/backend

# 首次使用集合时执行
GOTOOLCHAIN=local go run ./cmd/init_knowledge

# 普通模式：输出 JSON ID 数组后退出
GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --root ./test/knowledge/pipeline/testdata --file runbook.md

# 调试模式：注册图后等待 Ctrl+C / SIGTERM
GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --dev --root ./test/knowledge/pipeline/testdata --dev-port 52538

# 指定文件时先执行一次，再等待调试
GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --dev --root ./test/knowledge/pipeline/testdata --file runbook.md
```

普通模式要求 `--file`。调试输入为 `{"URI":"runbook.md"}`，图名为 `KnowledgeIndexing`。调试端口默认 52538，可用 `--dev-port` 覆盖。`devops.Init` 在 Compile 前注册回调；图注册成功后保持进程存活，收到 SIGINT/SIGTERM 时退出并关闭客户端。

DevOps v0.1.7 的调试服务监听所有网络接口，仅在开发环境启用。查询图列表：

```bash
curl -fsS http://127.0.0.1:52538/eino/devops/debug/v1/graphs
```

Go 固定 **1.23.12**，Eino 保持 **v0.6.0**，Embedding 与 Milvus SDK 版本不变。新增锁定 DevOps **v0.1.7** 用于图发现和调试回调；其依赖 `gorilla/mux v1.8.1`、`go-nanoid v1.5.1` 同步锁定。

## 验收命令

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...

# 显式调用真实 Embedding / Milvus，保留唯一来源的验收记录
ONCALL_INTEGRATION=1 GOTOOLCHAIN=local go test \
  ./test/knowledge/pipeline -run '^TestPipelineIntegration$' -v -count=1
```

默认测试不调用云端。真实测试只比较强一致性查询得到的可见主键集合，不通过物理行数判断 Upsert 幂等性。

## 本次验收结果（2026-10-16）

- 全量本地回归、竞态检测、`go vet` 和所有入口构建通过，包括普通聊天和 `modelcheck` 本地回归。
- 实际图执行通过：元数据及上传一致性、绝对/相对来源稳定 ID、空文档和非法文件、重复入库、文档更新、分批失败、返回 ID 不完整、清理失败及重试、取消传播、共享来源锁。
- Schema 与 Compile 回调捕获的真实图一致：三个业务节点、START/END、四条边和输入输出类型。
- 独立子进程调试服务通过：图发现、运行时画布五节点四条边、保持存活，以及 SIGINT/SIGTERM 正常退出。
- 实际 `knowledge_cmd --dev` 在 52539 启动；Postman CLI 检查图列表，HTTP 200 和图名两个断言通过；Ctrl+C 后正常退出。
- 真实云端图入库通过：保留来源 `stage04_1_acceptance_7c89ca4967edaca4ba72766aa9c3e5fe.md`，2 个片段；重复执行后可见主键集合不变。
- 实际普通 CLI 通过：保留来源 `stage04_1_cli_acceptance_c1932a6baf624d5995eba2e7ceed709c.md`，1 个片段；标准输出为 JSON ID 数组并正常退出；复用该来源验证 `--dev --file`，输出 ID 后保持存活并响应 SIGTERM。
- 其它阶段的真实聊天、上传和向量验收未在本次重复执行；默认回归中对应的显式集成测试为跳过。

## Agent 目录调整验收

当前图已迁移到 `backend/internal/ai/agent/knowledge_index_pipeline/`，同步更新包名、CLI 导入、Graph Schema 组件标识、Schema 测试路径及文档链接。

迁移后执行知识库本地回归、`go test -race ./test/knowledge/pipeline -count=1`（含调试服务发现和信号退出）、`go vet ./...`、`go build ./...`，均通过。该次仅调整目录与引用，真实云端集成测试未重复执行。CLI 启动命令保持不变。

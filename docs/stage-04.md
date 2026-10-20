# 知识上传与入库

## 入库核心

`KnowledgeService.Ingest(ctx, source, reader)` 校验并保存文件，再调用已编译的 KnowledgeIndexing Graph，由图内组件识别标题并切分、分批 EmbedStrings/Upsert，最后清理同一来源的旧版本。图接入与错误分类见下文“上传接口与 Graph 接入”。HTTP 使用原始文件名，目录导入使用相对路径作为来源，区分大小写。

文档 ID 是来源的 SHA-256；版本包含整文件内容 hash、切分大小、重叠长度和算法版本；分块 ID 包含文档 ID、版本与块序号。重复提交会使用相同 ID Upsert，改变批量大小不会改变分块 ID。每次写入均检查返回的 ID；只有全部批次确认后才删除其他版本。写入或清理失败返回可重试错误，重试收敛至完整新版本；失败期间允许新旧片段并存。

元数据包含 `source`、`title`、`chunk_index`、`content_hash`、`document_hash`、`document_id`、`version`。片段以 UTF-8 字节计数，不截断字符、不跨标题重叠；标题识别支持 # 至 ######，代码围栏中的 # 不作为标题。标题元数据最多 1024 字节，来源最多 4096 字节，正文完整保留在分块中。

保存名称由来源 hash 和整文件 hash 组成，保留小写扩展名；临时文件完整写入、关闭后原子重命名，使用文件 Stat 获取实际大小，解析保存目录的真实绝对路径。文件格式、空白、编码和大小在入库前检查。旧文件保留，旧版本清理只影响同一文档的向量记录。

同一来源在当前服务进程内串行处理，等待锁时可取消；本阶段约定不同进程不同时更新同一来源。客户端和 Graph 在启动时创建并复用，通过索引函数注入服务；不引入队列或新依赖。

## 配置

| 环境变量 | 默认值 |
| --- | --- |
| `ONCALL_FILE_DIR` | `./data/knowledge`，相对进程工作目录 |
| `ONCALL_UPLOAD_MAX_BYTES` | 10485760 |
| `ONCALL_KNOWLEDGE_CHUNK_SIZE` | 2000 字节 |
| `ONCALL_KNOWLEDGE_CHUNK_OVERLAP` | 200 字节 |
| `ONCALL_KNOWLEDGE_BATCH_SIZE` | 16 |

配置仍按进程环境 > `.env` > 默认值，显式空值报错。切分大小 4–65535，重叠非负且小于切分大小，上传大小和批量大小为正。

<a id="stage-04-1"></a>

## KnowledgeIndexing 图编排（第 4.1 步）

新增独立知识入库图，输入 `document.Source`，输出按片段序号排列的 `[]string`。上传接口与目录导入继续使用原有入口。

```text
START → FileLoader → MarkdownSplitter → MilvusIndexer → END
```

### 文件与契约

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

### 节点配置清单

| 节点 | Eino 类型、输入与输出 | 配置及行为 |
| --- | --- | --- |
| FileLoader | Loader：`document.Source → []*schema.Document` | `Config.Root`、`Knowledge.MaxUploadBytes`；只读根目录内常规 UTF-8 txt/md/markdown 文件，拒绝越界和子路径符号链接，不保存上传副本 |
| MarkdownSplitter | DocumentTransformer：`[]*schema.Document → []*schema.Document` | `Knowledge.ChunkSize/ChunkOverlap`；直接调用现有 `knowledge.Split`，保留标题和稳定 ID 算法 |
| MilvusIndexer | Indexer：`[]*schema.Document → []string` | 注入 `DocumentStore`、`Knowledge.BatchSize`；共享批量确认和旧版本清理逻辑，Embedding 由 Store 内部调用 |

`Root` 默认当前目录，来源默认统一为根目录内的相对路径并使用 `/`。上传通过 `WithLogicalSource(source)` LoaderOption 定向传给 `FileLoader`，在保留物理路径检查的前提下覆盖逻辑来源；该选项不改变图的输入输出或拓扑，独立 CLI 未传选项时仍使用相对路径。同一文件的绝对 URI、相对 URI 使用相同来源和 ID。原始字节的 SHA-256 用于 `document_hash`；片段保留 `source`、`title`、`chunk_index`、`content_hash`、`document_id` 和 `version`。切分参数变化会更新版本，批量大小变化不会更新版本。

上传使用独立的进程级来源锁覆盖保存与 Graph 调用；Indexer 在所有图实例间共享同来源写入锁，避免上传重入同一把锁。新版本所有批次的返回 ID 都确认一致后，才调用 `DeleteOld` 清理同来源旧版本。写入或清理失败时保留旧版本；使用相同内容重试可收敛。空白、空文件、非法 UTF-8、NUL 内容不进入 Store。该锁只协调当前进程，多进程并发更新同一来源需要后续增加跨进程协调。

### 启动与调试

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

### 验收命令

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

### 本次验收结果（2026-10-16）

- 全量本地回归、竞态检测、`go vet` 和所有入口构建通过，包括普通聊天和 `modelcheck` 本地回归。
- 实际图执行通过：元数据及上传一致性、绝对/相对来源稳定 ID、空文档和非法文件、重复入库、文档更新、分批失败、返回 ID 不完整、清理失败及重试、取消传播、共享来源锁。
- Schema 与 Compile 回调捕获的真实图一致：三个业务节点、START/END、四条边和输入输出类型。
- 独立子进程调试服务通过：图发现、运行时画布五节点四条边、保持存活，以及 SIGINT/SIGTERM 正常退出。
- 实际 `knowledge_cmd --dev` 在 52539 启动；Postman CLI 检查图列表，HTTP 200 和图名两个断言通过；Ctrl+C 后正常退出。
- 真实云端图入库通过：保留来源 `stage04_1_acceptance_7c89ca4967edaca4ba72766aa9c3e5fe.md`，2 个片段；重复执行后可见主键集合不变。
- 实际普通 CLI 通过：保留来源 `stage04_1_cli_acceptance_c1932a6baf624d5995eba2e7ceed709c.md`，1 个片段；标准输出为 JSON ID 数组并正常退出；复用该来源验证 `--dev --file`，输出 ID 后保持存活并响应 SIGTERM。
- 其它阶段的真实聊天、上传和向量验收未在本次重复执行；默认回归中对应的显式集成测试为跳过。

### Agent 目录调整验收

当前图已迁移到 `backend/internal/ai/agent/knowledge_index_pipeline/`，同步更新包名、CLI 导入、Graph Schema 组件标识、Schema 测试路径及文档链接。

迁移后执行知识库本地回归、`go test -race ./test/knowledge/pipeline -count=1`（含调试服务发现和信号退出）、`go vet ./...`、`go build ./...`，均通过。该次仅调整目录与引用，真实云端集成测试未重复执行。CLI 启动命令保持不变。

<a id="stage-04-2"></a>

## 上传接口与 Graph 接入（第 4.2 步）

`POST /api/upload` 现在经过已编译的 KnowledgeIndexing Graph 完成入库：

```text
上传控制器 → KnowledgeService 校验 → 原子保存版本文件
    → document.Source{URI: 实际保存路径}
    → FileLoader → MarkdownSplitter → MilvusIndexer
    → 文件信息 + chunkCount
```

### 实现与依赖

- `internal/service/knowledge/service.go`：注入 `IndexFileFunc`，负责校验、保存和调用 Graph；服务内不再直接切分、写入 Store 或删除旧向量。
- `internal/ai/agent/knowledge_index_pipeline/loader.go`、`upload.go`、`workflow.json`：增加逻辑来源选项和 Graph 调用适配，更新节点说明；输入输出和四条边不变。
- `main.go`：创建并解析上传目录的真实路径，以它作为 Loader 根目录；客户端、Store 和 Graph 均在启动时创建一次并复用。
- `internal/controller/upload/upload.go` 与 `utility/middleware/middleware.go`：控制器保持请求/响应职责，中间件统一映射保存、入库、超时和取消错误。
- `test/knowledge/`、`test/knowledge/pipeline/`、`test/upload/` 与 `test/testutil/knowledge_service.go`：服务流程、实际图执行、HTTP 错误契约和显式云端验收。

以上路径相对 `backend/`。Go 固定 1.23.12，Eino 及其它依赖版本不变；配置继续使用 `.env` 和进程环境变量，没有新增配置项或运行时 mock 模式。

服务构造函数使用以下依赖：

```go
type IndexFileFunc func(context.Context, document.Source, string) ([]string, error)

func NewKnowledgeService(cfg config.Knowledge, indexFile IndexFileFunc) (*KnowledgeService, error)
```

第三个参数为原始规范化逻辑来源。Graph 包的 `NewFileIndexer(graph)` 将其转换为 `WithLogicalSource(source)` LoaderOption，仅定向传给 `FileLoader`。业务服务不导入 Graph 包，避免循环依赖。

Loader 始终先校验物理 URI 是否位于根目录内、格式是否支持、是否为常规文件、路径是否含符号链接，以及实际字节数和文本内容。逻辑来源选项只覆盖文档元数据，不能放宽物理读取规则。未传选项的 CLI 保持根目录相对路径来源规则。

### 保存、幂等与并发

支持 txt、md、markdown，大小写不敏感；拒绝空文件、纯空白、BOM 后仅空白、非法 UTF-8、NUL 和超限文件。校验不通过时既不保存，也不调用 Graph。

保存名继续为 `来源SHA256_内容SHA256.扩展名`。文件完整写入、Sync、关闭、原子重命名后，使用真实路径和 Stat 大小生成响应；旧版本文件继续保留。

`source` 仍为原始逻辑文件名，来源区分大小写。标题默认值、文档 ID、版本和片段 ID 沿用已有算法，不会因为保存名包含 hash 而改变。重复上传复用保存路径和主键；更改内容后仍属于同一逻辑文档。

上传来源锁在进程内跨服务实例共享，覆盖读取、保存和 Graph 调用。Indexer 使用独立的同来源写入锁，避免重复获取同一把锁导致死锁；不同来源可以并发。等待锁支持请求取消，跨进程同时更新同一来源仍需调用方协调。

全部新片段写入并确认 ID 后，Indexer 才清理同来源旧向量。保存失败不调用 Graph；入库失败保留已保存文件、已有旧向量和已写入的新片段，重试通过稳定 ID 收敛。成功的 `chunkCount` 取 Graph 返回 ID 数量；失败时不返回成功的片段数量。

### HTTP 契约

请求保持 `multipart/form-data`，仅接收一个 `file` 文件。服务端限制请求体为文件上限加 1 MiB multipart 开销，并检查解析结果、文件数量和实际读取大小。成功为 HTTP 200：

```json
{"message":"OK","data":{"fileName":"来源hash_内容hash.md","filePath":"/实际目录/来源hash_内容hash.md","fileSize":42,"source":"runbook.md","contentHash":"内容SHA256","chunkCount":1}}
```

| 情况 | HTTP | message | data |
| --- | --- | --- | --- |
| 缺少文件、空文件或非法文本 | 400 | invalid or empty document | null |
| multipart 请求无效 | 400 | invalid or empty document | null |
| 文件过大 | 413 | file is too large | null |
| 格式或 Content-Type 不支持 | 415 | unsupported file format | null |
| 保存、输入读取或保存后 Stat 失败 | 500 | file save failed | `{"stage":"save","retryable":true}` |
| Graph 入库或旧版本清理失败 | 503 | knowledge ingestion failed; retry the upload | `{"stage":"ingest","retryable":true}` |
| Graph 调用或排队等待超时 | 504 | knowledge ingestion timed out | `{"stage":"ingest","retryable":true}` |
| 请求取消且还能写回响应 | 408 | request cancelled | null |

使用 `errors.Is` 判断 `ErrSave`、`ErrIngest` 和 context 错误。错误响应不包含文件信息、chunkCount、底层路径或依赖错误详情。Graph 内部的文档错误按入库失败处理，不会与入口校验失败混为一类。

### 启动与 curl 验收

在 `backend/.env` 配置好聊天、Embedding 和 Milvus 后启动；集合需已初始化：

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/init_knowledge  # 首次初始化时执行
GOTOOLCHAIN=local go run .
```

另开终端执行：

```bash
printf '# 值班手册\n检查依赖健康状态。\n' > /tmp/oncall-stage04-2.md

# 成功与重复上传：均为 200，文件信息与 chunkCount 相同
curl -i -F 'file=@/tmp/oncall-stage04-2.md;filename=stage04-2-manual.md' http://127.0.0.1:6872/api/upload
curl -i -F 'file=@/tmp/oncall-stage04-2.md;filename=stage04-2-manual.md' http://127.0.0.1:6872/api/upload

# 格式错误：415
curl -i -F 'file=@/tmp/oncall-stage04-2.md;filename=invalid.pdf' http://127.0.0.1:6872/api/upload

# 空文件：400
: > /tmp/oncall-stage04-2-empty.txt
curl -i -F 'file=@/tmp/oncall-stage04-2-empty.txt' http://127.0.0.1:6872/api/upload

# 超限：413；此例按默认 10 MiB 上限生成，修改配置后须同步调整大小
python3 -c 'from pathlib import Path; Path("/tmp/oncall-stage04-2-large.txt").write_bytes(b"x" * (10485760 + 1))'
curl -i -F 'file=@/tmp/oncall-stage04-2-large.txt' http://127.0.0.1:6872/api/upload
```

### 自动验收与结果

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...

# 显式经过 HTTP、实际 Graph、Ark Embedding 和云端 Milvus
ONCALL_INTEGRATION=1 GOTOOLCHAIN=local go test -v -count=1 -timeout 120s \
  ./test/upload -run '^TestUploadIntegration$'
```

2026-10-16 验收结果：

- 全量本地测试、竞态检测、静态检查和所有入口构建通过；包括聊天、modelcheck、CLI 图、Schema 一致性和 DevOps 调试回归。
- 默认测试使用实际 Graph 和 MemoryStore，不访问云端；覆盖验证与保存前停止、实际文件信息、稳定 ID、重复与更新、分批失败及重试、旧版本保留、500/503/504/408 错误分类和响应脱敏。
- 跨服务实例同来源排队、等待取消、不同来源并发通过；没有上传锁与 Indexer 锁重入死锁。
- 真实 HTTP 集成通过，保留来源 `stage04_2_acceptance_1f194cac7f306bf200ce6601be41f509.md`，初始/最终均为 3 个片段；重复上传可见主键集合不变，分批写入后丢失确认仍保留旧版本，重试清理并收敛。未使用物理行数判断幂等性。
- 实际主入口在临时端口 16872、128 字节上限下通过成功、重复、格式错误与超限的 Postman 断言，保留 `stage04_2_http_acceptance_010dd96b8888486da1b3fc11067c9a8f.md`，62 字节、1 个片段。
- 用临时进程环境将 Embedding 指向不可达本机端口、使用假密钥，实际客户端失败返回 503/ingest；未修改 `.env`。
- 实际主入口在临时上传目录被普通文件占用时返回 500/save，验收后恢复目录。上述六个 Postman 场景共 12 个最终断言通过；非 2xx 场景的 CLI 退出码为 1，断言本身无失败。验收进程均已停止。
- 独立 `knowledge_cmd` 使用相同标记文件实测通过，返回 1 个 ID 并退出；未传逻辑来源选项时的默认路径规则保持兼容。
- 其它阶段的独立真实聊天和向量集成未重复执行；默认测试中的这些显式集成项为跳过。

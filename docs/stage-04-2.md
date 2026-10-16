# 第 4.2 步：上传接口接入知识库 Graph

`POST /api/upload` 现在经过已编译的 KnowledgeIndexing Graph 完成入库：

```text
上传控制器 → KnowledgeService 校验 → 原子保存版本文件
    → document.Source{URI: 实际保存路径}
    → FileLoader → MarkdownSplitter → MilvusIndexer
    → 文件信息 + chunkCount
```

## 实现与依赖

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

## 保存、幂等与并发

支持 txt、md、markdown，大小写不敏感；拒绝空文件、纯空白、BOM 后仅空白、非法 UTF-8、NUL 和超限文件。校验不通过时既不保存，也不调用 Graph。

保存名继续为 `来源SHA256_内容SHA256.扩展名`。文件完整写入、Sync、关闭、原子重命名后，使用真实路径和 Stat 大小生成响应；旧版本文件继续保留。

`source` 仍为原始逻辑文件名，来源区分大小写。标题默认值、文档 ID、版本和片段 ID 沿用已有算法，不会因为保存名包含 hash 而改变。重复上传复用保存路径和主键；更改内容后仍属于同一逻辑文档。

上传来源锁在进程内跨服务实例共享，覆盖读取、保存和 Graph 调用。Indexer 使用独立的同来源写入锁，避免重复获取同一把锁导致死锁；不同来源可以并发。等待锁支持请求取消，跨进程同时更新同一来源仍需调用方协调。

全部新片段写入并确认 ID 后，Indexer 才清理同来源旧向量。保存失败不调用 Graph；入库失败保留已保存文件、已有旧向量和已写入的新片段，重试通过稳定 ID 收敛。成功的 `chunkCount` 取 Graph 返回 ID 数量；失败时不返回成功的片段数量。

## HTTP 契约

请求保持 `multipart/form-data`，仅接收一个 `file` 文件。成功为 HTTP 200：

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

## 启动与 curl 验收

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

## 自动验收与结果

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

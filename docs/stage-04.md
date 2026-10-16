# 知识上传与入库

## 入库核心

`KnowledgeService.Ingest(ctx, source, reader)` 校验并保存文件，再调用已编译的 KnowledgeIndexing Graph，由图内组件识别标题并切分、分批 EmbedStrings/Upsert，最后清理同一来源的旧版本。图接入与错误分类的完整说明见 [第 4.2 步](stage-04-2.md)。HTTP 使用原始文件名，目录导入使用相对路径作为来源，区分大小写。

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

## 核心验收

本地配置及知识库测试、竞态检测通过：包含中文/emoji/长行切分、标题和围栏、稳定 ID、重复提交、文档更新、分批部分失败、清理失败与重试、读写失败、取消传播和删除筛选范围。默认测试不调用云端。

## HTTP 接口

`POST /api/upload` 使用 `multipart/form-data`，只允许一个 `file` 文件，扩展名支持 `.txt`、`.md`、`.markdown`，大小写不敏感。服务端先限制请求体（文件上限加 1 MiB multipart 开销），再检查 multipart 解析结果、文件数量和实际读取大小。编码必须为 UTF-8，空文件、纯空白和 NUL 字符被拒绝。

成功返回 HTTP 200：

```json
{"message":"OK","data":{"fileName":"来源hash_内容hash.md","filePath":"/绝对路径/来源hash_内容hash.md","fileSize":42,"source":"guide.md","contentHash":"整文件SHA-256","chunkCount":1}}
```

| 情况 | 状态码 | data |
| --- | --- | --- |
| multipart、数量、空文档或编码错误 | 400 | null |
| 文件或请求体超限 | 413 | null |
| 不支持的扩展名或 Content-Type | 415 | null |
| 保存失败 | 500 | `{"stage":"save","retryable":true}` |
| Graph 入库或清理失败 | 503 | `{"stage":"ingest","retryable":true}` |
| Graph 调用超时 | 504 | `{"stage":"ingest","retryable":true}` |
| 请求取消且还能写回响应 | 408 | null |

控制器只负责取得文件并调用服务；统一中间件隐藏底层错误。启动时创建并复用 ChatModel、Embedding 和 Milvus 客户端，退出时关闭；需要预先执行 `init_knowledge`，Web 服务不会自动创建集合。聊天接口仍然是单轮聊天。

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run ./cmd/init_knowledge
GOTOOLCHAIN=local go run .
```

在另一个终端执行上传和重复上传：

```bash
printf '# 值班手册\n检查健康状态，定位错误日志。\n' > /tmp/oncall-stage04.md
curl -i -F 'file=@/tmp/oncall-stage04.md;filename=stage04-manual.md' http://127.0.0.1:6872/api/upload
curl -i -F 'file=@/tmp/oncall-stage04.md;filename=stage04-manual.md' http://127.0.0.1:6872/api/upload
```

两次响应的保存路径、内容 hash、片段数量相同；向量主键集合不变。更新该文件内容后继续使用相同 `filename`，全部新片段写入成功后旧版本清理；原保存文件保留。不同文件名代表不同来源。

```bash
# 默认不连接云端。
GOTOOLCHAIN=local go test -timeout 60s ./test/upload ./test/chat
# 显式调用云端，保留 stage04_2_acceptance_<随机ID>.md 来源记录。
ONCALL_INTEGRATION=1 GOTOOLCHAIN=local go test -v -count=1 -timeout 120s \
  ./test/upload -run '^TestUploadIntegration$'
```

真实集成测试经过 HTTP 上传与实际 Ark/Milvus，查询确认重复上传的 ID 集合不变；第二批已写入但模拟丢失确认时旧版本仍完整，重试后仅保留新版本。使用随机来源隔离验收数据，不删除其他文档。

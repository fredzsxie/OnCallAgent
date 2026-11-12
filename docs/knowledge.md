# 知识库使用与迁移

[文档索引](README.md) · [API](api.md#知识库接口) · [配置](configuration.md) · [验收记录](knowledge-acceptance.md)

知识库使用 SQLite 保存文档目录和版本，原文以版本文件保存，Milvus 保存统一的切片集合。术语、告警 SOP 与参考资料可跨类型检索，通过元数据筛选。当前部署模型为单进程、单写入者；Web 和知识 CLI 不应同时对同一个目录执行写入或恢复操作。

## 编写知识

| 类型 | 建议粒度 | 内容 |
| --- | --- | --- |
| glossary | 一个术语一篇 | 中英文名称、缩写、别名、定义、应用场景、相关术语 |
| sop | 一个告警场景一篇 | 告警标识、触发条件、影响、排查、处理、验证、回滚和升级条件 |
| reference | 一个主题一篇 | 架构、配置、参考手册等 |

从[术语模板](templates/cdn-term.md)和[SOP 模板](templates/cdn-alert-sop.md)开始。它们是编写结构，不是已经批准的线上处理步骤。

Markdown 可包含 YAML front matter：

```yaml
---
title: 回源
docType: glossary
domain: cdn
service: cache
tags: [cache]
aliases: [Origin Fetch, origin]
alertNames: []
owner: CDN Team
sourceUrl: https://docs.example.com/cdn/origin
---
```

支持的键为 `title`、`docType`、`domain`、`service`、`tags`、`aliases`、`alertNames`、`owner`、`sourceUrl`；未知键会被拒绝。数组最多 32 项，每项最多 256 UTF-8 字节；标题等文本字段最多 1024 字节，domain/service 最多 256 字节。省略类型时使用 reference，省略标题时使用逻辑来源文件名。

上传表单显式填写的字段覆盖 front matter；API 的显式空值参与覆盖，空数组清空列表；空标题和类型再按默认规则归一化。省略字段使用文件值或默认值。更新会根据新文件及显式字段重新生成元数据；页面会预填当前文档元数据。TXT 使用表单元数据。

逻辑 `source` 是文档身份，与保存路径分离。可以使用 `cdn/cache/ttl.md` 等相对来源。新建时来源冲突返回 409；通过文档详情的“更新文件”替换同一文档，来源保持不变。

## 浏览与检索

页面“知识库”提供目录、正文、切片、上传和文件更新。目录支持标题、类型、领域、服务、标签和告警名筛选，默认每页 20 篇。检索结果中的标题可以定位到文档切片；文档已更新时展示当前版本并提示差异。缺失历史原文的文档标记 indexOnly，只展示片段。

| 模式 | 得分 | 行为 |
| --- | --- | --- |
| dense | cosine 相似度 | 调用与入库相同的 Embedding，进行 HNSW/COSINE 检索 |
| bm25 | bm25 | Milvus 根据查询文本执行全文检索，不调用 Embedding |
| hybrid | rrf | 同时执行两路召回，使用 RRF(k=60) 融合排序 |

页面默认 hybrid、TopK=10，HTTP TopK 范围为 1–100；聊天及 AI Ops 默认 hybrid、TopK=3，CLI 可通过 `--mode` 覆盖。`ONCALL_RETRIEVAL_SCORE_THRESHOLD` 只用于 dense；BM25 和 RRF 分数不是概率，也不能与 COSINE 数值直接比较。混合检索任一路失败会返回错误。

每路初始候选数为 max(20, 5×TopK)，最多 16384。服务根据 SQLite 当前版本过滤待发布及旧片段，候选不足时扩大召回；达到上限后返回实际有效结果。类型和标签等过滤同时作用于两路召回。分词使用 jieba 和 lowercase；告警标识还可用精确元数据过滤。

在仓库根目录执行示例：

```bash
curl -sS http://127.0.0.1:6872/api/knowledge/search \
  -H 'Content-Type: application/json' \
  -d '{"query":"回源超时怎么排查","mode":"hybrid","topK":5,"filters":{"docType":"sop","domain":"cdn"}}'

GOTOOLCHAIN=local go -C backend run ./cmd/recall_cmd \
  --query 'CDN_EDGE_5XX' --mode bm25 --top-k 5
```

CLI 入库复用文件根目录和符号链接检查，并经过同一目录及发布服务：

```bash
GOTOOLCHAIN=local go -C backend run ./cmd/knowledge_cmd \
  --root /absolute/path/to/knowledge --file cdn/origin.md
```

Eino Dev 保留 FileLoader → MarkdownSplitter → MilvusIndexer 图用于调试。v2 图直接执行只暂存片段；正常入库请使用知识 API 或知识 CLI，以完成目录发布。

## 更新与恢复

发布顺序为保存原文、分批写入、Strong 查询确认全部切片可见、SQLite 事务切换 active_version、清理旧片段。正文、规范化元数据、模型与索引配置、切分参数共同决定版本指纹；同版本重试不会新增逻辑片段。

| 状态 | 含义 |
| --- | --- |
| indexing | 最新版本正在写入，检索仍使用已有 active_version |
| ready | 当前版本已发布，清理完成 |
| failed | 最新尝试失败，原有发布版本继续提供服务 |
| cleanup_pending | 新版已发布，旧片段清理将在恢复或重复入库时重试 |

首次上传失败后可以用相同文件和元数据重试；失败文档也可以通过更新接口携带空 expectedVersion 明确替换未发布内容。更新接口要求携带 expectedVersion；409 后重新加载正文核对再重试。取消或网络中断时，请刷新目录确认服务端状态。

Web 启动恢复会把中断的 indexing 标记为 failed，并重试 cleanup_pending。所有写入路径必须遵守单写入者约束；进程内来源锁不提供跨进程协调。

## 从旧集合迁移

迁移不修改源集合。目标集合和 SQLite 文件必须成对配置；目录还会绑定数据库、集合、Embedding 模型、维度及分析器配置。更换 Embedding 模型时使用独立集合和目录重新入库。

1. 备份原始文件和原配置，停止旧 Web 后端及知识 CLI 写入。
2. 准备新的持久化目录；将 `ONCALL_FILE_DIR` 指向包含旧版本文件的目录用于原文恢复，将 `ONCALL_KNOWLEDGE_CATALOG_PATH` 指向新的 SQLite 文件。
3. 在 backend 中执行预检查，默认只读：

   ```bash
   GOTOOLCHAIN=local go run ./cmd/knowledge_migrate \
     --source-collection oncall_knowledge \
     --target-collection oncall_knowledge_v2
   ```

4. 检查 sourceChunks、documents、indexOnly 和 conflicts。多版本冲突需要人工确定正确旧版本，并传入 `--versions selections.json`：

   ```json
   {"cdn/origin.md":"完整的旧版本指纹"}
   ```

5. 确认来源和目标后，在同一命令中增加 `--apply`。迁移先检查或创建目标 Schema 与双索引；优先读取 hash 匹配的原文重新切分，无原文时导入历史片段并标记 indexOnly。失败后可重复执行。
6. 确认报告 verified=true，并核对目录数量、关键查询、正文、更新与失败恢复。verified 表示源快照在迁移过程中未变化且文档写入完成，不替代业务知识质量验收。
7. 将新版本的 `ONCALL_MILVUS_COLLECTION` 和 `ONCALL_KNOWLEDGE_CATALOG_PATH` 同时切到目标值。在公开代理切换前完成验证，再恢复知识写入。

扫描通过主键范围二分读取，支持超过 Milvus offset 窗口的数据。单篇文档最多 16384 个切片；超过时应拆分来源。默认总预算为 30 分钟，可用 `--timeout` 调整。

## 备份与回滚

持久化完整原文目录及 SQLite 数据。SQLite 使用 WAL；在停止写入并关闭进程后备份，或使用 SQLite 一致性备份机制。运行时只复制主数据库文件不能代表完整备份。

验证窗口保持知识禁写，保留旧集合、旧应用及原配置；失败时恢复旧应用与旧集合。恢复写入后的回滚需要先保存并按清单补录新上传内容，不能只切换集合配置而忽略新增文档。完成验收前保留旧集合。

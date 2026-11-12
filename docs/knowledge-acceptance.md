# 知识库 v2 验收记录

[使用与迁移](knowledge.md) · [开发验证](development.md)

验收日期：2026-11-12。分支基于拉取后的 origin/main，使用独立工作区；现有工作区修改保留。此记录区分本地控制流验证、模拟 HTTP 依赖和真实检索结果。

## 本地检查

以下检查通过：

```bash
ONCALL_INTEGRATION=0 GOTOOLCHAIN=local go -C backend test ./...
ONCALL_INTEGRATION=0 GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
ONCALL_INTEGRATION=0 node --test backend/test/frontend/*.test.mjs
```

最终状态修复后再次执行 knowledge、chat/pipeline、chat/cli、frontend 的竞态回归，以及 vet、build 和全部前端测试。前端 24 项通过、1 项旧的显式云端浏览器验收跳过；默认 Go 测试中的外部集成按开关跳过。macOS 竞态构建出现系统链接器 LC_DYSYMTAB 警告，命令退出成功且测试通过。

覆盖目录持久化、front matter、稳定版本、批次失败、可见性确认、发布、清理重试、首次失败后重传、恢复既有版本、等价换行重试的实际文件元数据、检索模式与分数语义、候选扩充、HTTP 409，以及 1300 条无序历史片段的完整扫描、冲突选择和迁移重试。

## 真实 Milvus 与 Embedding

实际执行：

```bash
ONCALL_INTEGRATION=1 \
ONCALL_KNOWLEDGE_INTEGRATION_ENV=/absolute/path/to/backend/.env \
ONCALL_KNOWLEDGE_ACCEPTANCE_REPORT=/tmp/oncall-hybrid-acceptance.json \
GOTOOLCHAIN=local go -C backend test ./test/knowledge \
  -run '^TestHybridRetrievalIntegration$' -count=1 -v -timeout 15m
```

| 项目 | 环境或结果 |
| --- | --- |
| Milvus | 2.5.27 |
| Go SDK | client/v2 v2.5.2 |
| Embedding | doubao-embedding-vision-251215 |
| 语料 | 10 篇合成示例文档、30 条标注查询 |
| v2 验收集合 | knowledge_v2_acceptance_096794b7d556900da8b06733f1bae046 |
| 旧结构验收集合 | knowledge_v1_acceptance_096794b7d556900da8b06733f1bae046 |

| 模式 | Recall@5 | MRR | P95 |
| --- | --- | --- | --- |
| dense | 1.000 | 1.000 | 801 ms |
| bm25 | 1.000 | 1.000 | 471 ms |
| hybrid | 1.000 | 1.000 | 826 ms |

真实验证还覆盖 BM25 空结果、类型/服务/标签过滤、文档替换后的版本过滤、Agent 检索适配器、旧集合迁移及重复执行。总耗时约 64 秒。测试仅写入上述随机命名集合，保留记录；SQLite 和原文使用测试临时目录。原有知识集合未迁移或切换。

上述结果是单次、顺序执行的合成语料验收，不代表真实业务 SOP 的正确性、生产负载容量或所有模型的回答质量。评估语料见 `backend/test/knowledge/testdata/retrieval-corpus.json`；上线前应补充经过负责人确认的文档和真实问题。

## 浏览器与 HTTP 模拟

使用现有 `backend/test/frontend/stage11.sim.yaml`，Postman CLI 1.74.0，启动时使用 `--no-history`。真实 GoFrame 控制器、目录发布流程、SQLite 和 Eino 图运行在本地，Milvus 在该浏览器流程中使用 PublicationMemory；其匹配分数仅用于 UI 验证。

实际浏览器检查通过：

- 1280×900 桌面与 390×844 移动布局；移动端没有横向溢出。
- 三个 Tab 切换、聊天流式完成、AI Ops 报告完成及操作后控件恢复。
- 目录筛选及空结果、三种模式和对应分数标签、文档详情与命中切片定位。
- 通过文件选择器上传带 front matter 的 SOP，再指定同一文档替换文件，确认版本和正文变化。
- 文档切换后重新展开正确切片；切片区域状态修复后重新加载页面复验。
- 正文中的 script、img、事件属性及 javascript 链接没有形成可执行 DOM；浏览器控制台未记录页面错误。

模拟请求日志实际记录 chat 1 次、model 6 次、prom 1 次，全部 HTTP 200。知识页面操作没有调用模型；聊天和运维调用分别到达相应模型及告警模拟端点。模型输出为预定义内容，因此报告格式与 HTTP 链路通过不等于语义正确。复用假设与证据保存在 `backend/test/frontend/stage11.context.yaml`。

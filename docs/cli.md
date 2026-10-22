# CLI 与图调试

[文档索引](README.md) · [配置](configuration.md) · [开发与测试](development.md)

本页所有 Go 命令均在 `backend/` 执行。CLI 自动加载 `.env`，配置优先级与 Web 相同；参数覆盖仅作用于本次执行，不写回文件。运行日志默认只写文件，业务结果输出到 stdout，错误输出到 stderr 并非零退出。

## 入口与依赖

| 入口 | 用途 | 依赖 |
| --- | --- | --- |
| `modelcheck` | 验证模型 ID、目录和一次聊天 | 聊天模型 |
| `init_knowledge` | 探测维度，检查/创建集合和索引并加载 | Embedding、Milvus |
| `knowledge_cmd` | 文件经 KnowledgeIndexing 图入库 | Embedding、已初始化的 Milvus 集合 |
| `recall_cmd` | 普通检索，或列出/调用工具 | 根据模式按需初始化 |
| `chat_cmd` | Invoke/Stream，显式 History，Eino Dev | 聊天模型、Embedding、Milvus、启用的工具 |
| `tools_cmd` | 列工具定义或调用单个工具 | 只支持本次工具需要的依赖 |
| `ai_ops_cmd` | 自定义任务的运维分析 | 规划/执行模型、启用的工具；文档工具启用时需向量依赖 |

Web 与知识相关 CLI 不隐式初始化集合。除显式 `init_knowledge` 外，应先确认集合已存在、结构正确且已加载。

## 模型检查

```bash
GOTOOLCHAIN=local go run ./cmd/modelcheck
```

命令在一个共享总预算内请求 `GET <base_url>/models`，核对实际模型 ID，再调用与聊天服务相同的 Eino 模型客户端。成功输出结构示例：

```json
{"configured_model":"your-model-id","available_models":["your-model-id"],"model_id_verified":true,"chat_completion_available":true}
```

检查不依赖模型文本中的“自报身份”。若网关不支持模型目录，目录检查失败不表示聊天一定不可用，可使用 HTTP 聊天接口另行验证。该检查也不等于所有 Agent 工具调用协议均已通过。

## 集合初始化与文件入库

```bash
GOTOOLCHAIN=local go run ./cmd/init_knowledge

GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --root ./test/retriever/testdata --file auth_runbook.md
```

`init_knowledge` 成功输出 database、collection、dimension、ready。它会真实调用 Embedding，检查已有结构并创建缺失项，不删除或重建不匹配集合；数据库需事先存在。

`knowledge_cmd` 的 `--root` 默认当前目录；普通模式必须提供 `--file`，读取根目录中的 txt/md/markdown 文件，stdout 输出 JSON ID 数组。URI 可为根目录内的相对或绝对路径，规范化来源一致；不能借 URI 越过根目录校验。

## 检索与工具

普通检索与两个工具模式互斥：

```bash
GOTOOLCHAIN=local go run ./cmd/recall_cmd \
  --query 'AuroraAuth 认证失败后如何恢复？'

GOTOOLCHAIN=local go run ./cmd/recall_cmd \
  --query 'AuroraAuth 认证失败后如何恢复？' --top-k 3 --score-threshold -1

GOTOOLCHAIN=local go run ./cmd/recall_cmd --list-tools
GOTOOLCHAIN=local go run ./cmd/recall_cmd --tool get_current_time --input '{}'
GOTOOLCHAIN=local go run ./cmd/recall_cmd \
  --tool query_internal_docs --input '{"query":"恢复后如何验证？"}'
```

检索输出为 `{"query":"...","documents":[...]}`；每个片段包含 id、content、source、title、score。空结果为 `documents:[]`，退出码为 0；查询失败是错误，不能与无命中混淆。TopK/阈值参数也可用于文档工具，不能用于时间工具；普通检索不受文档工具开关限制。

工具定义与调用也可独立执行：

```bash
GOTOOLCHAIN=local go run ./cmd/tools_cmd --list-tools
GOTOOLCHAIN=local go run ./cmd/tools_cmd --tool get_current_time --input '{}'

ONCALL_TOOLS_QUERY_PROMETHEUS_ALERTS_ENABLED=true \
GOTOOLCHAIN=local go run ./cmd/tools_cmd \
  --tool query_prometheus_alerts --input '{}'
```

列定义和时间工具无需模型、Embedding 或 Milvus。告警工具按配置请求真实 Prometheus；`--list-tools` 不连接外部服务。未知和禁用工具均明确报错。

| 工具 | 输入 | 输出 |
| --- | --- | --- |
| `get_current_time` | `{}` | timestamp（RFC3339Nano，含偏移或 Z）、timezone、unix_seconds |
| `query_internal_docs` | `{"query":"非空问题"}` | query、documents 数组，含来源和相似度 |
| `query_prometheus_alerts` | `{}` | source、alerts 数组，每条保留 labels、annotations、state、activeAt、value |

同名告警的不同实例不会被合并。空告警是有效结果，认证失败、非法结构和上游错误不能当作空数组。工具成功结果只进行一次 JSON 编码；Schema 可以通过 `--list-tools` 获取。

## 对话 Agent

```bash
GOTOOLCHAIN=local go run ./cmd/chat_cmd --id demo --query '认证失败后如何恢复？'
GOTOOLCHAIN=local go run ./cmd/chat_cmd --query '现在几点？' --stream

GOTOOLCHAIN=local go run ./cmd/chat_cmd \
  --input '{"id":"demo","query":"恢复后如何验证？","history":[{"role":"user","content":"认证失败了"},{"role":"assistant","content":"请先刷新签名密钥缓存。"}]}'
```

`--query`、`--id`、`--history` 可以组合，`--input` 与三者互斥。Query 必须非空；History 只允许普通 user/assistant 消息。Id 是可选标签，不读取或保存 HTTP 会话。

Invoke 输出一个 Eino Message JSON，`--stream` 输出逐行 Message JSON（NDJSON），只包含最终回答片段。出现错误前可能已输出部分片段，应结合退出码判断成功；不要把 NDJSON 当作 SSE 协议。

## 运维分析

```bash
GOTOOLCHAIN=local go run ./cmd/ai_ops_cmd \
  --query '检查当前告警，结合内部手册给出排查报告。'

GOTOOLCHAIN=local go run ./cmd/ai_ops_cmd \
  --query '分析认证失败告警，并列出还需确认的证据。' \
  --events /tmp/oncall-ops-events.jsonl
```

`--query` 必填。`--events` 写入单次执行的概要 JSONL，目标必须是尚不存在的新文件；已存在时失败，不覆盖历史记录。SIGINT/SIGTERM 会取消任务并清理客户端。

CLI 与 HTTP 共用 AIOpsService，但字段命名不同：CLI 成功 stdout 为 `{"report":"...","detail":[]}`；HTTP 返回 `data.result` 和 `data.detail`。失败 stderr 为安全错误 JSON 和已完成 detail，退出码非零，不输出成功报告。

运维工具运行中的安全错误可作为证据限制继续分析；模型失败、总超时、取消及执行上限终止任务。规划与执行模型的专用配置逐项继承规则见[配置指南](configuration.md#运维-agent-与模型继承)。

## Eino Dev 调试

两个图均支持 `--dev`。只运行一个时可使用默认端口 52538；同时调试时选择不同端口。

```bash
GOTOOLCHAIN=local go run ./cmd/knowledge_cmd \
  --dev --root ./test/retriever/testdata --dev-port 52538

# 另一终端，同样在 backend/ 中
GOTOOLCHAIN=local go run ./cmd/chat_cmd --dev --dev-port 52539
```

```bash
curl -fsS http://127.0.0.1:52538/eino/devops/debug/v1/graphs
curl -fsS http://127.0.0.1:52539/eino/devops/debug/v1/graphs
```

知识图调试输入为 `{"URI":"auth_runbook.md"}`；对话图为 UserMessage JSON。devops 在所有图 Compile 前初始化；无文件/问题时注册后等待信号，指定输入时先执行一次再保持存活。正常依赖仍需有效配置。

Graph Schema 位于对应 agent 子目录，不标称编辑器导出文件。DevOps v0.1.7 的运行时画布可能插入 `parallel` 展示节点，不改变聊天业务图的七条边。该版本调试服务监听所有网络接口，仅用于受限开发环境，不应直接对外开放。

`knowledge_cmd`、`chat_cmd`、`recall_cmd`、`tools_cmd`、`ai_ops_cmd` 可用 `--help` 查看参数。`modelcheck` 和 `init_knowledge` 没有 flag 帮助入口；直接执行会加载配置并调用依赖。

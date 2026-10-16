# 第 8 步：聊天前端页面

新增根目录 `frontend/`，使用原生 HTML、CSS、JavaScript 实现浅色聊天工作台。普通聊天、流式聊天和上传使用既有接口，后端生产代码、Go 1.23.12、依赖版本与配置契约不变。

## 修改文件

- `frontend/index.html`、`styles.css`、`app.js`：响应式布局、输入交互、会话、操作状态及上传。
- `frontend/sse.mjs`、`api.mjs`：分块事件解析、聊天与上传契约、错误和取消。
- `frontend/history.mjs`、`render.mjs`：版本化浏览器记录和过滤后的 Markdown。
- `frontend/config.js`、`server.mjs`、`start.sh`：公开大小配置、静态资源白名单与开发 API 代理。
- `frontend/vendor/`：锁定 Marked 18.1.1、DOMPurify 3.4.17，附许可证与校验值。
- `backend/test/frontend/`：Node.js 测试、真实 GoFrame/Graph 本地验收服务、Postman 请求集合、上传样例及显式真实集成测试。
- `backend/test/chat/postman/mocks/chat-model/default.js`：复用现有协议模拟，增加资料回显、恶意 Markdown 和慢请求场景，保留第 7 步行为。
- 根 README、前端 README、本阶段文档：启动、部署、接口与验收说明。

## 行为与边界

普通和 SSE 使用相同 Id。浏览器仅发送 Id/Question，不发送 localStorage 消息；本地记录是展示历史，不能恢复后端因过期或重启丢失的上下文。恢复旧会话沿用 Id，并提示可能失效；30 分钟提醒按默认值估计，不声称检测了服务状态。

单页同时只允许一个操作，生成时禁用会话切换、上传和重复发送，支持停止普通或流式请求。停止立即恢复控件并取消 reader；迟到回调不能写入后续请求。错误或部分回答保留未完成标记，只有有效完成响应更新本地成功时间。网络异常不自动重试，也无法保证服务端尚未提交。

SSE 以流式 UTF-8 解码并按完整事件边界解析，支持网络分块、CRLF/LF、多行 data 和注释。data 使用 JSON，仅渲染 message.content；error、格式错误、缺少 done 的 EOF 都不是成功。用户内容和错误按纯文本展示，助手 Markdown 经过 DOMPurify 白名单，禁止远端图片及危险 HTML。

开发服务只监听 loopback，静态资源使用白名单，API origin 仅在启动时配置；它不读取后端 .env。代理不缓冲 SSE 或上传，浏览器断开关闭上游，连接/空闲超时返回安全错误。部署时静态文件与 /api 同源，使用反向代理关闭缓冲。

## 启动与默认验收命令

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run .

# 另一终端
cd OnCallAgent/frontend
ONCALL_API_ORIGIN=http://127.0.0.1:6872 ONCALL_FRONTEND_PORT=8080 ./start.sh

# 仓库根目录，默认不访问云端
node --test backend/test/frontend/*.test.mjs
GOTOOLCHAIN=local go -C backend test ./...
GOTOOLCHAIN=local go -C backend test -race ./...
GOTOOLCHAIN=local go -C backend vet ./...
GOTOOLCHAIN=local go -C backend build ./...
```

打开 http://127.0.0.1:8080。Node.js 20+，不需要 npm install。自定义上传上限修改 `frontend/config.js`，与后端 `ONCALL_UPLOAD_MAX_BYTES` 配合；默认 10 MiB。

## 本地完整链路与 Postman

下面三个终端命令从仓库根目录运行：

```bash
postman simulation run backend/test/chat/postman/stage07.sim.yaml \
  --port 4907 --no-history --output ndjson
```

```bash
ONCALL_FRONTEND_HELPER_READY=/tmp/oncall-stage08-url \
ONCALL_CHAT_SIM_BASE_URL=http://127.0.0.1:4907/model/v1 \
GOTOOLCHAIN=local go -C backend test ./test/frontend \
  -run '^TestFrontendServerHelper$' -v -count=1 -timeout 40m
```

```bash
ONCALL_API_ORIGIN="$(cat /tmp/oncall-stage08-url)" ./frontend/start.sh
```

随后在第四个终端验收：

```bash
postman collection run backend/test/frontend/collection.json \
  --env-var base_url=http://127.0.0.1:8080 \
  --env-var upload_file=backend/test/frontend/runbook.md
```

测试服务复用真实控制器、响应中间件、两个 Graph、模型 SDK 和记忆 Store；知识存储为 MemoryStore，Retriever 返回其中的片段，不验证真实语义向量检索。Postman 仅模拟模型 HTTP 协议，根据请求中的历史和资料生成确定结果，不自行保存会话。复用已有本地协议模拟，没有检索服务商远端 mock，也没有发布到 Postman 云端。

浏览器可用 `remember:Aurora-stage08`、`recall`、`__docs__`、`__markdown__`、`__error__` 和 `__slow__` 触发测试场景。这些仅是模拟模型行为，不是生产后端保留命令。Ctrl+C 停止开发服务、测试服务和模拟；测试日志不提交。

## 显式真实集成测试

使用独立集合，先显式初始化，再启动真实服务和另一前端代理。以下集合名为本次保留记录；下次验收可更换新的 `stage08_acceptance_` 名称：

```bash
# backend 目录：初始化独立集合
ONCALL_MILVUS_COLLECTION=stage08_acceptance_20261016_1910 \
GOTOOLCHAIN=local go run ./cmd/init_knowledge

# backend 目录：真实后端
ONCALL_SERVER_PORT=6873 \
ONCALL_MILVUS_COLLECTION=stage08_acceptance_20261016_1910 \
ONCALL_CHAT_MODEL_TIMEOUT=60s \
ONCALL_FILE_DIR=/tmp/oncall-stage08-knowledge \
GOTOOLCHAIN=local go run .

# 仓库根目录：另一终端
ONCALL_API_ORIGIN=http://127.0.0.1:6873 ONCALL_FRONTEND_PORT=8081 ./frontend/start.sh

# 仓库根目录：显式真实验收
ONCALL_INTEGRATION=1 ONCALL_FRONTEND_INTEGRATION_URL=http://127.0.0.1:8081 \
  node --test backend/test/frontend/integration.test.mjs
```

测试每次生成唯一来源与校验码，上传并重复上传，再验证普通检索回答引用来源、SSE 记忆承接及不同 Id 隔离。带标记的文档和向量保留；不会自动清理或初始化集合，不改默认工作集合。需要真实模型、Embedding 和 Milvus 凭据，由后端原有配置加载；浏览器和 Node 测试不持有密钥。

## 页面验收清单

| 操作 | 预期 |
| --- | --- |
| 普通模式发送问题 | 单个完整回答，保留原 message/data 契约 |
| 同会话切换流式并追问 | connected 后等待生成，正文出现后以 done 确认，多轮上下文共享 |
| Shift+Enter / Enter | 换行 / 发送；输入法组合输入期间不发送 |
| 新建会话后问旧代号 | 使用新 Id，没有旧会话记忆 |
| 上传样例后提问 | 显示实际文件信息和片段数；回答可检索正文和来源 |
| 重复选择同一文件 | 可再次上传，成功响应一致 |
| 空文件、PDF、超限文件 | 显示具体错误，不发送无效上传，控件可继续使用 |
| 停止生成后再次提问 | 上轮标记未完成，当前输入恢复，迟到内容不污染新轮次 |
| 模型错误、网络断开或异常 EOF | 安全提示，保留未完成内容，无自动重试 |
| 点击重新发送 | 问题回到输入框，用户再次提交，不假装回滚后端 |
| 刷新或切换旧会话 | 浏览器消息仍在，保留 Id，提示上下文可能失效 |
| Markdown 恶意内容 | 标题、表格、代码保留；危险节点、图片、事件和危险链接移除 |
| 手机视口与滚动 | 输入区可用，侧栏可收起，不抢占历史阅读位置，可回到最新消息 |
| 清除本地记录 | 显示确认；仅清除浏览器，不影响后端和共享知识库 |

## 本次结果（2026-10-16）

- 前端 Node 测试：15 项通过，默认跳过 1 项显式云端测试，覆盖协议、取消、存储、上传和真实 HTTP 代理。
- 后端全量测试、`go test -race ./...`、`go vet ./...` 和全部入口构建通过，上传、模型检查、CLI 与已有 Graph 回归通过。
- Postman 经前端代理：10 个请求、15 条断言全部通过，退出码 0。模拟日志确认请求传入前一轮 user/assistant 和上传后的正文、来源。
- 真实浏览器已验证普通到 SSE 续聊、刷新恢复、新 Id 隔离、停止和再次发送、模型失败提示、多行输入、Markdown 白名单、上传后提问，以及空文件/超限/格式错误。桌面与 390px 手机布局检查通过，控制台无错误。
- 独立云端集合初始化通过，Embedding 实测 2048 维。首轮真实上传和重复上传成功，首次普通聊天触发 60 秒 Agent 超时，安全返回 HTTP 504；此结果未当作通过。
- 改用直接依据资料且不调用工具的验收问题后，完整真实测试通过，耗时 38.44 秒：来源 `stage08-40e2dc11-781e-4d77-b9c0-8e185207768f.md`，258 字节、1 个片段，重复上传响应一致；普通回答包含 `ORBIT-3fe42295` 和来源，SSE 追问返回相同校验码，独立会话返回“不知道”。未调整默认 Agent 超时。
- 独立检索检查返回两条验收来源，COSINE 得分约 0.622 / 0.615；真实浏览器经过前端代理调用云端 SSE，也显示正确校验码和来源。

外部 Nginx 部署、真实输入法候选操作、浏览器跨标签页同步及其它阶段全部云端场景未执行。多标签页记录合并不在本阶段实现范围。实际 `.env`、Go 依赖与根目录另一张 `workflow.json` 保持原样。

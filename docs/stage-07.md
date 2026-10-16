# 第 7 步：会话记忆、普通聊天和 SSE

两个 HTTP 接口复用启动时编译的同一个 ChatAgent、Retriever、模型和工具。ChatService 持有进程内记忆；CLI 继续显式传入 History。

## 文件与依赖注入

以下路径相对 `backend/`：

- `api/chat/v1/chat.go`：普通和流式请求定义。
- `internal/controller/chat/`：构造、普通响应和 SSE 响应入口。
- `internal/service/chat/chat.go`：输入规范化、会话锁、历史、Invoke/Stream 与完整轮次提交。
- `internal/service/sse/sse.go`：事件编码、HTTP 写出/刷新错误检查、写截止时间和流结束处理。
- `utility/mem/mem.go`：实例化记忆 Store、可取消会话锁、历史副本及过期清理。
- `utility/middleware/middleware.go`：共享安全错误映射、已开始 SSE 的请求标记。
- `internal/config/memory.go`、`.env.example`、`main.go`：配置与启动时共享依赖装配。
- `test/mem/`、`test/chat/session/`、`test/chat/`、`test/config/memory_test.go`：记忆、服务、HTTP/SSE、SDK 协议、Postman 和显式真实集成测试。

`NewChatService(agent, memory, timeout)` 接收只包含 Invoke/Stream 的 Agent 接口、记忆 Store 和总超时，返回服务及构造错误。ChatService.Stream 使用 OnStart、OnMessage、OnComplete 回调；SSE Service 提供协议对应回调，因此聊天服务不依赖 GoFrame 或 SSE 编码。

Web 启动时创建一个 ChatAgent，注入已初始化的 Model、Retriever 和注册工具。构造与 Compile 错误直接终止启动，不在请求中建立连接或编译。客户端仍由启动入口统一复用和关闭。Go 1.23.12 及现有依赖版本保持不变。

## 会话与配置

```dotenv
ONCALL_CHAT_MEMORY_MAX_ROUNDS=3
ONCALL_CHAT_MEMORY_TTL=30m
```

配置优先级为进程环境变量 > `.env` > 默认值，轮数和时长必须为正，显式空值报错。实际 `.env` 不修改。

Id 和 Question 去除首尾空白，空值拒绝；Id 区分大小写。每轮只保存规范化问题和最终回答，通过 `schema.UserMessage`、`schema.AssistantMessage` 成对构造。最多保留最近三轮，即六条历史消息；不保存工具调用、推理内容和部分回答。读取时复制切片及消息。

同 Id 的普通和流式请求使用同一把可取消锁，覆盖读取历史、Agent 执行和成功提交。不同 Id 可并行。正在执行及排队的条目均被引用保护，过期清理不会为同 Id 创建另一把锁。

TTL 从最近一次成功提交开始计算；读取和失败请求不续期，到期时读取为空。清理在后续访问时进行，没有后台清理线程。记忆在服务重启后清空，多实例之间不共享。

`ONCALL_CHAT_AGENT_TIMEOUT` 默认 60s，同时覆盖排队、检索、模型、工具和流消费；父 context 更早截止时优先。`ONCALL_CHAT_MODEL_TIMEOUT` 仍限制单次模型 HTTP 请求。普通请求完整成功后提交；流式请求正常 EOF、最终文本非空、所有消息和 done 写出成功且上下文有效后提交，然后释放锁。

## HTTP 与 SSE 协议

两个接口均为 POST，请求：

```json
{"Id":"demo","Question":"我的值班代号是什么？"}
```

普通响应保持：

```json
{"message":"OK","data":{"answer":"Aurora"}}
```

流式成功示例：

```text
event: connected
data: {"status":"connected"}

event: message
data: {"content":"第一行\n第二行"}

event: done
data: {"status":"completed"}

```

connected 在业务输入校验后立即刷新，然后才等待会话锁和调用 Agent.Stream。成功为 connected → message... → done；流开始后发生错误则发送 error 并关闭，不发送 done：

```text
event: error
data: {"message":"chat agent execution failed"}

```

所有 data 通过 `encoding/json` 编码，换行、引号、反斜杠不会破坏帧边界；事件以空行分隔。忽略无正文的元数据块，文本内容原样拼接保存。保持第 6 步的完整模型轮次检查，因此 connected 可立即到达，最终回答片段需等当前模型轮次确认结束后才开始输出。

响应头包含 `Content-Type: text/event-stream; charset=utf-8`、`Cache-Control: no-cache`、`X-Accel-Buffering: no`。使用 GoFrame 跟踪的 RawWriter 写出，通过底层 ResponseController 检查 Flush 错误；每次写出最多阻塞 5 秒，常规消息还受请求剩余预算限制。底层不支持所需能力时返回初始化错误。

统一中间件通过私有请求标记跳过已开始的 SSE，流结束后不会追加 JSON。错误、取消、超时、写入或刷新失败均关闭流资源且不保存本轮；连接已断开时直接清理，终止错误只尽力发送。底层写入或刷新失败后不再追加事件，避免拼接到不完整帧；done/error 终止帧写出后也不会追加其它事件。

| 阶段/情况 | HTTP | 响应 |
| --- | --- | --- |
| 参数无效 | 400 | message/data JSON |
| SSE 写出能力初始化失败 | 500 | message/data JSON |
| 普通请求取消 | 408 | request cancelled |
| 普通请求超时 | 504 | model request timed out |
| 普通请求 Agent/执行上限失败 | 502 | chat agent execution failed |
| connected 发出后发生错误 | 保持 200 | error 事件，不追加统一 JSON |

## 启动与验收命令

```bash
cd OnCallAgent/backend
GOTOOLCHAIN=local go run .

curl -sS http://127.0.0.1:6872/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"Id":"demo","Question":"请记住我的值班代号是 Aurora。"}'
curl -N http://127.0.0.1:6872/api/chat_stream \
  -H 'Content-Type: application/json' \
  -d '{"Id":"demo","Question":"我的值班代号是什么？"}'

# 独立 Id，应没有 demo 的上下文
curl -N http://127.0.0.1:6872/api/chat_stream \
  -H 'Content-Type: application/json' \
  -d '{"Id":"isolated","Question":"我的值班代号是什么？不知道就说明不知道。"}'

GOTOOLCHAIN=local go test ./...
GOTOOLCHAIN=local go test -race ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go build ./...

# 显式真实验收：只读第 6 步验收集合，不创建或写入集合
ONCALL_INTEGRATION=1 ONCALL_CHAT_MODEL_TIMEOUT=60s GOTOOLCHAIN=local \
  go test ./test/chat -run '^TestChatMemoryIntegration$' -v -count=1 -timeout 240s
```

真实测试默认使用 `stage06_acceptance_bb0770e598ee2fff70d0f800ee1621ef`；可用测试专属变量 `ONCALL_CHAT_INTEGRATION_COLLECTION` 指定另一个 `stage06_acceptance_` 集合。需要放宽较慢模型的单请求超时时，Web 启动命令可添加 `ONCALL_CHAT_MODEL_TIMEOUT=60s`；Agent 的总预算仍为 60s。

### Postman 本地模拟验收

以下命令分别在三个终端执行，工作目录均为 backend：

```bash
postman simulation run test/chat/postman/stage07.sim.yaml \
  --port 4907 --no-history --output ndjson
```

```bash
ONCALL_CHAT_HELPER_READY=/tmp/oncall-stage07-url \
ONCALL_CHAT_SIM_BASE_URL=http://127.0.0.1:4907/model/v1 \
GOTOOLCHAIN=local go test ./test/chat -run '^TestPostmanServerHelper$' \
  -v -count=1 -timeout 10m
```

```bash
postman collection run test/chat/postman/collection.json \
  --env-var "base_url=$(cat /tmp/oncall-stage07-url)"
```

模拟仅实现当前 SDK 使用的 OpenAI 兼容请求/响应与流格式，基于现有协议测试构造；没有查询服务商维护的远端 mock，也不代表线上模型行为。模拟根据请求携带的历史回答，不自行保存会话，从而验证真实服务是否传递记忆。模型失败、超时和损坏流均有独立用例；测试凭据是固定假值。

`test/chat/postman/` 中的 YAML 只配置本地测试工具，应用仍仅读取进程变量和 `.env`。本地匿名运行不要求 Postman 登录，不发布运行详情；验收后 Ctrl+C 停止两个服务。模拟日志可能含测试问题和历史，不提交日志文件。

## 本次验收结果（2026-10-16）

- 全量 `go test ./...`、`go test -race ./...`、`go vet ./...` 和全部入口构建通过。
- 本地记忆、配置、服务及 HTTP/SSE 测试通过，包含三轮窗口、过期边界、成功续期、读取不续期、副本保护、跨接口共享、不同 Id 隔离、排队、等待取消和活动条目过期。
- 初始化失败、部分输出后失败、模型超时、客户端取消、写入/刷新/done 失败均不保存本轮，事件及流尾检查通过。
- connected 在等待会话锁前即刷新；真实慢客户端测试确认写出约 5 秒超时后释放锁，没有提交失败回答。
- Postman 本地模型模拟与实际 GoFrame 服务：9 个请求、15 条断言全部通过，退出码 0；日志确认 SDK 请求包含前一轮 user/assistant 历史、使用真实流参数，模型错误映射为安全 HTTP/SSE 错误。
- 云端 HTTP 验收通过，耗时 89.44s；模型配置 ID 为 `gpt-5.6-sol`，只读第 6 步验收集合，没有创建集合、初始化、Upsert 或删除调用。
- 真实会话 `stage07-4ba26e350be170d647d15cb2a03827ea` 通过普通接口保存代号；SSE 召回 `Aurora-4ba26e350b` 并以 done 结束，保存四条 user/assistant 消息；独立 Id 返回“不知道”。

其它阶段的真实上传、独立向量、检索和 modelcheck 集成测试本次未重复执行，默认回归中相应显式集成项为跳过。实际 `.env`、依赖版本和根目录另一张 `workflow.json` 未修改。

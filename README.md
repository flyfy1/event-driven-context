# Event-driven Context

Go 实现的项目事件存储后端与 CLI。项目成员共同读写，事件、原始文件和 metadata **只能追加，不允许修改或删除**。原始信息可以在后续由独立逻辑处理成 context。

第一版直接使用 `User → Project → Event`，不额外引入 Context 实体。范围与验收见 [MVP 文档](docs/mvp.md)。

## 启动

需要 Go 1.26.5 或更新版本；SQLite 使用纯 Go 驱动，不依赖外部数据库或 CGO。

```sh
make build
./bin/edc-server
```

默认监听 `127.0.0.1:8080`。SQLite 身份数据库为 `data/context.db`；event manifest 和原始文件写入 Git 忽略的 `data/`。修改地址、路径：

```sh
./bin/edc-server -addr 127.0.0.1:8090 -db data/context.db -data data
./bin/edc --server http://127.0.0.1:8090 help
```

`GET /healthz` 用于存活检查。`SIGINT` / `SIGTERM` 会停止接收请求并等待在途请求结束。

## CLI：跑通共同记录

注册和登录会在终端无回显地读取密码；注册还需要唯一邮箱。用户名为 3–64 个小写字母、数字、`_ . -`，密码为 12–72 字节。不提供命令行密码参数，自动化可通过 `--password-stdin` 输入。

```sh
./bin/edc register --username alice --email alice@example.invalid
./bin/edc login --username alice
./bin/edc project create --name "后端研发" --description "团队共享的原始记录"
```

复制返回的项目 `id`，用于以下命令的 `PROJECT_ID`。

```sh
# 第二个用户使用独立配置文件；也可以在另一台电脑上注册、登录。
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" register --username bob --email bob@example.invalid
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" login --username bob

# 创建者添加已注册成员。
./bin/edc project add-member --project PROJECT_ID --username bob
./bin/edc project members --project PROJECT_ID

# Bob 追加文本；作者从 Bob 的登录身份取得。
./bin/edc --config "$HOME/.config/event-driven-context/bob.json" record \
  --project PROJECT_ID --text '事件不允许修改和删除' \
  --metadata '{"source":"discussion","tags":["backend","decision"]}' \
  --idempotency-key decision-001

# 原始文本文件：必须主动声明类型。
./bin/edc record --project PROJECT_ID --file ./notes.txt --type text/plain \
  --metadata '{"description":"会议记录","document_type":"meeting-notes"}'

# 按半开时间区间和 metadata 查询。
./bin/edc query --project PROJECT_ID \
  --from 2026-09-08T00:00:00+08:00 --to 2026-09-09T00:00:00+08:00 \
  --metadata '{"source":"discussion"}' --exists tags

./bin/edc get --event EVENT_ID
./bin/edc metadata --project PROJECT_ID
./bin/edc metadata --project PROJECT_ID --key source
./bin/edc file --id FILE_ID --output ./downloaded-notes.txt
./bin/edc whoami
./bin/edc logout
```

全局 `--server`、`--config` 必须放在命令之前。命令结果写到 stdout，错误和密码提示写到 stderr。文件下载默认输出 JSON/base64；`--output -` 输出原始字节，指定路径时拒绝覆盖已有文件。

登录把有效期 30 天的令牌保存在系统用户配置目录下的 `event-driven-context/config.json`，权限 `0600`，不保存密码或打印令牌。服务器只保存令牌的 SHA256。`logout` 会立即吊销当前令牌；stdio 进程也不能继续使用它。

可设置 `EDC_SERVER`、`EDC_CONFIG`、`EDC_TOKEN`。配置中的令牌只用于其绑定的服务器，切换 `--server` 不会把旧服务器令牌发过去。CLI 只允许 loopback 使用明文 HTTP，远程地址要求 HTTPS，并拒绝自动重定向。

## 数据契约

事件示例：

```json
{
  "id": "evt_...",
  "project_id": "prj_...",
  "actor_user_id": "usr_...",
  "actor_username": "bob",
  "recorded_at": "2026-09-08T14:00:00.000000000Z",
  "occurred_at": "2026-09-07T09:00:00.000000000Z",
  "content": {"kind": "text", "text": "原始信息"},
  "metadata": {"source": "meeting", "tags": ["decision"]}
}
```

- **共享**：创建者添加成员；所有成员能读取全部历史事件、文件和 metadata，并以自己的身份追加。仅创建者能添加成员；其他项目默认不可访问。第一版不提供成员移除和项目删除。
- **身份与时间**：`actor_user_id`、`actor_username`、`recorded_at` 是服务端输出，输入这些字段会被拒绝。`occurred_at` 是可选的用户声明时间，服务端保存为 UTC。不能把它当成可信审计时间。
- **追加**：HTTP/MCP 无编辑、删除操作。每个 event 是项目目录内一个不可覆盖写入的 JSON manifest；原始上传文件使用独立的不可覆盖文件。SQLite 只保存用户、HTTP/OAuth 令牌与授权事务、项目和成员授权，不保存 event、metadata 或文件字节。拥有服务器文件系统写入权限的管理员仍能篡改或删除文件，这不是防篡改账本。
- **幂等**：`idempotency_key` 按「项目 + 作者」隔离；同键同输入返回原事件，不同输入返回冲突。metadata 对象键顺序和空白不影响比较。CLI 默认生成键；要跨命令重试，请显式传入同一个键。metadata 是识别标签，不承担唯一约束。
- **文件**：一次 `record_event` 原子写入文件和事件。输入为 `content={kind:"file",file:{filename,media_type,data_base64}}`；输出替换为文件 ID、声明类型、文件名、大小与 SHA256。通过 `get_file` 读取原始字节。第一版接受 `text/*`，UTF-8、无 NUL，最多 1 MiB；非文本 MIME 返回明确错误。未来可在这个字节信封上开放新类型。不会根据扩展名静默决定类型，也不会加载调用者传来的服务器文件路径。
- **限制**：直接文本最多 1 MiB；metadata 最多 32 KiB、128 个顶层字段，key 为 1–128 字节；HTTP 请求最多 2 MiB。不会静默截断。

### 查询语义

`query_events` 必须指定 `project_id`。默认按服务端 `recorded_at` 过滤，也可指定 `time_field="occurred_at"`；`from` 包含、`to` 不包含，要求 RFC3339 时区。不带 `occurred_at` 的事件不匹配发生时间边界。

结果按追加序号升序排列。`limit` 默认 50、最大 100；每页事件 JSON 另有 4 MiB 预算（至少返回一条完整事件），因此实际条数可能小于 limit。第一页固定当前项目的最大序号；用返回的 `next_cursor` 作为 `cursor`，并保留其他参数，即可读取同一快照，不混入后续追加的事件。重新开始查询可看到新事件。

`metadata` 是顶层 key 的精确 JSON 匹配，条件 AND：

```json
{
  "project_id": "prj_...",
  "metadata": {"source": "meeting", "verified": true},
  "metadata_exists": ["description"],
  "limit": 50
}
```

字符串 `"1"` 与数字 `1` 不同，`null` 与不存在不同；对象忽略 key 顺序，数组顺序有意义。JSON 数字保留原始表示与精度，`1` 与 `1.0` 在第一版属于不同值。支持任意 JSON metadata 存储，但第一版不支持嵌套路径、数组包含、模糊匹配或数值范围过滤。

`list_metadata` 不带 `key` 时列出实际出现过的顶层字段、类型和事件数；带 `key` 时列出该字段实际出现过的 JSON 值和计数。支持 `limit` / `offset`，通过 `has_more` 判断下一页。没有独立的 metadata 注册流程，推荐使用 `description`、`source`、`tags`、`document_type`。项目正式归属始终由 `project_id` 决定。

## HTTP API

除注册、登录和 healthz 外，均要求 `Authorization: Bearer <token>`。JSON 请求使用 `Content-Type: application/json`。

| 方法与路径 | 输入 / 用途 |
|---|---|
| `POST /v1/auth/register` | `{username,password}`，返回用户 |
| `POST /v1/auth/login` | `{username,password}`，返回用户、令牌和到期时间 |
| `POST /v1/auth/logout` | 吊销当前令牌 |
| `GET /v1/me` | 当前身份 |
| `POST /v1/projects` | `{name,description?}`，创建项目 |
| `GET /v1/projects` | 当前用户的项目 |
| `POST /v1/members` | `{project_id,username}`，添加成员 |
| `POST /v1/members/query` | `{project_id}`，列出成员 |
| `POST /v1/events` | `{project_id,content,metadata?,occurred_at?,idempotency_key?}` |
| `GET /v1/events/{id}` | 完整事件 |
| `POST /v1/events/query` | `{project_id,from?,to?,time_field?,metadata?,metadata_exists?,limit?,cursor?}` |
| `POST /v1/metadata/query` | `{project_id,key?,limit?,offset?}` |
| `GET /v1/files/{id}` | 文件信息与 `data_base64` |
| `/mcp` | 标准 MCP Streamable HTTP，Bearer 认证 |

业务错误格式为 `{"error":{"code":"...","message":"..."}}`，状态码包括 400（输入错误）、401（未认证）、403（权限）、404（不存在或无权读取）、409（冲突）、413（过大）、429（认证限流）。错误不会包含密码、令牌或数据库内部信息。

## MCP 接入

使用 [官方 Go SDK](https://github.com/modelcontextprotocol/go-sdk) v1.7.0。支持标准初始化、工具发现、工具调用、JSON Schema 和读写注解；业务错误以 `isError` 返回。所有工具返回结构化 JSON，同时提供 JSON 文本内容。

工具：`create_project`、`list_projects`、`add_project_member`、`list_project_members`、`record_event`、`get_event`、`query_events`、`list_metadata`、`get_file`。

### 本地 stdio：CLI、Codex、Claude Desktop / Claude Code

先执行 `edc login`。标准 MCP 客户端启动以下命令即可：

```sh
/absolute/path/to/event-driven-context/bin/edc mcp
```

它是一个连接 HTTP 后端的 stdio 代理，不会直接打开 SQLite；每次工具调用都重新走后端身份和项目权限检查。stdout 只输出 MCP 协议内容。

通用客户端配置示例见 [examples/mcp-stdio.json](examples/mcp-stdio.json)，替换绝对路径。根据 [Claude Code 官方文档](https://code.claude.com/docs/en/mcp)，可以这样注册：

```sh
claude mcp add --transport stdio event-context -- /absolute/path/to/event-driven-context/bin/edc mcp
```

Codex 使用同一标准 stdio 命令配置。仓库不会自动修改你的个人客户端设置。

### 远程 Streamable HTTP：ChatGPT OAuth、OpenAI API 和通用 MCP 客户端

服务器地址为 `https://YOUR_HOST/mcp`，客户端发送 Bearer 令牌。`GET` / `DELETE /mcp` 不承载会话，返回 405；服务使用无状态 Streamable HTTP，每个请求独立验证身份。

[OpenAI Responses API 官方文档](https://developers.openai.com/api/docs/guides/tools-connectors-mcp)支持远程 Streamable HTTP，并提供 `authorization` 字段用于发送访问令牌。请求中的 MCP 工具配置可写为：

```json
{
  "type": "mcp",
  "server_label": "event_context",
  "server_url": "https://YOUR_HOST/mcp",
  "authorization": "<EDC access token>",
  "require_approval": "always"
}
```

生产 MCP 地址是 `https://context-api.integ.life/mcp`。它同时保留上述静态 Bearer 接入，并提供 OAuth 2.1 Authorization Code + PKCE（只接受 S256）给 ChatGPT 等网页客户端：

- Protected Resource Metadata：`https://context-api.integ.life/.well-known/oauth-protected-resource/mcp`（根路径版本也可用）。未认证 `/mcp` 的 `WWW-Authenticate` 会指向这里。
- Authorization Server Metadata：`https://context-api.integ.life/.well-known/oauth-authorization-server`。
- 授权、换 token、动态客户端注册：`/oauth/authorize`、`/oauth/token`、`/oauth/register`。动态注册只接受无 client secret 的 public client、精确 HTTPS 或 loopback callback URL。
- `resource` 在授权请求和换 token 时都必须精确等于 `https://context-api.integ.life/mcp`；authorization code 5 分钟过期且只能使用一次，OAuth access token 1 小时过期。

最小 scope 模型：

| Scope | MCP 工具 |
|---|---|
| `context:read` | `list_projects`、`list_project_members`、`get_event`、`query_events`、`list_metadata`、`get_file` |
| `context:write` | `create_project`、`add_project_member`、`record_event` |

scope 不替代项目权限：即使有 `context:read` 或 `context:write`，调用者仍只能访问其已有成员身份允许的项目；添加成员仍仅限项目创建者，服务不开放匿名写入。工具的 `readOnlyHint` 与描述分别标明读写性质和所需 scope。

#### ChatGPT 自定义连接器

在 ChatGPT 开发者模式中新建自定义连接器，名称填写 `Event-driven Context`，URL 填写 `https://context-api.integ.life/mcp`，身份验证选择 OAuth，并使用服务器 discovery / Dynamic Client Registration，不填写静态 API token 或 client secret。ChatGPT 当前会在连接草稿中生成或提交一个精确 callback URL（通常是 `https://chatgpt.com/connector/oauth/<callback_id>`；旧连接可能使用 `https://chatgpt.com/connector_platform_oauth_redirect`）；本服务将 DCR 请求里的完整 URL 原样注册，授权请求必须逐字匹配，不支持通配符。

ChatGPT 会先收到 401 challenge，再发现两个 well-known JSON、注册 public client、带 `resource`、scope 和 S256 challenge 跳转到登录页。可以使用已有 Event-driven Context 用户名和密码登录，也可以在同一 OAuth 页面按现有用户名与密码规则创建账号；新账号只获得用户身份，不自动获得任何既有项目权限。OAuth 登录、注册、错误提示与授权确认页会按浏览器语言自动选择 English、简体中文、Bahasa Melayu 或 हिन्दी，并提供不丢失授权事务的手动切换。随后在授权页核对 client、resource 和 scope 后确认。ChatGPT 页面本身还会显示“未经 OpenAI 审查的自定义 MCP”风险提示；确认意味着允许第三方 MCP 读取或追加你有权访问的项目数据，应只在确认 URL、工具与 scope 后继续。

## Web 前端与发布

`frontend/` 是没有构建依赖的静态页面，生产域名为 `https://context.integ.life`，默认调用 `https://context-api.integ.life`。它提供注册、登录、项目创建和选择、文本／文本文件追加、metadata 浏览、metadata 筛选、事件分页及原始文件下载。项目成员管理保留在 CLI/MCP，因为第一版只让创建者添加已经注册的成员。

开发预览：

```sh
python3 -m http.server 4173 --directory frontend
go run ./cmd/edc-server -addr 127.0.0.1:8401 -db /tmp/event-context-dev.db \
  -allowed-origins http://127.0.0.1:4173
```

访问 `http://127.0.0.1:4173` 时页面会自动指向本机 API；生产页面只指向 `context-api.integ.life`。前端把短期访问令牌保存在此浏览器的 localStorage，退出会立即吊销它；不要在共享浏览器 profile 中保持登录。

生产发布前先提交一个干净工作树，然后运行：

```sh
make deploy-prod       # 编译 linux/amd64、上传 integ-prod、安装 systemd 服务
make deploy-frontend   # 将 frontend/ 推送到 gh-pages
```

服务运行于 integ-prod 的 `127.0.0.1:8401`，由独立的 `event-context-proxy.service`（Caddy）发布为 `https://context-api.integ.life`，不与主机上的全局 Caddy 实例混用。身份与授权数据库在 `/var/lib/event-driven-context/context.db`；event manifest 与原始文件在 `/var/lib/event-driven-context/data/projects/<project-id>/{events,files}/`。静态发布使用 `frontend/CNAME` 指定 `context.integ.life`。首次启动新版本会把旧 SQLite event/file 表导出为数据目录中的文件，再移除旧表。

### integ-prod 运维

服务进程和 SQLite 数据使用 Linux 账户 `yycy`，共享组为 `context-admins`。`yycy` 与 `songyy` 都在该组中；发布目录保持组可读写，后续发布目录继承该组。SQLite 驱动将数据库文件收紧为运行账户私有，协作者通过服务接口访问数据。systemd unit 仍由 root 管理。`yycy` 只能通过以下受限命令管理 Context 服务，不能获得通用 sudo：

```sh
sudo context-service-admin status
sudo context-service-admin health
sudo context-service-admin logs
sudo context-service-admin restart
sudo context-service-admin start
sudo context-service-admin stop
```

部署脚本会安装这个 helper 和 sudo 规则，并验证、启动或 reload 专用的 `event-context-proxy.service`；不会接管主机上的全局 `caddy.service`。部署在停止应用后使用 SQLite backup API 备份身份库，同时归档完整 `data/`，保留旧 release symlink 目标用于健康检查失败时回滚。

## 验证与当前边界

```sh
make check
make build
```

测试覆盖真实 CLI 子进程与 stdio MCP 子进程、官方 MCP HTTP 客户端、OAuth discovery/DCR/登录/授权/PKCE/一次性 code/resource/scope/token 过期、MCP initialize 与 tools/list、旧协议版本协商、跨用户共享、跨项目访问拒绝、伪造作者拒绝、自由 metadata 及大整数无损、文件字节往返、幂等并发重试、分页快照、文件化 event 重开后的持久化、旧 SQLite event 自动迁移和令牌吊销。

服务默认仅绑定 loopback。`-public-base-url` 只接受不带 path 的 HTTPS origin；留空会关闭 OAuth discovery/endpoint，静态 Bearer MCP 仍可用。浏览器 Origin 默认全拒绝，可用 `-allowed-origins https://YOUR_HOST` 配置明确名单；OAuth 自身 public origin 自动加入允许列表。MCP SDK 默认启用 loopback Host 检查，反向代理若连接 loopback 上游，应将上游 Host 设为该上游地址。应用限制密码认证并发与全局速率；DCR 总量也有限制，公网入口仍应按客户端限制滥用。

SQLite 仅用于身份与授权（包括 OAuth client、事务、code 与 token）；event 查询会扫描项目数据目录，适合第一版的小团队使用。备份必须同时包含一致性 SQLite 备份与 `data/` 整个目录；运行时不要只复制 WAL 模式的主文件，也不要只复制 event 文件而遗漏授权数据库。生产部署会在停止服务后，以 SQLite backup API 和 data tar archive 写入 `/var/lib/event-driven-context/backups/`。

后续只在实际需要时加入独立消费者、解析、摘要和检索。当前写入路径不调用模型，也不会执行事件内容。

## 目录

```text
cmd/edc-server/       HTTP + MCP 服务入口
cmd/edc/              CLI 与 stdio MCP 入口
internal/core/        共享业务规则、SQLite 授权与文件化 event 存储
internal/api/         HTTP 服务、客户端和端到端测试
internal/mcpserver/   标准 MCP 工具与 transport
docs/mvp.md          已确认的首版范围
```

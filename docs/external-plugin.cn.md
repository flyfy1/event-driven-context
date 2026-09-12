# 在你的机器上运行 Plugin（External Plugin）

[English](external-plugin.md) | 简体中文

安装页面出现一次性 Plugin token，表示这个 Plugin 需要由你控制的机器运行。请先把 token 安全保存，再启动 `edc host run`。安装本身只创建授权和配置，不会启动 processor；只有 processor host 正在运行时，Plugin 才会处理新的 Event。

> `evidence` 是例外：它是给对话 agent 使用的 Skill，没有后台 processor，不需要启动 host。如果通用安装接口仍返回了 token，也不要把它配置给 processor host。

## 整体流程

```text
在项目中安装 Plugin
        │
        ├─ 保存安装记录、manifest、configuration 和权限
        └─ 返回一次性 edcp_... token
                     │
                     ▼
             用户机器上的 edc host
                     │
        ┌────────────┼────────────┐
        │            │            │
  读取获准 Event   运行本地命令   启动本机 Codex
  和上次 _cursor   或本地模型     CLI 进程
        │            │            │
        └────────────┼────────────┘
                     ▼
       回写获准的 derived Event / State
                     │
                     ▼
              成功后更新 _cursor
```

远程部署时，host 只需要主动连接 Context API；不需要公网 IP、端口转发或对外开放监听端口。生产环境应使用 HTTPS，例如 `https://context-api.integ.life`。连接本机自托管服务时也可以使用 `http://127.0.0.1:...`。

## 哪些 Plugin 需要 host

| Plugin | 执行方式 | 本机依赖 | 主要输出 |
| --- | --- | --- | --- |
| `project-brief` | Agent processor | 已安装并登录的 Codex CLI | `project-brief/current` State |
| `daily-review` | 定时 Agent processor | 已安装并登录的 Codex CLI | `daily-review/YYYY-MM-DD` State |
| `audio-transcribe` | Command processor | adapter、Python + `mlx_audio`、本地 Qwen3-ASR 模型、FFmpeg、FFprobe | source-linked transcript Event |
| `notes-indexer` | 内置的受限 Agent protocol | 已安装并登录的 Codex CLI | 项目 Notes 文件树 |
| `evidence` | 对话 agent 中的 Skill | 支持该 Skill 的 agent 接入 | 即时证据回答；无后台输出 |

`audio-transcribe` 另有服务器托管路径。通过该路径启用的 built-in installation 不签发 bearer token；通过通用 manifest 安装并在自己的机器上运行时，才使用本页的 processor host 流程。

## 1. 准备 CLI 和 Plugin 包

以下命令假定你已经在受信任的 Event-driven Context 源码 checkout 根目录：

```sh
make build

export EDC_SERVER="https://context-api.integ.life"
export PROJECT_ID="prj_replace_me"
export PLUGIN_DIR="$PWD/backend/plugins"
```

自托管时，把 `EDC_SERVER` 换成你的 API 地址。`PLUGIN_DIR` 可以指向包含各 Plugin 子目录的目录，也可以直接指向单个 Plugin 目录；其中必须有 `manifest.json` 和 manifest 引用的本地资源。

只运行你信任并审核过的 Plugin 包。Plugin token 限制的是它能在 Context 服务器上访问的数据；它不是本机沙箱。Command processor 以当前操作系统用户身份执行，拥有这个用户本来就有的本机权限。

## 2. 保存一次性 token

服务器只保存 token 的 SHA-256 摘要，无法再次显示明文。不要把 token 放进仓库、命令参数、截图、聊天消息或普通日志。

如果从 Web 安装，请在页面复制 token 后，用隐藏输入把它写入私有文件。下面的例子适用于 macOS 默认的 zsh，token 本身不会进入 shell history 或终端输出：

```zsh
export PLUGIN_ID="project-brief"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"

install -d -m 700 "${TOKEN_FILE:h}"
umask 077
IFS= read -r -s 'PLUGIN_TOKEN?Paste the one-time Plugin token, then press Enter: '
printf '\n' >&2
printf '%s\n' "$PLUGIN_TOKEN" > "$TOKEN_FILE"
unset PLUGIN_TOKEN
chmod 600 "$TOKEN_FILE"
```

如果使用 CLI 安装，优先让 CLI 直接创建私有文件，不要让 token 出现在输出中：

```sh
export PLUGIN_ID="project-brief"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"

./bin/edc --server "$EDC_SERVER" plugin install \
  --project "$PROJECT_ID" \
  --manifest "$PLUGIN_DIR/$PLUGIN_ID/manifest.json" \
  --token-file "$TOKEN_FILE"
```

`--token-file` 使用新建文件语义并创建 mode `0600` 文件；已有文件不会被覆盖。`edc host run` 也会拒绝 group 或 other 可读写的 token 文件。长期运行时应使用 `--plugin-token-file`，不要把 token 持久化到 `EDC_PLUGIN_TOKEN` 环境变量。

如果 token 丢失，当前没有 reveal 或 rotate 操作。需要 remove 后重新安装，取得新 token。

## 3. 先运行一次

首次配置时先用 `--once`。成功结果会以 JSON 输出；没有新工作时返回 `"noop": true`。

### Agent processor：Project brief

```sh
export PLUGIN_ID="project-brief"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"
export CODEX_BIN="$(command -v codex)"

./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --agent-command "$CODEX_BIN" \
  --once
```

`project-brief` 和 `daily-review` 都从 Plugin 包读取固定 Skill，把获准的 Event、当前 configuration 和必要的前一版 State 组成输入，再启动本机 Codex CLI。当前实现使用固定模型和结构化输出 schema，禁用 Web search，并要求只返回可校验的 State；引用的 Event ID 必须来自授权输入。

`daily-review` 的命令结构相同，只需改为对应的 ID 和 token 文件：

```sh
export PLUGIN_ID="daily-review"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"
export CODEX_BIN="$(command -v codex)"

./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --agent-command "$CODEX_BIN" \
  --once
```

Daily review 按项目的 IANA timezone 解释 configuration 中的 `time`；默认是 `21:00`。一次执行检查最近已经到期的回顾周期，而不是安装后立即开启一个服务器定时任务。

### Command processor：Audio transcription

先构建 adapter，并提供所有本地 ASR 路径：

```sh
go -C backend build \
  -o ../bin/audio-transcribe-adapter \
  ./plugins/audio-transcribe/cmd/audio-transcribe-adapter

export PLUGIN_ID="audio-transcribe"
export TOKEN_FILE="$HOME/.config/event-driven-context/$PLUGIN_ID.token"
export EDC_RUNNER_SKILL_ROOT="$PWD/backend/skills"
export EDC_RUNNER_PYTHON="/absolute/path/to/python"
export EDC_RUNNER_ASR_SCRIPT="$PWD/backend/internal/runner/qwen_asr.py"
export EDC_RUNNER_ASR_MODEL="/absolute/path/to/local-qwen3-asr-model"
export EDC_RUNNER_FFMPEG="/absolute/path/to/ffmpeg"
export EDC_RUNNER_FFPROBE="/absolute/path/to/ffprobe"

./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --command "$PWD/bin/audio-transcribe-adapter" \
  --once
```

Python 环境必须能导入 `mlx_audio`，模型必须是本地目录。Host 只下载该 Plugin 获准读取的音频 File，写入私有临时目录并核对 SHA-256；adapter 用 FFprobe 检查时长，再用本地模型转录。临时目录在成功、失败或超时后都会删除。当前实现拒绝超过 20 MiB 或 600 秒的音频。

当前 command protocol 是为 `audio-transcribe` 的单音频输入和 derived transcript 输出实现的，不是通用的任意第三方命令运行时。

## 4. 通过 watch 持续运行

确认 `--once` 正常后，把最后一个参数换成 `--watch`：

```sh
./bin/edc --server "$EDC_SERVER" host run \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --plugin-dir "$PLUGIN_DIR" \
  --plugin-token-file "$TOKEN_FILE" \
  --agent-command "$CODEX_BIN" \
  --watch \
  --interval 30s
```

Command processor 把 `--agent-command` 换成它的 `--command`。Host 启动后会立即检查一次，之后按 interval 检查；默认 interval 是 30 秒，`notes-indexer` 是 15 秒，最小 1 秒。SIGINT 或 SIGTERM 会取消正在运行的子进程、清理临时目录并正常退出。

`--watch` 只是前台常驻进程。当前项目不会自动安装 launchd、systemd、容器、cron 或其他进程守护；生产使用时需要由你自己的 supervisor 管理它。

## 每一轮实际做什么

1. Host 用 token 读取它自己的 installation、当前 manifest、configuration revision 和项目 timezone。
2. Host 加载本地 `manifest.json`，并要求本地 Plugin ID、版本与服务器安装版本完全一致。
3. 对 cursor-driven processor，读取私有 State `<plugin-id>/_cursor`，然后只拉取 cursor 之后、且 `read_events` 允许的 Event。
4. Agent processor 启动 Codex；command processor 启动本地 executable。两类 processor 都只收到本轮需要的数据。
5. Host 校验 processor 输出、来源引用和写入权限。
6. 成功后写入 `derived` Event 或 Plugin 自己命名空间下的 State。
7. 对普通 cursor-driven processor，所有输出成功后才更新 `_cursor`。Daily review 会在执行前先把本日 attempt 记入 cursor data，再在发布成功后标记完成。

Token 绑定一个 installation、一个 Project 和一个 Plugin。服务器按 manifest 限制 `read_events`、`write_events` 和 `write_state`；Plugin 只能写 `derived` Event，并只能写获准的自身 State namespace。File 也只有在被可读 Event 引用时才能下载。这个 token 不是用户登录 token，不能因此获得项目成员管理权限或其他项目的数据。

## 失败、重试和重复执行

如果读取、processor 执行、输出校验或发布失败，普通 cursor-driven host 不会推进 `_cursor`；`--watch` 下一轮会从同一位置重试。因此应把它理解为 at-least-once，而不是 exactly-once。

存在“输出已被服务器接受，但 host 尚未成功更新 cursor”的失败窗口。当前 audio command processor 为 derived Event 生成确定性 ID，重试同一结果时服务器会按 duplicate 处理；自定义 processor 也必须能安全处理 replay。State 写入和 cursor 写入使用 expected version，多个 host 竞争时可能产生 revision conflict；多 host 协调目前不是正式支持的部署方式。

`daily-review` 还会在 `_cursor` 中记录当日 attempt，并受 manifest 的 `max_runs_per_day` 限制。网络读取失败不消耗 attempt；已经发布的日期不会重复生成。

## Configuration 和生命周期

- **修改 configuration**：服务器增加 configuration revision；下一轮 host 读取新值，不需要更换 token。
- **Pause**：token 认证返回 `plugin_paused`，host 不能继续读取或写入。
- **Resume**：原 token 恢复可用，host 从原 `_cursor` 继续。
- **Remove**：installation 标记为 removed，token 摘要被删除，原 token 永久失效。已有 Event、State 和 Notes 不会因此自动删除。

Configuration 可以通过 Web 修改，也可以用 CLI 的 optimistic revision 更新：

```sh
./bin/edc --server "$EDC_SERVER" plugin config \
  --project "$PROJECT_ID" \
  --plugin "$PLUGIN_ID" \
  --expected-revision 1 \
  --config '{"language":"zh-CN","prompt":"Keep the result concise."}'
```

请以安装页面针对该 Plugin 展示的 configuration 字段说明为准；不同 Plugin 接受的字段不同。

## 数据边界：运行在本机不等于数据不离开本机

- Context Server 仍是 Event、File、State、installation 和 cursor 的持久化位置。远程 Server 意味着这些数据会通过 HTTPS 在 Server 和 host 之间传输。
- `audio-transcribe` 可以在本地 Python、FFmpeg 和本地模型中完成推理，不需要把音频发送给云模型；但原音频本来已经存储在 Context Server，转录结果也会回写该 Server。
- `project-brief`、`daily-review` 和 `notes-indexer` 虽然由本机启动 Codex CLI，但获准的项目内容会进入 Codex 模型请求，因此会发送到模型服务。部署前必须把这条模型数据流与“数据存储在哪里”分开评估。
- Plugin token 只应由 host 读取。不要把 token 写进 Plugin prompt、processor stdin、configuration 或模型输入。

## 当前限制

- Web 安装只完成授权和配置，不会自动安装 CLI、Plugin 包、Codex、Python、模型、FFmpeg 或 FFprobe。
- Web 目前不能证明 processor host 在线，也没有 heartbeat、last seen 或最后成功处理时间。
- `--watch` 不安装系统服务；机器休眠或进程退出时不会继续处理。
- 多台 host 同时运行同一个 installation 尚未作为正式能力支持。
- 手动 rerun 请求可以持久化到 `_requests`，但当前 processor host 不消费这些请求。
- 当前 host 支持仓库中定义的固定 agent/command protocol；它不是通用 Plugin marketplace，也不是第三方代码的安全沙箱。
- `evidence` 没有 host processor；它必须由已经接入对应 Skill 的对话 agent 使用。

遇到问题时，先用 `--once` 保留完整错误，再检查：API 地址、Project ID、token 文件权限、本地 manifest 版本、Plugin 状态、Codex 登录状态，以及 command processor 的绝对依赖路径。

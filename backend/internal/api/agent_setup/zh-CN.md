# 让本地编程 Agent 接入 Event-driven Context

## 目标项目

`{{.Project}}`

{{if .HasProject}}上面已经写明目标项目 ID。即使下载后没有原 URL，也必须在所有项目命令中使用这个 ID。本说明不证明项目存在或用户有访问权限；必须用 CLI 实际验证。{{else}}尚未选择项目。请先向用户确认一个已有项目，再配置或读取上下文。下方 PROJECT_ID 只是占位符，不是真实项目；不要自动创建项目。{{end}}

## 目标

让本地 Codex 或 Claude Code 通过已登录的 `edc` CLI 接入，安装官方记录 Skill，并真实读取指定项目。本地编程 Agent 不配置 MCP。

- API 服务：`{{.APIURL}}`
- 记录 Skill：`{{.SkillURL}}`

CLI 会自行读取私有登录配置。不要打开该文件，不要复制或打印其中的 token，也不要让用户把 token 粘贴到对话中。修改项目前先检查已有文件并保留无关内容。

## 1. 找到并验证 CLI

使用 `PATH` 中已有的 `edc`，或仓库里的 `./bin/edc`；不要下载并执行未知二进制。用下面的命令确认 CLI 与现有登录态，过程中不暴露凭据：

```sh
edc help
edc --server {{.APIURL}} whoami
```

如果 `whoami` 尚未登录，暂停并请用户在自己的终端私密执行 `edc --server {{.APIURL}} login --username USERNAME`。如果用户使用非默认配置，后续每条命令都复用用户提供的同一个 `--config /absolute/path/to/config.json`；不要读取配置文件来提取 token。

## 2. 绑定目录并验证项目

在要接入的工作目录执行：

```sh
edc --server {{.APIURL}} project list
edc --server {{.APIURL}} link {{.Project}}
edc --server {{.APIURL}} status
```

无权访问或找不到项目时，检查当前 CLI 账号与成员资格。不要切换项目或新建项目绕过问题。

## 3. 安装记录 Skill

先读取上方 URL 中的原始 Skill。目标文件已存在时先比较，保留用户修改。

- Codex：保存为 `.agents/skills/edc-recorder/SKILL.md`。
- Claude Code：先预览 `edc --server {{.APIURL}} setup claude-code`；核对所有变更后执行 `edc --server {{.APIURL}} setup --apply claude-code`。它只安装 Skill 与可选的自动记录 hook，不添加 MCP。共享项目启用 hook 需要用户另行明确确认。

不要提交 CLI 凭据、私有配置或客户端本地状态。

## 4. 证明 CLI 直连接入

通过 `edc` 读取指定项目，不调用 MCP：

```sh
edc --server {{.APIURL}} query --project {{.Project}} --limit 5
edc --server {{.APIURL}} state list --project {{.Project}}
```

汇报实际项目名称、返回的 Event UUID，以及 State 的 lag 或覆盖限制。历史为空也是有效结果，不要编造 Event。真实 CLI 读取成功才算接入完成；未经明确要求不要写入测试 Event。

## 可选：安装 Memory Recall

需要检索已有信息时，阅读 `{{.RecallSkillURL}}`，保存为 `.agents/skills/memory-recall/SKILL.md`（Codex）或 `.claude/skills/memory-recall/SKILL.md`（Claude Code），保留已有修改。使用 `$memory-recall`，指定项目 `{{.Project}}` 和问题。它将已发布 Notes 同步到本地目录，按需读取相关文件，并通过 CLI 获取来源 Events。检索不会启用录制 hook 或写入远程记录。

# 让编程 Agent 接入 Event-driven Context

## 目标项目

`{{.Project}}`

{{if .HasProject}}上面已经写明目标项目 ID。即使下载后没有原 URL，也必须在所有项目调用中使用这个 ID。本说明不证明项目存在或用户有访问权限；OAuth 完成后仍须验证成员资格。{{else}}尚未选择项目。请先向用户确认一个已有项目，再配置或读取上下文。下方 PROJECT_ID 只是占位符，不是真实项目；不要自动创建项目。{{end}}

## 目标

通过远程 MCP 连接当前工作目录与用户已有账号，安装官方记录 Skill，再真实读取指定项目。仅有配置文件不算完成。

- MCP 地址: `{{.MCPURL}}`
- 记录 Skill: `{{.SkillURL}}`

不要让用户把访问令牌粘贴到对话中。使用浏览器 OAuth；先检查已有配置并保留无关条目，未经单独要求不要开启自动记录 hook。

## 1. 配置远程 MCP

根据当前运行任务的客户端选择步骤。已有 event-context 条目时先核对端点并保留其他设置；如果它指向其他服务，不要静默覆盖。

### Codex

先检查；只有条目不存在时才添加：

```sh
codex mcp get event-context
codex mcp add event-context \
  --url {{.MCPURL}} \
  --oauth-resource {{.MCPURL}}
```

使用两个 scope 完成 OAuth。需要时为用户打开授权页，并等待命令确认认证成功：

```sh
codex mcp login event-context --scopes context:read,context:write
```

### Claude Code

先检查；只有条目不存在时才添加：

```sh
claude mcp get event-context
claude mcp add --transport http --scope local \
  event-context {{.MCPURL}}
```

在 Claude Code 中打开 /mcp，选择 event-context，在浏览器中完成 OAuth。使用受保护资源发现和动态客户端注册，不要填入静态令牌或客户端密钥。

## 2. 安装记录 Skill

读取并下载上方 URL 中的原始 Skill，按当前客户端保存到工作项目中的对应路径：

- Codex: `.agents/skills/edc-recorder/SKILL.md`
- Claude Code: `.claude/skills/edc-recorder/SKILL.md`

目标文件已存在时先比较，保留用户修改；不要提交 OAuth 凭据或客户端私有状态。

## 3. 验证真实连接

重新连接 MCP 或开启新 Agent 会话，使 MCP 与 Skill 生效。调用 list_projects 确认目标项目 ID 在列表中，再按以下参数调用 query_events：

```json
{
  "project_id": "{{.Project}}",
  "limit": 5
}
```

若有 State，对同一个项目调用 list_state，读取项目概况并说明 lag，保留来源引用。汇报实际返回的项目名称与 Event UUID。历史为空也是有效结果，不要编造 Event。

无权访问或找不到项目时，检查 OAuth 账号和成员资格，不要切换项目或新建项目绕过问题。实际读取成功才算完成；未经明确要求不要写入测试 Event。

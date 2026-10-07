package hubconnectors

// Expanded connectors use documented, fixed provider endpoints. Tokens are
// supplied by the owner; an adapter does not claim a configured or live account.
func expandedProviders() []Provider {
	op := func(id, description string, props map[string]any, required ...string) Operation {
		return Operation{ID: id, Description: description, ReadOnly: true, InputSchema: schema(props, required...)}
	}
	list := func() map[string]any { return map[string]any{"limit": limitParam(), "cursor": textParam(4096)} }
	with := func(key string) map[string]any { p := list(); p[key] = textParam(256); return p }
	p := func(id, name, doc, requirement string, ops ...Operation) Provider {
		return Provider{ID: id, Name: name, AuthMode: "owner_token", ImplementationStatus: "adapter_available", MultipleAccounts: true, Operations: ops, Requirements: []string{requirement}, Limitations: []string{"Read operations only; no provider writes or automatic synchronization", "Owner-supplied token; browser onboarding and token refresh are unavailable unless explicitly advertised", "Read one bounded page; provider cursors and incompleteness are preserved"}, DocumentationURL: doc}
	}
	providers := []Provider{
		p("asana", "Asana", "https://developers.asana.com/reference/gettasks", "Personal access token or OAuth token with task/project read access", op("workspaces.list", "List available workspaces", list()), op("projects.list", "List projects in one workspace", with("workspace_id"), "workspace_id"), op("tasks.list", "List tasks in one project", with("project_id"), "project_id"), op("tasks.get", "Read one task", map[string]any{"task_id": textParam(256)}, "task_id")),
		p("airtable", "Airtable", "https://airtable.com/developers/web/api/list-records", "Personal access token with schema.bases:read and/or data.records:read, restricted to selected bases", op("bases.list", "List accessible bases", list()), op("records.list", "Read a table page", func() map[string]any { p := with("base_id"); p["table_id"] = textParam(256); return p }(), "base_id", "table_id")),
		p("linear", "Linear", "https://linear.app/developers/graphql", "Owner API key with selected teams and read-only permissions", op("identity.get", "Read the authenticated user", map[string]any{}), op("teams.list", "List accessible teams", list()), op("issues.list", "List one team's issues", with("team_id"), "team_id")),
		p("gitlab", "GitLab.com", "https://docs.gitlab.com/api/issues/", "GitLab personal access token with read_api; GitLab.com only", op("projects.list", "List projects accessible through membership", list()), op("issues.list", "Read one project's issues", with("project_id"), "project_id")),
		p("box", "Box", "https://developer.box.com/reference/get-folders-id-items/", "Box OAuth user access token with permitted folder/file reads", op("files.list", "Read one folder's items using marker pagination", with("folder_id"), "folder_id"), op("files.get", "Read file metadata, without downloading content", map[string]any{"file_id": textParam(256)}, "file_id")),
		p("discord-bot", "Discord Bot", "https://docs.discord.com/developers/resources/message#get-channel-messages", "Bot token, guild installation and View Channel / Read Message History permissions; message content requires MESSAGE_CONTENT intent", op("identity.get", "Read bot identity", map[string]any{}), op("channels.list", "List channels in an installed guild", map[string]any{"guild_id": textParam(256)}, "guild_id"), op("messages.list", "Read accessible channel messages; cursor is a before-message snowflake", with("channel_id"), "channel_id")),
		p("feishu", "Feishu", "https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/reference/im-v1/message/list", "Owner tenant/user access token with im:chat:readonly and im:message:readonly and access to the selected chats", op("chats.list", "List chats accessible to the application/user", list()), op("messages.list", "Read a selected chat's message history", with("chat_id"), "chat_id")),
		p("lark", "Lark", "https://open.larksuite.com/document/server-docs/im-v1/message/list", "Owner tenant/user access token and required chat/message read scopes; Lark international endpoint", op("chats.list", "List chats accessible to the application/user", list()), op("messages.list", "Read a selected chat's message history", with("chat_id"), "chat_id")),
		p("google-docs", "Google Docs", "https://developers.google.com/workspace/docs/api/reference/rest/v1/documents/get", "Enable Google Docs API and grant documents.readonly", op("documents.get", "Read a document including all tabs", map[string]any{"document_id": textParam(256)}, "document_id")),
		p("google-sheets", "Google Sheets", "https://developers.google.com/workspace/sheets/api/reference/rest/v4/spreadsheets.values/get", "Enable Google Sheets API and grant spreadsheets.readonly", op("spreadsheets.get", "Read spreadsheet/sheet metadata", map[string]any{"spreadsheet_id": textParam(256)}, "spreadsheet_id"), op("values.get", "Read one explicit A1 range", map[string]any{"spreadsheet_id": textParam(256), "range": textParam(1024)}, "spreadsheet_id", "range")),
		p("google-chat", "Google Chat", "https://developers.google.com/workspace/chat/api/reference/rest/v1/spaces.messages/list", "Enable Google Chat API and grant chat.spaces.readonly / chat.messages.readonly; user-accessible spaces only", op("spaces.list", "List accessible spaces", list()), op("messages.list", "Read one space's messages", with("space_id"), "space_id")),
		p("microsoft-contacts", "Microsoft Contacts", "https://learn.microsoft.com/en-us/graph/api/user-list-contacts?view=graph-rest-1.0", "Delegated Microsoft Graph token with Contacts.Read", op("contacts.list", "Read personal contact fields", list())),
		p("microsoft-onenote", "Microsoft OneNote", "https://learn.microsoft.com/en-us/graph/api/onenote-list-pages?view=graph-rest-1.0", "Delegated Microsoft Graph token with Notes.Read", op("notebooks.list", "Read notebook metadata", list()), op("pages.list", "Read page metadata", list()), op("pages.content", "Read one page's HTML content as untrusted data", map[string]any{"page_id": textParam(256)}, "page_id")),
		p("microsoft-teams", "Microsoft Teams", "https://learn.microsoft.com/en-us/graph/api/channel-list-messages?view=graph-rest-1.0", "Delegated work/school Graph token with Team.ReadBasic.All / Channel.ReadBasic.All / ChannelMessage.Read.All; tenant restrictions apply", op("teams.list", "Read joined Teams", map[string]any{}), op("channels.list", "Read one team's channels", map[string]any{"team_id": textParam(256)}, "team_id"), op("messages.list", "Read a selected channel's root messages, excluding replies", func() map[string]any { p := with("team_id"); p["channel_id"] = textParam(256); return p }(), "team_id", "channel_id")),
	}
	for i := range providers {
		v := &providers[i]
		if len(GoogleScopes(v.ID)) > 0 {
			v.AuthMode = "oauth2"
			v.Limitations = []string{"Google OAuth or owner token; enable the provider API in the owner's Google project", "Read operations only, responses capped at 10 MiB; no background synchronization"}
			for j := range v.Operations {
				v.Operations[j].ScopeAlternatives = GoogleScopes(v.ID)[1:]
			}
		}
	}
	return providers
}

var expandedHosts = map[string]string{
	"asana": "app.asana.com", "airtable": "api.airtable.com", "linear": "api.linear.app", "gitlab": "gitlab.com", "box": "api.box.com", "discord-bot": "discord.com", "feishu": "open.feishu.cn", "lark": "open.larksuite.com", "google-docs": "docs.googleapis.com", "google-sheets": "sheets.googleapis.com", "google-chat": "chat.googleapis.com", "microsoft-contacts": "graph.microsoft.com", "microsoft-onenote": "graph.microsoft.com", "microsoft-teams": "graph.microsoft.com",
}

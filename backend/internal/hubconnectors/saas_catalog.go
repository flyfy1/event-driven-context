package hubconnectors

// SaaS credentials are supplied by the owner and encrypted by the host. These
// providers have working read adapters, not automatic OAuth onboarding/refresh.
// API contracts: https://developer.todoist.com/api/v1/
// https://developers.notion.com/reference/post-search
// https://www.dropbox.com/developers/documentation/http/documentation
// https://readwise.io/reader_api
// https://docs.github.com/en/rest/repos/repos
// https://docs.slack.dev/reference/methods/conversations.history/
func saasProviders() []Provider {
	op := func(id, description string, scopes []string, properties map[string]any, required ...string) Operation {
		return Operation{ID: id, Description: description, ReadOnly: true, ScopeAlternatives: scopes, InputSchema: schema(properties, required...)}
	}
	paging := func() map[string]any { return map[string]any{"cursor": textParam(4096), "limit": limitParam()} }
	provider := func(id, name string, ops []Operation, requirements, limitations []string) Provider {
		return Provider{ID: id, Name: name, AuthMode: "owner_token", ImplementationStatus: "adapter_available", MultipleAccounts: true, Operations: ops, Requirements: requirements, Limitations: append([]string{"Owner-supplied provider token; no automatic OAuth onboarding or token refresh", "One bounded page per call; no background sync or provider writes"}, limitations...)}
	}
	todoPaging := paging()
	todoPaging["project_id"] = textParam(256)
	notionPaging := paging()
	notionPaging["query"] = textParam(1000)
	blockPaging := paging()
	blockPaging["block_id"] = textParam(256)
	dropboxPaging := map[string]any{"path": textParam(4096), "limit": limitParam()}
	readerPaging := paging()
	readerPaging["updated_after"] = textParam(64)
	readerPaging["location"] = saasEnum("new", "later", "shortlist", "archive", "feed")
	issuesPaging := paging()
	issuesPaging["owner"] = textParam(100)
	issuesPaging["repo"] = textParam(100)
	issuesPaging["state"] = saasEnum("open", "closed", "all")
	slackPaging := paging()
	slackPaging["type"] = saasEnum("public_channel", "private_channel", "im", "mpim")
	historyPaging := paging()
	historyPaging["channel_id"] = textParam(256)
	return []Provider{
		provider("todoist", "Todoist", []Operation{
			op("tasks.list", "List one page of active tasks", []string{"data:read"}, todoPaging),
			op("tasks.get", "Read one task", []string{"data:read"}, map[string]any{"task_id": textParam(256)}, "task_id"),
			op("projects.list", "List one page of active projects", []string{"data:read"}, paging()),
		}, []string{"Todoist personal API token or OAuth token with data:read"}, []string{"Uses Todoist API v1; completed/archived tasks are not included in tasks.list"}),
		provider("notion", "Notion", []Operation{
			op("search", "Search titles of shared pages and data sources", nil, notionPaging),
			op("pages.get", "Read page metadata and properties; use blocks.children for body", nil, map[string]any{"page_id": textParam(256)}, "page_id"),
			op("blocks.children", "Read one page of direct child blocks", nil, blockPaging, "block_id"),
		}, []string{"Notion connection token with Read content capability and explicitly shared pages"}, []string{"API version 2026-03-11", "Title search is not full-text search; nested blocks require separate calls; data source query is not implemented"}),
		provider("dropbox", "Dropbox", []Operation{
			op("files.list", "List one folder page; omit path for root", []string{"files.metadata.read"}, dropboxPaging),
			op("files.continue", "Continue a folder listing using its opaque cursor", []string{"files.metadata.read"}, map[string]any{"cursor": textParam(8192)}, "cursor"),
			op("files.get", "Read file or folder metadata", []string{"files.metadata.read"}, map[string]any{"path": textParam(4096)}, "path"),
			op("files.download", "Read one file, capped at 10 MiB", []string{"files.content.read"}, map[string]any{"path": textParam(4096)}, "path"),
		}, []string{"Dropbox user access token with files.metadata.read and/or files.content.read for the requested operation"}, []string{"Read RPCs use HTTP POST without mutating files", "Token access may be app-folder or full Dropbox; no team member impersonation", "Folder continuation keeps the initial listing's limit"}),
		provider("readwise-reader", "Readwise Reader", []Operation{
			op("documents.list", "List one page of Reader document metadata", nil, readerPaging),
			op("documents.get", "Retrieve one document including HTML through the list ID filter", nil, map[string]any{"document_id": textParam(256)}, "document_id"),
		}, []string{"Readwise access token for the owner account"}, []string{"The provider token is broader than this adapter's read operations", "HTML content remains untrusted data; an absent ID returns an empty result list"}),
		provider("github", "GitHub", []Operation{
			op("repos.list", "List repositories accessible to this authenticated token", nil, paging()),
			op("issues.list", "List repository issues, including pull requests in GitHub's response", nil, issuesPaging, "owner", "repo"),
			op("contents.get", "Read repository file content or directory listing", nil, map[string]any{"owner": textParam(100), "repo": textParam(100), "path": textParam(4096), "ref": textParam(256)}, "owner", "repo"),
		}, []string{"GitHub user token; fine-grained access to selected repositories", "Metadata read for repository listing, Issues read for issues, Contents read for files; organization SSO rules still apply"}, []string{"GitHub.com only; no arbitrary Enterprise host", "Cursor is a positive decimal page number, never a URL", "Contents API has upstream file-size and directory-entry limits; signed download URLs are not followed"}),
		provider("slack", "Slack", []Operation{
			op("conversations.list", "List one conversation type, public channels by default", []string{"channels:read", "groups:read", "im:read", "mpim:read"}, slackPaging),
			op("conversations.history", "Read one page of messages in an accessible conversation", []string{"channels:history", "groups:history", "im:history", "mpim:history"}, historyPaging, "channel_id"),
		}, []string{"Slack OAuth bot or user token with the scope matching the requested conversation type", "Bot tokens can read only conversations they have joined; workspace/app restrictions apply"}, []string{"Scope alternatives correspond to different conversation types, not interchangeable access to every type", "History reads are capped at 15 messages per call; rate limits vary by app distribution; provider truncation/cursors are preserved", "No thread replies, attachments, message sending, or arbitrary Slack methods"}),
	}
}

func saasEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

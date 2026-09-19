package hubconnectors

// graphProviders exposes only operations implemented by the fixed-origin reader.
// Scope requirements describe delegated Microsoft Graph v1.0 access tokens; the
// host imports owner-supplied credentials and does not refresh them automatically.
func graphProviders() []Provider {
	operation := func(id, description string, scopes []string, properties map[string]any, required ...string) Operation {
		return Operation{ID: id, Description: description, ReadOnly: true, ScopeAlternatives: scopes, InputSchema: schema(properties, required...)}
	}
	provider := func(id, name string, operations []Operation, limitation string) Provider {
		return Provider{ID: id, Name: name, AuthMode: "oauth2", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Operations:   operations,
			Requirements: []string{"Owner-supplied delegated Microsoft Graph access token with the required scope", "Microsoft global cloud account; token import and secure storage supplied by host"},
			Limitations:  []string{"Single bounded page only; continuation URLs are not returned or followed and incomplete results are explicitly marked", "No Microsoft OAuth onboarding, automatic refresh, writes, or durable synchronization", limitation}}
	}
	calendarScopes := []string{"https://graph.microsoft.com/Calendars.ReadBasic", "https://graph.microsoft.com/Calendars.Read"}
	taskScopes := []string{"https://graph.microsoft.com/Tasks.Read"}
	fileScopes := []string{"https://graph.microsoft.com/Files.Read"}
	mailBasicScopes := []string{"https://graph.microsoft.com/Mail.ReadBasic", "https://graph.microsoft.com/Mail.Read"}
	return []Provider{
		provider("microsoft-calendar", "Microsoft Calendar", []Operation{
			operation("calendars.list", "List one page of calendars", calendarScopes, map[string]any{"limit": limitParam()}),
			operation("events.list", "Read basic calendarView events within an explicit RFC3339 interval of at most 7 days", calendarScopes,
				map[string]any{"calendar_id": textParam(1024), "time_min": textParam(64), "time_max": textParam(64), "limit": limitParam()}, "calendar_id", "time_min", "time_max"),
		}, "Calendar views omit bodies, attachments, and extended properties; a page may not cover the entire requested interval"),
		provider("microsoft-todo", "Microsoft To Do", []Operation{
			operation("tasklists.list", "List one page of task lists", taskScopes, map[string]any{"limit": limitParam()}),
			operation("tasks.list", "Read one page of tasks in a task list", taskScopes, map[string]any{"tasklist_id": textParam(1024), "limit": limitParam()}, "tasklist_id"),
		}, "Task attachments and linked resources are not retrieved"),
		provider("onedrive", "OneDrive", []Operation{
			operation("files.list", "List one page of child metadata from the default drive root or a folder", fileScopes, map[string]any{"folder_id": textParam(1024), "limit": limitParam()}),
			operation("files.get", "Read item metadata only, without content or preauthenticated download URLs", fileScopes, map[string]any{"file_id": textParam(1024)}, "file_id"),
		}, "Delegated account default drive only; file content, shared-item expansion, and download URLs are not returned"),
		provider("outlook-mail", "Outlook Mail", []Operation{
			operation("messages.list", "List one page of basic message metadata without bodies", mailBasicScopes, map[string]any{"folder_id": textParam(1024), "limit": limitParam()}),
			operation("messages.get", "Read selected message metadata and text body without attachments", []string{"https://graph.microsoft.com/Mail.Read"}, map[string]any{"message_id": textParam(1024)}, "message_id"),
		}, "Only the delegated account mailbox is addressed; no attachment downloads, shared-mailbox selection, or full-mailbox completeness guarantee"),
	}
}

package hubconnectors

// File snapshots execute in the core store rather than through HTTP adapters.
func importProviders() []Provider {
	out := []Provider{}
	for _, v := range []struct{ id, name, format string }{
		{"whatsapp-import", "WhatsApp Chat Export", "whatsapp-text or whatsapp-zip (ZIP at most 10 MiB; one UTF-8 chat at most 1 MiB)"},
		{"telegram-import", "Telegram Desktop Export", "telegram-json"},
		{"wechat-import", "WeChat Prepared CSV", "wechat-csv (timestamp,sender,text; optional chat_id; not an official native export)"},
		{"calendar-import", "Calendar File Import", "ics"},
		{"markdown-import", "Markdown Notes Import", "markdown"},
	} {
		out = append(out, Provider{ID: v.id, Name: v.name, AuthMode: "owner_file_import", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Owner-selected export file imported through POST /v1/hub/imports", "Format: " + v.format},
			Limitations:  []string{"Immutable snapshot, not live synchronization; no inferred time zones or recurrence expansion", "Importing a new snapshot for the same account revokes earlier agent grants", "Original text is retained as provenance and must be treated as untrusted data"},
			Operations:   []Operation{{ID: "records.list", Description: "Read one page of records from an owner-imported snapshot", ReadOnly: true, InputSchema: schema(map[string]any{"limit": limitParam(), "offset": map[string]any{"type": "integer", "minimum": 0, "maximum": 1048576, "default": 0}})}}})
	}
	return out
}

// Package hubconnectors contains credential-free capability discovery and bounded
// provider reads. The caller must enforce owner, connection and operation grants
// before supplying a credential; this package is not an authorization service.
package hubconnectors

import (
	"errors"
	"strings"
)

type Operation struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	ReadOnly    bool   `json:"read_only"`
	// ScopeAlternatives are provider OAuth scopes, any one of which suffices.
	ScopeAlternatives []string       `json:"scope_alternatives,omitempty"`
	InputSchema       map[string]any `json:"input_schema"`
}

type Provider struct {
	ID                   string      `json:"id"`
	Name                 string      `json:"name"`
	AuthMode             string      `json:"auth_mode"`
	ImplementationStatus string      `json:"implementation_status"`
	MultipleAccounts     bool        `json:"multiple_accounts"`
	Operations           []Operation `json:"operations"`
	Requirements         []string    `json:"requirements"`
	Limitations          []string    `json:"limitations"`
}

// Connection identifies one owner's provider account independently of all other
// accounts of the same provider. It deliberately contains no credential fields.
// Configured means credentials exist, not that the provider has verified them.
type Connection struct {
	ID          string `json:"id"`
	OwnerID     string `json:"owner_id"`
	ProviderID  string `json:"provider_id"`
	AccountID   string `json:"account_id"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
}

func (c Connection) Validate() error {
	for _, v := range []string{c.ID, c.OwnerID, c.ProviderID, c.AccountID, c.DisplayName} {
		if strings.TrimSpace(v) == "" || len(v) > 512 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid connection identity")
		}
	}
	if _, ok := Lookup(c.ProviderID); !ok {
		return errors.New("unknown provider")
	}
	switch c.Status {
	case "needs_auth", "unconfigured", "configured", "connected", "reauth_required", "disconnected", "blocked":
	default:
		return errors.New("invalid connection status")
	}
	return nil
}

func schema(properties map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}
func textParam(max int) map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": max}
}
func limitParam() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 20}
}

// Catalog describes implemented code, not deployment configuration or an
// agent's effective permissions. Return account and grant status separately.
func Catalog() []Provider {
	driveScopes := []string{"https://www.googleapis.com/auth/drive.file", "https://www.googleapis.com/auth/drive.readonly"}
	mailScopes := []string{"https://www.googleapis.com/auth/gmail.readonly"}
	return []Provider{
		{ID: "google-drive", Name: "Google Drive", AuthMode: "oauth2", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Google OAuth application and enabled Drive API", "Owner consent, credential storage and refresh supplied by host"},
			Limitations:  []string{"drive.file is limited to app-selected files but includes provider write permission; these adapters only read", "Single-page reads; no OAuth onboarding, durable sync or binary-file download in this package"},
			Operations: []Operation{
				{ID: "files.list", Description: "List one page of available file metadata", ReadOnly: true, ScopeAlternatives: driveScopes, InputSchema: schema(map[string]any{"query": textParam(4096), "page_token": textParam(4096), "limit": limitParam()})},
				{ID: "files.get", Description: "Read file metadata", ReadOnly: true, ScopeAlternatives: driveScopes, InputSchema: schema(map[string]any{"file_id": textParam(256)}, "file_id")},
				{ID: "files.export", Description: "Export a Google Workspace document as text or PDF", ReadOnly: true, ScopeAlternatives: driveScopes, InputSchema: schema(map[string]any{"file_id": textParam(256), "mime_type": map[string]any{"type": "string", "enum": []string{"text/plain", "text/csv", "application/pdf"}}}, "file_id", "mime_type")},
			}},
		{ID: "gmail", Name: "Gmail", AuthMode: "oauth2", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Google OAuth application and enabled Gmail API", "Owner consent to restricted gmail.readonly scope; deployment verification requirements must be reviewed"},
			Limitations:  []string{"Mailbox query filters do not narrow Google's OAuth authorization", "Single-page reads; no OAuth onboarding, refresh, attachment download or durable sync in this package"},
			Operations: []Operation{
				{ID: "messages.list", Description: "List message IDs and thread IDs for one page", ReadOnly: true, ScopeAlternatives: mailScopes, InputSchema: schema(map[string]any{"query": textParam(4096), "page_token": textParam(4096), "limit": limitParam()})},
				{ID: "messages.get", Description: "Read one full message including MIME payload", ReadOnly: true, ScopeAlternatives: mailScopes, InputSchema: schema(map[string]any{"message_id": textParam(256)}, "message_id")},
			}},
		{ID: "telegram-bot", Name: "Telegram Bot", AuthMode: "bot_token", ImplementationStatus: "adapter_available", MultipleAccounts: true,
			Requirements: []string{"Owner-provided bot token", "Polling requires no active webhook and coordination with any other bot consumer"},
			Limitations:  []string{"Cannot read a personal Telegram account's chat history", "updates.peek does not acknowledge updates or advance a cursor; queued updates are retained by Telegram for at most 24 hours"},
			Operations: []Operation{
				{ID: "identity.get", Description: "Verify bot identity using getMe", ReadOnly: true, InputSchema: schema(map[string]any{})},
				{ID: "updates.peek", Description: "Read currently queued bot updates without acknowledgment", ReadOnly: true, InputSchema: schema(map[string]any{"limit": limitParam()})},
			}},
		{ID: "telegram-user", Name: "Telegram Personal Account", AuthMode: "mtproto_user_session", ImplementationStatus: "not_implemented", MultipleAccounts: true, Operations: []Operation{}, Requirements: []string{"Telegram API application credentials and user-authorized MTProto session"}, Limitations: []string{"Bot tokens cannot substitute for personal account authorization; session storage and ingestion are not implemented"}},
		{ID: "whatsapp-business", Name: "WhatsApp Business", AuthMode: "business_webhook", ImplementationStatus: "not_implemented", MultipleAccounts: true, Operations: []Operation{}, Requirements: []string{"Meta business application, WhatsApp Business account and phone number", "Verified webhook ingestion and secret storage"}, Limitations: []string{"Business webhook ingestion is not implemented; this is not a personal chat-history API"}},
		{ID: "whatsapp-import", Name: "WhatsApp Chat Export", AuthMode: "owner_file_import", ImplementationStatus: "not_implemented", MultipleAccounts: true, Operations: []Operation{}, Requirements: []string{"Owner-selected chat export with account provenance"}, Limitations: []string{"Personal chat export parser is not implemented; no live account connection"}},
	}
}

func Lookup(id string) (Provider, bool) {
	for _, p := range Catalog() {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

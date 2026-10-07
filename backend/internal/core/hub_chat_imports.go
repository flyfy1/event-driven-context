package core

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

const HubImportArchiveMaxBytes = 10 << 20
const HubImportRequestMaxBytes = 14 << 20

func parseHubImportSource(format string, source []byte) ([]HubImportRecord, string, []string, error) {
	if format != "whatsapp-zip" {
		if len(source) == 0 || len(source) > HubImportMaxBytes || !utf8.Valid(source) || bytes.ContainsRune(source, 0) {
			return nil, "", nil, Invalid("invalid bounded UTF-8 import")
		}
		return parseHubImport(format, string(source))
	}
	z, err := zip.NewReader(bytes.NewReader(source), int64(len(source)))
	if err != nil || len(z.File) > 1000 {
		return nil, "", nil, Invalid("invalid or oversized WhatsApp ZIP")
	}
	var chat *zip.File
	seen := map[string]bool{}
	var total uint64
	for _, f := range z.File {
		name := f.Name
		if !hubText(name, 512) || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || path.Clean(strings.TrimSuffix(name, "/")) != strings.TrimSuffix(name, "/") || path.Clean(name) == ".." || strings.HasPrefix(path.Clean(name), "../") || seen[name] {
			return nil, "", nil, Invalid("ZIP contains ambiguous or unsafe entries")
		}
		seen[name] = true
		if f.UncompressedSize64 > 100<<20 || total > (100<<20)-f.UncompressedSize64 {
			return nil, "", nil, Invalid("ZIP expanded size exceeds 100 MiB")
		}
		total += f.UncompressedSize64
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, "", nil, Invalid("ZIP entries must be regular files")
		}
		if strings.HasSuffix(strings.ToLower(name), ".txt") {
			if chat != nil {
				return nil, "", nil, Invalid("ZIP must contain exactly one chat text file; select a single chat export")
			}
			chat = f
		}
	}
	if chat == nil || chat.UncompressedSize64 > HubImportMaxBytes {
		return nil, "", nil, Invalid("ZIP requires a chat text file of at most 1 MiB")
	}
	f, err := chat.Open()
	if err != nil {
		return nil, "", nil, Invalid("cannot read ZIP chat text")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, HubImportMaxBytes+1))
	if err != nil || len(raw) == 0 || len(raw) > HubImportMaxBytes || !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		return nil, "", nil, Invalid("ZIP chat must be bounded UTF-8 without NUL")
	}
	records, _, _, err := parseHubImport("whatsapp-text", string(raw))
	if err != nil {
		return nil, "", nil, err
	}
	for i := range records {
		records[i].Fields = map[string]string{"archive_chat_file": chat.Name}
	}
	return records, "whatsapp_zip_messages_best_effort", []string{"Owner-selected personal chat snapshot; not live WhatsApp access", "Original ZIP including any media remains retained; media files are not indexed, downloaded or exposed by records.list", "Timestamp timezone/date order is uncertain; no UTC conversion"}, nil
}

func parseTelegramExport(content string) ([]HubImportRecord, string, []string, error) {
	type chat struct {
		ID       json.RawMessage   `json:"id"`
		Name     string            `json:"name"`
		Messages []json.RawMessage `json:"messages"`
	}
	var root struct {
		chat
		Chats struct {
			List []chat `json:"list"`
		} `json:"chats"`
	}
	if json.Unmarshal([]byte(content), &root) != nil {
		return nil, "", nil, Invalid("Telegram export must be JSON")
	}
	chats := root.Chats.List
	if root.Messages != nil {
		if chats != nil {
			return nil, "", nil, Invalid("ambiguous Telegram export")
		}
		chats = []chat{root.chat}
	}
	if chats == nil {
		return nil, "", nil, Invalid("expected Telegram Desktop messages or chats.list")
	}
	records := []HubImportRecord{}
	for _, c := range chats {
		for _, raw := range c.Messages {
			var m struct {
				ID    json.RawMessage `json:"id"`
				Type  string          `json:"type"`
				Date  string          `json:"date"`
				From  string          `json:"from"`
				Actor string          `json:"actor"`
				Text  json.RawMessage `json:"text"`
			}
			if json.Unmarshal(raw, &m) != nil || len(m.ID) == 0 || string(m.ID) == "null" || (m.Type != "message" && m.Type != "service") {
				return nil, "", nil, Invalid("invalid Telegram exported message")
			}
			text := ""
			if len(m.Text) > 0 {
				if json.Unmarshal(m.Text, &text) != nil {
					var parts []json.RawMessage
					if json.Unmarshal(m.Text, &parts) != nil {
						return nil, "", nil, Invalid("invalid Telegram rich text")
					}
					var b strings.Builder
					for _, part := range parts {
						var segment string
						if json.Unmarshal(part, &segment) != nil {
							var entity struct {
								Text *string `json:"text"`
							}
							if json.Unmarshal(part, &entity) != nil || entity.Text == nil {
								return nil, "", nil, Invalid("invalid Telegram text entity")
							}
							segment = *entity.Text
						}
						b.WriteString(segment)
					}
					text = b.String()
				}
			}
			sender := m.From
			if sender == "" {
				sender = m.Actor
			}
			records = append(records, HubImportRecord{Index: len(records), Kind: m.Type, Raw: string(raw), TimestampText: m.Date, TimestampUncertain: true, Sender: sender, Text: text, Fields: map[string]string{"chat_id": string(c.ID), "chat_name": c.Name, "message_id": string(m.ID)}})
		}
	}
	return records, "telegram_desktop_export", []string{"Owner-selected Telegram Desktop JSON snapshot; no personal session or live history access", "Raw exported message objects retained; linked media files are not read", "Dates preserved as source text; no timezone is inferred"}, nil
}

func parseWeChatCSV(content string) ([]HubImportRecord, string, []string, error) {
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(content, "\ufeff")))
	header, err := r.Read()
	if err != nil {
		return nil, "", nil, Invalid("CSV header required")
	}
	columns := map[string]int{}
	for i, name := range header {
		if _, ok := columns[name]; ok {
			return nil, "", nil, Invalid("duplicate CSV column")
		}
		switch name {
		case "timestamp", "sender", "text", "chat_id":
		default:
			return nil, "", nil, Invalid("CSV accepts timestamp,sender,text and optional chat_id only")
		}
		columns[name] = i
	}
	for _, name := range []string{"timestamp", "sender", "text"} {
		if _, ok := columns[name]; !ok {
			return nil, "", nil, Invalid("CSV requires timestamp,sender,text")
		}
	}
	records := []HubImportRecord{}
	for {
		start := r.InputOffset()
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", nil, Invalid("invalid CSV record")
		}
		fields := map[string]string{}
		if i, ok := columns["chat_id"]; ok {
			fields["chat_id"] = row[i]
		}
		records = append(records, HubImportRecord{Index: len(records), Kind: "message", Raw: strings.TrimPrefix(content, "\ufeff")[start:r.InputOffset()], TimestampText: row[columns["timestamp"]], TimestampUncertain: true, Sender: row[columns["sender"]], Text: row[columns["text"]], Fields: fields})
	}
	return records, "owner_prepared_wechat_csv", []string{"Owner-prepared UTF-8 CSV snapshot, not an official WeChat export or live account connection", "Encrypted native WeChat backups are not supported; no decryption or session access", "Source timestamps remain uncertain; media is excluded"}, nil
}

package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const HubImportMaxBytes = 1 << 20

type HubImportInput struct {
	ProviderID    string `json:"provider_id"`
	AccountID     string `json:"account_id"`
	DisplayName   string `json:"display_name"`
	Filename      string `json:"filename"`
	Format        string `json:"format"`
	Content       string `json:"content"`
	ContentBase64 string `json:"content_base64,omitempty"`
}
type HubImportMetadata struct {
	ID          string    `json:"id"`
	ProviderID  string    `json:"provider_id"`
	Filename    string    `json:"filename"`
	Format      string    `json:"format"`
	CreatedAt   time.Time `json:"created_at"`
	Bytes       int       `json:"bytes"`
	SHA256      string    `json:"sha256"`
	RecordCount int       `json:"record_count"`
	Snapshot    bool      `json:"snapshot"`
	Live        bool      `json:"live"`
	Parsing     string    `json:"parsing"`
	Limitations []string  `json:"limitations"`
}
type HubImportResult struct {
	Connection HubConnection     `json:"connection"`
	Import     HubImportMetadata `json:"import"`
}
type HubImportRecord struct {
	Index              int               `json:"index"`
	Kind               string            `json:"kind"`
	Raw                string            `json:"raw"`
	TimestampText      string            `json:"timestamp_text,omitempty"`
	TimestampUncertain bool              `json:"timestamp_uncertain,omitempty"`
	Sender             string            `json:"sender,omitempty"`
	Text               string            `json:"text,omitempty"`
	Fields             map[string]string `json:"fields,omitempty"`
}
type HubImportPage struct {
	Import     HubImportMetadata `json:"import"`
	Records    []HubImportRecord `json:"records"`
	NextOffset *int              `json:"next_offset"`
}
type hubImportReference struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	OwnerID    string `json:"owner_id"`
	ProviderID string `json:"provider_id"`
	AccountID  string `json:"account_id"`
}
type hubImportManifest struct {
	Metadata  HubImportMetadata `json:"metadata"`
	OwnerID   string            `json:"owner_id"`
	AccountID string            `json:"account_id"`
}

var hubImportID = regexp.MustCompile(`^import_[a-z0-9]{20,64}$`)

func hubImportFormat(provider, format string) bool {
	return provider == "telegram-import" && format == "telegram-json" || provider == "wechat-import" && format == "wechat-csv" || provider == "whatsapp-import" && (format == "whatsapp-text" || format == "whatsapp-zip") || provider == "calendar-import" && format == "ics" || provider == "markdown-import" && format == "markdown"
}
func hubImportFailure() error {
	return &Error{Code: "storage_unavailable", Message: "import snapshot is unavailable"}
}

// ImportHubSnapshot writes a new immutable original on every import. Updating
// the account's encrypted reference revokes previous grants via AddHubConnection.
func (s *Store) ImportHubSnapshot(ctx context.Context, in HubImportInput, key []byte) (HubImportResult, error) {
	var out HubImportResult
	owner := UserID(ctx)
	if owner == "" {
		return out, ErrUnauthenticated
	}
	if !hubImportFormat(in.ProviderID, in.Format) || !hubText(in.AccountID, 254) || !hubText(in.DisplayName, 150) || !hubText(in.Filename, 255) || strings.ContainsAny(in.Filename, "/\\") || in.Filename == "." || in.Filename == ".." {
		return out, Invalid("supported provider, format, account, display name and plain filename are required")
	}
	source := []byte(in.Content)
	if in.Format == "whatsapp-zip" {
		if in.Content != "" || in.ContentBase64 == "" || len(in.ContentBase64) > base64.StdEncoding.EncodedLen(HubImportArchiveMaxBytes) {
			return out, Invalid("ZIP requires bounded content_base64 only")
		}
		var err error
		source, err = base64.StdEncoding.Strict().DecodeString(in.ContentBase64)
		if err != nil || len(source) == 0 || len(source) > HubImportArchiveMaxBytes {
			return out, Invalid("ZIP must be valid base64, max 10 MiB")
		}
	} else if in.ContentBase64 != "" || len(source) == 0 || len(source) > HubImportMaxBytes || !utf8.Valid(source) || bytes.ContainsRune(source, 0) {
		return out, Invalid("import content must be non-empty UTF-8 text, max 1 MiB, without NUL")
	}
	if _, err := hubCipher(key); err != nil {
		return out, err
	}
	records, parsing, limitations, err := parseHubImportSource(in.Format, source)
	if err != nil {
		return out, err
	}
	checksum := sha256.Sum256(source)
	metadata := HubImportMetadata{ID: newID("import"), ProviderID: in.ProviderID, Filename: in.Filename, Format: in.Format, CreatedAt: time.Now().UTC(), Bytes: len(source), SHA256: hex.EncodeToString(checksum[:]), RecordCount: len(records), Snapshot: true, Live: false, Parsing: parsing, Limitations: limitations}
	manifest := hubImportManifest{Metadata: metadata, OwnerID: owner, AccountID: in.AccountID}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return out, hubImportFailure()
	}
	root, err := s.hubImportRoot(true)
	if err != nil {
		return out, err
	}
	defer root.Close()
	if err = writeHubImportFile(root, metadata.ID+".source", source); err != nil {
		return out, err
	}
	// Even a later failure leaves the original immutable, never replacing another snapshot.
	if err = writeHubImportFile(root, metadata.ID+".json", payload); err != nil {
		return out, err
	}
	reference, _ := json.Marshal(hubImportReference{Kind: "hub_import", ID: metadata.ID, OwnerID: owner, ProviderID: in.ProviderID, AccountID: in.AccountID})
	connection, err := s.AddHubConnection(ctx, HubConnection{ProviderID: in.ProviderID, AccountID: in.AccountID, DisplayName: in.DisplayName}, string(reference), key)
	if err != nil {
		return out, err
	}
	return HubImportResult{Connection: connection, Import: metadata}, nil
}
func (s *Store) hubImportRoot(create bool) (*os.Root, error) {
	if s.dataDir == "" || s.dataDir == ":memory:" {
		return nil, hubImportFailure()
	}
	root, err := os.OpenRoot(s.dataDir)
	if err != nil {
		return nil, hubImportFailure()
	}
	defer root.Close()
	if create {
		if err = root.Mkdir("hub-imports", 0700); err != nil && !os.IsExist(err) {
			return nil, hubImportFailure()
		}
	}
	info, err := root.Lstat("hub-imports")
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, hubImportFailure()
	}
	imports, err := root.OpenRoot("hub-imports")
	if err != nil {
		return nil, hubImportFailure()
	}
	return imports, nil
}
func writeHubImportFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return hubImportFailure()
	}
	_, err = file.Write(data)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return hubImportFailure()
	}
	return nil
}
func readHubImportFile(root *os.Root, name string, limit int64) ([]byte, error) {
	file, err := root.Open(name)
	if err != nil {
		return nil, hubImportFailure()
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, hubImportFailure()
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, hubImportFailure()
	}
	return data, nil
}

// ExecuteHubImport only implements records.list. The API must check the agent's
// exact connection/operation grant before this call and again before release.
func (s *Store) ExecuteHubImport(ctx context.Context, c HubConnection, secret string, args map[string]any) (HubImportPage, error) {
	var out HubImportPage
	var ref hubImportReference
	if len(secret) > 16384 || json.Unmarshal([]byte(secret), &ref) != nil || ref.Kind != "hub_import" || !hubImportID.MatchString(ref.ID) || ref.OwnerID == "" || ref.OwnerID != c.OwnerID || ref.ProviderID != c.ProviderID || ref.AccountID != c.AccountID {
		return out, ErrActionForbidden
	}
	current, err := s.HubConnection(ctx, c.OwnerID, c.ID)
	if err != nil {
		return out, err
	}
	if current.Status != "configured" || current.ProviderID != ref.ProviderID || current.AccountID != ref.AccountID {
		return out, ErrActionForbidden
	}
	for key := range args {
		if key != "offset" && key != "limit" {
			return out, Invalid("records.list supports only offset and limit")
		}
	}
	offset, err := hubImportInt(args, "offset", 0, 0, HubImportMaxBytes)
	if err != nil {
		return out, err
	}
	limit, err := hubImportInt(args, "limit", 20, 1, 100)
	if err != nil {
		return out, err
	}
	root, err := s.hubImportRoot(false)
	if err != nil {
		return out, err
	}
	defer root.Close()
	raw, err := readHubImportFile(root, ref.ID+".json", 16<<10)
	if err != nil {
		return out, err
	}
	var manifest hubImportManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.OwnerID != ref.OwnerID || manifest.AccountID != ref.AccountID || manifest.Metadata.ID != ref.ID || manifest.Metadata.ProviderID != ref.ProviderID || !hubImportFormat(ref.ProviderID, manifest.Metadata.Format) || !manifest.Metadata.Snapshot || manifest.Metadata.Live {
		return out, ErrActionForbidden
	}
	source, err := readHubImportFile(root, ref.ID+".source", HubImportArchiveMaxBytes)
	if err != nil {
		return out, err
	}
	hash := sha256.Sum256(source)
	if len(source) != manifest.Metadata.Bytes || hex.EncodeToString(hash[:]) != manifest.Metadata.SHA256 {
		return out, hubImportFailure()
	}
	records, _, _, err := parseHubImportSource(manifest.Metadata.Format, source)
	if err != nil || len(records) != manifest.Metadata.RecordCount {
		return out, hubImportFailure()
	}
	if offset > len(records) {
		return out, Invalid("offset exceeds snapshot record count")
	}
	end := min(offset+limit, len(records))
	out = HubImportPage{Import: manifest.Metadata, Records: records[offset:end]}
	if end < len(records) {
		out.NextOffset = &end
	}
	return out, nil
}
func hubImportInt(args map[string]any, name string, fallback, minValue, maxValue int) (int, error) {
	raw, ok := args[name]
	if !ok {
		return fallback, nil
	}
	var value float64
	switch n := raw.(type) {
	case int:
		value = float64(n)
	case float64:
		value = n
	case json.Number:
		var err error
		value, err = n.Float64()
		if err != nil {
			return 0, Invalid("invalid pagination")
		}
	default:
		return 0, Invalid("invalid pagination")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || value < float64(minValue) || value > float64(maxValue) {
		return 0, Invalid("invalid pagination")
	}
	return int(value), nil
}

var whatsappAndroid = regexp.MustCompile(`^([0-9]{1,4}[./-][0-9]{1,2}[./-][0-9]{1,4},? [0-9]{1,2}:[0-9]{2}(?::[0-9]{2})?(?:[ \x{00A0}\x{202F}]*[AaPp][Mm])?) - (.*)$`)
var whatsappIOS = regexp.MustCompile(`^\[([0-9]{1,4}[./-][0-9]{1,2}[./-][0-9]{1,4},? [0-9]{1,2}:[0-9]{2}(?::[0-9]{2})?(?:[ \x{00A0}\x{202F}]*[AaPp][Mm])?)\] (.*)$`)

func sourceLines(content string) []string {
	lines := strings.SplitAfter(content, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
func parseHubImport(format, content string) ([]HubImportRecord, string, []string, error) {
	records := []HubImportRecord{}
	switch format {
	case "telegram-json":
		return parseTelegramExport(content)
	case "wechat-csv":
		return parseWeChatCSV(content)
	case "markdown":
		records = append(records, HubImportRecord{Kind: "markdown", Raw: content})
		return records, "original_document", []string{"Owner-selected snapshot; no live filesystem access or synchronization"}, nil
	case "whatsapp-text":
		var current HubImportRecord
		var original, text strings.Builder
		flush := func() {
			if original.Len() == 0 {
				return
			}
			current.Raw = original.String()
			current.Text = text.String()
			current.Index = len(records)
			records = append(records, current)
			original.Reset()
			text.Reset()
		}
		for _, raw := range sourceLines(content) {
			line := strings.TrimRight(raw, "\r\n")
			line = strings.TrimLeft(line, "\ufeff\u200e\u200f")
			match := whatsappAndroid.FindStringSubmatch(line)
			if match == nil {
				match = whatsappIOS.FindStringSubmatch(line)
			}
			if match != nil {
				flush()
				current = HubImportRecord{Kind: "message", TimestampText: match[1], TimestampUncertain: true}
				if sender, body, ok := strings.Cut(match[2], ": "); ok && sender != "" {
					current.Sender = sender
					text.WriteString(body)
				} else {
					current.Kind = "system"
					text.WriteString(match[2])
				}
			} else if original.Len() == 0 {
				current = HubImportRecord{Kind: "unparsed", TimestampUncertain: true}
			} else if current.Kind != "unparsed" {
				text.WriteString("\n")
				text.WriteString(strings.TrimRight(raw, "\r\n"))
			}
			original.WriteString(raw)
		}
		flush()
		return records, "whatsapp_messages_best_effort", []string{"Snapshot only; exported attachments are not included", "Timestamp text has no verified timezone or date order; no UTC timestamps are inferred", "Unrecognized lines and multiline messages are preserved as original text"}, nil
	case "ics":
		trimmed := strings.TrimSpace(strings.TrimPrefix(content, "\ufeff"))
		if !strings.HasPrefix(trimmed, "BEGIN:VCALENDAR") || !strings.HasSuffix(trimmed, "END:VCALENDAR") {
			return nil, "", nil, Invalid("ICS content must contain a VCALENDAR envelope")
		}
		var block strings.Builder
		inside := false
		flush := func(kind string) {
			if block.Len() > 0 {
				r := HubImportRecord{Kind: kind, Raw: block.String(), TimestampUncertain: true}
				if kind == "ical_event" {
					r.Fields = icalTextFields(r.Raw)
				}
				records = append(records, r)
				block.Reset()
			}
		}
		for _, line := range sourceLines(content) {
			marker := strings.TrimSpace(line)
			if marker == "BEGIN:VEVENT" {
				if inside {
					return nil, "", nil, Invalid("nested VEVENT is not supported")
				}
				flush("ical_metadata")
				inside = true
			}
			block.WriteString(line)
			if marker == "END:VEVENT" {
				if !inside {
					return nil, "", nil, Invalid("unmatched VEVENT end")
				}
				flush("ical_event")
				inside = false
			}
		}
		if inside {
			return nil, "", nil, Invalid("incomplete VEVENT")
		}
		flush("ical_metadata")
		for i := range records {
			records[i].Index = i
		}
		return records, "ical_raw_components", []string{"Snapshot of original calendar components; not a calendar query engine", "Recurrences are not expanded; exceptions and timezone definitions remain in raw source", "Date values and property parameters are untrusted source text; no timezone conversion is performed"}, nil
	default:
		return nil, "", nil, Invalid("unsupported import format")
	}
}
func icalTextFields(raw string) map[string]string {
	unfolded := bytes.ReplaceAll([]byte(raw), []byte("\r\n "), nil)
	unfolded = bytes.ReplaceAll(unfolded, []byte("\r\n\t"), nil)
	unfolded = bytes.ReplaceAll(unfolded, []byte("\n "), nil)
	unfolded = bytes.ReplaceAll(unfolded, []byte("\n\t"), nil)
	fields := map[string]string{}
	depth := 0
	for _, line := range strings.Split(string(unfolded), "\n") {
		key, value, ok := strings.Cut(strings.TrimSuffix(line, "\r"), ":")
		if !ok {
			continue
		}
		base, _, _ := strings.Cut(key, ";")
		if base == "BEGIN" {
			depth++
			continue
		}
		if base == "END" {
			depth--
			continue
		}
		if depth != 1 {
			continue
		}
		switch base {
		case "UID", "SUMMARY", "DTSTART", "DTEND", "RRULE", "RECURRENCE-ID", "STATUS":
			if _, exists := fields[key]; !exists {
				fields[key] = value
			}
		}
	}
	return fields
}

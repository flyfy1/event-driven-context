package core

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"event-driven-context/internal/hubwebhooks"
)

type HubWhatsAppEvent struct {
	Sequence   int64           `json:"sequence"`
	EventKey   string          `json:"event_key"`
	Kind       string          `json:"kind"`
	ReceiptID  string          `json:"receipt_id"`
	RecordedAt time.Time       `json:"recorded_at"`
	Data       json.RawMessage `json:"data"`
}
type HubWhatsAppPage struct {
	Events       []HubWhatsAppEvent `json:"events"`
	NextSequence int64              `json:"next_sequence"`
	HasMore      bool               `json:"has_more"`
	Notice       string             `json:"notice"`
}

// This lookup is for signature-authenticated webhook ingestion only. It is not
// an owner/agent discovery route and must not be exposed in a JSON response.
func (s *Store) HubWhatsAppConnection(ctx context.Context, id string) (HubConnection, error) {
	c := HubConnection{}
	err := s.db.QueryRowContext(ctx, "SELECT id,owner_id,provider_id,account_id,display_name,status FROM hub_connections WHERE id=? AND provider_id='whatsapp-business' AND status='configured'", id).Scan(&c.ID, &c.OwnerID, &c.ProviderID, &c.AccountID, &c.DisplayName, &c.Status)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return c, err
}

// Persist the validated original and deduplicated message/status records in one
// transaction. Secrets and retained source payloads are encrypted. Replays of
// an identical envelope or a repeated message ID never create duplicate events.
func (s *Store) IngestHubWhatsApp(ctx context.Context, c HubConnection, expectedSecret string, key []byte, envelope hubwebhooks.VerifiedWhatsAppEnvelope) (string, error) {
	binding := envelope.Binding()
	if binding.ConnectionID != c.ID || binding.OwnerID != c.OwnerID || c.ProviderID != "whatsapp-business" {
		return "", ErrForbidden
	}
	var payload struct {
		Entry []struct {
			Changes []struct {
				Value struct {
					Messages []json.RawMessage `json:"messages"`
					Statuses []json.RawMessage `json:"statuses"`
				} `json:"value"`
			} `json:"changes"`
		} `json:"entry"`
	}
	if json.Unmarshal(envelope.RawBody(), &payload) != nil {
		return "", Invalid("invalid WhatsApp envelope")
	}
	type event struct {
		key, kind string
		body      []byte
	}
	events := []event{}
	for _, entry := range payload.Entry {
		for _, change := range entry.Changes {
			for _, raw := range change.Value.Messages {
				var m struct {
					ID string `json:"id"`
				}
				if json.Unmarshal(raw, &m) != nil || !hubText(m.ID, 512) {
					return "", Invalid("invalid WhatsApp message identity")
				}
				events = append(events, event{"message:" + m.ID, "message", raw})
			}
			for _, raw := range change.Value.Statuses {
				var m struct {
					ID        string `json:"id"`
					Status    string `json:"status"`
					Timestamp string `json:"timestamp"`
				}
				if json.Unmarshal(raw, &m) != nil || !hubText(m.ID, 512) || !hubText(m.Status, 64) || !hubText(m.Timestamp, 32) {
					return "", Invalid("invalid WhatsApp status identity")
				}
				identity, _ := json.Marshal([]string{m.ID, m.Status, m.Timestamp})
				events = append(events, event{"status:" + string(identity), "status", raw})
			}
		}
	}
	if len(events) > 1000 {
		return "", Invalid("WhatsApp batch is too large")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var current []byte
	if err = tx.QueryRowContext(ctx, "SELECT credential FROM hub_connections WHERE id=? AND owner_id=? AND provider_id='whatsapp-business' AND status='configured'", c.ID, c.OwnerID).Scan(&current); err != nil {
		return "", ErrConflict
	}
	plain, err := openHubPayload(key, c, current)
	if err != nil || subtle.ConstantTimeCompare(plain, []byte(expectedSecret)) != 1 {
		return "", ErrConflict
	}
	var receipt string
	err = tx.QueryRowContext(ctx, "SELECT id FROM hub_webhook_receipts WHERE connection_id=? AND sha256=?", c.ID, envelope.SHA256()).Scan(&receipt)
	if err == nil {
		return receipt, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	receipt = newID("receipt")
	aad := c
	aad.ID += "/receipt/" + receipt
	encrypted, err := sealHubCredential(key, aad, envelope.RawBody())
	if err != nil {
		return "", err
	}
	now := time.Now().Unix()
	if _, err = tx.ExecContext(ctx, "INSERT INTO hub_webhook_receipts(id,connection_id,sha256,encrypted_body,recorded_at) VALUES(?,?,?,?,?)", receipt, c.ID, envelope.SHA256(), encrypted, now); err != nil {
		return "", err
	}
	for _, e := range events {
		aad = c
		aad.ID += "/event/" + e.key
		encrypted, err = sealHubCredential(key, aad, e.body)
		if err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO hub_webhook_events(connection_id,receipt_id,event_key,kind,encrypted_body,recorded_at) VALUES(?,?,?,?,?,?) ON CONFLICT(connection_id,event_key) DO NOTHING", c.ID, receipt, e.key, e.kind, encrypted, now); err != nil {
			return "", err
		}
	}
	return receipt, tx.Commit()
}
func openHubPayload(key []byte, c HubConnection, encrypted []byte) ([]byte, error) {
	a, err := hubCipher(key)
	if err != nil {
		return nil, err
	}
	if len(encrypted) < a.NonceSize() {
		return nil, ErrConflict
	}
	plain, err := a.Open(nil, encrypted[:a.NonceSize()], encrypted[a.NonceSize():], []byte(c.OwnerID+":"+c.ID))
	if err != nil {
		return nil, ErrConflict
	}
	return plain, nil
}

func (s *Store) ReadHubWhatsApp(ctx context.Context, c HubConnection, key []byte, operation string, args map[string]any) (any, error) {
	if c.ProviderID != "whatsapp-business" {
		return nil, ErrForbidden
	}
	if operation == "receipts.get" {
		if len(args) != 1 {
			return nil, Invalid("receipt_id is required")
		}
		id, ok := args["receipt_id"].(string)
		if !ok || !hubText(id, 100) {
			return nil, Invalid("invalid receipt_id")
		}
		var encrypted []byte
		var sha string
		var at int64
		err := s.db.QueryRowContext(ctx, "SELECT encrypted_body,sha256,recorded_at FROM hub_webhook_receipts WHERE id=? AND connection_id=?", id, c.ID).Scan(&encrypted, &sha, &at)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		aad := c
		aad.ID += "/receipt/" + id
		raw, err := openHubPayload(key, aad, encrypted)
		if err != nil {
			return nil, err
		}
		return map[string]any{"receipt_id": id, "sha256": sha, "recorded_at": time.Unix(at, 0).UTC(), "original": json.RawMessage(raw), "original_base64": base64.StdEncoding.EncodeToString(raw)}, nil
	}
	if operation != "events.list" {
		return nil, Invalid("unsupported WhatsApp read operation")
	}
	after := int64(0)
	limit := 20
	for k, v := range args {
		switch k {
		case "after_sequence":
			n, e := strconv.ParseInt(jsonNumber(v), 10, 64)
			if e != nil || n < 0 {
				return nil, Invalid("invalid after_sequence")
			}
			after = n
		case "limit":
			n, e := strconv.Atoi(jsonNumber(v))
			if e != nil || n < 1 || n > 100 {
				return nil, Invalid("limit must be 1-100")
			}
			limit = n
		default:
			return nil, Invalid("unknown WhatsApp read argument")
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,event_key,kind,receipt_id,encrypted_body,recorded_at FROM hub_webhook_events WHERE connection_id=? AND sequence>? ORDER BY sequence LIMIT ?", c.ID, after, limit+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	page := HubWhatsAppPage{Events: []HubWhatsAppEvent{}, NextSequence: after, Notice: "Only webhook messages/statuses received after subscription are available; no personal WhatsApp history or media downloads."}
	for rows.Next() {
		var e HubWhatsAppEvent
		var encrypted []byte
		var at int64
		if err = rows.Scan(&e.Sequence, &e.EventKey, &e.Kind, &e.ReceiptID, &encrypted, &at); err != nil {
			return nil, err
		}
		if len(page.Events) == limit {
			page.HasMore = true
			break
		}
		aad := c
		aad.ID += "/event/" + e.EventKey
		raw, err := openHubPayload(key, aad, encrypted)
		if err != nil {
			return nil, err
		}
		e.Data = raw
		e.RecordedAt = time.Unix(at, 0).UTC()
		page.Events = append(page.Events, e)
		page.NextSequence = e.Sequence
	}
	return page, rows.Err()
}
func jsonNumber(v any) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case float64:
		return strconv.FormatFloat(n, 'f', -1, 64)
	case int:
		return strconv.Itoa(n)
	case int64:
		return strconv.FormatInt(n, 10)
	}
	return ""
}

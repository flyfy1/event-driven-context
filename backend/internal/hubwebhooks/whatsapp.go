// Package hubwebhooks validates webhook signatures and trusted account bindings.
// The API host registers routes; core atomically retains encrypted originals and
// deduplicates records. Signature verification alone does not prove freshness.
package hubwebhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

const MaxWhatsAppPayloadBytes = 1024 * 1024

var (
	ErrInvalidConfiguration = errors.New("webhook configuration is invalid")
	ErrInvalidChallenge     = errors.New("webhook subscription challenge is invalid")
	ErrInvalidSignature     = errors.New("webhook signature is invalid")
	ErrPayloadTooLarge      = errors.New("webhook payload exceeds the size limit")
	ErrInvalidPayload       = errors.New("webhook payload is invalid")
	ErrUnsupportedEvent     = errors.New("webhook event is unsupported")
	ErrBindingMismatch      = errors.New("webhook account does not match the configured binding")
)

// WhatsAppBinding is trusted owner configuration, never request-derived routing.
// The host must authenticate the owner before creating or changing this binding.
// One verifier binds one Hub connection to one WABA and business phone number.
type WhatsAppBinding struct {
	OwnerID       string
	ConnectionID  string
	WABAID        string
	PhoneNumberID string
}

// WhatsAppConfig contains secrets and must not be logged or exposed to Agents.
// AppSecret is the Meta application's secret, not an access token. VerifyToken
// is an independently generated owner-configured subscription verification token.
type WhatsAppConfig struct {
	AppSecret   string
	VerifyToken string
	Binding     WhatsAppBinding
}

type WhatsAppVerifier struct {
	appSecret       []byte
	verifyTokenHash [sha256.Size]byte
	binding         WhatsAppBinding
}

// VerifiedWhatsAppEnvelope preserves the exact authenticated bytes and local
// provenance for future atomic ingestion. Its body remains private source data,
// not trusted instructions. No timestamp, replay key or receipt is invented.
// SHA256 is a content fingerprint, not a deduplication or replay guarantee.
type VerifiedWhatsAppEnvelope struct {
	body        []byte
	binding     WhatsAppBinding
	sha256      string
	changeCount int
}

func (e VerifiedWhatsAppEnvelope) RawBody() []byte          { return bytes.Clone(e.body) }
func (e VerifiedWhatsAppEnvelope) Binding() WhatsAppBinding { return e.binding }
func (e VerifiedWhatsAppEnvelope) SHA256() string           { return e.sha256 }
func (e VerifiedWhatsAppEnvelope) ChangeCount() int         { return e.changeCount }

// NewWhatsAppVerifier requires 16-1024 printable ASCII bytes for AppSecret and
// 16-256 for VerifyToken. This is a local configuration policy, not a statement
// about Meta's permitted token formats. Raw secrets are never included in errors.
func NewWhatsAppVerifier(config WhatsAppConfig) (*WhatsAppVerifier, error) {
	if !secretText(config.AppSecret, 16, 1024) || !secretText(config.VerifyToken, 16, 256) || config.AppSecret == config.VerifyToken || !identityText(config.Binding.OwnerID) || !identityText(config.Binding.ConnectionID) || !decimalID(config.Binding.WABAID) || !decimalID(config.Binding.PhoneNumberID) {
		return nil, ErrInvalidConfiguration
	}
	return &WhatsAppVerifier{appSecret: []byte(config.AppSecret), verifyTokenHash: sha256.Sum256([]byte(config.VerifyToken)), binding: config.Binding}, nil
}

// Challenge validates the GET subscription handshake and returns hub.challenge
// unchanged. The future HTTP host must require GET, reject duplicate hub.* query
// parameters and return this value as text/plain, never as HTML. This method
// neither registers a subscription nor confirms access to any account's data.
func (v *WhatsAppVerifier) Challenge(mode, verifyToken, challenge string) (string, error) {
	if !v.configured() {
		return "", ErrInvalidConfiguration
	}
	if mode != "subscribe" || !secretText(verifyToken, 16, 256) || !secretText(challenge, 1, 1024) {
		return "", ErrInvalidChallenge
	}
	supplied := sha256.Sum256([]byte(verifyToken))
	if subtle.ConstantTimeCompare(supplied[:], v.verifyTokenHash[:]) != 1 {
		return "", ErrInvalidChallenge
	}
	return challenge, nil
}

// Verify authenticates X-Hub-Signature-256 over an exact snapshot of the raw POST
// bytes before parsing JSON. The future HTTP host must bound the body while
// reading, reject duplicate signature headers and avoid decode/re-encode steps.
// Only object=whatsapp_business_account and field=messages are supported. Every
// entry and change must match the configured WABA and phone number; mixed batches
// fail atomically with a zero envelope. Message bodies and statuses are preserved
// as opaque data rather than interpreted, normalized or acted upon.
func (v *WhatsAppVerifier) Verify(signature string, rawBody []byte) (VerifiedWhatsAppEnvelope, error) {
	zero := VerifiedWhatsAppEnvelope{}
	if !v.configured() {
		return zero, ErrInvalidConfiguration
	}
	if len(rawBody) > MaxWhatsAppPayloadBytes {
		return zero, ErrPayloadTooLarge
	}
	if len(signature) != len("sha256=")+sha256.Size*2 || !strings.HasPrefix(signature, "sha256=") {
		return zero, ErrInvalidSignature
	}
	provided, err := hex.DecodeString(signature[len("sha256="):])
	if err != nil {
		return zero, ErrInvalidSignature
	}
	body := bytes.Clone(rawBody)
	mac := hmac.New(sha256.New, v.appSecret)
	_, _ = mac.Write(body)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return zero, ErrInvalidSignature
	}
	if !utf8.Valid(body) || validateUniqueJSON(body) != nil {
		return zero, ErrInvalidPayload
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(body, &envelope) != nil || envelope == nil {
		return zero, ErrInvalidPayload
	}
	object, ok := requiredString(envelope, "object")
	if !ok {
		return zero, ErrInvalidPayload
	}
	if object != "whatsapp_business_account" {
		return zero, ErrUnsupportedEvent
	}
	var entries []json.RawMessage
	if json.Unmarshal(envelope["entry"], &entries) != nil || len(entries) == 0 {
		return zero, ErrInvalidPayload
	}
	changeCount := 0
	for _, rawEntry := range entries {
		var entry map[string]json.RawMessage
		if json.Unmarshal(rawEntry, &entry) != nil || entry == nil {
			return zero, ErrInvalidPayload
		}
		id, ok := requiredString(entry, "id")
		if !ok {
			return zero, ErrInvalidPayload
		}
		if id != v.binding.WABAID {
			return zero, ErrBindingMismatch
		}
		var changes []json.RawMessage
		if json.Unmarshal(entry["changes"], &changes) != nil || len(changes) == 0 {
			return zero, ErrInvalidPayload
		}
		for _, rawChange := range changes {
			var change map[string]json.RawMessage
			if json.Unmarshal(rawChange, &change) != nil || change == nil {
				return zero, ErrInvalidPayload
			}
			field, ok := requiredString(change, "field")
			if !ok {
				return zero, ErrInvalidPayload
			}
			if field != "messages" {
				return zero, ErrUnsupportedEvent
			}
			var value map[string]json.RawMessage
			if json.Unmarshal(change["value"], &value) != nil || value == nil {
				return zero, ErrInvalidPayload
			}
			product, ok := requiredString(value, "messaging_product")
			if !ok {
				return zero, ErrInvalidPayload
			}
			if product != "whatsapp" {
				return zero, ErrUnsupportedEvent
			}
			var metadata map[string]json.RawMessage
			if json.Unmarshal(value["metadata"], &metadata) != nil || metadata == nil {
				return zero, ErrInvalidPayload
			}
			phone, ok := requiredString(metadata, "phone_number_id")
			if !ok {
				return zero, ErrInvalidPayload
			}
			if phone != v.binding.PhoneNumberID {
				return zero, ErrBindingMismatch
			}
			if err := supportedMessageValue(value); err != nil {
				return zero, err
			}
			changeCount++
		}
	}
	digest := sha256.Sum256(body)
	return VerifiedWhatsAppEnvelope{body: body, binding: v.binding, sha256: hex.EncodeToString(digest[:]), changeCount: changeCount}, nil
}

func (v *WhatsAppVerifier) configured() bool {
	return v != nil && len(v.appSecret) >= 16 && identityText(v.binding.OwnerID) && identityText(v.binding.ConnectionID) && decimalID(v.binding.WABAID) && decimalID(v.binding.PhoneNumberID)
}
func secretText(s string, min, max int) bool {
	if len(s) < min || len(s) > max {
		return false
	}
	for _, b := range []byte(s) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func identityText(s string) bool { return secretText(s, 1, 256) }
func decimalID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for _, b := range []byte(s) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}
func requiredString(m map[string]json.RawMessage, key string) (string, bool) {
	var value string
	err := json.Unmarshal(m[key], &value)
	return value, err == nil && value != ""
}

// The messages subscription can deliver inbound messages, delivery statuses or
// errors. Their details remain opaque; an unsupported/empty event is not accepted
// merely because it copied the expected account metadata.
func supportedMessageValue(value map[string]json.RawMessage) error {
	found := false
	for _, key := range []string{"messages", "statuses", "errors"} {
		raw, exists := value[key]
		if !exists {
			continue
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil || len(items) == 0 {
			return ErrInvalidPayload
		}
		for _, item := range items {
			var object map[string]json.RawMessage
			if json.Unmarshal(item, &object) != nil || len(object) == 0 {
				return ErrInvalidPayload
			}
		}
		found = true
	}
	if !found {
		return ErrUnsupportedEvent
	}
	return nil
}

// Reject ambiguous duplicate/case-variant keys, concatenated JSON documents and
// excessive nesting before semantic validation. Unknown provider fields remain
// preserved in the original body; only supported routing fields are interpreted.
func validateUniqueJSON(body []byte) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return ErrInvalidPayload
		}
		token, err := d.Token()
		if err != nil {
			return ErrInvalidPayload
		}
		delim, isDelim := token.(json.Delim)
		if !isDelim {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return ErrInvalidPayload
				}
				name, ok := key.(string)
				if !ok {
					return ErrInvalidPayload
				}
				folded := strings.ToLower(name)
				if keys[folded] {
					return ErrInvalidPayload
				}
				keys[folded] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return ErrInvalidPayload
			}
		case '[':
			for d.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return ErrInvalidPayload
			}
		default:
			return ErrInvalidPayload
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalidPayload
	}
	return nil
}

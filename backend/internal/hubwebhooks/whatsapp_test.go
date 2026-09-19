package hubwebhooks

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

const testSecret = "synthetic-app-secret-for-fixtures"
const testToken = "synthetic-verification-token"
const testBody = `{"object":"whatsapp_business_account","entry":[{"id":"1001","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"2001"},"messages":[{"id":"wamid.fixture","text":{"body":"Synthetic fixture"}}]}}]}]}`

func testConfig() WhatsAppConfig {
	return WhatsAppConfig{AppSecret: testSecret, VerifyToken: testToken, Binding: WhatsAppBinding{OwnerID: "owner-a", ConnectionID: "conn-a", WABAID: "1001", PhoneNumberID: "2001"}}
}
func verifier(t *testing.T) *WhatsAppVerifier {
	t.Helper()
	v, err := NewWhatsAppVerifier(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func signature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
func TestWhatsAppChallengeAndConfiguration(t *testing.T) {
	v := verifier(t)
	if challenge, err := v.Challenge("subscribe", testToken, "123456789"); err != nil || challenge != "123456789" {
		t.Fatal("challenge", err)
	}
	for _, tt := range []struct{ mode, token, challenge string }{
		{"unsubscribe", testToken, "123"}, {"subscribe", "different-synthetic-token", "123"}, {"subscribe", testToken, ""}, {"subscribe", testToken, "123\n"}, {"subscribe", testToken, strings.Repeat("x", 1025)},
	} {
		if got, err := v.Challenge(tt.mode, tt.token, tt.challenge); !errors.Is(err, ErrInvalidChallenge) || got != "" {
			t.Fatal("invalid challenge accepted")
		}
	}
	for _, mutate := range []func(*WhatsAppConfig){
		func(c *WhatsAppConfig) { c.AppSecret = "" }, func(c *WhatsAppConfig) { c.VerifyToken = "short" }, func(c *WhatsAppConfig) { c.VerifyToken = c.AppSecret }, func(c *WhatsAppConfig) { c.AppSecret += "\n" }, func(c *WhatsAppConfig) { c.Binding.OwnerID = "" }, func(c *WhatsAppConfig) { c.Binding.ConnectionID = "" }, func(c *WhatsAppConfig) { c.Binding.WABAID = "../1001" }, func(c *WhatsAppConfig) { c.Binding.PhoneNumberID = "" },
	} {
		c := testConfig()
		mutate(&c)
		if got, err := NewWhatsAppVerifier(c); got != nil || !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatal("invalid configuration accepted")
		}
	}
	for _, v := range []*WhatsAppVerifier{nil, {}} {
		if _, err := v.Challenge("subscribe", testToken, "123"); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatal("unconfigured challenge")
		}
		if _, err := v.Verify(signature([]byte(testBody), testSecret), []byte(testBody)); !errors.Is(err, ErrInvalidConfiguration) {
			t.Fatal("unconfigured verification")
		}
	}
}
func TestWhatsAppExactBytesProvenanceAndAccountIsolation(t *testing.T) {
	v := verifier(t)
	body := []byte(testBody)
	envelope, err := v.Verify(signature(body, testSecret), body)
	if err != nil {
		t.Fatal(err)
	}
	expected := sha256.Sum256(body)
	if envelope.Binding() != testConfig().Binding || envelope.ChangeCount() != 1 || envelope.SHA256() != hex.EncodeToString(expected[:]) || !bytes.Equal(envelope.RawBody(), body) {
		t.Fatal("lost provenance")
	}
	body[0] = '!'
	copy := envelope.RawBody()
	copy[1] = '!'
	if string(envelope.RawBody()) != testBody {
		t.Fatal("mutable authenticated bytes")
	}
	c := testConfig()
	c.Binding = WhatsAppBinding{OwnerID: "owner-b", ConnectionID: "conn-b", WABAID: "1002", PhoneNumberID: "2002"}
	other, err := NewWhatsAppVerifier(c)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := other.Verify(signature([]byte(testBody), testSecret), []byte(testBody)); !errors.Is(err, ErrBindingMismatch) || len(out.RawBody()) != 0 {
		t.Fatal("cross-account event accepted")
	}
	second := strings.NewReplacer("1001", "1002", "2001", "2002").Replace(testBody)
	if out, err := other.Verify(signature([]byte(second), testSecret), []byte(second)); err != nil || out.Binding() != c.Binding {
		t.Fatal("separate account rejected", err)
	}
	// Identical signed deliveries remain valid: this helper deliberately does not claim deduplication.
	for i := 0; i < 2; i++ {
		if _, err := v.Verify(signature([]byte(testBody), testSecret), []byte(testBody)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestWhatsAppRejectsInvalidSignaturesBeforeParsing(t *testing.T) {
	v := verifier(t)
	body := []byte(testBody)
	valid := signature(body, testSecret)
	for _, sig := range []string{"", "sha1=" + strings.Repeat("0", 40), "sha256=" + strings.Repeat("z", 64), valid + " ", signature(body, "wrong-synthetic-secret")} {
		if out, err := v.Verify(sig, body); !errors.Is(err, ErrInvalidSignature) || len(out.RawBody()) != 0 {
			t.Fatal("invalid signature accepted")
		}
	}
	if _, err := v.Verify(valid, append(body, ' ')); !errors.Is(err, ErrInvalidSignature) {
		t.Fatal("changed exact bytes accepted")
	}
	if _, err := v.Verify(valid, []byte("{broken")); !errors.Is(err, ErrInvalidSignature) {
		t.Fatal("parsed unauthenticated JSON")
	}
	oversized := bytes.Repeat([]byte("x"), MaxWhatsAppPayloadBytes+1)
	if _, err := v.Verify(signature(oversized, testSecret), oversized); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatal("oversized body accepted")
	}
}
func TestWhatsAppRejectsMalformedAndMixedSignedBatches(t *testing.T) {
	v := verifier(t)
	mixedEntry := strings.TrimSuffix(testBody, "]}") + `,{"id":"1002","changes":[{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"2002"},"messages":[{"id":"other"}]}}]}]}`
	change := `{"field":"messages","value":{"messaging_product":"whatsapp","metadata":{"phone_number_id":"2002"},"messages":[{"id":"other"}]}}`
	mixedChange := strings.Replace(testBody, `}}]}]}`, `}},`+change+`]}]}`, 1)
	cases := []struct {
		name, body string
		want       error
	}{
		{"broken", "{", ErrInvalidPayload}, {"empty", "", ErrInvalidPayload}, {"root null", "null", ErrInvalidPayload},
		{"wrong object", strings.Replace(testBody, "whatsapp_business_account", "page", 1), ErrUnsupportedEvent},
		{"no entries", `{"object":"whatsapp_business_account","entry":[]}`, ErrInvalidPayload},
		{"wrong WABA", strings.Replace(testBody, "1001", "1002", 1), ErrBindingMismatch},
		{"wrong phone", strings.Replace(testBody, "2001", "2002", 1), ErrBindingMismatch},
		{"mixed entries", mixedEntry, ErrBindingMismatch}, {"mixed changes", mixedChange, ErrBindingMismatch},
		{"unsupported field", strings.Replace(testBody, `"field":"messages"`, `"field":"account_update"`, 1), ErrUnsupportedEvent},
		{"wrong product", strings.Replace(testBody, `"messaging_product":"whatsapp"`, `"messaging_product":"messenger"`, 1), ErrUnsupportedEvent},
		{"duplicate", strings.Replace(testBody, `"object":`, `"object":"page","object":`, 1), ErrInvalidPayload},
		{"case duplicate", strings.Replace(testBody, `"object":`, `"Object":"page","object":`, 1), ErrInvalidPayload},
		{"extra document", testBody + ` {}`, ErrInvalidPayload},
		{"invalid UTF8", testBody + string([]byte{0xff}), ErrInvalidPayload},
		{"deep", `{"nested":` + strings.Repeat("[", 70) + `0` + strings.Repeat("]", 70) + `}`, ErrInvalidPayload},
		{"no message kind", strings.Replace(testBody, `"messages":[{"id":"wamid.fixture","text":{"body":"Synthetic fixture"}}]`, `"contacts":[]`, 1), ErrUnsupportedEvent},
		{"empty messages", strings.Replace(testBody, `[{"id":"wamid.fixture","text":{"body":"Synthetic fixture"}}]`, `[]`, 1), ErrInvalidPayload},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			b := []byte(tt.body)
			out, err := v.Verify(signature(b, testSecret), b)
			if !errors.Is(err, tt.want) || len(out.RawBody()) != 0 || out.ChangeCount() != 0 {
				t.Fatalf("got %v, want %v", err, tt.want)
			}
		})
	}
	for _, kind := range []string{"statuses", "errors"} {
		b := []byte(strings.Replace(testBody, `"messages":[`, `"`+kind+`":[`, 1))
		if _, err := v.Verify(signature(b, testSecret), b); err != nil {
			t.Fatal("supported event rejected", kind, err)
		}
	}
}

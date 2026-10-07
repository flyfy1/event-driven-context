package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"event-driven-context/internal/core"
	"event-driven-context/internal/hubwebhooks"
)

type hubWhatsAppCredential struct {
	AppSecret     string `json:"app_secret"`
	VerifyToken   string `json:"verify_token"`
	WABAID        string `json:"waba_id"`
	PhoneNumberID string `json:"phone_number_id"`
}

func hubWhatsAppVerifier(c core.HubConnection, secret string) (*hubwebhooks.WhatsAppVerifier, error) {
	var cfg hubWhatsAppCredential
	d := json.NewDecoder(strings.NewReader(secret))
	d.DisallowUnknownFields()
	if d.Decode(&cfg) != nil || d.Decode(new(any)) != io.EOF {
		return nil, hubwebhooks.ErrInvalidConfiguration
	}
	return hubwebhooks.NewWhatsAppVerifier(hubwebhooks.WhatsAppConfig{AppSecret: cfg.AppSecret, VerifyToken: cfg.VerifyToken, Binding: hubwebhooks.WhatsAppBinding{OwnerID: c.OwnerID, ConnectionID: c.ID, WABAID: cfg.WABAID, PhoneNumberID: cfg.PhoneNumberID}})
}
func registerHubWhatsAppHandlers(mux *http.ServeMux, store *core.Store, key []byte) {
	load := func(r *http.Request) (core.HubConnection, string, *hubwebhooks.WhatsAppVerifier, error) {
		c, err := store.HubWhatsAppConnection(r.Context(), r.PathValue("id"))
		if err != nil {
			return c, "", nil, err
		}
		secret, err := store.HubCredential(r.Context(), c, key)
		if err != nil {
			return c, "", nil, err
		}
		v, err := hubWhatsAppVerifier(c, secret)
		return c, secret, v, err
	}
	reject := func(w http.ResponseWriter) {
		respond(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "webhook_rejected", "message": "Webhook verification failed"}})
	}
	mux.HandleFunc("GET /v1/hub/webhooks/whatsapp/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		_, _, verifier, err := load(r)
		if err != nil {
			reject(w)
			return
		}
		q := r.URL.Query()
		for _, name := range []string{"hub.mode", "hub.verify_token", "hub.challenge"} {
			if len(q[name]) != 1 {
				reject(w)
				return
			}
		}
		challenge, err := verifier.Challenge(q.Get("hub.mode"), q.Get("hub.verify_token"), q.Get("hub.challenge"))
		if err != nil {
			reject(w)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, challenge)
	})
	mux.HandleFunc("POST /v1/hub/webhooks/whatsapp/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		c, secret, verifier, err := load(r)
		if err != nil {
			reject(w)
			return
		}
		signatures := r.Header.Values("X-Hub-Signature-256")
		if len(signatures) != 1 {
			reject(w)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, hubwebhooks.MaxWhatsAppPayloadBytes))
		if err != nil {
			respond(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "webhook body limit"})
			return
		}
		envelope, err := verifier.Verify(signatures[0], bytes.Clone(raw))
		if err != nil {
			reject(w)
			return
		}
		_, err = store.IngestHubWhatsApp(r.Context(), c, secret, key, envelope)
		if err != nil {
			hubFail(w, err)
			return
		}
		respond(w, http.StatusOK, map[string]bool{"accepted": true})
	})
}

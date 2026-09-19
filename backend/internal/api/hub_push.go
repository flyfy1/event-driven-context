package api

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/hubpush"
)

var hubPushOnce sync.Once
var hubPushSender *hubpush.Sender
var hubPushKey []byte
var hubPushSlots = make(chan struct{}, 4)
var hubPushCooldownMu sync.Mutex
var hubPushCooldown = make(map[string]time.Time)

func reserveHubPush(owner string) bool {
	hubPushCooldownMu.Lock()
	defer hubPushCooldownMu.Unlock()
	now := time.Now()
	for owner, t := range hubPushCooldown {
		if now.Sub(t) >= 30*time.Second {
			delete(hubPushCooldown, owner)
		}
	}
	if _, ok := hubPushCooldown[owner]; ok {
		return false
	}
	if len(hubPushCooldown) >= 1024 {
		return false
	}
	hubPushCooldown[owner] = now
	return true
}

func configureHubPush() {
	hubPushOnce.Do(func() {
		var err error
		hubPushSender, err = hubpush.FromEnv()
		hubPushKey, _ = base64.StdEncoding.DecodeString(os.Getenv("EDC_HUB_CREDENTIAL_KEY"))
		if err != nil || len(hubPushKey) != 32 {
			hubPushSender = nil
			if err != nil {
				log.Print("Hub push is unavailable: check FCM configuration")
			}
		}
	})
}

func registerHubPushHandlers(mux *http.ServeMux, store *core.Store) {
	configureHubPush()
	mux.Handle("POST /v1/hub/devices", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token    string `json:"token"`
			Platform string `json:"platform"`
		}
		if err := decode(r, &in); err != nil {
			failV2(w, err)
			return
		}
		err := store.RegisterHubDevice(r.Context(), in.Token, in.Platform, hubPushKey)
		v2RespondResult(w, 200, map[string]any{"registered": err == nil, "push_configured": hubPushSender != nil}, err)
	})))
	mux.Handle("DELETE /v1/hub/devices", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if err := decode(r, &in); err != nil {
			failV2(w, err)
			return
		}
		err := store.DeleteHubDevice(r.Context(), in.Token)
		v2RespondResult(w, 200, map[string]bool{"ok": err == nil}, err)
	})))
	mux.Handle("GET /v1/hub/devices/status", authenticated(store, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		registered, err := store.HasHubDevice(r.Context())
		v2RespondResult(w, 200, map[string]bool{"registered": registered, "push_configured": hubPushSender != nil}, err)
	})))
}

// notifyHubOwner is best effort and bounded. Authorization state is persisted before
// calling it; push failure must never undo a request or imply the user was notified.
func notifyHubOwner(ctx context.Context, store *core.Store, ownerID string) {
	configureHubPush()
	if hubPushSender == nil || !reserveHubPush(ownerID) {
		return
	}
	select {
	case hubPushSlots <- struct{}{}:
	default:
		return
	}
	defer func() { <-hubPushSlots }()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	devices, err := store.HubDeviceTokens(ctx, ownerID, hubPushKey)
	if err != nil {
		return
	}
	for _, token := range devices {
		if ctx.Err() != nil {
			return
		}
		if err := hubPushSender.Send(ctx, token); errors.Is(err, hubpush.ErrUnregistered) {
			_ = store.DeleteHubDevice(core.WithUser(ctx, ownerID), token)
		} else if err != nil {
			log.Print("Hub push delivery failed; pending requests remain available in My authorizations")
		}
	}
}

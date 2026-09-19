package core

import (
	"bytes"
	"context"
	"testing"
)

func TestHubDeviceEncryptionAndOwnerRebinding(t *testing.T) {
	s := openTest(t)
	if _, err := s.db.Exec(HubDeviceSchema); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	a, err := s.Register(ctx, Credentials{Username: "device-owner-a", Email: "device-a@example.invalid", Password: "device-password-long"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Register(ctx, Credentials{Username: "device-owner-b", Email: "device-b@example.invalid", Password: "device-password-long"})
	if err != nil {
		t.Fatal(err)
	}
	ca, cb := WithUser(ctx, a.ID), WithUser(ctx, b.ID)
	key := bytes.Repeat([]byte{5}, 32)
	token := "test-private-device-token"
	if err := s.RegisterHubDevice(ctx, token, "android", key); err != ErrUnauthenticated {
		t.Fatalf("anonymous = %v", err)
	}
	if err := s.RegisterHubDevice(ca, token, "android", nil); err == nil {
		t.Fatal("missing key allowed")
	}
	if err := s.RegisterHubDevice(ca, token, "ios", key); err == nil {
		t.Fatal("invalid platform allowed")
	}
	if err := s.RegisterHubDevice(ca, token, "android", key); err != nil {
		t.Fatal(err)
	}
	var stored []byte
	if err := s.db.QueryRow("SELECT token_cipher FROM hub_devices").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte(token)) {
		t.Fatal("plaintext token stored")
	}
	tokens, err := s.HubDeviceTokens(ctx, a.ID, key)
	if err != nil || len(tokens) != 1 || tokens[0] != token {
		t.Fatalf("decrypt count %d: %v", len(tokens), err)
	}
	if _, err := s.HubDeviceTokens(ctx, a.ID, bytes.Repeat([]byte{6}, 32)); err == nil {
		t.Fatal("wrong key allowed")
	}
	if err := s.DeleteHubDevice(cb, token); err != nil {
		t.Fatal(err)
	}
	if exists, err := s.HasHubDevice(ca); err != nil || !exists {
		t.Fatal("other owner deleted device")
	}
	if err := s.RegisterHubDevice(cb, token, "android", key); err != nil {
		t.Fatal(err)
	}
	if exists, err := s.HasHubDevice(ca); err != nil || exists {
		t.Fatal("device still bound to old owner")
	}
	tokens, err = s.HubDeviceTokens(ctx, b.ID, key)
	if err != nil || len(tokens) != 1 || tokens[0] != token {
		t.Fatalf("rebound token: %v", err)
	}
	if err := s.DeleteHubDevice(ca, token); err != nil {
		t.Fatal(err)
	}
	if exists, _ := s.HasHubDevice(cb); !exists {
		t.Fatal("old owner removed new binding")
	}
	if err := s.DeleteHubDevice(cb, token); err != nil {
		t.Fatal(err)
	}
	if exists, _ := s.HasHubDevice(cb); exists {
		t.Fatal("logout did not remove token")
	}
}

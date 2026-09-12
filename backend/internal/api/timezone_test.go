package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

func TestV2HTTPProjectTimezoneLifecycleAndPluginSelfView(t *testing.T) {
	f := newV2APIFixture(t)
	if f.project.Timezone != "UTC" {
		t.Fatalf("default timezone = %q", f.project.Timezone)
	}

	path := "/v1/projects/" + f.project.ID
	w := f.request(t, http.MethodPatch, path, f.token, "application/json", v2JSONBody(t, map[string]string{"timezone": "not/a-zone"}))
	if w.Code != http.StatusBadRequest || v2ErrorCode(decodeV2APIError(t, w)) != "invalid_input" {
		t.Fatalf("invalid timezone: %d %s", w.Code, w.Body.String())
	}

	if _, err := f.store.AddMember(core.WithUser(context.Background(), f.alice.ID), core.MemberInput{ProjectID: f.project.ID, Username: f.bob.Username}); err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodPatch, path, f.bobToken, "application/json", v2JSONBody(t, map[string]string{"timezone": "Asia/Singapore"}))
	if w.Code != http.StatusForbidden || v2ErrorCode(decodeV2APIError(t, w)) != "forbidden" {
		t.Fatalf("member timezone update: %d %s", w.Code, w.Body.String())
	}

	w = f.request(t, http.MethodPatch, path, f.token, "application/json", v2JSONBody(t, map[string]string{"timezone": "Asia/Singapore"}))
	updated := decodeV2Response[core.Project](t, w)
	if w.Code != http.StatusOK || updated.ID != f.project.ID || updated.Timezone != "Asia/Singapore" {
		t.Fatalf("timezone update: %d %#v", w.Code, updated)
	}
	w = f.request(t, http.MethodGet, "/v1/projects", f.token, "", nil)
	projects := decodeV2Response[core.Projects](t, w)
	if w.Code != http.StatusOK || len(projects.Projects) != 1 || projects.Projects[0].Timezone != "Asia/Singapore" {
		t.Fatalf("timezone list: %d %#v", w.Code, projects)
	}

	manifest := v2.Manifest{
		ID: "timezone-reader", Version: "0.1.0", Name: "Timezone reader",
		Permissions: v2.Permissions{ReadEvents: []string{"note"}},
	}
	w = f.request(t, http.MethodPost, path+"/plugins", f.token, "application/json", v2JSONBody(t, v2.InstallPluginInput{Manifest: manifest}))
	installed := decodeV2Response[v2.InstallPluginResult](t, w)
	if w.Code != http.StatusCreated {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	w = f.request(t, http.MethodGet, path+"/plugins", installed.Token, "", nil)
	self := decodeV2Response[struct {
		Plugins []v2.Installation `json:"plugins"`
	}](t, w)
	if w.Code != http.StatusOK || len(self.Plugins) != 1 || self.Plugins[0].ProjectTimezone != "Asia/Singapore" {
		t.Fatalf("plugin timezone: %d %#v", w.Code, self)
	}
}

func decodeV2APIError(t *testing.T, w *httptest.ResponseRecorder) error {
	t.Helper()
	var envelope struct {
		Error v2.Error `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	return &envelope.Error
}

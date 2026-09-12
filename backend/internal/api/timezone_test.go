package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
)

func TestV2HTTPProjectNameAndTimezoneLifecycleAndPluginSelfView(t *testing.T) {
	f := newV2APIFixture(t)
	if f.project.Timezone != "UTC" {
		t.Fatalf("default timezone = %q", f.project.Timezone)
	}
	w := f.request(t, http.MethodPost, "/v1/projects", f.token, "application/json", v2JSONBody(t, core.ProjectInput{Name: "HTTP default timezone"}))
	created := decodeV2Response[core.Project](t, w)
	if w.Code != http.StatusCreated || created.Timezone != "UTC" {
		t.Fatalf("HTTP create default timezone: %d %#v", w.Code, created)
	}

	path := "/v1/projects/" + f.project.ID
	w = f.request(t, http.MethodPatch, path, f.token, "application/json", v2JSONBody(t, map[string]string{"name": "  Renamed project  "}))
	renamed := decodeV2Response[core.Project](t, w)
	if w.Code != http.StatusOK || renamed.ID != f.project.ID || renamed.Name != "Renamed project" {
		t.Fatalf("owner name update: %d %#v", w.Code, renamed)
	}

	w = f.request(t, http.MethodPatch, path, f.token, "application/json", v2JSONBody(t, map[string]string{"timezone": "not/a-zone"}))
	if w.Code != http.StatusBadRequest || v2ErrorCode(decodeV2APIError(t, w)) != "invalid_input" {
		t.Fatalf("invalid timezone: %d %s", w.Code, w.Body.String())
	}

	if _, err := f.store.AddMember(core.WithUser(context.Background(), f.alice.ID), core.MemberInput{ProjectID: f.project.ID, Username: f.bob.Username}); err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodPatch, path, f.bobToken, "application/json", v2JSONBody(t, map[string]string{"name": "Member rename"}))
	if w.Code != http.StatusForbidden || v2ErrorCode(decodeV2APIError(t, w)) != "forbidden" {
		t.Fatalf("member name update: %d %s", w.Code, w.Body.String())
	}
	if _, err := f.store.SetMemberRole(core.WithUser(context.Background(), f.alice.ID), core.MemberRoleInput{ProjectID: f.project.ID, UserID: f.bob.ID, Role: "owner"}); err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodPatch, path, f.bobToken, "application/json", v2JSONBody(t, map[string]string{"name": "Co-owner rename"}))
	if updated := decodeV2Response[core.Project](t, w); w.Code != http.StatusOK || updated.Name != "Co-owner rename" {
		t.Fatalf("additional owner name update: %d %#v", w.Code, updated)
	}
	for label, body := range map[string]any{
		"empty":     map[string]string{"name": "  "},
		"oversize":  map[string]string{"name": strings.Repeat("界", 67)},
		"no fields": map[string]string{},
	} {
		w = f.request(t, http.MethodPatch, path, f.token, "application/json", v2JSONBody(t, body))
		if w.Code != http.StatusBadRequest || v2ErrorCode(decodeV2APIError(t, w)) != "invalid_input" {
			t.Fatalf("%s project update: %d %s", label, w.Code, w.Body.String())
		}
	}
	w = f.request(t, http.MethodPatch, path, f.bobToken, "application/json", v2JSONBody(t, map[string]string{"timezone": "Asia/Singapore"}))
	if w.Code != http.StatusOK {
		t.Fatalf("additional owner timezone update: %d %s", w.Code, w.Body.String())
	}

	w = f.request(t, http.MethodPatch, path, f.token, "application/json", v2JSONBody(t, map[string]string{"timezone": "Asia/Singapore"}))
	updated := decodeV2Response[core.Project](t, w)
	if w.Code != http.StatusOK || updated.ID != f.project.ID || updated.Timezone != "Asia/Singapore" {
		t.Fatalf("timezone update: %d %#v", w.Code, updated)
	}
	w = f.request(t, http.MethodGet, "/v1/projects", f.token, "", nil)
	projects := decodeV2Response[core.Projects](t, w)
	listed := projectByID(projects.Projects, f.project.ID)
	if w.Code != http.StatusOK || listed.Name != "Co-owner rename" || listed.Timezone != "Asia/Singapore" {
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

func projectByID(projects []core.Project, projectID string) core.Project {
	for _, project := range projects {
		if project.ID == projectID {
			return project
		}
	}
	return core.Project{}
}

func projectTimezone(projects []core.Project, projectID string) string {
	for _, project := range projects {
		if project.ID == projectID {
			return project.Timezone
		}
	}
	return ""
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

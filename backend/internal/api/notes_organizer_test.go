package api

import (
	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
	"net/http"
	"testing"
)

func TestOrganizerHTTPRequiresGrantedProjectPlugin(t *testing.T) {
	f := newV2APIFixture(t)
	base := "/v1/projects/" + f.project.ID + "/notes/organizer/"
	for _, action := range []string{"begin", "publish", "cancel"} {
		w := f.request(t, http.MethodPost, base+action, f.token, "application/json", v2JSONBody(t, map[string]string{}))
		if w.Code != http.StatusForbidden {
			t.Fatalf("user %s: %d %s", action, w.Code, w.Body.String())
		}
	}
	// Bodies must match strict endpoint schemas to reach authorization.
	out, err := f.service.InstallPlugin(core.WithUser(t.Context(), f.alice.ID), f.project.ID, v2.InstallPluginInput{Manifest: v2.Manifest{ID: "notes-indexer", Version: "1", Name: "Notes", Permissions: v2.Permissions{ReadEvents: []string{"log", "note", "derived"}, OrganizeNotes: true}}})
	if err != nil {
		t.Fatal(err)
	}
	body := map[string]string{"prompt_version": "1"}
	w := f.request(t, http.MethodPost, base+"begin", out.Token, "application/json", v2JSONBody(t, body))
	if w.Code != 200 {
		t.Fatalf("granted begin %d %s", w.Code, w.Body.String())
	}
	other, err := f.store.CreateProject(core.WithUser(t.Context(), f.bob.ID), core.ProjectInput{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodPost, "/v1/projects/"+other.ID+"/notes/organizer/begin", out.Token, "application/json", v2JSONBody(t, body))
	if w.Code == 200 {
		t.Fatal("cross-project organizer accepted")
	}
}

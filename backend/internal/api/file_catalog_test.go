package api

import (
	"bytes"
	"encoding/json"
	"event-driven-context/internal/core"
	"event-driven-context/internal/v2"
	"net/http"
	"testing"
)

func TestFileCatalogHTTPScopesAndSnapshot(t *testing.T) {
	f := newV2APIFixture(t)
	ctx := core.WithUser(t.Context(), f.alice.ID)
	file, err := f.service.PutFile(ctx, f.project.ID, v2.FileUpload{Filename: "hello.txt", MediaType: "text/plain", SizeBytes: 5, Reader: bytes.NewBufferString("hello")})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/v1/projects/" + f.project.ID + "/files"
	w := f.request(t, http.MethodGet, prefix+"/catalog", f.token, "", nil)
	out := decodeV2Response[v2.FileCatalogPage](t, w)
	if w.Code != 200 || len(out.Files) != 0 {
		t.Fatalf("unattached %d %#v", w.Code, out)
	}
	written, err := f.service.RecordEvents(ctx, f.project.ID, v2.RecordEventsInput{Events: []v2.EventInput{{ID: "11111111-1111-4111-8111-111111111111", Type: "note", Content: v2.EventContent{Kind: "file", FileID: file.ID}, Source: map[string]json.RawMessage{"channel": json.RawMessage(`"app"`)}}}})
	if err != nil || written.Results[0].Status != "created" {
		t.Fatalf("write %#v %v", written, err)
	}
	for _, suffix := range []string{"/catalog", "/" + file.ID + "/metadata", "/" + file.ID + "/references"} {
		w = f.request(t, http.MethodGet, prefix+suffix, f.token, "", nil)
		if w.Code != 200 {
			t.Fatalf("authorized %s %d %s", suffix, w.Code, w.Body.String())
		}
		w = f.request(t, http.MethodGet, prefix+suffix, f.bobToken, "", nil)
		if w.Code != 404 && w.Code != 403 {
			t.Fatalf("foreign %s %d", suffix, w.Code)
		}
	}
	w = f.request(t, http.MethodGet, prefix+"/catalog?through_sequence=0", f.token, "", nil)
	out = decodeV2Response[v2.FileCatalogPage](t, w)
	if w.Code != 200 || out.ThroughSequence != 0 || len(out.Files) != 0 {
		t.Fatalf("historical %#v", out)
	}
	w = f.request(t, http.MethodGet, prefix+"/catalog?through_sequence=2", f.token, "", nil)
	if w.Code != 400 {
		t.Fatalf("future %d", w.Code)
	}
	w = f.request(t, http.MethodGet, prefix+"/catalog?limit=1&limit=2", f.token, "", nil)
	if w.Code != 400 {
		t.Fatalf("duplicate query %d", w.Code)
	}
	grant, err := f.service.InstallPlugin(ctx, f.project.ID, v2.InstallPluginInput{Manifest: v2.Manifest{ID: "catalog-reader", Name: "Catalog", Version: "1", Permissions: v2.Permissions{ReadEvents: []string{"note"}}}})
	if err != nil {
		t.Fatal(err)
	}
	w = f.request(t, http.MethodGet, prefix+"/catalog", grant.Token, "", nil)
	out = decodeV2Response[v2.FileCatalogPage](t, w)
	if w.Code != 200 || len(out.Files) != 1 || out.Files[0].ID != file.ID {
		t.Fatalf("plugin %d %#v", w.Code, out)
	}
	w = f.request(t, http.MethodGet, "/v1/projects/prj_other/files/catalog", grant.Token, "", nil)
	if w.Code == 200 {
		t.Fatal("plugin crossed project")
	}
}

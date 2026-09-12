package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"event-driven-context/internal/buildinfo"
)

func TestCheckCacheAndDailyNotification(t *testing.T) {
	withBuildVersion(t, "v1.0.0")
	now := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	calls := 0
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/.well-known/edc-cli" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(buildinfo.CLIPolicy{
			LatestVersion: "v1.1.0", MinimumVersion: "v0.9.0",
			ReleaseAPIURL: server.URL + "/release", ReleasePageURL: server.URL + "/page",
		})
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client(), Now: func() time.Time { return now }, OS: "darwin", Arch: "arm64"}
	cache := filepath.Join(t.TempDir(), "update-check.json")

	first, err := client.CheckCached(context.Background(), server.URL, cache, 24*time.Hour)
	if err != nil || !first.Status.UpdateAvailable || !first.Status.Compatible || calls != 1 {
		t.Fatalf("first check = %#v, calls=%d, error=%v", first, calls, err)
	}
	if _, err = client.CheckCached(context.Background(), server.URL, cache, 24*time.Hour); err != nil || calls != 1 {
		t.Fatalf("cached check calls=%d, error=%v", calls, err)
	}
	if _, notify, err := client.ShouldNotify(context.Background(), server.URL, cache, 24*time.Hour); err != nil || !notify {
		t.Fatalf("first notify=%v, error=%v", notify, err)
	}
	if _, notify, err := client.ShouldNotify(context.Background(), server.URL, cache, 24*time.Hour); err != nil || notify {
		t.Fatalf("repeat notify=%v, error=%v", notify, err)
	}
	now = now.Add(25 * time.Hour)
	if _, notify, err := client.ShouldNotify(context.Background(), server.URL, cache, 24*time.Hour); err != nil || !notify || calls != 2 {
		t.Fatalf("next-day notify=%v calls=%d, error=%v", notify, calls, err)
	}
}

func TestUpdateDownloadsVerifiesAndAtomicallyReplaces(t *testing.T) {
	withBuildVersion(t, "v1.0.0")
	newBinary := []byte("#!/bin/sh\necho updated\n")
	sum := sha256.Sum256(newBinary)
	digest := hex.EncodeToString(sum[:])
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/edc-cli":
			_ = json.NewEncoder(w).Encode(buildinfo.CLIPolicy{LatestVersion: "v1.1.0", MinimumVersion: "v1.0.0", ReleaseAPIURL: server.URL + "/release", ReleasePageURL: server.URL + "/page"})
		case "/release":
			if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
				http.Error(w, "unsupported media type", http.StatusUnsupportedMediaType)
				return
			}
			_ = json.NewEncoder(w).Encode(releaseResponse{TagName: "v1.1.0", HTMLURL: server.URL + "/page", Assets: []releaseAsset{{Name: "edc-darwin-arm64", BrowserDownloadURL: server.URL + "/binary", Digest: "sha256:" + digest, Size: int64(len(newBinary))}}})
		case "/binary":
			_, _ = w.Write(newBinary)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	target := filepath.Join(dir, "edc")
	oldBinary := []byte("old binary")
	if err := os.WriteFile(target, oldBinary, 0755); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTP: server.Client(), Now: func() time.Time { return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) }, OS: "darwin", Arch: "arm64"}

	result, status, err := client.Update(context.Background(), server.URL, target)
	if err != nil || !result.Updated || !status.UpdateAvailable || result.InstalledVersion != "v1.1.0" {
		t.Fatalf("result=%#v status=%#v error=%v", result, status, err)
	}
	if got, readErr := os.ReadFile(target); readErr != nil || string(got) != string(newBinary) {
		t.Fatalf("updated bytes=%q error=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(result.Backup); readErr != nil || string(got) != string(oldBinary) {
		t.Fatalf("backup bytes=%q error=%v", got, readErr)
	}
	if info, statErr := os.Stat(target); statErr != nil || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("updated mode=%v error=%v", info, statErr)
	}
}

func TestUpdateRejectsChecksumMismatchWithoutReplacingTarget(t *testing.T) {
	withBuildVersion(t, "v1.0.0")
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/edc-cli":
			_ = json.NewEncoder(w).Encode(buildinfo.CLIPolicy{LatestVersion: "v1.1.0", MinimumVersion: "v1.0.0", ReleaseAPIURL: server.URL + "/release", ReleasePageURL: server.URL + "/page"})
		case "/release":
			_ = json.NewEncoder(w).Encode(releaseResponse{TagName: "v1.1.0", Assets: []releaseAsset{{Name: "edc-darwin-arm64", BrowserDownloadURL: server.URL + "/binary", Digest: "sha256:" + hex.EncodeToString(make([]byte, 32)), Size: 3}}})
		case "/binary":
			_, _ = w.Write([]byte("new"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "edc")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTP: server.Client(), OS: "darwin", Arch: "arm64"}
	if _, _, err := client.Update(context.Background(), server.URL, target); err == nil {
		t.Fatal("checksum mismatch was accepted")
	}
	if got, _ := os.ReadFile(target); string(got) != "old" {
		t.Fatalf("target changed to %q", got)
	}
}

func TestUpdateFallsBackToChecksumsAsset(t *testing.T) {
	withBuildVersion(t, "v1.0.0")
	newBinary := []byte("released binary")
	sum := sha256.Sum256(newBinary)
	digest := hex.EncodeToString(sum[:])
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/edc-cli":
			_ = json.NewEncoder(w).Encode(buildinfo.CLIPolicy{LatestVersion: "v1.1.0", MinimumVersion: "v1.0.0", ReleaseAPIURL: server.URL + "/release", ReleasePageURL: server.URL + "/page"})
		case "/release":
			_ = json.NewEncoder(w).Encode(releaseResponse{TagName: "v1.1.0", Assets: []releaseAsset{
				{Name: "edc-darwin-arm64", BrowserDownloadURL: server.URL + "/binary", Size: int64(len(newBinary))},
				{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/checksums"},
			}})
		case "/binary":
			_, _ = w.Write(newBinary)
		case "/checksums":
			_, _ = w.Write([]byte(digest + "  edc-darwin-arm64\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "edc")
	if err := os.WriteFile(target, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	client := &Client{HTTP: server.Client(), OS: "darwin", Arch: "arm64"}
	result, _, err := client.Update(context.Background(), server.URL, target)
	if err != nil || !result.Updated {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestCheckTreatsMissingPolicyAsOlderServer(t *testing.T) {
	server := httptest.NewTLSServer(http.NotFoundHandler())
	defer server.Close()
	client := &Client{HTTP: server.Client()}
	snapshot, err := client.Check(context.Background(), server.URL)
	if err != nil || snapshot.Status.PolicyAvailable || !snapshot.Status.Compatible {
		t.Fatalf("snapshot=%#v error=%v", snapshot, err)
	}
}

func TestCheckRejectsUntrustedReleaseEndpoint(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(buildinfo.CLIPolicy{
			LatestVersion: "v1.1.0", MinimumVersion: "v1.0.0",
			ReleaseAPIURL: "https://updates.example/release", ReleasePageURL: buildinfo.ReleasePageURL,
		})
	}))
	defer server.Close()
	client := &Client{HTTP: server.Client()}
	if _, err := client.Check(context.Background(), server.URL); err == nil {
		t.Fatal("untrusted release API endpoint was accepted")
	}
}

func TestSemverComparison(t *testing.T) {
	for _, test := range []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"v1.0.0", "v1.1.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.0.0-rc.1", "v1.0.0", -1},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", -1},
	} {
		if got := compareSemver(test.a, test.b); got != test.want {
			t.Errorf("compareSemver(%q, %q)=%d, want %d", test.a, test.b, got, test.want)
		}
	}
}

func withBuildVersion(t *testing.T, version string) {
	t.Helper()
	previous := buildinfo.Version
	buildinfo.Version = version
	t.Cleanup(func() { buildinfo.Version = previous })
}

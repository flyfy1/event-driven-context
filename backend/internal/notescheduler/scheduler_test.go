package notescheduler

import (
	"context"
	"encoding/json"
	"errors"
	"event-driven-context/internal/core"
	"event-driven-context/internal/noteindexer"
	"event-driven-context/internal/v2"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoveryIsolationPauseAndFailure(t *testing.T) {
	root := t.TempDir()
	store, err := core.Open(filepath.Join(root, "identity.db"), filepath.Join(root, "old"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	user, err := store.Register(context.Background(), core.Credentials{Username: "scheduler", Email: "scheduler@example.invalid", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	owner := core.WithUser(context.Background(), user.ID)
	first, err := store.CreateProject(owner, core.ProjectInput{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := v2.New(store, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	seen := make(chan string, 20)
	execute := func(ctx context.Context, c noteindexer.Client, o noteindexer.Options) (noteindexer.Result, error) {
		if _, err := c.GetEvent(ctx, "wrong-project", "anything"); err == nil {
			t.Error("cross-project request accepted")
		}
		// Existing organizer protocol works through the internal adapter, including empty projects.
		if _, err := c.BeginNotesOrganization(ctx, o.ProjectID, noteindexer.PromptVersion); err != nil {
			t.Errorf("begin: %v", err)
		}
		seen <- o.ProjectID
		return noteindexer.Result{}, errors.New("simulated model failure")
	}
	go func() { defer close(done); runLoop(ctx, store, service, "unused", 10*time.Millisecond, execute) }()
	select {
	case id := <-seen:
		if id != first.ID {
			t.Fatal(id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("existing project not discovered")
	}
	second, err := store.CreateProject(owner, core.ProjectInput{Name: "created after startup"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-seen:
		if id != second.ID {
			t.Fatalf("failure retried without backoff: %s", id)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("new project starved by failed project")
	}
	cancel()
	<-done
	var m v2.Manifest
	if err = json.Unmarshal(manifestJSON, &m); err != nil {
		t.Fatal(err)
	}
	installations, err := service.ListPlugins(owner, first.ID)
	if err != nil || len(installations) != 1 {
		t.Fatalf("installations: %v %v", installations, err)
	}
	if _, err = service.SetPluginStatus(owner, first.ID, m.ID, "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.RemovePlugin(owner, second.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel2()
	runLoop(ctx2, store, service, "unused", 10*time.Millisecond, func(context.Context, noteindexer.Client, noteindexer.Options) (noteindexer.Result, error) {
		t.Error("paused/removed project ran")
		return noteindexer.Result{}, nil
	})
}

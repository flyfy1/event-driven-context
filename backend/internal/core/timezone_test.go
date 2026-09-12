package core

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func TestProjectTimezoneDefaultsUpdatesAndLists(t *testing.T) {
	store := openTest(t)
	owner := user(t, store, "timezone-owner")
	member := user(t, store, "timezone-member")
	project, err := store.CreateProject(owner, ProjectInput{Name: "scheduled review"})
	if err != nil {
		t.Fatal(err)
	}
	if project.Timezone != "UTC" {
		t.Fatalf("default timezone=%q, want UTC", project.Timezone)
	}
	updated, err := store.UpdateProjectTimezone(owner, project.ID, "Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Timezone != "Asia/Singapore" {
		t.Fatalf("updated timezone=%q", updated.Timezone)
	}
	projects, err := store.ListProjects(owner, Empty{})
	if err != nil || len(projects.Projects) != 1 || projects.Projects[0].Timezone != "Asia/Singapore" {
		t.Fatalf("listed projects=%#v err=%v", projects, err)
	}
	if _, err = store.AddMember(owner, MemberInput{ProjectID: project.ID, Username: "timezone-member"}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.UpdateProjectTimezone(member, project.ID, "UTC"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("member update err=%v, want forbidden", err)
	}
	if _, err = store.UpdateProjectTimezone(owner, project.ID, "not/a-real-zone"); err == nil {
		t.Fatal("invalid IANA timezone was accepted")
	}
}

func TestOpenMigratesExistingProjectsToUTC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE projects (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, description TEXT NOT NULL,
		owner_user_id TEXT NOT NULL, created_at TEXT NOT NULL
	)`)
	if err == nil {
		_, err = db.Exec("INSERT INTO projects VALUES(?,?,?,?,?)", "prj_legacy", "legacy", "", "usr_legacy", "2026-01-01T00:00:00Z")
	}
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	timezone, err := store.ProjectTimezone(context.Background(), "prj_legacy")
	if err != nil || timezone != "UTC" {
		t.Fatalf("migrated timezone=%q err=%v", timezone, err)
	}
}

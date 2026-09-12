package core

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAdminIdentitySnapshotAndProjectMembership(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, "identity.db"), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	alice, err := store.Register(context.Background(), Credentials{Username: "alice-admin", Email: "alice@example.test", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	bob, err := store.Register(context.Background(), Credentials{Username: "bob-admin", Email: "bob@example.test", Password: "password-123"})
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(WithUser(context.Background(), alice.ID), ProjectInput{Name: "Admin project"})
	if err != nil {
		t.Fatal(err)
	}

	adminContext := WithUser(context.Background(), alice.ID)
	if err = store.RequireAdmin(adminContext, []string{"ALICE@EXAMPLE.TEST"}); err != nil {
		t.Fatalf("email allowlist: %v", err)
	}
	if err = store.RequireAdmin(WithUser(context.Background(), bob.ID), []string{alice.Username}); errorCode(err) != "forbidden" {
		t.Fatalf("non-admin error = %v", err)
	}
	if _, err = store.SetAdminProjectMember(adminContext, []string{alice.ID}, project.ID, bob.ID, true); err != nil {
		t.Fatalf("grant membership: %v", err)
	}
	snapshot, err := store.AdminIdentitySnapshot(adminContext, []string{alice.Username})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Users) != 2 || len(snapshot.Projects) != 1 || len(snapshot.Projects[0].Members) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Users[0].OwnedProjectCount != 1 || snapshot.Users[0].ProjectCount != 1 || snapshot.Users[1].ProjectCount != 1 {
		t.Fatalf("user counts = %#v", snapshot.Users)
	}
	if snapshot.Projects[0].Owner.ID != alice.ID || !snapshot.Projects[0].Members[0].Owner {
		t.Fatalf("owner summary = %#v", snapshot.Projects[0])
	}
	if _, err = store.SetAdminProjectMember(adminContext, []string{alice.Username}, project.ID, alice.ID, false); errorCode(err) != "invalid_input" {
		t.Fatalf("remove owner error = %v", err)
	}
	if _, err = store.SetAdminProjectMember(adminContext, []string{alice.Username}, project.ID, bob.ID, false); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	members, err := store.ListMembers(adminContext, ProjectRef{ProjectID: project.ID})
	if err != nil || len(members.Members) != 1 || members.Members[0].ID != alice.ID {
		t.Fatalf("members after removal = %#v, %v", members, err)
	}
}

func errorCode(err error) string {
	if typed, ok := err.(*Error); ok {
		return typed.Code
	}
	return ""
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMigrationEmailRequiresPrivateRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "email")
	if err := os.WriteFile(path, []byte(" User@Example.com\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readMigrationEmail(path)
	if err != nil || got != "User@Example.com" {
		t.Fatalf("readMigrationEmail = %q, %v", got, err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = readMigrationEmail(path); err == nil {
		t.Fatal("world-readable migration file accepted")
	}
}

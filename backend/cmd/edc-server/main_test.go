package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerSkillRootFlag(t *testing.T) {
	if mode := os.Getenv("EDC_TEST_SERVER_FLAGS"); mode != "" {
		flag.CommandLine = flag.NewFlagSet("edc-server", flag.ExitOnError)
		os.Args = []string{"edc-server"}
		if mode == "removed" {
			os.Args = append(os.Args, "-skill-root", "unused")
		}
		os.Args = append(os.Args, "-help")
		main()
		return
	}
	for _, mode := range []string{"help", "removed"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestServerSkillRootFlag$")
			cmd.Env = append(os.Environ(), "EDC_TEST_SERVER_FLAGS="+mode)
			output, err := cmd.CombinedOutput()
			if mode == "help" {
				if err != nil || strings.Contains(string(output), "skill-root") || !strings.Contains(string(output), "-addr") {
					t.Fatalf("unexpected server help: %v\n%s", err, output)
				}
			} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 2 || !strings.Contains(string(output), "flag provided but not defined: -skill-root") {
				t.Fatalf("expected removed flag to be rejected: %v\n%s", err, output)
			}
		})
	}
}

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

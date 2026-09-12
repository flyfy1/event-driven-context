package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"event-driven-context/internal/buildinfo"
	"event-driven-context/internal/updater"
	"golang.org/x/term"
)

const updateCheckInterval = 24 * time.Hour

func (a *app) version(args []string) error {
	f := a.flags("version")
	if err := parse(f, args); err != nil {
		return err
	}
	return a.json(buildinfo.Current())
}

func (a *app) update(args []string) error {
	f := a.flags("update")
	checkOnly := f.Bool("check", false, "check for an update without installing it")
	if err := parse(f, args); err != nil {
		return err
	}
	client := a.updateClient()
	ctx, cancel := context.WithTimeout(a.ctx, 2*time.Minute)
	defer cancel()
	if *checkOnly {
		snapshot, err := client.Check(ctx, a.server)
		if err != nil {
			snapshot.Status.UpdateCheckError = err.Error()
		}
		return a.result(snapshot.Status, err)
	}
	result, status, err := client.Update(ctx, a.server, a.executablePath())
	if err != nil {
		status.UpdateCheckError = err.Error()
		if printErr := a.json(struct {
			CLI updater.Status `json:"cli"`
		}{CLI: status}); printErr != nil {
			return printErr
		}
		return err
	}
	return a.json(struct {
		CLI    updater.Status `json:"cli"`
		Result updater.Result `json:"result"`
	}{CLI: status, Result: result})
}

func (a *app) cachedUpdateStatus() updater.Status {
	client := a.updateClient()
	ctx, cancel := context.WithTimeout(a.ctx, 3*time.Second)
	defer cancel()
	snapshot, err := client.CheckCached(ctx, a.server, a.updateCachePath(), updateCheckInterval)
	if err != nil {
		snapshot.Status.CLIVersion = buildinfo.Version
		snapshot.Status.CLICommit = buildinfo.Commit
		snapshot.Status.Compatible = true
		snapshot.Status.UpdateCheckError = err.Error()
	}
	return snapshot.Status
}

func (a *app) maybeNotifyUpdate(command string) {
	if a.updates == nil || !interactiveWriter(a.io.err) || updateCheckDisabled() {
		return
	}
	switch command {
	case "hook", "mcp", "host", "version", "update", "status":
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status, notify, err := a.updates.ShouldNotify(ctx, a.server, a.updateCachePath(), updateCheckInterval)
	if err != nil || !notify {
		return
	}
	if !status.Compatible {
		_, _ = fmt.Fprintf(a.io.err, "edc: CLI %s is older than this server's minimum %s; run 'edc update'\n", status.CLIVersion, status.MinimumVersion)
		return
	}
	_, _ = fmt.Fprintf(a.io.err, "edc: update available %s -> %s; run 'edc update'\n", status.CLIVersion, status.LatestVersion)
}

func (a *app) updateClient() *updater.Client {
	if a.updates != nil {
		return a.updates
	}
	return updater.New()
}

func (a *app) executablePath() string {
	if a.executable != "" {
		return a.executable
	}
	path, _ := os.Executable()
	return path
}

func (a *app) updateCachePath() string {
	configPath, err := filepath.Abs(a.configPath)
	if err != nil || strings.TrimSpace(a.configPath) == "" {
		return filepath.Join(os.TempDir(), "edc-update-check.json")
	}
	return filepath.Join(filepath.Dir(configPath), "update-check.json")
}

func interactiveWriter(writer any) bool {
	file, ok := writer.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func updateCheckDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("EDC_UPDATE_CHECK"))) {
	case "0", "false", "off", "no":
		return true
	default:
		return false
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"event-driven-context/internal/capture"
)

const hookTimeout = 4 * time.Second

func (a *app) captureManager() (*capture.Manager, error) {
	configPath, err := filepath.Abs(a.configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	return capture.Open(filepath.Join(filepath.Dir(configPath), "capture"), a.client)
}

func (a *app) accountID(ctx context.Context) (string, error) {
	if a.config.Server == a.server && a.config.Token == a.token && a.config.AccountID != "" {
		return a.config.AccountID, nil
	}
	user, err := a.client.Me(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve authenticated account: %w", err)
	}
	return user.ID, nil
}

func (a *app) currentBinding(ctx context.Context) (*capture.Manager, capture.Binding, error) {
	manager, err := a.captureManager()
	if err != nil {
		return nil, capture.Binding{}, err
	}
	accountID, err := a.accountID(ctx)
	if err != nil {
		return nil, capture.Binding{}, err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, capture.Binding{}, err
	}
	binding, err := manager.CurrentLink(cwd, a.server, accountID)
	return manager, binding, err
}

func (a *app) requiredProject(projectID *string) error {
	if strings.TrimSpace(*projectID) != "" {
		*projectID = strings.TrimSpace(*projectID)
		return nil
	}
	_, binding, err := a.currentBinding(a.ctx)
	if err != nil {
		return fmt.Errorf("--project omitted and current directory has no binding: %w", err)
	}
	*projectID = binding.ProjectID
	return nil
}

func (a *app) link(args []string) error {
	f := a.flags("link")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() > 1 {
		return fmt.Errorf("link accepts at most one PROJECT_ID")
	}
	manager, err := a.captureManager()
	if err != nil {
		return err
	}
	accountID, err := a.accountID(a.ctx)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if f.NArg() == 0 {
		binding, getErr := manager.CurrentLink(cwd, a.server, accountID)
		return a.result(binding, getErr)
	}
	projectID := strings.TrimSpace(f.Arg(0))
	projects, err := a.client.ListProjects(a.ctx)
	if err != nil {
		return err
	}
	found := false
	for _, project := range projects.Projects {
		if project.ID == projectID {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("project is not accessible: %s", projectID)
	}
	binding, err := manager.Link(a.ctx, cwd, projectID, a.server, accountID)
	return a.result(binding, err)
}

func (a *app) status(args []string) error {
	f := a.flags("status")
	if err := parse(f, args); err != nil {
		return err
	}
	manager, binding, err := a.currentBinding(a.ctx)
	if err != nil {
		return err
	}
	status, err := manager.Status(binding.Directory, a.server, binding.AccountID)
	return a.result(status, err)
}

func (a *app) hook(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("hook requires one CLIENT")
	}
	ctx, cancel := context.WithTimeout(a.ctx, hookTimeout)
	defer cancel()
	manager, err := a.captureManager()
	if err != nil {
		return err
	}
	accountID, err := a.accountID(ctx)
	if err != nil {
		return err
	}
	_, err = manager.HandleHook(ctx, args[0], a.server, accountID, a.io.in, a.io.out)
	return err
}

func (a *app) setup(args []string) error {
	f := a.flags("setup")
	apply := f.Bool("apply", false, "apply the displayed setup changes")
	disableHooks := f.Bool("disable-hooks", false, "remove automatic log hooks")
	enableShared := f.Bool("enable-shared-hooks", false, "confirm automatic logs are visible to all project members")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("setup requires one CLIENT")
	}
	manager, binding, err := a.currentBinding(a.ctx)
	if err != nil {
		return fmt.Errorf("setup requires a linked directory: %w", err)
	}
	edcPath, err := os.Executable()
	if err != nil {
		return err
	}
	configPath, err := filepath.Abs(a.configPath)
	if err != nil {
		return err
	}
	preview, err := manager.SetupPreview(capture.SetupOptions{Directory: binding.Directory, Client: f.Arg(0), EDCPath: edcPath, ConfigPath: configPath, Server: binding.Server, AccountID: binding.AccountID, DisableHooks: *disableHooks})
	if err != nil {
		return err
	}
	members, err := a.client.ListMembers(a.ctx, binding.ProjectID)
	if err != nil {
		return err
	}
	sharedHooksNeedApproval := len(members.Members) > 1 && !*disableHooks
	if sharedHooksNeedApproval {
		preview.Warnings = append(preview.Warnings, "automatic logs in this shared project are visible to every project member; apply requires --enable-shared-hooks")
	}
	if !*apply {
		return a.json(struct {
			Preview capture.SetupPreview `json:"preview"`
			Applied bool                 `json:"applied"`
		}{Preview: preview})
	}
	if sharedHooksNeedApproval && !*enableShared {
		return fmt.Errorf("shared-project hooks require --enable-shared-hooks after reviewing the setup preview")
	}
	// The reviewed bytes are emitted before mutation. --apply is the user's
	// explicit approval for this exact preview.
	if err = json.NewEncoder(a.io.err).Encode(struct {
		Preview capture.SetupPreview `json:"preview"`
	}{Preview: preview}); err != nil {
		return err
	}
	if err = manager.ApplySetup(preview, true); err != nil {
		return err
	}
	return a.json(map[string]any{"applied": true, "changes": len(preview.Changes)})
}

func (a *app) outbox(args []string) error {
	command := "list"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}
	f := a.flags("outbox " + command)
	if err := parse(f, args); err != nil {
		return err
	}
	manager, binding, err := a.currentBinding(a.ctx)
	if err != nil {
		return err
	}
	switch command {
	case "list":
		items, listErr := manager.OutboxList(binding)
		return a.result(struct {
			Items []capture.OutboxItem `json:"items"`
		}{Items: items}, listErr)
	case "flush":
		result, flushErr := manager.Flush(a.ctx, binding)
		if flushErr != nil {
			if printErr := a.json(result); printErr != nil {
				return errors.Join(flushErr, printErr)
			}
			return flushErr
		}
		return a.json(result)
	default:
		return fmt.Errorf("outbox requires list or flush")
	}
}

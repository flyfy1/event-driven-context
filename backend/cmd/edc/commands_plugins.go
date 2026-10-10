package main

import (
	"encoding/json"
	"fmt"

	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/google/uuid"
)

func (a *app) plugin(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("plugin requires install, list, config, pause, resume, rerun or remove")
	}
	command := args[0]
	f := a.flags("plugin " + command)
	projectID := f.String("project", "", "project ID")
	switch command {
	case "list":
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		out, err := a.client.ListPlugins(a.ctx, *projectID)
		return a.result(struct {
			Plugins []v2.Installation `json:"plugins"`
		}{out}, err)
	case "install":
		manifestPath := f.String("manifest", "", "manifest JSON path or -")
		configRaw := f.String("config", "", "optional config JSON")
		tokenFile := f.String("token-file", "", "new private file for the one-time plugin token")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *manifestPath == "" {
			return fmt.Errorf("--manifest is required")
		}
		raw, err := readPathOrStdin(a.io.in, *manifestPath, 1<<20)
		if err != nil {
			return err
		}
		var manifest v2.Manifest
		if err = json.Unmarshal(raw, &manifest); err != nil {
			return fmt.Errorf("manifest must be JSON: %w", err)
		}
		var config json.RawMessage
		if *configRaw != "" {
			if !json.Valid([]byte(*configRaw)) {
				return fmt.Errorf("--config must be JSON")
			}
			config = json.RawMessage(*configRaw)
		}
		out, err := a.client.InstallPlugin(a.ctx, *projectID, v2.InstallPluginInput{Manifest: manifest, Config: config})
		if err != nil {
			return err
		}
		if *tokenFile != "" {
			if err = writePrivateNewFile(*tokenFile, []byte(out.Token+"\n")); err != nil {
				return fmt.Errorf("save plugin token: %w", err)
			}
		}
		return a.json(struct {
			Installation  v2.Installation `json:"installation"`
			TokenReturned bool            `json:"token_returned"`
			TokenFile     string          `json:"token_file,omitempty"`
		}{out.Installation, out.Token != "", *tokenFile})
	case "config":
		pluginID := f.String("plugin", "", "plugin id")
		revision := f.Int64("expected-revision", 0, "expected config revision")
		configRaw := f.String("config", "", "config JSON")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" || *revision < 1 || *configRaw == "" {
			return fmt.Errorf("--plugin, positive --expected-revision and --config are required")
		}
		if !json.Valid([]byte(*configRaw)) {
			return fmt.Errorf("--config must be JSON")
		}
		out, err := a.client.PatchPlugin(a.ctx, *projectID, *pluginID, v2client.PatchPluginInput{Action: "config", ExpectedRevision: revision, Config: json.RawMessage(*configRaw)})
		return a.result(out, err)
	case "pause", "resume":
		pluginID := f.String("plugin", "", "plugin id")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" {
			return fmt.Errorf("--plugin is required")
		}
		out, err := a.client.PatchPlugin(a.ctx, *projectID, *pluginID, v2client.PatchPluginInput{Action: command})
		return a.result(out, err)
	case "rerun":
		pluginID := f.String("plugin", "", "plugin id")
		requestID := f.String("request-id", "", "stable request UUID")
		var sources stringList
		f.Var(&sources, "source-event", "source event UUID, repeatable")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" {
			return fmt.Errorf("--plugin is required")
		}
		if *requestID == "" {
			id, e := uuid.NewV7()
			if e != nil {
				return e
			}
			*requestID = id.String()
		}
		if _, e := uuid.Parse(*requestID); e != nil {
			return fmt.Errorf("--request-id must be UUID")
		}
		out, err := a.client.RequestManualRun(a.ctx, *projectID, *pluginID, v2.ManualRunInput{RequestID: *requestID, SourceEventIDs: sources})
		return a.result(out, err)
	case "remove":
		pluginID := f.String("plugin", "", "plugin id")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if *pluginID == "" {
			return fmt.Errorf("--plugin is required")
		}
		out, err := a.client.RemovePlugin(a.ctx, *projectID, *pluginID)
		return a.result(out, err)
	default:
		return fmt.Errorf("unknown plugin command %q", command)
	}
}

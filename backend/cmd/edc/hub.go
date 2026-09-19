package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"event-driven-context/internal/core"
	"event-driven-context/internal/v2client"
)

const hubHelp = `Agent Hub commands (structured JSON output):
  capabilities                         Discover providers, operations and schemas
  agent connect --owner USERNAME --name NAME --output NEW_CONFIG
  agent status                         Inspect pairing / revocation / expiry
  source list                          Discover owner's accounts after pairing
  source integrations                  Discover deployment/account/API availability
  source integrations --owner          Inspect owner integration registry
  access request --connection ID --operation OP --reason TEXT [--duration 1h] [--constraints JSON]
  access list                          Inspect pending and effective grants
  api call --connection ID --operation OP [--args '{"key":"value"}']

Owner-only setup (use the owner's normal configuration):
  source add --provider ID --account ACCOUNT --name NAME [--credential-stdin]
  source import --provider ID --account ACCOUNT --name NAME --format FORMAT --file PATH
  source disconnect CONNECTION_ID

Use --config NEW_CONFIG before the command when acting as the agent.
Agent connect creates a new 0600 file and never prints its token. Ask the owner
 to compare the verification code and approve in the web/mobile My authorizations
 page. Pairing enables account discovery only; each API operation needs a grant.
Approval and credential management require the owner's session. Provider secrets
must be entered privately, never placed in command arguments or chat.
`

func (a *app) hubJSON(method, path string, in any) error {
	out, err := a.client.HubJSON(a.ctx, method, path, in)
	if err != nil {
		var apiErr *v2client.APIError
		if errors.As(err, &apiErr) {
			payload := map[string]any{"error": map[string]any{"code": apiErr.Code, "message": apiErr.Message}}
			if apiErr.Code == "authorization_required" || apiErr.Code == "connection_required" {
				payload["next_action"] = "Ask the owner to review My authorizations"
				payload["approval_path"] = "/hub.html"
			}
			_ = a.json(payload)
		}
		return err
	}
	return a.json(out)
}
func (a *app) hub(command string, args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "help") {
		_, err := fmt.Fprint(a.io.out, hubHelp)
		return err
	}
	switch command {
	case "capabilities":
		if len(args) != 0 {
			return fmt.Errorf("capabilities takes no arguments")
		}
		return a.hubJSON("GET", "/v1/hub/capabilities", nil)
	case "agent":
		if len(args) == 0 {
			return fmt.Errorf("agent requires connect or status")
		}
		if args[0] == "status" && len(args) == 1 {
			return a.hubJSON("GET", "/v1/hub/agent", nil)
		}
		if args[0] != "connect" {
			return fmt.Errorf("unknown agent command")
		}
		f := flag.NewFlagSet("agent connect", flag.ContinueOnError)
		f.SetOutput(a.io.err)
		owner := f.String("owner", "", "owner's Hub username")
		name := f.String("name", "", "agent display name")
		output := f.String("output", "", "new private config file")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || *owner == "" || *name == "" || *output == "" {
			return fmt.Errorf("owner, name and output are required")
		}
		path, err := filepath.Abs(*output)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create a new agent config: %w", err)
		}
		saved := false
		defer func() {
			_ = file.Close()
			if !saved {
				_ = os.Remove(path)
			}
		}()
		raw, err := a.client.HubJSON(a.ctx, "POST", "/v1/hub/agents", map[string]string{"owner": *owner, "name": *name})
		if err != nil {
			return err
		}
		var registration struct {
			core.HubAgentRegistration
			ApprovalURL string `json:"approval_url"`
		}
		if err = json.Unmarshal(raw, &registration); err != nil {
			return err
		}
		if registration.Token == "" {
			return fmt.Errorf("server did not return an agent token")
		}
		cfg := config{Server: a.server, Token: registration.Token}
		if err = json.NewEncoder(file).Encode(cfg); err != nil {
			return err
		}
		if err = file.Sync(); err != nil {
			return err
		}
		if err = file.Close(); err != nil {
			return err
		}
		saved = true
		return a.json(map[string]any{"approval_url": registration.ApprovalURL, "agent": registration.HubAgent, "config": path, "next_action": "Ask the owner to compare verification_code and approve this agent in My authorizations", "approval_path": "/hub.html"})
	case "source":
		if len(args) == 0 {
			return fmt.Errorf("source requires list, add or disconnect")
		}
		switch args[0] {
		case "integrations":
			if len(args) == 1 {
				return a.hubJSON("GET", "/v1/hub/integrations-agent", nil)
			}
			if len(args) == 2 && args[1] == "--owner" {
				return a.hubJSON("GET", "/v1/hub/integrations", nil)
			}
			return fmt.Errorf("source integrations accepts only --owner")
		case "list":
			if len(args) != 1 {
				return fmt.Errorf("source list takes no arguments")
			}
			return a.hubJSON("GET", "/v1/hub/sources", nil)
		case "import":
			f := flag.NewFlagSet("source import", flag.ContinueOnError)
			f.SetOutput(a.io.err)
			provider := f.String("provider", "", "import provider ID")
			account := f.String("account", "", "owner-declared source account")
			name := f.String("name", "", "connection display name")
			format := f.String("format", "", "whatsapp-text, ics or markdown")
			path := f.String("file", "", "owner-selected UTF-8 file")
			if err := f.Parse(args[1:]); err != nil {
				return err
			}
			if f.NArg() != 0 || *path == "" || *provider == "" || *account == "" || *name == "" || *format == "" {
				return fmt.Errorf("provider, account, name, format and file are required")
			}
			file, err := os.Open(*path)
			if err != nil {
				return fmt.Errorf("could not open import file")
			}
			defer file.Close()
			data, err := io.ReadAll(io.LimitReader(file, core.HubImportMaxBytes+1))
			if err != nil || len(data) > core.HubImportMaxBytes {
				return fmt.Errorf("import file must be readable and at most 1 MiB")
			}
			return a.hubJSON("POST", "/v1/hub/imports", map[string]string{"provider_id": *provider, "account_id": *account, "display_name": *name, "format": *format, "filename": filepath.Base(*path), "content": string(data)})
		case "add":
			f := flag.NewFlagSet("source add", flag.ContinueOnError)
			f.SetOutput(a.io.err)
			provider := f.String("provider", "", "provider ID")
			account := f.String("account", "", "account identity")
			name := f.String("name", "", "connection display name")
			stdin := f.Bool("credential-stdin", false, "read provider credential privately from standard input")
			if err := f.Parse(args[1:]); err != nil {
				return err
			}
			if f.NArg() != 0 || *provider == "" || *account == "" || *name == "" {
				return fmt.Errorf("provider, account and name required")
			}
			secret := ""
			if *stdin {
				data, err := io.ReadAll(io.LimitReader(a.io.in, 16385))
				if err != nil {
					return fmt.Errorf("could not read credential")
				}
				if len(data) > 16384 {
					return fmt.Errorf("credential too large")
				}
				secret = strings.TrimSpace(string(data))
				if secret == "" {
					return fmt.Errorf("credential is empty")
				}
			}
			return a.hubJSON("POST", "/v1/hub/connections", map[string]string{"provider_id": *provider, "account_id": *account, "display_name": *name, "credential": secret})
		case "disconnect":
			if len(args) != 2 || !hubID(args[1]) {
				return fmt.Errorf("source disconnect requires a connection ID")
			}
			return a.hubJSON("POST", "/v1/hub/connections/"+args[1]+"/disconnect", struct{}{})
		}
	case "access":
		if len(args) == 0 {
			return fmt.Errorf("access requires request or list")
		}
		if args[0] == "list" && len(args) == 1 {
			return a.hubJSON("GET", "/v1/hub/requests", nil)
		}
		if args[0] != "request" {
			return fmt.Errorf("unknown access command")
		}
		f := flag.NewFlagSet("access request", flag.ContinueOnError)
		f.SetOutput(a.io.err)
		connection := f.String("connection", "", "connection ID")
		operation := f.String("operation", "", "operation ID")
		reason := f.String("reason", "", "explain the task to the owner")
		constraints := f.String("constraints", "", "calendar authorization boundaries as JSON")
		duration := f.Duration("duration", time.Hour, "grant duration, 1m to 168h")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || !hubID(*connection) || *operation == "" || *reason == "" || *duration < time.Minute || *duration > 168*time.Hour {
			return fmt.Errorf("connection, operation, reason and duration (1m to 168h) required")
		}
		var bounds map[string]any
		if *constraints != "" && (len(*constraints) > 4096 || json.Unmarshal([]byte(*constraints), &bounds) != nil || bounds == nil) {
			return fmt.Errorf("constraints must be a JSON object, max 4096 bytes")
		}
		return a.hubJSON("POST", "/v1/hub/requests", map[string]any{"constraints": bounds, "connection_id": *connection, "operation": *operation, "reason": *reason, "duration_seconds": int64(duration.Seconds())})
	case "api":
		if len(args) == 0 || args[0] != "call" {
			return fmt.Errorf("api requires call")
		}
		f := flag.NewFlagSet("api call", flag.ContinueOnError)
		f.SetOutput(a.io.err)
		connection := f.String("connection", "", "connection ID")
		operation := f.String("operation", "", "operation ID")
		raw := f.String("args", "{}", "operation JSON arguments")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if f.NArg() != 0 || !hubID(*connection) || *operation == "" {
			return fmt.Errorf("connection and operation required")
		}
		var params map[string]any
		if len(*raw) > 1<<20 || json.Unmarshal([]byte(*raw), &params) != nil || params == nil {
			return fmt.Errorf("args must be a JSON object, max 1 MiB")
		}
		return a.hubJSON("POST", "/v1/hub/execute", map[string]any{"connection_id": *connection, "operation": *operation, "args": params})
	}
	return fmt.Errorf("unknown Hub command; use edc capabilities --help")
}
func hubID(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

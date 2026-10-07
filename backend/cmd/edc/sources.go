package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
)

func (a *app) sourceDiscover(command string, args []string) error {
	f := flag.NewFlagSet("source "+command, flag.ContinueOnError)
	f.SetOutput(a.io.err)
	provider := f.String("provider", "", "provider ID")
	connection := f.String("connection", "", "connected account ID")
	owner := f.Bool("owner", false, "use owner's authenticated registry")
	available := f.Bool("available-only", false, "show implemented adapters only, not connected accounts")
	name := f.String("name", "", "display name to prefill in browser onboarding")
	open := f.Bool("open", false, "open the connection page in the default browser")
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected source arguments")
	}
	if command == "list" {
		if *provider != "" || *connection != "" || *open || *available || *name != "" {
			return fmt.Errorf("source list accepts only --owner")
		}
		raw, e := a.client.HubJSON(a.ctx, "GET", "/v1/hub/owner", nil)
		if e != nil {
			return e
		}
		var overview struct {
			Connections []json.RawMessage `json:"connections"`
		}
		if e = json.Unmarshal(raw, &overview); e != nil {
			return e
		}
		return a.json(map[string]any{"connections": overview.Connections})
	}
	if *connection != "" {
		if command != "operations" || *provider != "" || *available || *open || *name != "" || !hubID(*connection) {
			return fmt.Errorf("source operations requires exactly one of --provider or --connection")
		}
		path := "/v1/hub/integrations-agent"
		if *owner {
			path = "/v1/hub/integrations"
		}
		raw, e := a.client.HubJSON(a.ctx, "GET", path, nil)
		if e != nil {
			return e
		}
		var registry struct {
			Providers []struct {
				Connections []struct {
					ID         string            `json:"id"`
					Operations []json.RawMessage `json:"operations"`
				} `json:"connections"`
			} `json:"providers"`
		}
		if e = json.Unmarshal(raw, &registry); e != nil {
			return e
		}
		for _, p := range registry.Providers {
			for _, c := range p.Connections {
				if c.ID == *connection {
					return a.json(map[string]any{"connection_id": c.ID, "operations": c.Operations, "mode": "connection", "notice": "Account operations and grants are returned; provider token validity is checked only on reads."})
				}
			}
		}
		return fmt.Errorf("connection is not currently available in this registry")
	}
	if *owner || *open && command != "connect" || *name != "" && command != "connect" || *available && command != "catalog" {
		return fmt.Errorf("invalid flags for source %s", command)
	}
	if command != "catalog" && *provider == "" {
		return fmt.Errorf("--provider is required")
	}
	raw, e := a.client.HubJSON(a.ctx, "GET", "/v1/hub/capabilities", nil)
	if e != nil {
		return e
	}
	var catalog struct {
		Providers     []map[string]any `json:"providers"`
		Authorization struct {
			Page string `json:"owner_approval_page"`
		} `json:"authorization"`
		Storage bool `json:"credential_storage_configured"`
	}
	if e = json.Unmarshal(raw, &catalog); e != nil {
		return e
	}
	selected := []map[string]any{}
	for _, p := range catalog.Providers {
		if *provider != "" && p["id"] != *provider {
			continue
		}
		if *available && p["implementation_status"] != "adapter_available" {
			continue
		}
		selected = append(selected, p)
	}
	if *provider != "" && len(selected) == 0 {
		return fmt.Errorf("unknown or unavailable provider %q", *provider)
	}
	switch command {
	case "catalog":
		return a.json(map[string]any{"providers": selected, "credential_storage_configured": catalog.Storage, "notice": "Adapter support is separate from deployment setup, connected accounts and operation grants."})
	case "operations":
		return a.json(map[string]any{"provider_id": *provider, "operations": selected[0]["operations"], "mode": "capability", "notice": "These are API schemas, not proof of account access. Use --connection to inspect effective grants."})
	case "connect":
		if selected[0]["implementation_status"] != "adapter_available" {
			return fmt.Errorf("provider adapter is not implemented")
		}
		reg, e := a.client.HubJSON(a.ctx, "GET", "/v1/hub/integrations", nil)
		if e != nil {
			return e
		}
		var registry struct {
			Providers []struct {
				Provider struct {
					ID string `json:"id"`
				} `json:"provider"`
				Connectable bool   `json:"connectable"`
				Onboarding  string `json:"onboarding_method"`
				Reason      string `json:"hidden_reason"`
			} `json:"providers"`
		}
		if e = json.Unmarshal(reg, &registry); e != nil {
			return e
		}
		for _, row := range registry.Providers {
			if row.Provider.ID != *provider {
				continue
			}
			if !row.Connectable {
				return a.json(map[string]any{"provider_id": *provider, "connected": false, "blocked_reason": row.Reason, "requirements": selected[0]["requirements"], "next_action": "Configure this deployment's credential storage and provider application first."})
			}
			if selected[0]["auth_mode"] == "owner_file_import" {
				if *open {
					return fmt.Errorf("this source uses an owner-selected file import")
				}
				return a.json(map[string]any{"provider_id": *provider, "connected": false, "onboarding_method": "owner_file_import", "requirements": selected[0]["requirements"], "next_action": "Use edc source import --provider " + *provider + " --account ACCOUNT --name NAME --format FORMAT --file PATH; consult requirements for formats."})
			}
			if row.Onboarding != "browser_oauth" {
				if *open {
					return fmt.Errorf("this provider needs owner CLI credential setup, not browser OAuth")
				}
				return a.json(map[string]any{"provider_id": *provider, "connected": false, "onboarding_method": row.Onboarding, "requirements": selected[0]["requirements"], "documentation_url": selected[0]["documentation_url"], "next_action": "Use edc source add --provider " + *provider + " --account ACCOUNT --name NAME --credential-stdin, supplying the provider credential privately through stdin."})
			}
			base, e := url.Parse(a.server)
			if e != nil {
				return e
			}
			target, e := url.Parse(catalog.Authorization.Page)
			if e != nil {
				return e
			}
			target = base.ResolveReference(target)
			if target.Scheme != "https" && !(target.Scheme == "http" && (target.Hostname() == "127.0.0.1" || target.Hostname() == "localhost")) {
				return fmt.Errorf("invalid onboarding URL")
			}
			q := target.Query()
			q.Set("provider", *provider)
			q.Set("api", a.server)
			if *name != "" {
				q.Set("name", *name)
			}
			target.RawQuery = q.Encode()
			if *open {
				var cmd *exec.Cmd
				switch runtime.GOOS {
				case "darwin":
					cmd = exec.CommandContext(a.ctx, "open", target.String())
				case "windows":
					cmd = exec.CommandContext(a.ctx, "rundll32", "url.dll,FileProtocolHandler", target.String())
				default:
					cmd = exec.CommandContext(a.ctx, "xdg-open", target.String())
				}
				if e := cmd.Run(); e != nil {
					return fmt.Errorf("could not open browser; use source connect without --open to obtain the URL")
				}
			}
			return a.json(map[string]any{"provider_id": *provider, "connected": false, "onboarding_method": "browser_oauth", "onboarding_url": target.String(), "next_action": "Sign in as the owner in this browser page, then review and complete provider OAuth. No account is connected until the callback succeeds."})
		}
		return fmt.Errorf("provider absent from owner registry")
	}
	return fmt.Errorf("unknown source discovery command")
}

// A read uses the same per-account, per-operation authorization as api call.
func (a *app) sourceRead(args []string) error { return a.hub("api", append([]string{"call"}, args...)) }

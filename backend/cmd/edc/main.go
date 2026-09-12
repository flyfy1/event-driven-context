package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"event-driven-context/internal/core"
	"event-driven-context/internal/processorhost"
	"event-driven-context/internal/v2client"
	"golang.org/x/term"
)

const help = `edc — append-only project context

Usage: edc [--server URL] [--config PATH] COMMAND

Commands:
  register --username NAME --email ADDRESS | login | logout | whoami
  project create | list | members | add-member
  link [PROJECT_ID] | status
  push [TEXT] | --file PATH | --json | --jsonl
  query | get EVENT_ID | metadata
  file get [--cache DIR] FILE_ID
  state list | get | put
  notes sync --project ID --output DIR
  sync --project ID --output DIR [--files all]
  plugin install | list | config | pause | resume | rerun | remove
  pull --after SEQUENCE
  hook CLIENT | setup CLIENT [--apply]
  outbox [list|flush]
  host run --plugin ID --plugin-dir PATH --plugin-token-file PATH (--once | --watch)
  mcp

Global flags precede COMMAND. Commands use the current directory binding when
--project is omitted. Passwords are prompted without echo; --password-stdin
reads one password from stdin. Login writes a private config. Setup previews
changes and writes only with --apply. EDC_SERVER, EDC_CONFIG and EDC_TOKEN
override defaults. Structured output is JSON.
`

type config struct {
	Server    string `json:"server"`
	Token     string `json:"token"`
	AccountID string `json:"account_id,omitempty"`
}

type streams struct {
	in       io.Reader
	out, err io.Writer
}

type app struct {
	ctx           context.Context
	io            streams
	client        *v2client.Client
	configPath    string
	config        config
	server, token string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], streams{os.Stdin, os.Stdout, os.Stderr}); err != nil {
		fmt.Fprintln(os.Stderr, "edc:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, ioStreams streams) error {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	g := flag.NewFlagSet("edc", flag.ContinueOnError)
	g.SetOutput(ioStreams.err)
	server := g.String("server", os.Getenv("EDC_SERVER"), "server origin URL")
	configPath := g.String("config", envDefault("EDC_CONFIG", filepath.Join(baseDir, "event-driven-context", "config.json")), "private config path")
	g.Usage = func() { fmt.Fprint(ioStreams.err, help) }
	if err = g.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = g.Args()
	if len(args) == 0 || args[0] == "help" {
		_, err = fmt.Fprint(ioStreams.out, help)
		return err
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	if *server == "" {
		*server = cfg.Server
	}
	if *server == "" {
		*server = "http://127.0.0.1:8080"
	}
	*server = strings.TrimRight(*server, "/")
	token := os.Getenv("EDC_TOKEN")
	if token == "" && cfg.Server == *server {
		token = cfg.Token
	}
	c, err := v2client.New(*server, token)
	if err != nil {
		return err
	}
	a := &app{ctx: ctx, io: ioStreams, client: c, configPath: *configPath, config: cfg, server: *server, token: token}
	return a.dispatch(args[0], args[1:])
}

func (a *app) dispatch(command string, args []string) error {
	if command == "register" || command == "login" {
		return a.auth(command, args)
	}
	if a.token == "" && command != "host" {
		return fmt.Errorf("login first or set EDC_TOKEN")
	}
	switch command {
	case "whoami":
		if len(args) != 0 {
			return fmt.Errorf("whoami takes no arguments")
		}
		out, err := a.client.Me(a.ctx)
		return a.result(out, err)
	case "logout":
		if len(args) != 0 {
			return fmt.Errorf("logout takes no arguments")
		}
		if err := a.client.Logout(a.ctx); err != nil {
			return err
		}
		if a.config.Server == a.server && a.config.Token == a.token {
			updated := a.config
			updated.Token, updated.AccountID = "", ""
			if err := saveConfig(a.configPath, updated); err != nil {
				return err
			}
		}
		return a.json(map[string]bool{"logged_out": true})
	case "project":
		return a.project(args)
	case "link":
		return a.link(args)
	case "status":
		return a.status(args)
	case "push":
		return a.push(args)
	case "query":
		return a.query(args)
	case "get":
		return a.get(args)
	case "metadata":
		return a.metadata(args)
	case "file":
		return a.file(args)
	case "sync":
		return a.sync(args)
	case "notes":
		return a.notes(args)
	case "state":
		return a.state(args)
	case "plugin":
		return a.plugin(args)
	case "pull":
		return a.pull(args)
	case "mcp":
		return a.mcp(args)
	case "hook":
		return a.hook(args)
	case "setup":
		return a.setup(args)
	case "outbox":
		return a.outbox(args)
	case "host":
		return a.host(args)
	default:
		return fmt.Errorf("unknown command %q; run edc help", command)
	}
}

func (a *app) host(args []string) error {
	if len(args) == 0 || args[0] != "run" {
		return fmt.Errorf("host requires run")
	}
	f := a.flags("host run")
	projectID := f.String("project", "", "project ID")
	pluginID := f.String("plugin", "", "plugin id")
	pluginDir := f.String("plugin-dir", "", "directory containing local plugin packages")
	pluginTokenFile := f.String("plugin-token-file", "", "private file containing the plugin token")
	agentCommand := f.String("agent-command", "", "Codex executable path for agent processors")
	command := f.String("command", "", "executable override for command processors")
	timeout := f.Duration("timeout", 0, "processor timeout override (default from manifest)")
	once := f.Bool("once", false, "run one processor pass")
	watch := f.Bool("watch", false, "keep checking the processor schedule and event cursor")
	interval := f.Duration("interval", 0, "watch interval (default 15s for notes-indexer, 30s otherwise)")
	if err := parse(f, args[1:]); err != nil {
		return err
	}
	if a.token == "" && strings.TrimSpace(*projectID) == "" {
		return fmt.Errorf("--project is required when running the host with only a plugin token")
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if *pluginID == "" || *pluginDir == "" || *once == *watch {
		return fmt.Errorf("--plugin, --plugin-dir and exactly one of --once or --watch are required")
	}
	pluginToken := os.Getenv("EDC_PLUGIN_TOKEN")
	if *pluginTokenFile != "" {
		if pluginToken != "" {
			return fmt.Errorf("use either --plugin-token-file or EDC_PLUGIN_TOKEN")
		}
		value, err := readPrivateTokenFile(*pluginTokenFile)
		if err != nil {
			return err
		}
		pluginToken = value
	}
	if pluginToken == "" {
		return fmt.Errorf("--plugin-token-file or EDC_PLUGIN_TOKEN is required")
	}
	opts := processorhost.Options{
		ProjectID: *projectID, PluginID: *pluginID, PluginDir: *pluginDir,
		PluginToken: pluginToken, AgentCommand: *agentCommand, Command: *command,
		Once: *once, Watch: *watch, Interval: *interval, Timeout: *timeout,
	}
	if *once {
		result, err := processorhost.RunOnce(a.ctx, a.client, opts)
		return a.result(result, err)
	}
	opts.OnResult = func(result processorhost.Result, runErr error) {
		if runErr == nil {
			if result.PluginID == "notes-indexer" && result.Noop {
				return
			}
			_ = json.NewEncoder(a.io.out).Encode(result)
			return
		}
		_ = json.NewEncoder(a.io.err).Encode(struct {
			Result processorhost.Result `json:"result"`
			Error  string               `json:"error"`
		}{Result: result, Error: runErr.Error()})
	}
	return processorhost.Run(a.ctx, a.client, opts)
}

func readPrivateTokenFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read plugin token file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("plugin token file must be a private regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read plugin token file: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	// Accept installation receipts used by early V2 deployments as well as the
	// plain token files written by current CLI installs.
	if strings.HasPrefix(value, "{") {
		var receipt struct {
			Token string `json:"token"`
		}
		if json.Unmarshal(raw, &receipt) != nil {
			return "", fmt.Errorf("plugin token file is invalid")
		}
		value = strings.TrimSpace(receipt.Token)
	}
	if value == "" || len(value) > 4096 {
		return "", fmt.Errorf("plugin token file is invalid")
	}
	return value, nil
}

func writePrivateNewFile(path string, contents []byte) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("token file path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	writeErr := error(nil)
	if _, writeErr = f.Write(contents); writeErr == nil {
		writeErr = f.Sync()
	}
	if closeErr := f.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		_ = os.Remove(path)
	}
	return writeErr
}

func (a *app) auth(command string, args []string) error {
	f := a.flags(command)
	username := f.String("username", "", "username")
	var email *string
	if command == "register" {
		email = f.String("email", "", "email address")
	}
	fromStdin := f.Bool("password-stdin", false, "read password from stdin")
	if err := parse(f, args); err != nil {
		return err
	}
	if *username == "" {
		return fmt.Errorf("--username is required")
	}
	if command == "register" && strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required for register")
	}
	password, err := a.password(*fromStdin)
	if err != nil {
		return err
	}
	credentials := core.Credentials{Username: *username, Password: password}
	if command == "register" {
		credentials.Email = strings.TrimSpace(*email)
		out, err := a.client.Register(a.ctx, credentials)
		return a.result(out, err)
	}
	out, err := a.client.Login(a.ctx, credentials)
	if err != nil {
		return err
	}
	updated := a.config
	updated.Server, updated.Token, updated.AccountID = a.server, out.Token, out.User.ID
	if err = saveConfig(a.configPath, updated); err != nil {
		return err
	}
	return a.json(map[string]any{"user": out.User, "expires_at": out.ExpiresAt, "config": a.configPath})
}

func (a *app) password(fromStdin bool) (string, error) {
	if fromStdin {
		b, err := io.ReadAll(io.LimitReader(a.io.in, 75))
		if err != nil {
			return "", err
		}
		value := strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r")
		if len(value) > 72 {
			return "", fmt.Errorf("password max 72 bytes")
		}
		return value, nil
	}
	f, ok := a.io.in.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", fmt.Errorf("use --password-stdin when not running in a terminal")
	}
	fmt.Fprint(a.io.err, "Password: ")
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(a.io.err)
	return string(b), err
}

func (a *app) flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(a.io.err)
	return f
}
func parse(f *flag.FlagSet, args []string) error {
	if err := f.Parse(args); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", f.Args())
	}
	return nil
}
func (a *app) json(v any) error {
	e := json.NewEncoder(a.io.out)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func (a *app) result(v any, err error) error {
	if err != nil {
		return err
	}
	return a.json(v)
}
func envDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func readConfig(path string) (config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config{}, nil
	}
	if err != nil {
		return config{}, err
	}
	var cfg config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return config{}, fmt.Errorf("read config: %w", err)
	}
	return cfg, nil
}

func saveConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(b)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

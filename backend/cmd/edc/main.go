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
	"event-driven-context/internal/v2client"
	"golang.org/x/term"
)

const help = `edc — append-only project context

Usage: edc [--server URL] [--config PATH] COMMAND

Commands:
  register | login | logout | whoami
  project create | list | members | add-member
  push [TEXT] | --file PATH | --json | --jsonl
  query | get EVENT_ID | metadata
  file get FILE_ID
  state list | get | put
  plugin install | list | config | pause | resume | rerun | remove
  pull --after SEQUENCE
  mcp

Global flags precede COMMAND. Project commands require --project until directory
binding is implemented. Passwords are prompted without echo; --password-stdin
reads one password from stdin. Login writes a private config. EDC_SERVER,
EDC_CONFIG and EDC_TOKEN override defaults. Structured output is JSON.
`

type config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
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
	if a.token == "" {
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
			if err := saveConfig(a.configPath, config{Server: a.server}); err != nil {
				return err
			}
		}
		return a.json(map[string]bool{"logged_out": true})
	case "project":
		return a.project(args)
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
	case "state":
		return a.state(args)
	case "plugin":
		return a.plugin(args)
	case "pull":
		return a.pull(args)
	case "mcp":
		return a.mcp(args)
	case "link", "status", "hook", "setup", "outbox", "host":
		return fmt.Errorf("%s is not implemented in this V2 CLI build", command)
	default:
		return fmt.Errorf("unknown command %q; run edc help", command)
	}
}

func (a *app) auth(command string, args []string) error {
	f := a.flags(command)
	username := f.String("username", "", "username")
	fromStdin := f.Bool("password-stdin", false, "read password from stdin")
	if err := parse(f, args); err != nil {
		return err
	}
	if *username == "" {
		return fmt.Errorf("--username is required")
	}
	password, err := a.password(*fromStdin)
	if err != nil {
		return err
	}
	credentials := core.Credentials{Username: *username, Password: password}
	if command == "register" {
		out, err := a.client.Register(a.ctx, credentials)
		return a.result(out, err)
	}
	out, err := a.client.Login(a.ctx, credentials)
	if err != nil {
		return err
	}
	if err = saveConfig(a.configPath, config{Server: a.server, Token: out.Token}); err != nil {
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
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

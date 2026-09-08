package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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

	"event-driven-context/internal/api"
	"event-driven-context/internal/core"
	"event-driven-context/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"
)

const help = `edc — append-only project context

Global flags must precede the command:
  edc [--server http://127.0.0.1:8080] [--config PATH] COMMAND

Commands:
  register --username NAME [--password-stdin]
  login --username NAME [--password-stdin]
  logout | whoami
  project create --name NAME [--description TEXT]
  project list
  project add-member --project ID --username NAME
  project members --project ID
  record --project ID (--text TEXT | --file PATH) [--type text/plain]
         [--metadata JSON] [--occurred-at RFC3339] [--idempotency-key KEY]
  query --project ID [--from RFC3339] [--to RFC3339]
        [--time-field recorded_at|occurred_at] [--metadata JSON]
        [--exists KEY (repeatable)] [--limit 50] [--cursor CURSOR]
  get --event ID
  file --id ID [--output PATH]
  metadata --project ID [--key KEY] [--limit 50] [--offset 0]
  mcp                    Serve standard MCP over stdin/stdout

Files require an explicit --type. --file - reads bytes from stdin.
Passwords are prompted without echo; use --password-stdin for automation.
Login saves a token in a private local config; EDC_TOKEN can override it.
EDC_SERVER and EDC_CONFIG supply global defaults. Output is JSON.
`

type config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "edc:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	baseDir, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	g := flag.NewFlagSet("edc", flag.ContinueOnError)
	g.SetOutput(os.Stderr)
	server := g.String("server", os.Getenv("EDC_SERVER"), "server origin URL")
	path := g.String("config", envDefault("EDC_CONFIG", filepath.Join(baseDir, "event-driven-context", "config.json")), "private config path")
	g.Usage = func() { fmt.Fprint(os.Stderr, help) }
	if err = g.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	args = g.Args()
	if len(args) == 0 || args[0] == "help" {
		fmt.Print(help)
		return nil
	}
	cfg, err := readConfig(*path)
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
	if token == "" && *server == cfg.Server {
		token = cfg.Token
	}
	client, err := api.NewClient(*server, token)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := args[0]
	args = args[1:]
	if cmd == "register" || cmd == "login" {
		fs := flags(cmd)
		user := fs.String("username", "", "username")
		stdin := fs.Bool("password-stdin", false, "read password from stdin")
		if err = parse(fs, args); err != nil {
			return err
		}
		if *user == "" {
			return fmt.Errorf("--username is required")
		}
		pwd, err := password(*stdin)
		if err != nil {
			return err
		}
		in := core.Credentials{Username: *user, Password: pwd}
		if cmd == "register" {
			out, err := client.Register(ctx, in)
			return printResult(out, err)
		}
		out, err := client.Login(ctx, in)
		if err != nil {
			return err
		}
		if err = saveConfig(*path, config{*server, out.Token}); err != nil {
			return err
		}
		return printJSON(map[string]any{"user": out.User, "expires_at": out.ExpiresAt, "config": *path})
	}
	if token == "" {
		return fmt.Errorf("login first or set EDC_TOKEN")
	}
	switch cmd {
	case "whoami":
		if len(args) > 0 {
			return fmt.Errorf("whoami takes no arguments")
		}
		out, err := client.Me(ctx)
		return printResult(out, err)
	case "logout":
		if len(args) > 0 {
			return fmt.Errorf("logout takes no arguments")
		}
		if err = client.Logout(ctx); err != nil {
			return err
		}
		if cfg.Server == *server && cfg.Token == token {
			if err = saveConfig(*path, config{Server: *server}); err != nil {
				return err
			}
		}
		return printJSON(map[string]bool{"logged_out": true})
	case "project":
		return project(ctx, client, args)
	case "record":
		return record(ctx, client, args)
	case "query":
		fs := flags(cmd)
		var in core.QueryInput
		fs.StringVar(&in.ProjectID, "project", "", "project ID")
		fs.StringVar(&in.From, "from", "", "inclusive RFC3339")
		fs.StringVar(&in.To, "to", "", "exclusive RFC3339")
		fs.StringVar(&in.TimeField, "time-field", "recorded_at", "time field")
		meta := fs.String("metadata", "{}", "JSON filters")
		fs.IntVar(&in.Limit, "limit", 50, "page size")
		fs.StringVar(&in.Cursor, "cursor", "", "next cursor")
		var exists stringList
		fs.Var(&exists, "exists", "required metadata key, repeatable")
		if err = parse(fs, args); err != nil {
			return err
		}
		in.Metadata, err = metadata(*meta)
		if err != nil {
			return err
		}
		in.MetadataExists = exists
		out, err := client.QueryEvents(ctx, in)
		return printResult(out, err)
	case "get":
		fs := flags(cmd)
		id := fs.String("event", "", "event ID")
		if err = parse(fs, args); err != nil {
			return err
		}
		if *id == "" {
			return fmt.Errorf("--event required")
		}
		out, err := client.GetEvent(ctx, core.EventRef{EventID: *id})
		return printResult(out, err)
	case "file":
		fs := flags(cmd)
		id := fs.String("id", "", "file ID")
		path := fs.String("output", "", "write original bytes to a new file; - for stdout")
		if err = parse(fs, args); err != nil {
			return err
		}
		if *id == "" {
			return fmt.Errorf("--id required")
		}
		out, err := client.GetFile(ctx, core.FileRef{FileID: *id})
		if err != nil {
			return err
		}
		if *path == "" {
			return printJSON(out)
		}
		b, err := base64.StdEncoding.DecodeString(out.DataBase64)
		if err != nil {
			return err
		}
		if *path == "-" {
			_, err = os.Stdout.Write(b)
			return err
		}
		f, err := os.OpenFile(*path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if _, err = f.Write(b); err != nil {
			f.Close()
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		return printJSON(out.File)
	case "metadata":
		fs := flags(cmd)
		var in core.MetadataInput
		fs.StringVar(&in.ProjectID, "project", "", "project ID")
		key := fs.String("key", "", "list this key's values")
		fs.IntVar(&in.Limit, "limit", 50, "page size")
		fs.IntVar(&in.Offset, "offset", 0, "offset")
		if err = parse(fs, args); err != nil {
			return err
		}
		if visited(fs, "key") {
			in.Key = key
		}
		out, err := client.ListMetadata(ctx, in)
		return printResult(out, err)
	case "mcp":
		if len(args) > 0 {
			return fmt.Errorf("mcp takes no arguments")
		}
		if _, err = client.Me(ctx); err != nil {
			return err
		}
		return mcpserver.New(client).Run(ctx, &mcp.StdioTransport{})
	default:
		return fmt.Errorf("unknown command %q; run edc help", cmd)
	}
}
func project(ctx context.Context, c *api.Client, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("project requires create, list, add-member or members")
	}
	cmd := args[0]
	fs := flags("project " + cmd)
	switch cmd {
	case "list":
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		out, err := c.ListProjects(ctx, core.Empty{})
		return printResult(out, err)
	case "create":
		name := fs.String("name", "", "name")
		desc := fs.String("description", "", "description")
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		out, err := c.CreateProject(ctx, core.ProjectInput{Name: *name, Description: *desc})
		return printResult(out, err)
	case "add-member":
		pid := fs.String("project", "", "project ID")
		user := fs.String("username", "", "registered username")
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		out, err := c.AddMember(ctx, core.MemberInput{ProjectID: *pid, Username: *user})
		return printResult(out, err)
	case "members":
		pid := fs.String("project", "", "project ID")
		if err := parse(fs, args[1:]); err != nil {
			return err
		}
		out, err := c.ListMembers(ctx, core.ProjectRef{ProjectID: *pid})
		return printResult(out, err)
	default:
		return fmt.Errorf("unknown project command %q", cmd)
	}
}
func record(ctx context.Context, c *api.Client, args []string) error {
	fs := flags("record")
	var in core.RecordInput
	fs.StringVar(&in.ProjectID, "project", "", "project ID")
	txt := fs.String("text", "", "text")
	file := fs.String("file", "", "file path or - for stdin")
	media := fs.String("type", "", "explicit MIME type")
	meta := fs.String("metadata", "{}", "JSON metadata")
	fs.StringVar(&in.OccurredAt, "occurred-at", "", "RFC3339 event time")
	fs.StringVar(&in.IdempotencyKey, "idempotency-key", "", "stable key for retries")
	if err := parse(fs, args); err != nil {
		return err
	}
	hasText, hasFile := visited(fs, "text"), visited(fs, "file")
	if hasText == hasFile {
		return fmt.Errorf("provide exactly one of --text or --file")
	}
	var err error
	in.Metadata, err = metadata(*meta)
	if err != nil {
		return err
	}
	if hasText {
		if *media != "" {
			return fmt.Errorf("--type applies to --file")
		}
		in.Content = core.ContentInput{Kind: "text", Text: txt}
	} else {
		if *media == "" {
			return fmt.Errorf("--file requires an explicitly declared --type")
		}
		var r io.Reader = os.Stdin
		if *file != "-" {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		b, err := io.ReadAll(io.LimitReader(r, core.MaxContentBytes+1))
		if err != nil {
			return err
		}
		if len(b) > core.MaxContentBytes {
			return fmt.Errorf("file max 1 MiB")
		}
		name := filepath.Base(*file)
		if name == "-" {
			name = "stdin.txt"
		}
		in.Content = core.ContentInput{Kind: "file", File: &core.FileInput{Filename: name, MediaType: *media, DataBase64: base64.StdEncoding.EncodeToString(b)}}
	}
	if in.IdempotencyKey == "" {
		in.IdempotencyKey = rand.Text()
	}
	out, err := c.RecordEvent(ctx, in)
	return printResult(out, err)
}
func flags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
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
func visited(f *flag.FlagSet, name string) bool {
	ok := false
	f.Visit(func(v *flag.Flag) {
		if v.Name == name {
			ok = true
		}
	})
	return ok
}
func metadata(s string) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return nil, fmt.Errorf("metadata must be a JSON object")
	}
	return m, nil
}
func password(stdin bool) (string, error) {
	if stdin {
		b, err := io.ReadAll(io.LimitReader(os.Stdin, 75))
		if err != nil {
			return "", err
		}
		b = []byte(strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"))
		if len(b) > 72 {
			return "", fmt.Errorf("password max 72 bytes")
		}
		return string(b), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", fmt.Errorf("use --password-stdin when not running in a terminal")
	}
	fmt.Fprint(os.Stderr, "Password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}
func printJSON(v any) error {
	e := json.NewEncoder(os.Stdout)
	e.SetIndent("", "  ")
	return e.Encode(v)
}
func printResult(v any, err error) error {
	if err != nil {
		return err
	}
	return printJSON(v)
}
func envDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func readConfig(path string) (config, error) {
	var c config
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("invalid config file")
	}
	return c, nil
}
func saveConfig(path string, c config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".edc-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

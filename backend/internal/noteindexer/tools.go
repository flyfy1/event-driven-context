package noteindexer

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"event-driven-context/internal/notes"
	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type draft struct {
	mu          sync.Mutex
	client      *v2client.Client
	project     string
	run         notes.OrganizationRun
	files       map[string]string
	read        map[string]bool
	accounted   map[string]string
	fileSources map[string]bool
	finished    bool
	Calls       int
}

type toolArgs struct {
	Path        string `json:"path"`
	Folder      string `json:"folder"`
	Query       string `json:"query"`
	EventID     string `json:"event_id"`
	Start       int    `json:"start"`
	Limit       int    `json:"limit"`
	Outline     bool   `json:"outline"`
	Content     string `json:"content"`
	From        string `json:"from"`
	To          string `json:"to"`
	Disposition string `json:"disposition"`
	FileID      string `json:"file_id"`
	Cursor      string `json:"cursor"`
}

func (d *draft) server() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "edc-notes-draft", Version: "1"}, nil)
	add := func(name, description string, fields map[string]string, required ...string) {
		props := map[string]any{}
		for k, typ := range fields {
			props[k] = map[string]string{"type": typ}
		}
		schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		s.AddTool(&mcp.Tool{Name: name, Description: description, InputSchema: schema}, func(ctx context.Context, r *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var in toolArgs
			if len(r.Params.Arguments) > notes.MaxFileBytes+4096 {
				return toolResult(nil, fmt.Errorf("tool arguments too large")), nil
			}
			if err := json.Unmarshal(r.Params.Arguments, &in); err != nil {
				return toolResult(nil, err), nil
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			d.Calls++
			if d.Calls > 500 {
				return toolResult(nil, fmt.Errorf("run tool budget exhausted")), nil
			}
			out, err := d.call(ctx, name, in)
			return toolResult(out, err), nil
		})
	}
	add("list_events", "List the current source batch previews; start is zero-based, limit <=20.", map[string]string{"start": "integer", "limit": "integer"})
	add("list_notes", "List immediate children of a folder, with short summaries; start zero-based, limit <=50.", map[string]string{"folder": "string", "start": "integer", "limit": "integer"})
	add("read_note", "Read selected lines (start one-based, limit <=120; <=12000 characters), or outline=true for headings.", map[string]string{"path": "string", "start": "integer", "limit": "integer", "outline": "boolean"}, "path")
	add("search_notes", "Search note text; returns up to 20 paths and matching snippets.", map[string]string{"query": "string", "limit": "integer"}, "query")
	add("read_event", "Read source text on demand with metadata and references. start is a zero-based character offset; limit <=8000. Follow next_start for more.", map[string]string{"event_id": "string", "start": "integer", "limit": "integer"}, "event_id")
	add("list_files", "Discover attachment metadata at this run's fixed Event boundary; limit <=20. Follow next_cursor for more. No bytes are downloaded.", map[string]string{"limit": "integer", "cursor": "string"})
	add("file_metadata", "Read one attachment's metadata and up to five source/derived Event references. Metadata does not prove file contents.", map[string]string{"file_id": "string"}, "file_id")
	add("file_references", "Page source and derived Event references for one attachment at this run's boundary; limit <=20. Read the returned Events for evidence.", map[string]string{"file_id": "string", "limit": "integer", "cursor": "string"}, "file_id")
	add("write_note", "Create or replace one draft Markdown note; does not publish. Keep ordinary notes under1000 words and organization.md under500.", map[string]string{"path": "string", "content": "string"}, "path", "content")
	add("move_note", "Move a draft note while preserving its content and ID. Destination must not exist.", map[string]string{"from": "string", "to": "string"}, "from", "to")
	add("account_event", "Classify a batch event as used or irrelevant. Used events must have been read with read_event.", map[string]string{"event_id": "string", "disposition": "string"}, "event_id", "disposition")
	add("finish", "Validate the complete draft and source accounting. Successful validation lets the host publish after this session exits.", nil)
	return s
}

func toolResult(out any, err error) *mcp.CallToolResult {
	if err != nil {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
	}
	data, _ := json.Marshal(out)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}
}

func pageBounds(start, limit, defaultLimit, maxLimit, size int) (int, int) {
	if start < 0 {
		start = 0
	}
	if start > size {
		start = size
	}
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	end := start + limit
	if end > size {
		end = size
	}
	return start, end
}

func (d *draft) call(ctx context.Context, name string, in toolArgs) (any, error) {
	switch name {
	case "list_events":
		start, end := pageBounds(in.Start, in.Limit, 20, 20, len(d.run.Events))
		return map[string]any{"events": d.run.Events[start:end], "has_more": end < len(d.run.Events), "next_start": end}, nil
	case "list_notes":
		prefix := strings.Trim(in.Folder, "/")
		if prefix != "" {
			prefix += "/"
		}
		entries := map[string]map[string]string{}
		for file, text := range d.files {
			if !strings.HasPrefix(file, prefix) {
				continue
			}
			suffix := strings.TrimPrefix(file, prefix)
			first, _, dir := strings.Cut(suffix, "/")
			p := prefix + first
			entry := map[string]string{"path": p, "kind": "file"}
			if dir {
				entry["kind"] = "directory"
			} else {
				for _, line := range strings.Split(text, "\n") {
					if strings.HasPrefix(line, "> ") {
						entry["summary"] = clip(line[2:], 160)
						break
					}
				}
			}
			entries[p] = entry
		}
		keys := sortedKeys(entries)
		start, end := pageBounds(in.Start, in.Limit, 30, 50, len(keys))
		out := []map[string]string{}
		for _, k := range keys[start:end] {
			out = append(out, entries[k])
		}
		return map[string]any{"entries": out, "has_more": end < len(keys), "next_start": end}, nil
	case "read_note":
		text, ok := d.files[in.Path]
		if !ok {
			return nil, fmt.Errorf("note not found")
		}
		lines := strings.Split(text, "\n")
		if in.Outline {
			out := []string{}
			for i, line := range lines {
				if strings.HasPrefix(line, "#") {
					out = append(out, fmt.Sprintf("%d: %s", i+1, clip(line, 200)))
					if len(out) == 60 {
						break
					}
				}
			}
			return map[string]any{"headings": out, "lines": len(lines)}, nil
		}
		start := in.Start - 1
		start, end := pageBounds(start, in.Limit, 80, 120, len(lines))
		out := strings.Join(lines[start:end], "\n")
		if len([]rune(out)) > 12000 {
			return nil, fmt.Errorf("selected lines exceed12000 characters; request fewer lines")
		}
		return map[string]any{"text": out, "start": start + 1, "next_start": end + 1, "has_more": end < len(lines)}, nil
	case "search_notes":
		if strings.TrimSpace(in.Query) == "" {
			return nil, fmt.Errorf("query required")
		}
		limit := in.Limit
		if limit <= 0 || limit > 20 {
			limit = 20
		}
		matcher := regexp.MustCompile("(?i)" + regexp.QuoteMeta(in.Query))
		out := []map[string]string{}
		for _, p := range sortedKeys(d.files) {
			text := d.files[p]
			if match := matcher.FindStringIndex(text); match != nil {
				out = append(out, map[string]string{"path": p, "snippet": clip(text[match[0]:], 240)})
				if len(out) == limit {
					break
				}
			}
		}
		return out, nil
	case "read_event":
		e, err := d.client.GetEvent(ctx, d.project, in.EventID)
		if err != nil {
			return nil, err
		}
		if e.Sequence > d.run.ThroughSequence {
			return nil, fmt.Errorf("event is newer than this run snapshot")
		}
		chars := []rune(e.Content.Text)
		start, end := pageBounds(in.Start, in.Limit, 6000, 8000, len(chars))
		d.read[e.ID] = true
		if e.Content.Kind == "file" {
			if d.fileSources == nil {
				d.fileSources = map[string]bool{}
			}
			d.fileSources[e.Content.FileID] = true
		}
		return map[string]any{"id": e.ID, "sequence": e.Sequence, "type": e.Type, "actor": e.Actor, "recorded_at": e.RecordedAt, "occurred_at": e.OccurredAt, "refs": e.Refs, "kind": e.Content.Kind, "file_id": e.Content.FileID, "filename": e.Content.Filename, "text": string(chars[start:end]), "next_start": end, "has_more": end < len(chars)}, nil
	case "list_files":
		limit := in.Limit
		if limit <= 0 || limit > 20 {
			limit = 20
		}
		return d.client.ListFiles(ctx, d.project, v2.FileCatalogInput{Limit: limit, Cursor: in.Cursor, ThroughSequence: &d.run.ThroughSequence})
	case "file_metadata":
		out, err := d.client.FileMetadata(ctx, d.project, in.FileID, &d.run.ThroughSequence)
		if err == nil {
			if d.fileSources == nil {
				d.fileSources = map[string]bool{}
			}
			d.fileSources[out.ID] = true
		}
		return out, err
	case "file_references":
		limit := in.Limit
		if limit <= 0 || limit > 20 {
			limit = 20
		}
		return d.client.FileReferences(ctx, d.project, in.FileID, v2.FileCatalogInput{Limit: limit, Cursor: in.Cursor, ThroughSequence: &d.run.ThroughSequence})
	case "write_note":
		if err := notes.ValidatePath(in.Path); err != nil {
			return nil, err
		}
		if !utf8.ValidString(in.Content) || strings.ContainsRune(in.Content, 0) || len(in.Content) > notes.MaxFileBytes {
			return nil, fmt.Errorf("invalid note text")
		}
		if len(d.files) >= notes.MaxFiles {
			if _, ok := d.files[in.Path]; !ok {
				return nil, fmt.Errorf("too many notes")
			}
		}
		d.files[in.Path] = in.Content
		d.finished = false
		return map[string]bool{"draft_saved": true}, nil
	case "move_note":
		if err := notes.ValidatePath(in.From); err != nil {
			return nil, err
		}
		if err := notes.ValidatePath(in.To); err != nil {
			return nil, err
		}
		text, ok := d.files[in.From]
		if !ok {
			return nil, fmt.Errorf("source note not found")
		}
		if _, ok = d.files[in.To]; ok {
			return nil, fmt.Errorf("destination exists")
		}
		d.files[in.To] = text
		delete(d.files, in.From)
		d.finished = false
		return map[string]bool{"draft_moved": true}, nil
	case "account_event":
		found := false
		for _, e := range d.run.Events {
			if e.ID == in.EventID {
				found = true
			}
		}
		if !found || (in.Disposition != "used" && in.Disposition != "irrelevant") {
			return nil, fmt.Errorf("choose a batch event and used or irrelevant")
		}
		if in.Disposition == "used" && !d.read[in.EventID] {
			return nil, fmt.Errorf("read the source before using it")
		}
		d.accounted[in.EventID] = in.Disposition
		d.finished = false
		return map[string]bool{"accounted": true}, nil
	case "finish":
		if len(d.accounted) != len(d.run.Events) {
			return nil, fmt.Errorf("account for every source event before finishing")
		}
		sources := map[string]bool{}
		files := map[string]bool{}
		for id := range d.fileSources {
			files[id] = true
		}
		for id := range d.read {
			sources[id] = true
		}
		pattern := regexp.MustCompile(`edc-event://([^\s)\]>]+)`)
		for _, f := range d.run.Snapshot.Files {
			for _, id := range notes.FileLinkIDs(f.Content) {
				files[id] = true
			}
			for _, m := range pattern.FindAllStringSubmatch(f.Content, -1) {
				sources[m[1]] = true
			}
		}
		if err := notes.ValidateOrganizedTree(d.all(), d.run.Snapshot.Files, sources, files); err != nil {
			return nil, err
		}
		linked := map[string]bool{}
		for _, f := range d.all() {
			if strings.HasSuffix(f.Path, "/organization.md") {
				continue
			}
			for _, id := range notes.FileLinkIDs(f.Content) {
				linked[id] = true
			}
		}
		for _, event := range d.run.Events {
			if event.Kind != "file" {
				continue
			}
			source, err := d.client.GetEvent(ctx, d.project, event.ID)
			if err != nil {
				return nil, err
			}
			if !linked[source.Content.FileID] {
				return nil, fmt.Errorf("include attachment link for file Event %s", event.ID)
			}
		}
		d.finished = true
		return map[string]bool{"validated": true}, nil
	}
	return nil, fmt.Errorf("unknown tool")
}

func (d *draft) all() []notes.WriteFile {
	out := []notes.WriteFile{}
	for _, p := range sortedKeys(d.files) {
		out = append(out, notes.WriteFile{Path: p, Content: d.files[p]})
	}
	return out
}
func sortedKeys[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + " …"
	}
	return s
}

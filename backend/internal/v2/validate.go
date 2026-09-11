package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)
	namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)
)

func v2err(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, v2err("invalid_input", "invalid JSON")
	}
	var trailing any
	if err := d.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, v2err("invalid_input", "invalid JSON")
	}
	b, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func canonicalObject(in map[string]json.RawMessage, maxBytes int) (map[string]json.RawMessage, error) {
	if in == nil {
		return map[string]json.RawMessage{}, nil
	}
	if len(in) > 128 {
		return nil, v2err("too_large", "JSON object exceeds 128 fields")
	}
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		if k == "" || len(k) > 128 || !utf8.ValidString(k) {
			return nil, v2err("invalid_input", "invalid object key")
		}
		c, err := canonicalJSON(v)
		if err != nil {
			return nil, err
		}
		out[k] = c
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if len(b) > maxBytes {
		return nil, v2err("too_large", "JSON object exceeds %d bytes", maxBytes)
	}
	return out, nil
}

func normalizedInput(in EventInput) (EventInput, error) {
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	if !uuidPattern.MatchString(in.ID) {
		return in, v2err("invalid_input", "event id must be a UUID")
	}
	if in.Type != "log" && in.Type != "note" && in.Type != "derived" {
		return in, v2err("invalid_input", "event type must be log, note, or derived")
	}
	switch in.Content.Kind {
	case "text":
		if in.Content.Text == "" || !utf8.ValidString(in.Content.Text) {
			return in, v2err("invalid_input", "text content must be non-empty UTF-8")
		}
		if len(in.Content.Text) > MaxTextBytes {
			return in, v2err("too_large", "text exceeds 1 MiB")
		}
		if in.Content.FileID != "" {
			return in, v2err("invalid_input", "text content cannot name a file")
		}
		in.Content.FileID, in.Content.MediaType, in.Content.Filename, in.Content.SHA256 = "", "", "", ""
		in.Content.SizeBytes, in.Content.DurationMS = 0, 0
	case "file":
		if in.Content.FileID == "" || in.Content.Text != "" {
			return in, v2err("invalid_input", "file content requires file_id only")
		}
		// All file facts other than duration are server supplied.
		in.Content.MediaType, in.Content.Filename, in.Content.SHA256 = "", "", ""
		in.Content.SizeBytes = 0
		if in.Content.DurationMS < 0 {
			return in, v2err("invalid_input", "duration_ms cannot be negative")
		}
	default:
		return in, v2err("invalid_input", "content kind must be text or file")
	}
	var err error
	if in.Metadata, err = canonicalObject(in.Metadata, MaxMetadata); err != nil {
		return in, err
	}
	if in.Source, err = canonicalObject(in.Source, MaxMetadata); err != nil {
		return in, err
	}
	channel, ok := in.Source["channel"]
	if !ok {
		return in, v2err("invalid_input", "source.channel is required")
	}
	var channelValue string
	if json.Unmarshal(channel, &channelValue) != nil {
		return in, v2err("invalid_input", "source.channel must be a string")
	}
	allowedChannel := map[string]bool{"app": true, "hook": true, "skill": true, "cli": true, "api": true, "web": true, "plugin": true}
	if !allowedChannel[channelValue] {
		return in, v2err("invalid_input", "unsupported source.channel")
	}
	if len(in.Refs) > MaxRefs {
		return in, v2err("too_large", "event refs exceed 32")
	}
	if len(in.Refs) == 0 {
		in.Refs = []Ref{}
	} else {
		in.Refs = append([]Ref(nil), in.Refs...)
	}
	seen := map[string]bool{}
	for i := range in.Refs {
		in.Refs[i].ID = strings.ToLower(strings.TrimSpace(in.Refs[i].ID))
		if in.Refs[i].ID == in.ID || !uuidPattern.MatchString(in.Refs[i].ID) {
			return in, v2err("invalid_ref", "invalid or self event ref")
		}
		if !map[string]bool{"supersedes": true, "retracts": true, "resolves": true, "derived_from": true, "replies_to": true}[in.Refs[i].Rel] {
			return in, v2err("invalid_ref", "unsupported ref relation")
		}
		key := in.Refs[i].Rel + "\x00" + in.Refs[i].ID
		if seen[key] {
			return in, v2err("invalid_ref", "duplicate event ref")
		}
		seen[key] = true
	}
	sort.Slice(in.Refs, func(i, j int) bool {
		if in.Refs[i].Rel == in.Refs[j].Rel {
			return in.Refs[i].ID < in.Refs[j].ID
		}
		return in.Refs[i].Rel < in.Refs[j].Rel
	})
	if in.OccurredAt != "" {
		t, e := time.Parse(time.RFC3339Nano, in.OccurredAt)
		if e != nil {
			return in, v2err("invalid_input", "occurred_at must be RFC3339")
		}
		in.OccurredAt = t.UTC().Format(time.RFC3339Nano)
	}
	return in, nil
}

func sameInput(a Event, b EventInput) bool {
	if a.Content.Kind == "file" {
		a.Content.MediaType, a.Content.Filename, a.Content.SHA256 = "", "", ""
		a.Content.SizeBytes = 0
	}
	probe := struct {
		Type             string
		Content          EventContent
		Metadata, Source map[string]json.RawMessage
		Refs             []Ref
		OccurredAt       string
	}{b.Type, b.Content, b.Metadata, b.Source, b.Refs, b.OccurredAt}
	stored := struct {
		Type             string
		Content          EventContent
		Metadata, Source map[string]json.RawMessage
		Refs             []Ref
		OccurredAt       string
	}{a.Type, a.Content, a.Metadata, a.Source, a.Refs, a.OccurredAt}
	x, _ := json.Marshal(probe)
	y, _ := json.Marshal(stored)
	return bytes.Equal(x, y)
}

func parseTimeFilter(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, e := time.Parse(time.RFC3339Nano, v)
	if e != nil {
		return time.Time{}, v2err("invalid_input", "time filter must be RFC3339")
	}
	return t, nil
}

func validPluginID(v string) bool { return namePattern.MatchString(v) }

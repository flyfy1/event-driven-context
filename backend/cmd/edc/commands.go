package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func decodeInputJSON(r io.Reader, limit int64, out any) error {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return fmt.Errorf("input exceeds %d bytes", limit)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("invalid JSON input: %w", err)
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("invalid JSON input: trailing data")
	}
	return nil
}

func objectWithPairs(raw string, pairs []string, jsonValues bool) (map[string]json.RawMessage, error) {
	var out map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return nil, fmt.Errorf("value must be a JSON object")
	}
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, "=")
		if !ok || key == "" {
			return nil, fmt.Errorf("expected KEY=VALUE, got %q", pair)
		}
		encoded, err := json.Marshal(value)
		if jsonValues && json.Valid([]byte(value)) {
			encoded = []byte(value)
			err = nil
		}
		if err != nil {
			return nil, err
		}
		out[key] = encoded
	}
	return out, nil
}

func readPathOrStdin(stdin io.Reader, path string, limit int) ([]byte, error) {
	var r io.Reader = stdin
	var f *os.File
	var err error
	if path != "-" {
		f, err = os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return b, nil
}

func (a *app) mcp(args []string) error { return runMCP(a, args) }

var _ flag.Value = (*stringList)(nil)

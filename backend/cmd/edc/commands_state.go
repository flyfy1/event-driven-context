package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	"event-driven-context/internal/v2"
)

func (a *app) state(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("state requires list, get or put")
	}
	command := args[0]
	f := a.flags("state " + command)
	projectID := f.String("project", "", "project ID")
	switch command {
	case "list":
		prefix := f.String("prefix", "", "key prefix")
		if err := parse(f, args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		out, err := a.client.ListState(a.ctx, *projectID, *prefix)
		return a.result(out, err)
	case "get":
		versionRaw := f.String("version", "", "historical version")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("state get requires one KEY")
		}
		var version *int64
		if *versionRaw != "" {
			n, err := strconv.ParseInt(*versionRaw, 10, 64)
			if err != nil || n < 1 {
				return fmt.Errorf("--version must be positive")
			}
			version = &n
		}
		out, err := a.client.GetState(a.ctx, *projectID, f.Arg(0), version)
		return a.result(out, err)
	case "put":
		contentPath := f.String("content", "", "content file or -")
		format := f.String("format", "markdown", "markdown or text")
		dataRaw := f.String("data", "", "optional JSON data")
		based := f.Int64("based-on", 0, "based-on sequence")
		expectedRaw := f.String("expected-version", "", "expected current version")
		asPlugin := f.String("as-plugin", "", "installed plugin id")
		var refs stringList
		f.Var(&refs, "ref", "source event UUID, repeatable")
		if err := f.Parse(args[1:]); err != nil {
			return err
		}
		if err := a.requiredProject(projectID); err != nil {
			return err
		}
		if f.NArg() != 1 {
			return fmt.Errorf("state put requires one KEY")
		}
		if *contentPath == "" {
			return fmt.Errorf("--content is required")
		}
		content, err := readPathOrStdin(a.io.in, *contentPath, v2.MaxStateBytes)
		if err != nil {
			return err
		}
		var data json.RawMessage
		if *dataRaw != "" {
			if !json.Valid([]byte(*dataRaw)) {
				return fmt.Errorf("--data must be JSON")
			}
			data = json.RawMessage(*dataRaw)
		}
		var expected *int64
		if *expectedRaw != "" {
			n, e := strconv.ParseInt(*expectedRaw, 10, 64)
			if e != nil || n < 0 {
				return fmt.Errorf("--expected-version must be nonnegative")
			}
			expected = &n
		}
		in := v2.PutStateInput{Key: f.Arg(0), ExpectedVersion: expected, Content: v2.StateContent{Format: *format, Text: string(content)}, Data: data, BasedOnSequence: *based, Refs: refs, AsPluginID: *asPlugin}
		out, err := a.client.PutState(a.ctx, *projectID, in)
		return a.result(out, err)
	default:
		return fmt.Errorf("unknown state command %q", command)
	}
}

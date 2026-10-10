package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"event-driven-context/internal/v2"
)

func (a *app) query(args []string) error {
	f := a.flags("query")
	projectID := f.String("project", "", "project ID")
	types := f.String("types", "", "comma-separated types")
	metadata := f.String("metadata", "{}", "metadata filter JSON")
	source := f.String("source", "{}", "source filter JSON")
	refsTo := f.String("refs-to", "", "referenced event UUID")
	after := f.Int64("after", 0, "after sequence")
	order := f.String("order", "asc", "sequence order: asc or desc")
	from := f.String("from", "", "inclusive RFC3339")
	to := f.String("to", "", "exclusive RFC3339")
	timeField := f.String("time-field", "recorded_at", "recorded_at or occurred_at")
	limit := f.Int("limit", 50, "page size")
	cursor := f.String("cursor", "", "snapshot cursor")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	meta, err := objectWithPairs(*metadata, nil, true)
	if err != nil {
		return err
	}
	src, err := objectWithPairs(*source, nil, true)
	if err != nil {
		return err
	}
	var typeList []string
	if *types != "" {
		typeList = strings.Split(*types, ",")
	}
	out, err := a.client.QueryEvents(a.ctx, *projectID, v2.QueryEventsInput{Types: typeList, Metadata: meta, Source: src, RefsTo: *refsTo, AfterSequence: *after, Order: *order, From: *from, To: *to, TimeField: *timeField, Limit: *limit, Cursor: *cursor})
	return a.result(out, err)
}

func (a *app) get(args []string) error {
	f := a.flags("get")
	projectID := f.String("project", "", "project ID")
	if err := f.Parse(args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if f.NArg() != 1 {
		return fmt.Errorf("get requires one EVENT_ID")
	}
	out, err := a.client.GetEvent(a.ctx, *projectID, f.Arg(0))
	return a.result(out, err)
}

func (a *app) metadata(args []string) error {
	f := a.flags("metadata")
	projectID := f.String("project", "", "project ID")
	key := f.String("key", "", "metadata key")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	out, err := a.client.ListMetadata(a.ctx, *projectID, *key)
	return a.result(out, err)
}

func (a *app) pull(args []string) error {
	f := a.flags("pull")
	projectID := f.String("project", "", "project ID")
	after := f.Int64("after", 0, "after sequence")
	follow := f.Bool("follow", false, "continue watching")
	if err := parse(f, args); err != nil {
		return err
	}
	if err := a.requiredProject(projectID); err != nil {
		return err
	}
	if *follow {
		return fmt.Errorf("pull --follow is not implemented in this V2 CLI build")
	}
	cursor := ""
	encoder := json.NewEncoder(a.io.out)
	for {
		page, err := a.client.QueryEvents(a.ctx, *projectID, v2.QueryEventsInput{AfterSequence: *after, Limit: 100, Cursor: cursor})
		if err != nil {
			return err
		}
		for _, event := range page.Events {
			if err := encoder.Encode(event); err != nil {
				return err
			}
		}
		if page.NextCursor == "" {
			return nil
		}
		cursor = page.NextCursor
	}
}

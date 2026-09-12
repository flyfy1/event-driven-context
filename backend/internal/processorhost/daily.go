package processorhost

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"event-driven-context/internal/v2"
	"event-driven-context/internal/v2client"
)

type dailyCursorData struct {
	AfterSequence     int64    `json:"after_sequence"`
	LastScheduledDate string   `json:"last_scheduled_date,omitempty"`
	AttemptDate       string   `json:"attempt_date,omitempty"`
	Attempts          int      `json:"attempts,omitempty"`
	Completed         bool     `json:"completed,omitempty"`
	SkippedDates      []string `json:"skipped_dates,omitempty"`
}

type dailyStateData struct {
	ScheduledAt  string   `json:"scheduled_at"`
	WindowFrom   string   `json:"window_from"`
	WindowTo     string   `json:"window_to"`
	Timezone     string   `json:"timezone"`
	SkippedDates []string `json:"skipped_dates,omitempty"`
}

type dailyPeriod struct {
	Date string
	At   time.Time
	From time.Time
	To   time.Time
	Zone string
}

func runDaily(ctx context.Context, client *v2client.Client, opts Options, root string, spec processorSpec, installation v2.Installation, result Result) (Result, error) {
	zone := installation.ProjectTimezone
	if zone == "" {
		zone = "UTC"
	}
	runTime := spec.Schedule.Time
	var config struct {
		Time     string `json:"time"`
		Language string `json:"language"`
	}
	if len(installation.Config) > 0 {
		_ = json.Unmarshal(installation.Config, &config)
		if config.Time != "" {
			runTime = config.Time
		}
	}
	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}
	period, err := latestDailyPeriod(now, zone, runTime)
	if err != nil {
		return result, err
	}
	result.ScheduledDate = period.Date
	result.ScheduledAt = period.At.Format(time.RFC3339)
	result.WindowFrom = period.From.Format(time.RFC3339)
	result.WindowTo = period.To.Format(time.RFC3339)
	result.Timezone = period.Zone

	cursor, cursorExists, err := optionalState(ctx, client, opts.ProjectID, opts.PluginID+"/_cursor")
	if err != nil {
		return result, err
	}
	data, err := parseDailyCursor(cursor, cursorExists)
	if err != nil {
		return result, err
	}
	result.SkippedDates = skippedDates(data, period)
	stateKey := opts.PluginID + "/" + period.Date
	state, stateExists, err := optionalState(ctx, client, opts.ProjectID, stateKey)
	if err != nil {
		return result, err
	}
	if stateExists {
		result.StateVersion, result.ThroughSequence = state.Version, state.BasedOnSequence
		result.Noop, result.Reason = true, "already_published"
		var published dailyStateData
		if len(state.Data) > 0 && strictJSON(state.Data, &published) == nil {
			result.ScheduledAt, result.WindowFrom, result.WindowTo = published.ScheduledAt, published.WindowFrom, published.WindowTo
			result.Timezone = published.Timezone
			result.SkippedDates = append([]string(nil), published.SkippedDates...)
		}
		if data.LastScheduledDate != period.Date || !data.Completed {
			data.AfterSequence = max64(data.AfterSequence, state.BasedOnSequence)
			data.LastScheduledDate, data.AttemptDate, data.Completed = period.Date, period.Date, true
			data.SkippedDates = appendUnique(data.SkippedDates, result.SkippedDates...)
			written, putErr := putDailyCursor(ctx, client, opts.ProjectID, opts.PluginID, cursor, cursorExists, data)
			if putErr != nil {
				return result, fmt.Errorf("recover daily cursor: %w", putErr)
			}
			result.CursorVersion = written.Version
		}
		return result, nil
	}

	attempts := 0
	if data.AttemptDate == period.Date {
		attempts = data.Attempts
	}
	maxRuns := spec.Limits.MaxRunsPerDay
	if maxRuns <= 0 {
		maxRuns = 1
	}
	if attempts >= maxRuns {
		result.Noop, result.Reason = true, "max_runs_per_day"
		return result, nil
	}
	events, _, err := queryWindow(ctx, client, opts.ProjectID, spec.Input.Types, period.From, period.To)
	if err != nil {
		return result, err
	}
	result.ProcessedEvents = len(events)
	based := data.AfterSequence
	refs := make([]string, 0, len(events))
	for _, event := range events {
		if event.Sequence > based {
			based = event.Sequence
		}
		refs = append(refs, event.ID)
	}
	result.ThroughSequence = based
	data.AttemptDate, data.Attempts, data.Completed = period.Date, attempts+1, false
	data.SkippedDates = appendUnique(data.SkippedDates, result.SkippedDates...)
	attemptCursor, err := putDailyCursor(ctx, client, opts.ProjectID, opts.PluginID, cursor, cursorExists, data)
	if err != nil {
		return result, fmt.Errorf("record daily attempt: %w", err)
	}
	cursor, cursorExists = attemptCursor, true
	result.CursorVersion = attemptCursor.Version

	content := v2.StateContent{Format: "markdown"}
	if len(events) == 0 {
		if strings.HasPrefix(strings.ToLower(config.Language), "zh") {
			content.Text = "## 覆盖范围\n今天没有新记录。"
		} else {
			content.Text = "## Coverage\nThere were no new records in today's scheduled review window."
		}
		refs = nil
	} else {
		candidate, runErr := runAgent(ctx, opts, root, spec, installation, events, v2.State{}, false, agentTarget{
			Name: period.Date, Date: period.Date, StateKey: stateKey,
			Window: &agentWindow{From: period.From.Format(time.RFC3339), To: period.To.Format(time.RFC3339), Timezone: period.Zone},
		})
		if runErr != nil {
			return result, runErr
		}
		content = v2.StateContent{Format: candidate.State.Format, Text: candidate.State.Text}
		refs = candidate.State.SourceEventIDs
	}
	expected := int64(0)
	stateData, _ := json.Marshal(dailyStateData{ScheduledAt: result.ScheduledAt, WindowFrom: result.WindowFrom, WindowTo: result.WindowTo, Timezone: period.Zone, SkippedDates: result.SkippedDates})
	written, err := client.PutState(ctx, opts.ProjectID, v2.PutStateInput{Key: stateKey, ExpectedVersion: &expected, Content: content, Data: stateData, BasedOnSequence: based, Refs: refs})
	if err != nil {
		return result, fmt.Errorf("publish daily review: %w", err)
	}
	result.StateVersion = written.Version

	data.AfterSequence, data.LastScheduledDate, data.Completed = based, period.Date, true
	writtenCursor, err := putDailyCursor(ctx, client, opts.ProjectID, opts.PluginID, cursor, cursorExists, data)
	if err != nil {
		return result, fmt.Errorf("complete daily cursor: %w", err)
	}
	result.CursorVersion = writtenCursor.Version
	return result, nil
}

func latestDailyPeriod(now time.Time, zone, clock string) (dailyPeriod, error) {
	location, err := time.LoadLocation(zone)
	if err != nil {
		return dailyPeriod{}, fmt.Errorf("invalid project timezone %q", zone)
	}
	parts := strings.Split(clock, ":")
	if len(parts) != 2 {
		return dailyPeriod{}, fmt.Errorf("daily schedule time must be HH:MM")
	}
	hour, hourErr := strconv.Atoi(parts[0])
	minute, minuteErr := strconv.Atoi(parts[1])
	if hourErr != nil || minuteErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 || len(parts[0]) != 2 || len(parts[1]) != 2 {
		return dailyPeriod{}, fmt.Errorf("daily schedule time must be HH:MM")
	}
	local := now.In(location)
	due := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, location)
	if local.Before(due) {
		previous := due.AddDate(0, 0, -1)
		due = time.Date(previous.Year(), previous.Month(), previous.Day(), hour, minute, 0, 0, location)
	}
	previous := due.AddDate(0, 0, -1)
	from := time.Date(previous.Year(), previous.Month(), previous.Day(), hour, minute, 0, 0, location)
	return dailyPeriod{Date: due.Format("2006-01-02"), At: due, From: from, To: due, Zone: zone}, nil
}

func parseDailyCursor(state v2.State, exists bool) (dailyCursorData, error) {
	if !exists {
		return dailyCursorData{}, nil
	}
	data := dailyCursorData{}
	if len(state.Data) > 0 {
		if err := strictJSON(state.Data, &data); err != nil {
			return data, fmt.Errorf("daily cursor data is invalid")
		}
	} else {
		n, err := strconv.ParseInt(strings.TrimSpace(state.Content.Text), 10, 64)
		if err != nil || n < 0 {
			return data, fmt.Errorf("daily cursor state is invalid")
		}
		data.AfterSequence = n
	}
	if data.AfterSequence < 0 || data.Attempts < 0 {
		return data, fmt.Errorf("daily cursor state is invalid")
	}
	return data, nil
}

func putDailyCursor(ctx context.Context, client *v2client.Client, projectID, pluginID string, current v2.State, exists bool, data dailyCursorData) (v2.State, error) {
	expected := int64(0)
	if exists {
		expected = current.Version
	}
	raw, _ := json.Marshal(data)
	return client.PutState(ctx, projectID, v2.PutStateInput{Key: pluginID + "/_cursor", ExpectedVersion: &expected, Content: v2.StateContent{Format: "text", Text: strconv.FormatInt(data.AfterSequence, 10)}, Data: raw, BasedOnSequence: data.AfterSequence})
}

func queryWindow(ctx context.Context, client *v2client.Client, projectID string, types []string, from, to time.Time) ([]v2.Event, int64, error) {
	var events []v2.Event
	cursor := ""
	latest := int64(0)
	for {
		page, err := client.QueryEvents(ctx, projectID, v2.QueryEventsInput{Types: types, From: from.Format(time.RFC3339Nano), To: to.Format(time.RFC3339Nano), TimeField: "recorded_at", Limit: 100, Cursor: cursor})
		if err != nil {
			return nil, 0, fmt.Errorf("query daily window: %w", err)
		}
		if page.LatestSequence > latest {
			latest = page.LatestSequence
		}
		events = append(events, page.Events...)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return events, latest, nil
}

func skippedDates(data dailyCursorData, period dailyPeriod) []string {
	anchor := data.LastScheduledDate
	if anchor == "" {
		anchor = data.AttemptDate
	}
	if anchor == "" || anchor >= period.Date {
		return nil
	}
	location, _ := time.LoadLocation(period.Zone)
	date, err := time.ParseInLocation("2006-01-02", anchor, location)
	if err != nil {
		return nil
	}
	var skipped []string
	for next := date.AddDate(0, 0, 1); next.Format("2006-01-02") < period.Date; next = next.AddDate(0, 0, 1) {
		skipped = append(skipped, next.Format("2006-01-02"))
	}
	return skipped
}

func appendUnique(values []string, more ...string) []string {
	seen := map[string]bool{}
	for _, v := range values {
		seen[v] = true
	}
	for _, v := range more {
		if !seen[v] {
			values = append(values, v)
			seen[v] = true
		}
	}
	return values
}
func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

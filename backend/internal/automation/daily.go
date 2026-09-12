package automation

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"event-driven-context/internal/core"
)

const maxDailyInputBytes = 1 << 20

func (c *Coordinator) generateDailyRunsLocked(installation Installation, snapshot core.AutomationSnapshot) error {
	revision, ok := findRevision(installation, installation.CurrentRevisionID)
	if !ok || revision.Trigger.Type != "daily" {
		return fmt.Errorf("daily installation revision is missing")
	}
	location, err := time.LoadLocation(revision.Trigger.Timezone)
	if err != nil {
		return err
	}
	hour, minute, err := parseLocalTime(revision.Trigger.LocalTime)
	if err != nil {
		return err
	}
	activatedAt, err := parseStoredTime(revision.ActivatedAt)
	if err != nil {
		return err
	}
	last := time.Time{}
	if installation.LastDailySlot != "" {
		last, err = parseStoredTime(installation.LastDailySlot)
		if err != nil {
			return err
		}
	}
	dueSlots, err := dueDailySlots(activatedAt, last, c.now(), location, hour, minute)
	if err != nil || len(dueSlots) == 0 {
		return err
	}
	for index, due := range dueSlots {
		current := cloneInstallation(installation)
		current.LastDailySlot = due.Format(time.RFC3339Nano)
		current.UpdatedAt = c.nowString()
		status := RunSkipped
		inputs := []RunInput{}
		selection := dailyInputSelection{}
		windowStart, err := dailySlotForDate(due.In(location).AddDate(0, 0, -1), location, hour, minute)
		if err != nil {
			return err
		}
		if index == len(dueSlots)-1 {
			status = RunQueued
			selection = selectDailyInputs(snapshot, windowStart, due)
			inputs = selection.Inputs
		}
		run := c.newRun(current, revision, "daily", 1, snapshot.SnapshotSequence, inputs)
		run.WindowStart = windowStart.UTC().Format(time.RFC3339Nano)
		run.WindowEnd = due.UTC().Format(time.RFC3339Nano)
		run.Slot = due.UTC().Format(time.RFC3339Nano)
		if index == len(dueSlots)-1 {
			run.Inputs = selection.Inputs
			run.EligibleInputCount = selection.Eligible
			run.OmittedInputCount = selection.Omitted
			run.InputsTruncated = selection.Omitted != 0
			inputs = selection.Inputs
		}
		run.Status = status
		if status == RunSkipped {
			run.FailureCode = "skipped_misfire"
		} else if len(inputs) == 0 {
			run.Status = RunSucceeded
			run.NoOutputReason = "no_relevant_records"
		}
		key := current.ID + "\x00" + revision.ID + "\x00" + run.Slot
		if existing, exists := c.state.Dedupe[dedupeKey("daily", key)]; exists {
			_ = existing
			installation = current
			continue
		}
		if err = writeJSONOnce(c.runRequestPath(run), run); err != nil {
			return err
		}
		action := "run.daily_triggered"
		if status == RunSkipped {
			action = "run.daily_misfire_skipped"
		} else if run.Status == RunSucceeded {
			action = "run.daily_no_output"
		}
		if err = c.appendJournal(journalEntry{Action: action, ActorType: "system", ActorID: "dispatcher", Installation: &current, Runs: []Run{run}, DedupeKind: "daily", DedupeKey: key, DedupeRunID: run.ID}); err != nil {
			return err
		}
		installation = current
	}
	return nil
}

func parseLocalTime(value string) (int, int, error) {
	var hour, minute int
	if _, err := fmt.Sscanf(value, "%02d:%02d", &hour, &minute); err != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("invalid stored daily local time")
	}
	return hour, minute, nil
}

// dueDailySlots returns every unrecorded slot through now. The caller records
// all but the newest as skipped, so a restart catches up only the newest run.
func dueDailySlots(activatedAt, last, now time.Time, location *time.Location, hour, minute int) ([]time.Time, error) {
	startLocal := activatedAt.In(location)
	if !last.IsZero() {
		startLocal = last.In(location).AddDate(0, 0, 1)
	}
	endLocal := now.In(location)
	if startLocal.After(endLocal) {
		return nil, nil
	}
	if days := int(endLocal.Sub(startLocal).Hours()/24) + 3; days > 3660 {
		return nil, fmt.Errorf("daily slot recovery exceeds ten years")
	}
	out := []time.Time{}
	for date := localDate(startLocal, location); !date.After(localDate(endLocal, location)); date = date.AddDate(0, 0, 1) {
		slot, err := dailySlotForDate(date, location, hour, minute)
		if err != nil {
			return nil, err
		}
		if !slot.After(activatedAt) || (!last.IsZero() && !slot.After(last)) || slot.After(now) {
			continue
		}
		out = append(out, slot.UTC())
	}
	return out, nil
}

func localDate(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, location)
}

// dailySlotForDate chooses the first occurrence during a repeated DST minute.
// If a wall minute does not exist, it chooses the first later wall minute that
// day, which is the documented spring-forward behavior.
func dailySlotForDate(date time.Time, location *time.Location, hour, minute int) (time.Time, error) {
	year, month, day := date.In(location).Date()
	anchor := time.Date(year, month, day, 12, 0, 0, 0, location).UTC()
	start := anchor.Add(-18 * time.Hour)
	end := anchor.Add(18 * time.Hour)
	var shifted *time.Time
	for instant := start; !instant.After(end); instant = instant.Add(time.Minute) {
		local := instant.In(location)
		if local.Year() != year || local.Month() != month || local.Day() != day {
			continue
		}
		wall := local.Hour()*60 + local.Minute()
		target := hour*60 + minute
		if wall == target {
			return instant, nil
		}
		if wall > target && shifted == nil {
			copy := instant
			shifted = &copy
		}
	}
	if shifted != nil {
		return *shifted, nil
	}
	return time.Time{}, fmt.Errorf("could not resolve daily slot")
}

type dailyInputSelection struct {
	Inputs   []RunInput
	Eligible int
	Omitted  int
}

func selectDailyInputs(snapshot core.AutomationSnapshot, start, end time.Time) dailyInputSelection {
	type candidate struct {
		recorded time.Time
		sequence int64
		event    core.Event
	}
	values := []candidate{}
	for _, record := range snapshot.Records {
		event := record.Event
		if event.Content.Kind != "text" || event.Content.Text == nil || strings.TrimSpace(*event.Content.Text) == "" {
			continue
		}
		// Daily reviews and their suggestion slots never feed another daily
		// review, avoiding recursive amplification.
		if event.Provenance.SkillID == DailyReviewSkill {
			continue
		}
		recorded, err := time.Parse(time.RFC3339Nano, event.RecordedAt)
		if err != nil || recorded.Before(start) || !recorded.Before(end) {
			continue
		}
		values = append(values, candidate{recorded: recorded, sequence: record.Sequence, event: event})
	}
	// When both an original text and a transcript refer to the same source,
	// prefer the transcript and include that source only once.
	sort.Slice(values, func(i, j int) bool { return values[i].sequence > values[j].sequence })
	seenRoots := map[string]bool{}
	deduplicated := []candidate{}
	for _, value := range values {
		root := value.event.ID
		if value.event.Provenance.Kind == core.ProvenanceTranscript && len(value.event.Provenance.SourceEventIDs) != 0 {
			root = value.event.Provenance.SourceEventIDs[0]
		}
		if seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		deduplicated = append(deduplicated, value)
	}
	selected := []candidate{}
	total := 0
	for _, value := range deduplicated {
		size := len(*value.event.Content.Text)
		if len(selected) >= 128 || total+size > maxDailyInputBytes {
			continue
		}
		total += size
		selected = append(selected, value)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].sequence < selected[j].sequence })
	out := make([]RunInput, 0, len(selected))
	for _, value := range selected {
		out = append(out, RunInput{EventID: value.event.ID})
	}
	return dailyInputSelection{Inputs: out, Eligible: len(deduplicated), Omitted: len(deduplicated) - len(out)}
}

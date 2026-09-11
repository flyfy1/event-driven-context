package automation

import (
	"context"
	"errors"
	"sort"

	"event-driven-context/internal/core"
	"event-driven-context/internal/runner"
)

type InboxEntries struct {
	Entries []InboxEntry `json:"entries"`
}

type Runs struct {
	Runs []Run `json:"runs"`
}

func (c *Coordinator) ListInbox(ctx context.Context) (InboxEntries, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	userID := core.UserID(ctx)
	if userID == "" {
		return InboxEntries{}, core.ErrUnauthenticated
	}
	out := InboxEntries{Entries: []InboxEntry{}}
	for _, entry := range c.state.Inbox {
		if entry.RecipientUserID != userID {
			continue
		}
		if _, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: entry.ProjectID}); err == nil {
			out.Entries = append(out.Entries, cloneInbox(entry))
		} else if !errors.Is(err, core.ErrNotFound) {
			return InboxEntries{}, err
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].CreatedAt > out.Entries[j].CreatedAt })
	return out, nil
}

func (c *Coordinator) ReadInbox(ctx context.Context, entryID string) (InboxEntry, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.state.Inbox[entryID]
	if !ok || entry.RecipientUserID != core.UserID(ctx) {
		return InboxEntry{}, core.ErrNotFound
	}
	if _, err := c.store.AutomationSnapshot(ctx, core.AutomationSnapshotInput{ProjectID: entry.ProjectID}); err != nil {
		return InboxEntry{}, err
	}
	if entry.ReadAt != "" {
		return cloneInbox(entry), nil
	}
	entry = cloneInbox(entry)
	entry.ReadAt = c.nowString()
	if err := c.appendJournal(journalEntry{Action: "inbox.read", ActorType: "user", ActorID: entry.RecipientUserID, Inbox: &entry}); err != nil {
		return InboxEntry{}, err
	}
	return entry, nil
}

func cloneInbox(in InboxEntry) InboxEntry {
	out := in
	out.OutputEventIDs = append([]string(nil), in.OutputEventIDs...)
	out.Candidate.SourceEventIDs = append([]string(nil), in.Candidate.SourceEventIDs...)
	out.Candidate.Items = append([]runner.CandidateItem(nil), in.Candidate.Items...)
	for i := range out.Candidate.Items {
		out.Candidate.Items[i].SourceEventIDs = append([]string(nil), in.Candidate.Items[i].SourceEventIDs...)
	}
	return out
}

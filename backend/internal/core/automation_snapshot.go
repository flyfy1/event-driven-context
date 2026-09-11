package core

import "context"

// AutomationSnapshotInput fixes the event watermark used by one automation run.
// ThroughSequence zero selects the current project watermark.
type AutomationSnapshotInput struct {
	ProjectID       string `json:"project_id"`
	ThroughSequence int64  `json:"through_sequence,omitempty"`
}

// AutomationRecord deliberately exposes only the immutable sequence and Event.
// Idempotency keys and internal request hashes remain private to the Store.
type AutomationRecord struct {
	Sequence int64 `json:"sequence"`
	Event    Event `json:"event"`
}

type AutomationSnapshot struct {
	ProjectID        string             `json:"project_id"`
	SnapshotSequence int64              `json:"snapshot_sequence"`
	Records          []AutomationRecord `json:"records"`
}

// AutomationSnapshot returns a member-authorized, immutable project snapshot.
// It is an internal coordinator boundary and is not registered as an HTTP/MCP API.
func (s *Store) AutomationSnapshot(ctx context.Context, in AutomationSnapshotInput) (AutomationSnapshot, error) {
	out := AutomationSnapshot{ProjectID: in.ProjectID, Records: []AutomationRecord{}}
	if in.ThroughSequence < 0 {
		return out, Invalid("through_sequence must be non-negative")
	}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	records, err := s.loadProjectEvents(in.ProjectID)
	if err != nil {
		return out, err
	}
	current := int64(0)
	if len(records) != 0 {
		current = records[len(records)-1].Sequence
	}
	watermark := in.ThroughSequence
	if watermark == 0 {
		watermark = current
	} else if watermark > current {
		return out, Invalid("through_sequence exceeds current project sequence")
	}
	out.SnapshotSequence = watermark
	for _, record := range records {
		if record.Sequence > watermark {
			break
		}
		out.Records = append(out.Records, AutomationRecord{Sequence: record.Sequence, Event: record.Event})
	}
	return out, nil
}

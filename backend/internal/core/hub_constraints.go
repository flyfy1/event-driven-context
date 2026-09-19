package core

import (
	"context"
	"strings"
	"time"
)

// Constraints are authorization boundaries, not agent-provided search hints.
type HubConstraints struct {
	CalendarID string `json:"calendar_id"`
	TimeMin    string `json:"time_min"`
	TimeMax    string `json:"time_max"`
}

func HubCalendarOperation(provider, operation string) bool {
	return (provider == "google-calendar" || provider == "microsoft-calendar") && (operation == "events.list" || operation == "freebusy.query")
}
func validateHubConstraints(provider, operation string, c *HubConstraints) error {
	if !HubCalendarOperation(provider, operation) {
		if c != nil {
			return Invalid("constraints are not supported for this operation")
		}
		return nil
	}
	if c == nil || !hubText(c.CalendarID, 256) || strings.ContainsAny(c.CalendarID, "/\\%?") {
		return Invalid("calendar_id and a bounded date range are required")
	}
	start, e1 := time.Parse(time.RFC3339, c.TimeMin)
	end, e2 := time.Parse(time.RFC3339, c.TimeMax)
	if e1 != nil || e2 != nil || !end.After(start) || end.Sub(start) > 7*24*time.Hour {
		return Invalid("calendar date window must be positive and at most seven days")
	}
	c.TimeMin = start.UTC().Format(time.RFC3339Nano)
	c.TimeMax = end.UTC().Format(time.RFC3339Nano)
	return nil
}
func sameHubConstraints(a, b *HubConstraints) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// CheckHubAccessArgs is called before the upstream read and again before delivery.
// It must not treat a grant for a different calendar or time window as authority.
func (s *Store) CheckHubAccessArgs(ctx context.Context, a HubAgent, connection, operation string, args map[string]any) error {
	if err := s.CheckHubAccess(ctx, a, connection, operation); err != nil {
		return err
	}
	c, err := s.HubConnection(ctx, a.OwnerID, connection)
	if err != nil {
		return err
	}
	if !HubCalendarOperation(c.ProviderID, operation) {
		return nil
	}
	id, _ := args["calendar_id"].(string)
	lo, _ := args["time_min"].(string)
	hi, _ := args["time_max"].(string)
	requested := &HubConstraints{CalendarID: id, TimeMin: lo, TimeMax: hi}
	if err := validateHubConstraints(c.ProviderID, operation, requested); err != nil {
		return err
	}
	start, _ := time.Parse(time.RFC3339, requested.TimeMin)
	end, _ := time.Parse(time.RFC3339, requested.TimeMax)
	grants, err := s.HubRequests(ctx, a.OwnerID, a.ID)
	if err != nil {
		return err
	}
	for _, grant := range grants {
		if grant.ConnectionID != connection || grant.Operation != operation || grant.Status != "approved" || grant.Constraints == nil {
			continue
		}
		bounds := *grant.Constraints
		if validateHubConstraints(c.ProviderID, operation, &bounds) != nil || bounds.CalendarID != id {
			continue
		}
		allowedStart, _ := time.Parse(time.RFC3339, bounds.TimeMin)
		allowedEnd, _ := time.Parse(time.RFC3339, bounds.TimeMax)
		if !start.Before(allowedStart) && !end.After(allowedEnd) {
			return nil
		}
	}
	return &Error{Code: "authorization_required", Message: "Request approval for this exact calendar and data date range"}
}

package core

import "testing"

func TestHubCalendarConstraintsAreEnforcedAndRevocable(t *testing.T) {
	s, owner, _, registration, key := hubFixture(t)
	if err := s.DecideHubAgent(owner, registration.ID, "approve", registration.VerificationCode); err != nil {
		t.Fatal(err)
	}
	a, err := s.HubAgent(owner, registration.Token, true)
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.AddHubConnection(owner, HubConnection{ProviderID: "google-calendar", AccountID: "work", DisplayName: "Work"}, "test-token", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RequestHubAccess(owner, a, c.ID, "events.list", "Read agenda", 3600, nil); err == nil {
		t.Fatal("unconstrained calendar grant accepted")
	}
	bounds := &HubConstraints{CalendarID: "primary", TimeMin: "2026-09-21T00:00:00Z", TimeMax: "2026-09-22T00:00:00Z"}
	r, err := s.RequestHubAccess(owner, a, c.ID, "events.list", "Read agenda", 3600, bounds)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DecideHubRequest(owner, r.ID, "approve"); err != nil {
		t.Fatal(err)
	}
	valid := map[string]any{"calendar_id": "primary", "time_min": "2026-09-21T02:00:00Z", "time_max": "2026-09-21T03:00:00Z"}
	if err = s.CheckHubAccessArgs(owner, a, c.ID, "events.list", valid); err != nil {
		t.Fatal(err)
	}
	for _, v := range []map[string]any{
		{"calendar_id": "personal", "time_min": bounds.TimeMin, "time_max": bounds.TimeMax},
		{"calendar_id": "primary", "time_min": "2026-09-20T00:00:00Z", "time_max": bounds.TimeMax},
		{"calendar_id": "primary", "time_min": bounds.TimeMin, "time_max": "2026-09-23T00:00:00Z"},
	} {
		if s.CheckHubAccessArgs(owner, a, c.ID, "events.list", v) == nil {
			t.Fatal("grant bypass", v)
		}
	}
	second, err := s.RequestHubAccess(owner, a, c.ID, "events.list", "Other day", 3600, &HubConstraints{CalendarID: "primary", TimeMin: "2026-09-22T00:00:00Z", TimeMax: "2026-09-23T00:00:00Z"})
	if err != nil || second.ID == r.ID {
		t.Fatal("distinct constraints deduplicated", err)
	}
	if err = s.DecideHubRequest(owner, r.ID, "revoke"); err != nil {
		t.Fatal(err)
	}
	if s.CheckHubAccessArgs(owner, a, c.ID, "events.list", valid) == nil {
		t.Fatal("revoked grant allowed")
	}
	requests, err := s.HubRequests(owner, a.OwnerID, a.ID)
	if err != nil || len(requests) != 2 || requests[0].Constraints == nil {
		t.Fatal("constraints not persisted", err)
	}
}

func TestHubCalendarLiteralIDsAndFractionalBounds(t *testing.T) {
	bounds := &HubConstraints{CalendarID: "en.singapore#holiday@group.v.calendar.google.com", TimeMin: "2026-09-21T00:00:00.1Z", TimeMax: "2026-09-21T00:00:00.2Z"}
	if err := validateHubConstraints("google-calendar", "events.list", bounds); err != nil {
		t.Fatal(err)
	}
	if bounds.TimeMin != "2026-09-21T00:00:00.1Z" || bounds.TimeMax != "2026-09-21T00:00:00.2Z" {
		t.Fatal("fractional scope truncated")
	}
}

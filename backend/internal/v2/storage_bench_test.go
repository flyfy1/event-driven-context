package v2

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"event-driven-context/internal/core"
)

// BenchmarkRecordEventAtScale measures one durable storage write at a fixed
// history size, including validation, locking, serialization, rename and fsync.
// Seed construction and persistence are excluded, as are identity/HTTP overhead.
func BenchmarkRecordEventAtScale(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("events_%d", count), func(b *testing.B) {
			root := b.TempDir()
			identity, err := core.Open(filepath.Join(root, "identity.db"), filepath.Join(root, "legacy"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = identity.Close() })
			s, err := New(identity, filepath.Join(root, "data"))
			if err != nil {
				b.Fatal(err)
			}
			b.Cleanup(func() { _ = s.Close() })
			p := s.projectLocked("benchmark")
			auth := eventAuthority{actor: Actor{Type: "user", ID: "benchmark", Username: "benchmark"}}
			for i := 0; i < count; i++ {
				input := textInput(fmt.Sprintf("%08x-1111-4111-8111-111111111111", i), strings.Repeat("x", 400))
				p.Events = append(p.Events, Event{ID: input.ID, ProjectID: "benchmark", Type: input.Type, Content: input.Content, Metadata: input.Metadata, Source: input.Source, Sequence: int64(i + 1), RecordedAt: "2026-10-10T00:00:00Z", Actor: auth.actor})
			}
			p.LatestSequence = int64(count)
			if err := s.persistLocked(); err != nil {
				b.Fatal(err)
			}
			baseline := s.data
			input := RecordEventsInput{Events: []EventInput{textInput("ffffffff-1111-4111-8111-111111111111", strings.Repeat("x", 400))}}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Candidates never mutate baseline; keep every iteration at count+1 events.
				s.data = baseline
				result, err := s.recordEvents("benchmark", input, auth)
				if err != nil || len(result.Results) != 1 || result.Results[0].Status != "created" {
					b.Fatalf("record: %#v, %v", result, err)
				}
			}
		})
	}
}

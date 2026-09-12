package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxContextOutputBytes = 24_000
	maxContextTerms       = 64
)

type rankedContextEvidence struct {
	evidence ContextEvidence
	sequence int64
	score    int
	kind     string
	state    string
}

type selectedContextEvent struct {
	score  int
	reason string
}

func (s *Store) QueryContext(ctx context.Context, in ContextQueryInput) (ContextQueryResult, error) {
	out := ContextQueryResult{
		ProjectID:   in.ProjectID,
		Evidence:    []ContextEvidence{},
		Suggestions: []ContextEvidence{},
		Conflicts:   []ContextConflict{},
		Warnings:    []string{"keyword_retrieval", "explicit_relations_only"},
		Coverage:    ContextCoverage{ConflictDetection: "explicit_update_forks_only"},
	}
	if err := s.requireMember(ctx, in.ProjectID); err != nil {
		return out, err
	}
	in.Query = strings.TrimSpace(in.Query)
	if in.Query == "" || len(in.Query) > 4096 || !utf8.ValidString(in.Query) {
		return out, Invalid("query must be UTF-8, 1-4096 bytes")
	}
	if in.MaxOutputBytes == 0 {
		in.MaxOutputBytes = MaxContextOutputBytes
	}
	if in.MaxOutputBytes < 512 || in.MaxOutputBytes > MaxContextOutputBytes {
		return out, Invalid("max_output_bytes must be 512-24000")
	}

	s.mu.Lock()
	records, err := s.loadProjectEvents(in.ProjectID)
	s.mu.Unlock()
	if err != nil {
		return out, err
	}
	if len(records) != 0 {
		out.Snapshot.Sequence = records[len(records)-1].Sequence
		out.Coverage.SnapshotSequence = out.Snapshot.Sequence
		out.Coverage.IndexedThroughSequence = out.Snapshot.Sequence
	}

	byID := make(map[string]storedEvent, len(records))
	transcribed := map[string]bool{}
	superseded := map[string]bool{}
	superseders := map[string][]string{}
	effectiveSuperseders := map[string][]string{}
	sourceChildren := map[string][]string{}
	for _, record := range records {
		byID[record.Event.ID] = record
		for _, sourceID := range record.Event.Provenance.SourceEventIDs {
			sourceChildren[sourceID] = append(sourceChildren[sourceID], record.Event.ID)
		}
		if record.Event.Provenance.Kind == ProvenanceTranscript {
			for _, sourceID := range record.Event.Provenance.SourceEventIDs {
				transcribed[sourceID] = true
			}
		}
		for _, targetID := range record.Event.Relations.SupersedesEventIDs {
			superseders[targetID] = append(superseders[targetID], record.Event.ID)
			target, ok := byID[targetID]
			if !ok {
				continue
			}
			if contextSupersessionApplies(record.Event, target.Event) {
				superseded[targetID] = true
				effectiveSuperseders[targetID] = append(effectiveSuperseders[targetID], record.Event.ID)
			}
		}
	}
	for _, record := range records {
		file := record.Event.Content.File
		if record.Event.Provenance.Kind == ProvenanceOriginal && file != nil && strings.HasPrefix(file.MediaType, "audio/") && !transcribed[record.Event.ID] {
			out.Coverage.PendingProcessingCount++
		}
	}
	if out.Coverage.PendingProcessingCount != 0 {
		out.Warnings = append(out.Warnings, "pending_audio")
	}
	terms := contextTerms(in.Query)
	texts := make(map[string]string, len(records))
	for _, record := range records {
		text, textErr := s.contextEventText(record)
		if textErr != nil {
			return out, textErr
		}
		texts[record.Event.ID] = text
	}
	selected := map[string]selectedContextEvent{}
	queue := []string{}
	for _, record := range records {
		score := contextMatchScore(texts[record.Event.ID], in.Query, terms)
		if score == 0 {
			continue
		}
		selected[record.Event.ID] = selectedContextEvent{score: score, reason: "keyword_match"}
		queue = append(queue, record.Event.ID)
	}
	for len(queue) != 0 {
		id := queue[0]
		queue = queue[1:]
		record := byID[id]
		neighbors := append([]string{}, record.Event.Relations.SupersedesEventIDs...)
		neighbors = append(neighbors, superseders[id]...)
		neighbors = append(neighbors, record.Event.Provenance.SourceEventIDs...)
		neighbors = append(neighbors, sourceChildren[id]...)
		for _, neighborID := range neighbors {
			if _, exists := byID[neighborID]; !exists {
				continue
			}
			if current, exists := selected[neighborID]; exists && current.score >= selected[id].score {
				continue
			}
			selected[neighborID] = selectedContextEvent{score: selected[id].score, reason: "relation_context"}
			queue = append(queue, neighborID)
		}
	}
	// Conflicts are scoped to the selected relationship closure. A project may
	// contain many unrelated forks; they must not consume this query's budget.
	for targetID, updateIDs := range effectiveSuperseders {
		if _, relevant := selected[targetID]; !relevant {
			continue
		}
		active := []string{}
		for _, updateID := range updateIDs {
			if !superseded[updateID] {
				active = append(active, updateID)
			}
		}
		if len(active) > 1 {
			sort.Strings(active)
			out.Conflicts = append(out.Conflicts, ContextConflict{Kind: "explicit_update_fork", TargetEventID: targetID, UpdateEventIDs: active})
		}
	}
	sort.Slice(out.Conflicts, func(i, j int) bool { return out.Conflicts[i].TargetEventID < out.Conflicts[j].TargetEventID })

	ranked := make([]rankedContextEvidence, 0, len(selected))
	for eventID, selection := range selected {
		record := byID[eventID]
		text := texts[eventID]
		if text == "" {
			continue
		}
		excerpt, excerptTruncated := contextExcerpt(text, in.Query, terms, 512)
		state := contextState(record.Event, superseded[record.Event.ID])
		ranked = append(ranked, rankedContextEvidence{
			evidence: ContextEvidence{
				EventID:          record.Event.ID,
				Excerpt:          excerpt,
				ExcerptTruncated: excerptTruncated,
				SourceEventIDs:   contextRootSources(record.Event.ID, byID, map[string]bool{}),
				Interpretation:   contextInterpretation(record.Event),
				State:            state,
				RetrievalReason:  selection.reason,
				RecordedAt:       record.Event.RecordedAt,
				OccurredAt:       record.Event.OccurredAt,
			},
			sequence: record.Sequence,
			score:    selection.score,
			kind:     record.Event.Provenance.Kind,
			state:    state,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if contextStateRank(ranked[i].state) != contextStateRank(ranked[j].state) {
			return contextStateRank(ranked[i].state) < contextStateRank(ranked[j].state)
		}
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].sequence != ranked[j].sequence {
			return ranked[i].sequence > ranked[j].sequence
		}
		return ranked[i].evidence.EventID < ranked[j].evidence.EventID
	})
	for _, item := range ranked {
		if item.kind == ProvenanceSuggestion {
			if in.IncludeSuggestions {
				out.Suggestions = append(out.Suggestions, item.evidence)
			}
			continue
		}
		out.Evidence = append(out.Evidence, item.evidence)
	}
	if err := fitContextBudget(&out, in.MaxOutputBytes); err != nil {
		return ContextQueryResult{}, err
	}
	return out, nil
}

func (s *Store) contextEventText(record storedEvent) (string, error) {
	if record.Event.Content.Text != nil {
		return strings.TrimSpace(*record.Event.Content.Text), nil
	}
	file := record.Event.Content.File
	if file == nil || file.SizeBytes > MaxContentBytes || !strings.HasPrefix(file.MediaType, "text/") {
		return "", nil
	}
	f, err := s.openVerifiedFile(record.Event.ProjectID, *file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxContentBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxContentBytes || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return "", fmt.Errorf("event text file %s has invalid content", file.ID)
	}
	return strings.TrimSpace(string(data)), nil
}

func contextRootSources(eventID string, records map[string]storedEvent, visiting map[string]bool) []string {
	record, ok := records[eventID]
	if !ok || visiting[eventID] {
		return []string{}
	}
	if record.Event.Provenance.Kind == ProvenanceOriginal || len(record.Event.Provenance.SourceEventIDs) == 0 {
		return []string{eventID}
	}
	visiting[eventID] = true
	roots := []string{}
	for _, sourceID := range record.Event.Provenance.SourceEventIDs {
		roots = append(roots, contextRootSources(sourceID, records, visiting)...)
	}
	delete(visiting, eventID)
	sort.Strings(roots)
	return uniqueStrings(roots)
}

func uniqueStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func contextStateRank(state string) int {
	switch state {
	case "confirmed", "active_evidence":
		return 0
	case "suggestion":
		return 1
	default:
		return 2
	}
}

func contextInterpretation(event Event) string {
	switch event.Provenance.Kind {
	case ProvenanceTranscript:
		return "transcript"
	case ProvenanceSummary:
		return "summary"
	case ProvenanceSuggestion:
		return "suggestion"
	case ProvenanceConfirmation:
		return "user_confirmation"
	default:
		if len(event.Relations.SupersedesEventIDs) != 0 {
			return "user_correction"
		}
		return "original_statement"
	}
}

func contextState(event Event, isSuperseded bool) string {
	if isSuperseded {
		return "superseded"
	}
	switch event.Provenance.Kind {
	case ProvenanceSuggestion:
		return "suggestion"
	case ProvenanceConfirmation:
		return "confirmed"
	default:
		return "active_evidence"
	}
}

func contextSupersessionApplies(update, target Event) bool {
	switch update.Provenance.Kind {
	case ProvenanceTranscript, ProvenanceSummary, ProvenanceSuggestion:
		// Derived versions replace only an earlier record from the same stage.
		// In particular, an unconfirmed suggestion cannot replace a commitment.
		return update.Provenance.Kind == target.Provenance.Kind
	default:
		return true
	}
}

func contextTerms(query string) []string {
	parts := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	seen := map[string]bool{}
	terms := []string{}
	for _, part := range parts {
		if len(terms) == maxContextTerms {
			break
		}
		if !seen[part] {
			seen[part] = true
			terms = append(terms, part)
		}
		runes := []rune(part)
		allHan := len(runes) > 2
		for _, r := range runes {
			allHan = allHan && unicode.Is(unicode.Han, r)
		}
		if allHan {
			for i := 0; i+1 < len(runes); i++ {
				if len(terms) == maxContextTerms {
					break
				}
				term := string(runes[i : i+2])
				if !seen[term] {
					seen[term] = true
					terms = append(terms, term)
				}
			}
		}
	}
	return terms
}

func contextMatchScore(text, query string, terms []string) int {
	normalized := strings.ToLower(text)
	score := 0
	if strings.Contains(normalized, strings.ToLower(query)) {
		score += 100
	}
	for _, term := range terms {
		if strings.Contains(normalized, term) {
			score++
		}
	}
	return score
}

func contextExcerpt(text, query string, terms []string, maxBytes int) (string, bool) {
	if len(text) <= maxBytes {
		return text, false
	}
	matchIndex, matchEnd, found := contextMatchBounds(text, query, terms)
	if !found {
		matchIndex, matchEnd = 0, 0
	}
	start := max(0, matchIndex-maxBytes/3)
	end := min(len(text), start+maxBytes)
	if matchEnd > end {
		end = min(len(text), matchEnd)
		start = max(0, end-maxBytes)
	}
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end > start && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	return strings.TrimSpace(text[start:end]), start > 0 || end < len(text)
}

func contextMatchBounds(text, query string, terms []string) (int, int, bool) {
	lowerText, lowerOffsets, originalOffsets := lowerStringWithOffsets(text)
	bestStart, bestEnd := -1, -1
	needles := make([]string, 0, len(terms)+1)
	needles = append(needles, query)
	needles = append(needles, terms...)
	for _, needle := range needles {
		lowerNeedle := strings.Map(unicode.ToLower, needle)
		if lowerNeedle == "" {
			continue
		}
		at := strings.Index(lowerText, lowerNeedle)
		if at < 0 {
			continue
		}
		startRune := sort.SearchInts(lowerOffsets, at)
		endRune := sort.SearchInts(lowerOffsets, at+len(lowerNeedle))
		if startRune < len(originalOffsets) && endRune < len(originalOffsets) && (bestStart < 0 || startRune < bestStart) {
			bestStart = startRune
			bestEnd = endRune
		}
	}
	if bestStart < 0 {
		return 0, 0, false
	}
	return originalOffsets[bestStart], originalOffsets[bestEnd], true
}

func lowerStringWithOffsets(value string) (string, []int, []int) {
	var lower strings.Builder
	lower.Grow(len(value))
	lowerOffsets := make([]int, 0, utf8.RuneCountInString(value)+1)
	originalOffsets := make([]int, 0, cap(lowerOffsets))
	for byteOffset, r := range value {
		lowerOffsets = append(lowerOffsets, lower.Len())
		originalOffsets = append(originalOffsets, byteOffset)
		lower.WriteRune(unicode.ToLower(r))
	}
	lowerOffsets = append(lowerOffsets, lower.Len())
	originalOffsets = append(originalOffsets, len(value))
	return lower.String(), lowerOffsets, originalOffsets
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func fitContextBudget(out *ContextQueryResult, maxBytes int) error {
	encoded, err := json.Marshal(out)
	if err != nil {
		return err
	}
	// The HTTP JSON encoder appends one newline; reserve it so the wire body also
	// honors max_output_bytes.
	if len(encoded)+1 <= maxBytes {
		return nil
	}
	out.Coverage.Truncated = true
	out.Warnings = append(out.Warnings, "response_truncated")
	for len(out.Suggestions) != 0 || len(out.Evidence) != 0 || len(out.Conflicts) != 0 {
		encoded, err = json.Marshal(out)
		if err != nil {
			return err
		}
		if len(encoded)+1 <= maxBytes {
			return nil
		}
		var target *[]ContextEvidence
		if len(out.Suggestions) != 0 {
			target = &out.Suggestions
		} else if len(out.Evidence) != 0 {
			target = &out.Evidence
		} else {
			out.Conflicts = out.Conflicts[:len(out.Conflicts)-1]
			continue
		}
		last := len(*target) - 1
		over := len(encoded) + 1 - maxBytes
		if len((*target)[last].Excerpt) > over {
			(*target)[last].Excerpt = truncateUTF8((*target)[last].Excerpt, len((*target)[last].Excerpt)-over)
			(*target)[last].ExcerptTruncated = true
		} else {
			*target = (*target)[:last]
		}
	}
	encoded, err = json.Marshal(out)
	if err != nil {
		return err
	}
	if len(encoded)+1 > maxBytes {
		return Invalid("max_output_bytes too small for response envelope")
	}
	return nil
}

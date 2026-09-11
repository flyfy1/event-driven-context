package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"
)

func recordText(t *testing.T, s *Store, ctx context.Context, projectID, text string, action *ActionInput) Event {
	t.Helper()
	event, err := s.RecordEvent(ctx, RecordInput{ProjectID: projectID, Content: ContentInput{Kind: "text", Text: &text}, Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func derivedText(t *testing.T, s *Store, ctx context.Context, projectID, text, kind string, sourceIDs []string) Event {
	t.Helper()
	event, err := s.RecordDerived(ctx, DerivedRecordInput{
		ProjectID:  projectID,
		Content:    ContentInput{Kind: "text", Text: &text},
		Provenance: Provenance{Kind: kind, SourceEventIDs: sourceIDs, RunID: "run-1", SkillID: "context", SkillVersion: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestContextQueryUsesExplicitProvenanceAndRelations(t *testing.T) {
	s := openTest(t)
	alice := user(t, s, "alice")
	bob := user(t, s, "bob")
	p, err := s.CreateProject(alice, ProjectInput{Name: "context"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.AddMember(alice, MemberInput{ProjectID: p.ID, Username: "bob"}); err != nil {
		t.Fatal(err)
	}

	text := "初始计划在周一发布"
	original, err := s.RecordEvent(alice, RecordInput{
		ProjectID: p.ID,
		Content:   ContentInput{Kind: "text", Text: &text},
		Metadata:  meta(`{"provenance":{"kind":"suggestion"},"relations":{"supersedes_event_ids":["forged"]}}`),
	})
	if err != nil || original.Provenance.Kind != ProvenanceOriginal || len(original.Relations.SupersedesEventIDs) != 0 {
		t.Fatalf("metadata gained platform semantics: %+v %v", original, err)
	}
	suggestion := derivedText(t, s, alice, p.ID, "建议改为周五发布", ProvenanceSuggestion, []string{original.ID})
	confirmation := recordText(t, s, alice, p.ID, "确认建议，周五发布", &ActionInput{Kind: "confirmation", SourceEventIDs: []string{suggestion.ID}})
	correction := recordText(t, s, alice, p.ID, "纠正：初始日期改为周五发布", &ActionInput{Kind: "correction", SourceEventIDs: []string{original.ID}, SupersedesEventIDs: []string{original.ID}})

	if _, err = s.RecordEvent(bob, RecordInput{ProjectID: p.ID, Content: ContentInput{Kind: "text", Text: &text}, Action: &ActionInput{Kind: "correction", SupersedesEventIDs: []string{original.ID}}}); !errors.Is(err, ErrActionForbidden) {
		t.Fatalf("member superseded another actor's record: %v", err)
	}
	if _, err = s.RecordEvent(bob, RecordInput{ProjectID: p.ID, Content: ContentInput{Kind: "text", Text: &text}, Action: &ActionInput{Kind: "confirmation", SourceEventIDs: []string{suggestion.ID}}}); !errors.Is(err, ErrActionForbidden) {
		t.Fatalf("member confirmed another installation's suggestion: %v", err)
	}

	result, err := s.QueryContext(alice, ContextQueryInput{ProjectID: p.ID, Query: "发布", MaxOutputBytes: MaxContextOutputBytes, IncludeSuggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	interpretations := map[string]string{}
	for _, evidence := range result.Evidence {
		states[evidence.EventID] = evidence.State
		interpretations[evidence.EventID] = evidence.Interpretation
	}
	if states[original.ID] != "superseded" || states[confirmation.ID] != "confirmed" || states[correction.ID] != "active_evidence" || interpretations[correction.ID] != "user_correction" {
		t.Fatalf("explicit states: %#v %#v", states, interpretations)
	}
	if len(result.Suggestions) != 1 || result.Suggestions[0].EventID != suggestion.ID || result.Suggestions[0].State != "suggestion" {
		t.Fatalf("suggestions treated as evidence: %+v", result.Suggestions)
	}
	withoutSuggestions, err := s.QueryContext(alice, ContextQueryInput{ProjectID: p.ID, Query: "周五发布", IncludeSuggestions: false})
	if err != nil || len(withoutSuggestions.Suggestions) != 0 {
		t.Fatalf("include_suggestions ignored: %+v %v", withoutSuggestions, err)
	}
}

func TestContextQueryChinesePendingAudioBudgetAndReadOnly(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "context-audio"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := s.RecordMediaEvent(ctx, MediaRecordInput{ProjectID: p.ID, Filename: "meeting.wav", MediaType: "audio/wav", Data: wavFixture(128), IdempotencyKey: "meeting"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "发布计划"})
	if err != nil || pending.Coverage.PendingProcessingCount != 1 || !containsString(pending.Warnings, "pending_audio") {
		t.Fatalf("pending audio disclosure: %+v %v", pending, err)
	}
	transcript := derivedText(t, s, ctx, p.ID, strings.Repeat("会议确认周五发布计划。", 120), ProvenanceTranscript, []string{media.ID})
	before, err := s.loadProjectEvents(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "周五发布计划", MaxOutputBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.loadProjectEvents(p.ID)
	if err != nil || len(after) != len(before) {
		t.Fatalf("context query wrote events: %d -> %d, %v", len(before), len(after), err)
	}
	if result.Coverage.PendingProcessingCount != 0 || result.Coverage.SnapshotSequence != before[len(before)-1].Sequence || result.Coverage.IndexedThroughSequence != result.Coverage.SnapshotSequence {
		t.Fatalf("coverage: %+v", result.Coverage)
	}
	if !result.Coverage.Truncated || !containsString(result.Warnings, "response_truncated") || !containsString(result.Warnings, "keyword_retrieval") || result.Coverage.ConflictDetection != "explicit_update_forks_only" {
		t.Fatalf("honesty warnings: %+v %+v", result.Coverage, result.Warnings)
	}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > 1000 || !utf8.Valid(encoded) {
		t.Fatalf("UTF-8 output budget: %d %v", len(encoded), err)
	}
	found := false
	for _, evidence := range result.Evidence {
		if !utf8.ValidString(evidence.Excerpt) {
			t.Fatal("excerpt split a UTF-8 rune")
		}
		if evidence.EventID == transcript.ID && evidence.Interpretation == "transcript" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Chinese transcript not retrieved: %+v", result.Evidence)
	}
}

func TestContextQueryIncludesRelationClosureTextFilesAndMatchExcerpt(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "retrieval-correctness"})
	if err != nil {
		t.Fatal(err)
	}
	oldBudget := recordText(t, s, ctx, p.ID, "预算上限五万元", nil)
	newBudget := recordText(t, s, ctx, p.ID, "改为三万元", &ActionInput{Kind: "correction", SupersedesEventIDs: []string{oldBudget.ID}})
	result, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "五万元"})
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[string]ContextEvidence{}
	for _, item := range result.Evidence {
		evidence[item.EventID] = item
	}
	if evidence[oldBudget.ID].State != "superseded" || evidence[newBudget.ID].State != "active_evidence" || evidence[newBudget.ID].RetrievalReason != "relation_context" {
		t.Fatalf("current explicit update missing from old-value query: %+v", result.Evidence)
	}
	if evidence[newBudget.ID].RecordedAt == "" {
		t.Fatal("evidence timestamp missing")
	}

	fileText := strings.Repeat("前置信息。", 180) + "供应商条款关键字" + strings.Repeat("后置信息。", 180)
	fileEvent, err := s.RecordEvent(ctx, RecordInput{ProjectID: p.ID, Content: ContentInput{Kind: "file", File: &FileInput{Filename: "agreement.txt", MediaType: "text/plain", DataBase64: base64.StdEncoding.EncodeToString([]byte(fileText))}}})
	if err != nil {
		t.Fatal(err)
	}
	fileResult, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "供应商条款关键字"})
	if err != nil {
		t.Fatal(err)
	}
	foundFile := false
	for _, item := range fileResult.Evidence {
		if item.EventID == fileEvent.ID {
			foundFile = strings.Contains(item.Excerpt, "供应商条款关键字") && item.ExcerptTruncated
		}
	}
	if !foundFile {
		t.Fatalf("verified text file or centered excerpt missing: %+v", fileResult.Evidence)
	}
}

func TestContextQueryRootsDerivedSourcesAndDetectsExplicitForks(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "lineage"})
	if err != nil {
		t.Fatal(err)
	}
	media, err := s.RecordMediaEvent(ctx, MediaRecordInput{ProjectID: p.ID, Filename: "meeting.wav", MediaType: "audio/wav", Data: wavFixture(128)})
	if err != nil {
		t.Fatal(err)
	}
	transcript := derivedText(t, s, ctx, p.ID, "会议原始转录", ProvenanceTranscript, []string{media.ID})
	summary := derivedText(t, s, ctx, p.ID, "里程碑摘要结论", ProvenanceSummary, []string{transcript.ID})
	lineage, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "里程碑摘要"})
	if err != nil {
		t.Fatal(err)
	}
	foundSummary := false
	for _, item := range lineage.Evidence {
		if item.EventID == summary.ID {
			foundSummary = len(item.SourceEventIDs) == 1 && item.SourceEventIDs[0] == media.ID
		}
	}
	if !foundSummary {
		t.Fatalf("derived source did not resolve to raw original: %+v", lineage.Evidence)
	}

	target := recordText(t, s, ctx, p.ID, "采用旧方案", nil)
	first := recordText(t, s, ctx, p.ID, "改成方案甲", &ActionInput{Kind: "correction", SupersedesEventIDs: []string{target.ID}})
	second := recordText(t, s, ctx, p.ID, "改成方案乙", &ActionInput{Kind: "correction", SupersedesEventIDs: []string{target.ID}})
	fork, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "旧方案"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fork.Conflicts) != 1 || fork.Conflicts[0].Kind != "explicit_update_fork" || fork.Conflicts[0].TargetEventID != target.ID || len(fork.Conflicts[0].UpdateEventIDs) != 2 {
		t.Fatalf("explicit fork not disclosed: %+v", fork.Conflicts)
	}
	active := map[string]bool{}
	for _, item := range fork.Evidence {
		if item.State == "active_evidence" {
			active[item.EventID] = true
		}
	}
	if !active[first.ID] || !active[second.ID] {
		t.Fatalf("fork updates absent from relation closure: %+v", fork.Evidence)
	}
}

func TestContextQueryUnicodeExcerptAndSuggestionSupersession(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "unicode-and-suggestions"})
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{strings.Repeat("Ⱥ", 1000), strings.Repeat("Ɫ", 1000)} {
		text := prefix + "budget"
		event := recordText(t, s, ctx, p.ID, text, nil)
		result, queryErr := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "BUDGET"})
		if queryErr != nil {
			t.Fatalf("Unicode case-fold excerpt panicked or failed: %v", queryErr)
		}
		found := false
		for _, item := range result.Evidence {
			if item.EventID == event.ID {
				found = strings.Contains(strings.ToLower(item.Excerpt), "budget") && utf8.ValidString(item.Excerpt) && item.ExcerptTruncated
			}
		}
		if !found {
			t.Fatalf("Unicode case-fold excerpt omitted tail match: %+v", result.Evidence)
		}
	}

	commitment := recordText(t, s, ctx, p.ID, "正式预算为五万元", nil)
	proposal := "建议预算改为三万元"
	suggestion, err := s.RecordDerived(ctx, DerivedRecordInput{
		ProjectID:  p.ID,
		Content:    ContentInput{Kind: "text", Text: &proposal},
		Provenance: Provenance{Kind: ProvenanceSuggestion, SourceEventIDs: []string{commitment.ID}},
		Relations:  Relations{SupersedesEventIDs: []string{commitment.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "正式预算", IncludeSuggestions: true})
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, item := range result.Evidence {
		states[item.EventID] = item.State
	}
	for _, item := range result.Suggestions {
		states[item.EventID] = item.State
	}
	if states[commitment.ID] != "active_evidence" || states[suggestion.ID] != "suggestion" {
		t.Fatalf("suggestion replaced a commitment: %+v", result)
	}
}

func TestContextQueryScopesAndBudgetsConflicts(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	p, err := s.CreateProject(ctx, ProjectInput{Name: "conflict-budget"})
	if err != nil {
		t.Fatal(err)
	}
	relevant := recordText(t, s, ctx, p.ID, "唯一检索词", nil)
	for i := 0; i < 40; i++ {
		target := recordText(t, s, ctx, p.ID, "无关目标", nil)
		recordText(t, s, ctx, p.ID, "无关更新甲", &ActionInput{Kind: "correction", SupersedesEventIDs: []string{target.ID}})
		recordText(t, s, ctx, p.ID, "无关更新乙", &ActionInput{Kind: "correction", SupersedesEventIDs: []string{target.ID}})
	}
	result, err := s.QueryContext(ctx, ContextQueryInput{ProjectID: p.ID, Query: "唯一检索词", MaxOutputBytes: 1000})
	if err != nil {
		t.Fatalf("unrelated conflicts exhausted response budget: %v", err)
	}
	if len(result.Conflicts) != 0 {
		t.Fatalf("unrelated conflicts leaked into query: %+v", result.Conflicts)
	}
	found := false
	for _, item := range result.Evidence {
		found = found || item.EventID == relevant.ID
	}
	if !found {
		t.Fatalf("relevant evidence lost to unrelated conflicts: %+v", result)
	}

	large := ContextQueryResult{
		ProjectID: "prj_budget",
		Snapshot:  ContextSnapshot{Sequence: 1},
		Conflicts: []ContextConflict{{Kind: "explicit_update_fork", TargetEventID: "evt_target", UpdateEventIDs: make([]string, 200)}},
		Coverage:  ContextCoverage{SnapshotSequence: 1, IndexedThroughSequence: 1, ConflictDetection: "explicit_update_forks_only"},
		Warnings:  []string{"keyword_retrieval", "explicit_relations_only"},
		Evidence:  []ContextEvidence{}, Suggestions: []ContextEvidence{},
	}
	for i := range large.Conflicts[0].UpdateEventIDs {
		large.Conflicts[0].UpdateEventIDs[i] = strings.Repeat("x", 40)
	}
	if err = fitContextBudget(&large, 512); err != nil {
		t.Fatalf("conflicts were not included in output budget: %v", err)
	}
	encoded, err := json.Marshal(large)
	if err != nil || len(encoded)+1 > 512 || !large.Coverage.Truncated || !containsString(large.Warnings, "response_truncated") {
		t.Fatalf("conflict budget result bytes=%d result=%+v err=%v", len(encoded)+1, large, err)
	}
}

func TestDerivedSourcesMustExistInProject(t *testing.T) {
	s := openTest(t)
	ctx := user(t, s, "alice")
	one, err := s.CreateProject(ctx, ProjectInput{Name: "one"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.CreateProject(ctx, ProjectInput{Name: "two"})
	if err != nil {
		t.Fatal(err)
	}
	source := recordText(t, s, ctx, one.ID, "source", nil)
	text := "derived"
	_, err = s.RecordDerived(ctx, DerivedRecordInput{ProjectID: two.ID, Content: ContentInput{Kind: "text", Text: &text}, Provenance: Provenance{Kind: ProvenanceSummary, SourceEventIDs: []string{source.ID}}})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project source accepted: %v", err)
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

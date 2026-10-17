package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
)

func TestCompactPublicHandoffRetainsOriginalPublicSources(t *testing.T) {
	root := compactPublicTestEntry(1, "user", "original task")
	prior := compactPublicTestEntry(2, "assistant", "prior public comparison is still uncertain")
	first := buildCompactPublicHandoff([]eventjournal.Entry{root, prior})
	boundary := compactPublicTestBoundary(t, 3, first)
	continuation := compactPublicTestEntry(4, "user", "continue")
	answer := compactPublicTestEntry(5, "user", "approved")
	answer.SourceEventType = "user_input_response"
	answer.Message["messageOrigin"] = "input_response"
	current := compactPublicTestEntry(6, "assistant", "new public observation")
	current.RunID = "replacement-lease"
	current.Message["runnerAttempt"] = 9
	entries := []eventjournal.Entry{root, boundary, continuation, answer, current}
	second := buildCompactPublicHandoff(entries)
	if second.TaskRootEventID != 1 || second.ThroughEventID != 6 {
		t.Fatalf("same-task scope changed across continuation: %+v", second)
	}
	found := false
	for _, message := range second.Entries {
		if message.SourceEventID == prior.EventID {
			found = message.Role == "assistant" && message.Text == runnerMessageText(prior.Message)
		}
	}
	if !found {
		t.Fatal("prior public text lost its original source ID, role or uncertainty")
	}
	if decoded, valid := decodeCompactPublicHandoff(compactPublicTestJSON(t, second)); !valid || !reflect.DeepEqual(decoded, second) {
		t.Fatal("bounded public projection did not survive the real generic JSON shape")
	}
	for _, build := range []func(sessionstore.Session, []eventjournal.Entry, string, string) (string, error){
		buildCompactModelContextSummary, buildCompactModelPrompt,
	} {
		text, err := build(sessionstore.Session{ID: root.SessionID}, entries, "", "auto")
		if err != nil || !strings.Contains(text, runnerMessageText(prior.Message)) ||
			!strings.Contains(text, runnerMessageText(current.Message)) || strings.Contains(text, "ARBITRARY_OLD_SUMMARY") {
			t.Fatalf("builder did not consume only attributable public inputs: %v", err)
		}
	}
	for id := int64(7); id < 13; id += 2 {
		entries = []eventjournal.Entry{root, compactPublicTestBoundary(t, id, second), compactPublicTestEntry(id+1, "assistant", "later public progress")}
		second = buildCompactPublicHandoff(entries)
		originalCopies := 0
		for _, message := range second.Entries {
			if message.SourceEventID == 2 && message.Text == runnerMessageText(prior.Message) {
				originalCopies++
			}
		}
		if originalCopies != 1 || len(second.Entries) > maxCompactSummaryConversationEntries {
			t.Fatal("repeated compaction renewed, duplicated or lost the bounded original source")
		}
	}
	foreign := compactPublicTestBoundary(t, 13, second)
	foreign.SessionID = "another-session"
	if _, usable := compactPublicHandoffForTask(foreign, root); usable {
		t.Fatal("a foreign compact supplied public continuity")
	}
	forged := compactPublicTestBoundary(t, 13, second)
	forged.SourceEventType = "user_message"
	if _, usable := compactPublicHandoffForTask(forged, root); usable {
		t.Fatal("a public message masqueraded as a host compact projection")
	}
}

func TestCompactPublicHandoffHasOneCurrentPriorityBudget(t *testing.T) {
	root := compactPublicTestEntry(1, "user", "original task")
	prior := compactPublicHandoff{Version: 1, TaskRootEventID: 1, ThroughEventID: 25, Entries: []compactPublicMessage{}}
	for id := int64(2); id <= 25; id++ {
		prior.Entries = append(prior.Entries, compactPublicMessage{SourceEventID: id, Role: "assistant", Text: "old public note"})
	}
	entries := []eventjournal.Entry{root, compactPublicTestBoundary(t, 26, prior)}
	for id := int64(27); id < 37; id++ {
		entries = append(entries, compactPublicTestEntry(id, "assistant", strings.Repeat("界", maxCompactSummaryLineRunes+20)))
	}
	projection := buildCompactPublicHandoff(entries)
	if len(projection.Entries) != maxCompactSummaryConversationEntries {
		t.Fatalf("new and old data did not share one entry budget: %d", len(projection.Entries))
	}
	currentCount, totalRunes := 0, 0
	for _, message := range projection.Entries {
		totalRunes += utf8.RuneCountInString(message.Text)
		if !utf8.ValidString(message.Text) || utf8.RuneCountInString(message.Text) > maxCompactSummaryLineRunes {
			t.Fatal("public excerpt exceeded its Unicode bound")
		}
		if message.SourceEventID >= 27 {
			currentCount++
			if !message.Truncated {
				t.Fatal("long current public text lost its truncation state")
			}
		}
	}
	encoded, err := json.Marshal(projection)
	if currentCount != 10 || totalRunes > maxCompactSummaryConversationEntries*maxCompactSummaryLineRunes ||
		err != nil || len(encoded) > maxCompactPublicHandoffBytes {
		t.Fatalf("shared public budget exceeded: current=%d runes=%d bytes=%d err=%v", currentCount, totalRunes, len(encoded), err)
	}
	if compactPublicExcerptRole(compactPublicMessage{Role: "assistant", Truncated: true}) != "assistant (truncated)" {
		t.Fatal("rendered public context concealed its truncation state")
	}
	// A currently loaded canonical message wins over the cached same source.
	entries = append(entries, compactPublicTestEntry(2, "assistant", "current canonical text"))
	currentCopies := 0
	for _, message := range buildCompactPublicHandoff(entries).Entries {
		if message.SourceEventID == 2 {
			currentCopies++
			if message.Text != "current canonical text" {
				t.Fatal("cached text overrode the current canonical source")
			}
		}
	}
	if currentCopies != 1 {
		t.Fatal("current canonical source was missing or duplicated")
	}
	entries[len(entries)-1].Message["visibility"] = "provisional"
	for _, message := range buildCompactPublicHandoff(entries).Entries {
		if message.SourceEventID == 2 {
			t.Fatal("cached text revived a currently non-public source")
		}
	}
	for id := int64(37); id < 70; id++ {
		entries = append(entries, compactPublicTestEntry(id, "assistant", "fresh public text"))
	}
	latest := buildCompactPublicHandoff(entries)
	if len(latest.Entries) != maxCompactSummaryConversationEntries {
		t.Fatal("current window did not fill its existing bounded capacity")
	}
	for _, message := range latest.Entries {
		if message.SourceEventID < 37 {
			t.Fatal("old notes displaced the newer current window")
		}
	}
	escaped := compactPublicHandoff{Version: 1, TaskRootEventID: 1, ThroughEventID: 25, Entries: []compactPublicMessage{}}
	for id := int64(2); id <= 25; id++ {
		escaped.Entries = append(escaped.Entries, compactPublicMessage{SourceEventID: id, Role: "assistant", Text: strings.Repeat("\x01", maxCompactSummaryLineRunes)})
	}
	encoded, err = json.Marshal(escaped)
	if _, valid := decodeCompactPublicHandoff(compactPublicTestJSON(t, escaped)); !valid || err != nil || len(encoded) > maxCompactPublicHandoffBytes {
		t.Fatal("maximum escaped public text exceeded the serialized projection bound")
	}
}

func TestCompactPublicHandoffUsesOnlyPublicTextAndKeepsManualWindow(t *testing.T) {
	entries := []eventjournal.Entry{compactPublicTestEntry(1, "user", "original task")}
	oldest := "OLDEST_CURRENT_PUBLIC " + strings.Repeat("x", 1000) + " COMPLETE_CURRENT_TAIL"
	for id := int64(2); id <= 35; id++ {
		text := fmt.Sprintf("current public message %d", id)
		if id == 2 {
			text = oldest
		}
		entries = append(entries, compactPublicTestEntry(id, "assistant", text))
	}
	structured := compactPublicTestEntry(36, "assistant", "")
	delete(structured.Message, "text")
	structured.Message["reasoning_content"] = "PRIVATE_REASONING"
	structured.Message["content"] = []any{
		map[string]any{"type": "thinking", "text": "PRIVATE_THINKING"},
		map[string]any{"type": "reasoning", "text": "PRIVATE_REASONING_BLOCK"},
		map[string]any{"type": "tool_result", "content": "NOT_PUBLIC_TOOL_DATA"},
		map[string]any{"type": "text", "text": "PUBLIC_STRUCTURED_TEXT"},
		map[string]any{"type": "output_text", "text": "PUBLIC_OUTPUT_TEXT"},
	}
	partial := compactPublicTestEntry(37, "assistant", "PROVISIONAL_TEXT")
	partial.Message["visibility"] = "provisional"
	entries = append(entries, structured, partial)
	prompt, err := buildCompactModelPrompt(sessionstore.Session{ID: entries[0].SessionID}, entries, "", "manual")
	if err != nil || !strings.Contains(prompt, oldest) || !strings.Contains(prompt, "PUBLIC_STRUCTURED_TEXT") ||
		!strings.Contains(prompt, "PUBLIC_OUTPUT_TEXT") {
		t.Fatalf("manual compaction lost its richer current transcript: %v", err)
	}
	for _, forbidden := range []string{"PRIVATE_REASONING", "PRIVATE_THINKING", "NOT_PUBLIC_TOOL_DATA", "PROVISIONAL_TEXT"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("non-public input reached the manual compaction prompt: %s", forbidden)
		}
	}
	structuredFound := false
	for _, message := range buildCompactPublicHandoff(entries).Entries {
		if message.SourceEventID == 2 || strings.Contains(message.Text, "PRIVATE_") {
			t.Fatal("deterministic public projection exceeded its smaller window or included private content")
		}
		if message.SourceEventID == structured.EventID {
			structuredFound = message.Text == "PUBLIC_STRUCTURED_TEXT\nPUBLIC_OUTPUT_TEXT"
		}
	}
	if !structuredFound {
		t.Fatal("known public output_text was lost or non-public blocks entered the projection")
	}
}

func TestCompactPublicHandoffManualWithoutTaskAnchorKeepsCurrentSummary(t *testing.T) {
	for _, withPrior := range []bool{false, true} {
		t.Run(fmt.Sprintf("unscoped-prior-%t", withPrior), func(t *testing.T) {
			srv := New(Options{FileRoot: t.TempDir()})
			t.Cleanup(func() { _ = srv.Close(context.Background()) })
			const sessionID = "manual-public-no-task-anchor"
			if err := srv.sessionStore.Upsert(sessionstore.Session{ID: sessionID}); err != nil {
				t.Fatal(err)
			}
			if withPrior {
				_, err := srv.eventJournal.Append(sessionID, eventjournal.Message{
					"type": "session_compact", "summary": "UNSCOPED_PRIOR_SUMMARY",
					"modelContext": map[string]any{
						"summary": "UNSCOPED_PRIOR_SUMMARY", compactPublicHandoffField: compactPublicHandoff{
							Version: 1, Entries: []compactPublicMessage{{SourceEventID: 1, Role: "assistant", Text: "UNSCOPED_PRIOR_NOTE"}},
						},
					},
				}, eventjournal.Metadata{ClientMessageID: "unscoped-prior"})
				if err != nil {
					t.Fatal(err)
				}
			}
			current, err := srv.appendSessionToolEvent(map[string]any{
				"sessionId": sessionID, "role": "assistant", "clientMessageId": "current-public",
				"message": map[string]any{"type": "message", "content": []any{
					map[string]any{"type": "output_text", "text": "CURRENT_PUBLIC_WITHOUT_ANCHOR"},
					map[string]any{"type": "reasoning", "text": "PRIVATE_NOT_PUBLIC"},
				}},
			})
			if err != nil || current == nil || current.SourceEventType != "" {
				t.Fatalf("legacy public append current=%+v err=%v", current, err)
			}
			if _, err := srv.executeCompactTool(context.Background(), map[string]any{
				"sessionId": sessionID, "trigger": "manual", "limit": float64(20),
			}); err != nil {
				t.Fatal(err)
			}
			entries, err := srv.eventJournal.ReadAfter(sessionID, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			if compactPublicTaskRoot(entries).EventID != 0 || len(entries) < 2 {
				t.Fatal("manual fixture unexpectedly gained a logical user task")
			}
			boundary := entries[len(entries)-1]
			if !isCompactJournalEntry(boundary) || boundary.EventID <= current.EventID {
				t.Fatal("manual Compact did not persist a distinct boundary")
			}
			if _, present := compactPublicHandoffValue(boundary.Message); present {
				t.Fatal("manual Compact claimed scoped v1 continuity without a public user-task anchor")
			}
			summary, crop, found := latestCompactModelContext(entries)
			if !found || crop != boundary.EventID || !strings.Contains(summary, "CURRENT_PUBLIC_WITHOUT_ANCHOR") ||
				strings.Contains(summary, "UNSCOPED_PRIOR_") || strings.Contains(summary, "PRIVATE_NOT_PUBLIC") {
				t.Fatal("manual Compact lost current public text or imported unscoped/non-public input")
			}
			messages, err := sessionEntriesToProviderMessages("system", entries)
			if err != nil {
				t.Fatal(err)
			}
			currentCopies := 0
			for _, message := range messages {
				currentCopies += strings.Count(message.Content, "CURRENT_PUBLIC_WITHOUT_ANCHOR")
				if message.Role == "user" || strings.Contains(message.Content, "UNSCOPED_PRIOR_") ||
					strings.Contains(message.Content, "PRIVATE_NOT_PUBLIC") {
					t.Fatal("manual provider context invented a user root or revived excluded input")
				}
			}
			if currentCopies != 1 {
				t.Fatalf("manual provider context retained current public text %d times", currentCopies)
			}
		})
	}
}

func TestCompactPublicHandoffRejectsInvalidInputWithoutOlderFallback(t *testing.T) {
	empty := compactPublicHandoff{Version: 1, TaskRootEventID: 1, ThroughEventID: 1, Entries: []compactPublicMessage{}}
	if decoded, ok := decodeCompactPublicHandoff(compactPublicTestJSON(t, empty)); !ok || len(decoded.Entries) != 0 {
		t.Fatal("a known empty projection was confused with malformed or absent input")
	}
	valid := compactPublicHandoff{Version: 1, TaskRootEventID: 1, ThroughEventID: 3, Entries: []compactPublicMessage{
		{SourceEventID: 2, Role: "assistant", Text: "OLD_PUBLIC_NOTE"},
	}}
	for _, test := range []struct {
		name   string
		change func(map[string]any) any
	}{
		{"unknown-version", func(raw map[string]any) any { raw["version"] = 2; return raw }},
		{"string-version", func(raw map[string]any) any { raw["version"] = "1"; return raw }},
		{"null", func(map[string]any) any { return nil }},
		{"freeform", func(map[string]any) any { return "arbitrary summary" }},
		{"extra-field", func(raw map[string]any) any {
			raw["summary"] = strings.Repeat("x", maxCompactPublicHandoffBytes+1)
			return raw
		}},
		{"missing-field", func(raw map[string]any) any { delete(raw, "throughEventId"); return raw }},
		{"non-exact-id", func(raw map[string]any) any { raw["taskRootEventId"] = float64(1); return raw }},
		{"oversized-count", func(raw map[string]any) any {
			raw["entries"] = make([]any, maxCompactSummaryConversationEntries+1)
			return raw
		}},
		{"duplicate-source", func(raw map[string]any) any {
			items := raw["entries"].([]any)
			raw["entries"] = append(items, items[0])
			return raw
		}},
		{"tool-role", func(raw map[string]any) any { raw["entries"].([]any)[0].(map[string]any)["role"] = "tool"; return raw }},
		{"future-source", func(raw map[string]any) any {
			raw["entries"].([]any)[0].(map[string]any)["sourceEventId"] = "4"
			return raw
		}},
		{"oversized-text", func(raw map[string]any) any {
			raw["entries"].([]any)[0].(map[string]any)["text"] = strings.Repeat("界", maxCompactSummaryLineRunes+1)
			return raw
		}},
		{"oversized-input", func(raw map[string]any) any {
			raw["entries"].([]any)[0].(map[string]any)["text"] = strings.Repeat("x", maxCompactPublicHandoffBytes+1)
			return raw
		}},
		{"invalid-utf8", func(raw map[string]any) any {
			raw["entries"].([]any)[0].(map[string]any)["text"] = string([]byte{0xff})
			return raw
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := test.change(compactPublicTestJSON(t, valid).(map[string]any))
			if _, accepted := decodeCompactPublicHandoff(raw); accepted {
				t.Fatal("invalid public projection was accepted")
			}
			root := compactPublicTestEntry(1, "user", "original task")
			older := compactPublicTestBoundary(t, 4, valid)
			newer := compactPublicTestBoundary(t, 5, valid)
			newer.Message["modelContext"].(map[string]any)[compactPublicHandoffField] = raw
			entries := []eventjournal.Entry{root, older, newer, compactPublicTestEntry(6, "assistant", "CURRENT_PUBLIC_NOTE")}
			if summary, crop, found := latestCompactModelContext(entries); !found || crop != 5 || summary != "" {
				t.Fatal("invalid latest public data revived an arbitrary freeform summary or lost its crop")
			}
			projection := buildCompactPublicHandoff(entries)
			currentCopies := 0
			for _, message := range projection.Entries {
				if message.SourceEventID == 2 {
					t.Fatal("invalid latest projection revived an older compact")
				}
				if message.SourceEventID == 6 {
					currentCopies++
				}
			}
			if currentCopies != 1 {
				t.Fatal("invalid prior projection discarded current public progress")
			}
		})
	}
	largeID := int64(9007199254740993)
	large := compactPublicHandoff{Version: 1, TaskRootEventID: largeID, ThroughEventID: largeID + 1, Entries: []compactPublicMessage{
		{SourceEventID: largeID + 1, Role: "assistant", Text: "exact public source ID"},
	}}
	if decoded, ok := decodeCompactPublicHandoff(compactPublicTestJSON(t, large)); !ok || !reflect.DeepEqual(decoded, large) {
		t.Fatal("generic JSON rounded a public source ID beyond float64's exact range")
	}
}

func TestCompactPublicHandoffScopesSummaryWithoutLosingCropOrTypedState(t *testing.T) {
	for _, test := range []struct {
		name, input                         string
		version                             int
		legacy, empty, wantSummary, newTask bool
	}{
		{"same-task", "continue", 1, false, false, true, false},
		{"new-task-phrase", "continue after compact", 1, false, false, false, true},
		{"unknown-version", "continue", 2, false, false, false, false},
		{"empty-new-format", "continue", 1, false, true, false, false},
		{"legacy-summary", "continue after compact", 0, true, false, true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := compactPublicTestEntry(1, "user", "original task")
			public := compactPublicTestEntry(2, "assistant", "OLD_PUBLIC_NOTE")
			proposal := eventjournal.Entry{SessionID: root.SessionID, EventID: 3, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{
				"type": "runner_checkpoint", "modelToolCalls": []any{map[string]any{
					"id": "known-call", "type": "function", "function": map[string]any{"name": "web_fetch", "arguments": "{}"},
				}},
			}}
			terminal := eventjournal.Entry{SessionID: root.SessionID, EventID: 4, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{
				"type": "runner_checkpoint", "toolCallId": "known-call", "toolName": "web_fetch", "toolPhase": "completed",
				"toolResult": map[string]any{"ok": true, "observation": "independent typed receipt"},
			}}
			before := []eventjournal.Entry{root, public, proposal, terminal}
			ledger, err := sessionRunnerToolContinuityRecords(before)
			if err != nil || len(ledger) != 1 {
				t.Fatalf("typed fixture ledger=%+v err=%v", ledger, err)
			}
			handoff := buildCompactPublicHandoff(before)
			handoff.Version = test.version
			boundary := compactPublicTestBoundary(t, 5, handoff)
			boundary.Message[sessionRunnerToolContinuityField] = ledger
			if test.legacy {
				delete(boundary.Message["modelContext"].(map[string]any), compactPublicHandoffField)
			}
			if test.empty {
				boundary.Message["summary"] = ""
				boundary.Message["modelContext"].(map[string]any)["summary"] = ""
			}
			input := compactPublicTestEntry(6, "user", test.input)
			// Keep the existing phrase boundary: the canonical explicit command
			// "continue" resumes, while "continue after compact" is a new task.
			if runnerEntryStartsNewLogicalTask(input) != test.newTask {
				t.Fatal("existing canonical new-task classification changed")
			}
			entries := append(before, boundary, input)
			summary, crop, found := latestCompactModelContext(entries)
			if !found || crop != 5 || (summary != "") != test.wantSummary {
				t.Fatalf("scoped summary=%q crop=%d found=%t", summary, crop, found)
			}
			messages, err := sessionEntriesToProviderMessages("system", entries)
			if err != nil {
				t.Fatal(err)
			}
			rootCopies := 0
			for _, message := range messages {
				if message.Role == "user" && message.Content == runnerMessageText(root.Message) {
					rootCopies++
				}
				if len(message.ToolCalls) != 0 || message.Role == "tool" ||
					!test.wantSummary && strings.Contains(message.Content, "ARBITRARY_OLD_SUMMARY") {
					t.Fatal("unusable handoff revived old summary or pre-compact tool transactions")
				}
			}
			if rootCopies != 1 {
				t.Fatal("task fence rewrote or removed original user authority")
			}
			actual, err := sessionRunnerToolContinuityRecords(entries)
			if err != nil || !reflect.DeepEqual(actual, ledger) || runtimeProviderCacheMarkersFromEntries(entries).CompactedThroughEventID != 5 {
				t.Fatal("task fence changed the independent typed ledger or compact marker")
			}
			if test.newTask {
				for _, note := range buildCompactPublicHandoff(entries).Entries {
					if note.SourceEventID < input.EventID {
						t.Fatal("a new logical task inherited earlier public observations")
					}
				}
			}
		})
	}
}

func compactPublicTestEntry(id int64, role, text string) eventjournal.Entry {
	return eventjournal.Entry{SessionID: "public-handoff-frame", EventID: id, SourceEventType: role + "_message",
		Message: eventjournal.Message{"type": "message", "role": role, "text": text}}
}

func compactPublicTestBoundary(t *testing.T, id int64, handoff compactPublicHandoff) eventjournal.Entry {
	t.Helper()
	return eventjournal.Entry{SessionID: "public-handoff-frame", EventID: id, SourceEventType: "runner_checkpoint", Message: eventjournal.Message{
		"type": "runner_checkpoint", "toolPhase": "auto_compact", "summary": "ARBITRARY_OLD_SUMMARY",
		"modelContext": map[string]any{"summary": "ARBITRARY_OLD_SUMMARY", compactPublicHandoffField: compactPublicTestJSON(t, handoff)},
	}}
}

func compactPublicTestJSON(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var raw any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

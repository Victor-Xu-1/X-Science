package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	taskruns "x-science/internal/persistence/taskruns"
)

func appendTaskRunLatestEvidenceFixture(t *testing.T, journal *eventjournal.EventJournal, sessionID, runID string, nested bool) {
	t.Helper()
	appendMessage := func(message eventjournal.Message, id string) {
		t.Helper()
		if _, err := journal.Append(sessionID, message, eventjournal.Metadata{RunID: runID, ClientMessageID: id}); err != nil {
			t.Fatal(err)
		}
	}
	appendMessage(eventjournal.Message{"type": "message", "role": "assistant", "text": "old step output"}, "old-output")
	for i := 0; i < 220; i++ {
		appendMessage(eventjournal.Message{"type": "taskrun_event", "role": "system", "text": fmt.Sprintf("progress %d", i)}, fmt.Sprintf("progress-%d", i))
	}
	latest := eventjournal.Message{"type": "message", "role": "assistant", "text": "current step output"}
	if nested {
		delete(latest, "text")
		latest["message"] = map[string]any{"text": "current step output"}
	}
	appendMessage(latest, "latest-output")
	appendMessage(eventjournal.Message{"type": "runner_finished", "role": "system", "status": "completed"}, "finished")
}

func TestTaskRunLatestAssistantMessageBeyondFirstJournalPage(t *testing.T) {
	for _, nested := range []bool{false, true} {
		t.Run(fmt.Sprintf("nested_%t", nested), func(t *testing.T) {
			journal := eventjournal.NewEventJournal(t.TempDir())
			appendTaskRunLatestEvidenceFixture(t, journal, "taskrun:long", "long", nested)
			server := &Server{eventJournal: journal}
			got, err := server.latestTaskRunAssistantMessage("taskrun:long")
			if err != nil {
				t.Fatal(err)
			}
			if got != "current step output" {
				t.Fatalf("latest assistant output = %q, want current step output after event 200", got)
			}
		})
	}
}

func TestTaskRunReconcileUsesLatestAssistantEvidenceBeyondFirstJournalPage(t *testing.T) {
	root := t.TempDir()
	runs := taskruns.NewStore(filepath.Join(root, "runs"))
	run, err := runs.Create(taskruns.Input{RunID: "long", Action: "start", Objective: "complete current step", TaskGraph: &taskruns.Graph{Steps: []taskruns.StepInput{{ID: "current", Title: "Current", Executor: map[string]any{"kind": "agent"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	sessions := sessionstore.NewStore(filepath.Join(root, "sessions"))
	if err := sessions.Upsert(sessionstore.Session{ID: run.SessionID, Runner: &sessionstore.Runner{RunnerID: "test-runner", Status: "completed", Attempt: 1}}); err != nil {
		t.Fatal(err)
	}
	journal := eventjournal.NewEventJournal(filepath.Join(root, "journal"))
	appendTaskRunLatestEvidenceFixture(t, journal, run.SessionID, run.RunID, false)
	server := &Server{eventJournal: journal, sessionStore: sessions, taskRunStore: runs}
	reconciled, err := server.reconcileTaskRun(run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.EvidenceIndex) != 1 || reconciled.EvidenceIndex[0].Summary != "current step output" || reconciled.EvidenceIndex[0].StepID != "current" {
		t.Fatalf("current step evidence = %#v, want current step output", reconciled.EvidenceIndex)
	}
	if len(reconciled.Artifacts) != 1 || reconciled.Artifacts[0].Metadata["preview"] != "current step output" {
		t.Fatalf("current step artifacts = %#v, want current step output", reconciled.Artifacts)
	}
}

func TestTaskRunLatestAssistantMessageSelectionCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages []eventjournal.Message
		want     string
	}{
		{name: "empty"},
		{name: "no_assistant", messages: []eventjournal.Message{{"role": "user", "text": "input"}}},
		{name: "blank_and_system_tail", messages: []eventjournal.Message{{"role": "assistant", "text": " earlier "}, {"role": "assistant", "text": "  "}, {"role": "system", "text": "finished"}}, want: " earlier "},
		{name: "nested_fallback", messages: []eventjournal.Message{{"role": "assistant", "text": " ", "message": map[string]any{"text": "nested"}}}, want: "nested"},
		{name: "top_level_precedence", messages: []eventjournal.Message{{"role": "assistant", "text": "top", "message": map[string]any{"text": "nested"}}}, want: "top"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "session-events", url.QueryEscape("legacy")+".jsonl")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			// Historical role-only envelopes and malformed lines retain ReadAll's existing tolerance.
			var lines strings.Builder
			lines.WriteString("malformed historical line\n")
			for i, message := range tc.messages {
				data, err := json.Marshal(eventjournal.Entry{SessionID: "legacy", EventID: int64(i + 1), CreatedAt: "historical timestamp", Message: message})
				if err != nil {
					t.Fatal(err)
				}
				lines.Write(data)
				lines.WriteByte('\n')
			}
			if err := os.WriteFile(path, []byte(lines.String()), 0600); err != nil {
				t.Fatal(err)
			}
			server := &Server{eventJournal: eventjournal.NewEventJournal(root)}
			got, err := server.latestTaskRunAssistantMessage("legacy")
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestTaskRunReconcileJournalReadFailurePreservesRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits eventjournal.Limits
		want   error
	}{
		{"entry_size", eventjournal.Limits{MaxEntryBytes: 16}, eventjournal.ErrEntrySizeLimit},
		{"file_size", eventjournal.Limits{MaxFileBytes: 16}, eventjournal.ErrFileSizeLimit},
		{"entry_count", eventjournal.Limits{MaxEntries: 1}, eventjournal.ErrEntryCountLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			runs := taskruns.NewStore(filepath.Join(root, "runs"))
			run, err := runs.Create(taskruns.Input{RunID: "read-failure", Action: "start", Objective: "preserve current step", TaskGraph: &taskruns.Graph{Steps: []taskruns.StepInput{{ID: "current", Title: "Current", Executor: map[string]any{"kind": "agent"}}}}})
			if err != nil {
				t.Fatal(err)
			}
			sessions := sessionstore.NewStore(filepath.Join(root, "sessions"))
			if err := sessions.Upsert(sessionstore.Session{ID: run.SessionID, Runner: &sessionstore.Runner{RunnerID: "test", Status: "completed", Attempt: 1}}); err != nil {
				t.Fatal(err)
			}
			journalRoot := filepath.Join(root, "journal")
			writer := eventjournal.NewEventJournal(journalRoot)
			for i := 0; i < 2; i++ {
				if _, err := writer.Append(run.SessionID, eventjournal.Message{"type": "message", "role": "assistant", "text": "output"}, eventjournal.Metadata{RunID: run.RunID, ClientMessageID: fmt.Sprint(i)}); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := runs.Get(run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			server := &Server{eventJournal: eventjournal.NewEventJournalWithLimits(journalRoot, tc.limits), sessionStore: sessions, taskRunStore: runs}
			if _, err := server.reconcileTaskRun(run.RunID); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
			after, _, err := runs.Get(run.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("read failure mutated TaskRun: before=%#v after=%#v", before, after)
			}
		})
	}
}

package server

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	eventjournal "x-science/internal/persistence/journal"
)

func TestMemoryExtractionLegacySourcePrefixRejectsReplacement(t *testing.T) {
	for _, phase := range []string{"before snapshot read", "during extraction", "later append remains valid"} {
		t.Run(phase, func(t *testing.T) {
			server := openWorkspaceMemoryToolServer(t)
			server.eventJournal = eventjournal.NewEventJournal(filepath.Join(t.TempDir(), "journal"))
			server.memoryConfig.ExtractEnabled = true
			server.memoryConfig.ExtractMode = "compact"
			server.memoryConfig.ExtractEveryN = 1
			server.memoryConfig.PIClassifierEnabled = false
			messages := func(answer string) []eventjournal.Message {
				return []eventjournal.Message{
					{"type": "message", "role": "user", "text": "Remember the durable report convention."},
					{"type": "message", "role": "assistant", "text": answer},
					{"type": "runner_finished", "status": "completed", "runnerAttempt": 7},
				}
			}
			for _, message := range messages("ORIGINAL_COMPLETED_EVIDENCE") {
				if _, err := server.eventJournal.Append("frame-root", message, eventjournal.Metadata{}); err != nil {
					t.Fatal(err)
				}
			}
			var calls atomic.Int64
			entered := make(chan string, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			releaseModel := func() { releaseOnce.Do(func() { close(release) }) }
			runtime := newMemoryExtractionRuntimeWithFactory(server, func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
				return memoryExtractorFunc(func(ctx context.Context, input memoryextract.ExtractRequest) (map[string]any, error) {
					calls.Add(1)
					entered <- input.Transcript
					select {
					case <-release:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return map[string]any{"append": []any{map[string]any{
						"text": "Reports must include confidence intervals.", "evidence": "stated", "entity": "project:project-1",
					}}}, nil
				})
			})
			runtime.rememberLastRequest("frame-root", 7, agentruntime.ModelRequest{})
			state := runtime.sessions["frame-root"]
			var unlockOnce sync.Once
			if phase == "before snapshot read" {
				state.runMu.Lock()
			}
			unlock := func() {
				if phase == "before snapshot read" {
					unlockOnce.Do(state.runMu.Unlock)
				}
			}
			done := runtime.ScheduleCompletedRootEvent("frame-root", 3)
			t.Cleanup(func() {
				releaseModel()
				unlock()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("legacy extraction did not drain after releasing its fixture barrier")
				}
			})
			if phase != "before snapshot read" {
				select {
				case text := <-entered:
					if !strings.Contains(text, "ORIGINAL_COMPLETED_EVIDENCE") || strings.Contains(text, "LATER_INPUT") {
						t.Fatal("extractor did not receive the captured original prefix")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("fixture did not reach the channel-controlled extractor")
				}
			}
			if phase == "later append remains valid" {
				if _, err := server.eventJournal.Append("frame-root", eventjournal.Message{
					"type": "message", "role": "user", "text": "LATER_INPUT must stay outside the captured prefix.",
				}, eventjournal.Metadata{}); err != nil {
					t.Fatal(err)
				}
			} else {
				replaced, err := server.eventJournal.ReplaceSession("frame-root", messages("REPLACEMENT_COMPLETED_EVIDENCE"))
				if err != nil || len(replaced) != 3 || replaced[2].EventID != 3 {
					t.Fatalf("fixture did not reuse the completed event coordinate: %v", err)
				}
				if attempt, ok := computeReceiptInteger(replaced[2].Message["runnerAttempt"]); !ok || attempt != 7 {
					t.Fatal("fixture did not reuse the original completed attempt")
				}
			}
			releaseModel()
			unlock()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("legacy extraction did not settle its bounded fixture")
			}
			cursor, err := server.workspaceStore.LastExtractMessageIndex(t.Context(), "frame-root")
			if err != nil {
				t.Fatal(err)
			}
			rows, err := server.workspaceStore.ListMemoryExtractionRows(t.Context(), "user-1", "project-1")
			if err != nil {
				t.Fatal(err)
			}
			if phase == "later append remains valid" {
				if calls.Load() != 1 || cursor != 2 || len(rows) != 1 {
					t.Fatalf("normal append invalidated original prefix: calls=%d cursor=%d rows=%d", calls.Load(), cursor, len(rows))
				}
			} else if cursor != 0 || len(rows) != 0 || phase == "before snapshot read" && calls.Load() != 0 {
				t.Fatalf("replaced completion was accepted: calls=%d cursor=%d rows=%d", calls.Load(), cursor, len(rows))
			}
		})
	}
}

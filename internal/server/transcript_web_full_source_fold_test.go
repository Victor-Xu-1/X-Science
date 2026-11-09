package server

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebFullProjectionReadsEachSourcePageOnce(t *testing.T) {
	// Cross the actual 1000-event page boundary, then require the existing
	// ordinary-tool fallback. This is a query-count regression, not a timer.
	fixture := newTranscriptWebReadModelServingFixture(t, 1001)
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "source-fold-reader",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim: claimed=%t err=%v", claim.Claimed, err)
	}
	appendRunnerToolCheckpoint(t, fixture.repository, claim.Claim, "source-fold-tool", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolCallId": "source-fold-call",
		"toolName": "read_file", "toolInput": map[string]any{"path": "synthetic.txt"},
		"toolResult": map[string]any{"content": "synthetic result"},
	})
	work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
	probe, err := fixture.server.tryIncrementalTranscriptWebReadModel(t.Context(), fixture.stream, work)
	if err != nil || probe.Disposition != transcriptWebIncrementalFallbackTool {
		t.Fatalf("ordinary tool fallback changed: result=%#v err=%v", probe, err)
	}
	counter := attachTranscriptWebSourcePageCounter(t, fixture)
	if err := fixture.server.rebuildTranscriptWebReadModel(t.Context(), work); err != nil {
		t.Fatal(err)
	}
	pages := counter.snapshot()
	// The oracle deliberately uses the original canonical path after the
	// measured owner finished. Its reads do not change the captured counts.
	assertPreparationProjectionMatchesCanonical(t, fixture)
	if len(pages) != 2 {
		t.Fatalf("expected two bounded source pages, got %v", pages)
	}
	for page, reads := range pages {
		if reads != 1 {
			t.Errorf("fixed-prefix source page %s read %d times; want one traversal", page, reads)
		}
	}
}

func TestTranscriptWebFullSourceFoldPreservesCanonicalSemantics(t *testing.T) {
	for _, history := range []string{"empty", "tool_recovery"} {
		t.Run(history, func(t *testing.T) {
			count := 0
			if history == "tool_recovery" {
				count = 1
			}
			fixture := newTranscriptWebReadModelServingFixture(t, count)
			if history == "tool_recovery" {
				seedTranscriptWebSourceFoldRecovery(t, fixture)
			}
			canonical, snapshot, err := fixture.server.projectTranscriptWebHistory(t.Context(), fixture.stream,
				fixture.stream.OwnerID, fixture.stream.SessionID, fixture.fence.BranchID)
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := fixture.server.projectTranscriptWebSourceMetadata(t.Context(), fixture.stream,
				fixture.stream.OwnerID, fixture.stream.SessionID, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			messages, folded, err := fixture.server.projectTranscriptWebHistoryPrefix(t.Context(), fixture.stream,
				fixture.stream.OwnerID, fixture.stream.SessionID, snapshot)
			if err != nil || !reflect.DeepEqual(messages, canonical) || !reflect.DeepEqual(folded, metadata) {
				t.Fatalf("single-read fold changed canonical messages/coordinates/ranges/raw chain/assistant state: %v", err)
			}
			if history == "empty" {
				seed, err := transcriptWebSourceEventChain(nil)
				if err != nil || folded.eventChain != seed || len(folded.eventChain) != 64 {
					t.Fatal("empty prefix lost the canonical source-chain seed")
				}
			} else {
				if len(messages) < 4 {
					t.Fatal("recovery fixture did not exercise the rebased assistant/tool boundaries")
				}
				work := requireSingleTranscriptWebProjectionWork(t, fixture.readModel, fixture.stream.OwnerID)
				if err := fixture.server.rebuildTranscriptWebReadModel(t.Context(), work); err != nil {
					t.Fatal(err)
				}
				assertPreparationProjectionMatchesCanonical(t, fixture)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, _, err := fixture.server.projectTranscriptWebHistoryPrefix(ctx, fixture.stream,
				fixture.stream.OwnerID, fixture.stream.SessionID, snapshot); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled prefix read lost cancellation: %v", err)
			}
		})
	}
}

func seedTranscriptWebSourceFoldRecovery(t *testing.T, fixture transcriptWebReadModelServingFixture) {
	t.Helper()
	repo := fixture.repository
	claim, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "source-fold-before",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim: claimed=%t err=%v", claim.Claimed, err)
	}
	appendRunnerPayloadEvent(t, repo, claim.Claim, "source-fold-hidden", "content_delta", map[string]any{
		"text": "synthetic progress", "synthetic_progress": true,
		"assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, ""),
	})
	appendRunnerPayloadEvent(t, repo, claim.Claim, "source-fold-before", "content_delta", map[string]any{
		"text": "before", "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, ""),
	})
	appendRunnerToolCheckpoint(t, repo, claim.Claim, "source-fold-start", map[string]any{
		"status": "running", "toolPhase": "start", "toolCallId": "fold-recovery", "toolName": "read_file",
		"toolInput": map[string]any{"path": "synthetic.txt"},
	})
	interrupted, err := repo.InterruptRunner(t.Context(), transcriptstore.InterruptRunnerInput{
		Claim: claim.Claim, ClientMessageID: "source-fold-interrupt",
		ReasonCode: sessionRunnerToolLifecyclePersistenceReasonCode, AutoResume: true,
	})
	if err != nil || !interrupted.Created {
		t.Fatalf("interrupt: created=%t err=%v", interrupted.Created, err)
	}
	resumed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "source-fold-after",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: interrupted.Checkpoint.Sequence,
	})
	if err != nil || !resumed.Claimed || resumed.Claim.Attempt != claim.Claim.Attempt {
		t.Fatalf("resume changed recovery attempt: claimed=%t err=%v", resumed.Claimed, err)
	}
	appendRunnerPayloadEvent(t, repo, resumed.Claim, "source-fold-after", "content_delta", map[string]any{
		"text": "after", "assistant_segment": transcriptstore.AssistantSegmentPayloadV1(1, ""),
	})
	if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: resumed.Claim, ClientMessageID: "source-fold-finish", Status: "completed",
		PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
}

// The wrapper delegates to real SQLite. It counts only canonical source page
// SELECTs, excluding the 256-event incremental probe and artifact/detail SQL.
type transcriptWebSourcePageCounter struct {
	mu    sync.Mutex
	pages map[string]int
}

type transcriptWebSourcePageConn struct {
	driver.Conn
	owner *transcriptWebSourcePageCounter
}

func (d *transcriptWebSourcePageCounter) Open(name string) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(name)
	if err != nil {
		return nil, err
	}
	return &transcriptWebSourcePageConn{Conn: conn, owner: d}, nil
}

func (c *transcriptWebSourcePageConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, options)
}

func (c *transcriptWebSourcePageConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(query, "SELECT event.stream_uid,event.event_id,event.publication_seq") &&
		strings.Contains(query, "FROM transcript_branch_events membership") &&
		len(args) == 6 && args[5].Value == int64(1000) {
		key := fmt.Sprintf("after=%v/through=%v", args[2].Value, args[4].Value)
		c.owner.mu.Lock()
		c.owner.pages[key]++
		c.owner.mu.Unlock()
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *transcriptWebSourcePageConn) CheckNamedValue(value *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(value)
	}
	return driver.ErrSkip
}

func (d *transcriptWebSourcePageCounter) snapshot() map[string]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	pages := make(map[string]int, len(d.pages))
	for key, count := range d.pages {
		pages[key] = count
	}
	return pages
}

func attachTranscriptWebSourcePageCounter(t *testing.T, fixture transcriptWebReadModelServingFixture) *transcriptWebSourcePageCounter {
	t.Helper()
	rows, err := fixture.db.QueryContext(t.Context(), "PRAGMA database_list")
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for rows.Next() {
		var sequence int
		var name, filename string
		if err := rows.Scan(&sequence, &name, &filename); err != nil {
			t.Fatal(err)
		}
		if name == "main" {
			path = filename
		}
	}
	if err := rows.Close(); err != nil || path == "" {
		t.Fatalf("isolated database path unavailable: %v", err)
	}
	counter := &transcriptWebSourcePageCounter{pages: map[string]int{}}
	name := "transcript-source-page-count:" + path
	sql.Register(name, counter)
	reader, err := sql.Open(name, "file:"+filepath.ToSlash(path)+"?_pragma=query_only(1)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	reader.SetMaxOpenConns(1)
	original := fixture.server.transcriptStore
	fixture.server.transcriptStore = transcriptstore.NewRepositoryWithReadPool(fixture.db, reader)
	t.Cleanup(func() {
		fixture.server.transcriptStore = original
		_ = reader.Close()
	})
	return counter
}

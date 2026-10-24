package server

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	"x-science/internal/memoryextract"
	"x-science/internal/memorytools"
	"x-science/internal/outbox"
	sessionstore "x-science/internal/persistence/sessions"
	settingsstore "x-science/internal/persistence/settings"
	workspace "x-science/internal/persistence/workspace"
)

func prepareDurableMemoryFixture(t *testing.T) *agentSaveArtifactsFixture {
	t.Helper()
	f := newAgentSaveArtifactsFixture(t)
	f.server.memoryConfig = memoryconfig.Default()
	f.server.memoryConfig.Enabled = true
	f.server.memoryConfig.ExtractEnabled = true
	f.server.memoryConfig.ExtractEveryN = 1
	f.server.memoryConfig.PIClassifierEnabled = false
	f.server.settingsStore = settingsstore.New(filepath.Join(t.TempDir(), "settings.json"))
	if err := f.store.SetMemoryEnabled(t.Context(), f.stream.OwnerID, true); err != nil {
		t.Fatal(err)
	}
	return f
}

func rememberDurableMemoryFixture(t *testing.T, f *agentSaveArtifactsFixture, request agentruntime.ModelRequest) {
	t.Helper()
	snapshot := f.server.memoryExtraction.recordLastRequest(f.stream.SessionID, f.claim.Attempt, request)
	f.server.memoryExtraction.bindRecordedModel(f.stream.SessionID, f.claim.Attempt, snapshot, workspace.MemoryExtractionModelIdentity{Model: "original-fixture-model"})
}

func settleDurableMemoryFixture(t *testing.T, f *agentSaveArtifactsFixture, reply string, runErr error) (*SessionRunnerCycleResult, error) {
	t.Helper()
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), Transcript: authority}
	if err := ensureSessionRunnerExecutionPhaseMachine(run); err != nil {
		t.Fatal(err)
	}
	result := &SessionRunnerCycleResult{SessionID: run.SessionID, Attempt: run.Attempt}
	err := f.server.settleSessionRunnerChatOutcome(t.Context(), SessionRunnerChatOptions{RunnerID: f.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, authority, run, sessionstore.Session{ID: run.SessionID}, nil, reply, runErr, "")
	return result, err
}

func claimDurableMemoryFixture(t *testing.T, store *workspace.Store, worker string) workspace.OutboxEvent {
	t.Helper()
	events, err := store.ClaimOutbox(t.Context(), workspace.ClaimOutboxInput{WorkerID: worker, Topics: []string{workspace.MemoryExtractionOutboxTopic}, Limit: 1, Lease: time.Minute})
	if err != nil || len(events) != 1 {
		t.Fatalf("Memory claim count=%d err=%v", len(events), err)
	}
	return events[0]
}

func durableMemoryFixtureAppend() map[string]any {
	return map[string]any{"append": []any{map[string]any{"text": "Reproducible reports retain explicit random seeds.", "evidence": "stated", "entity": "project:project-save"}}}
}

func requireNoDurableMemoryEffects(t *testing.T, f *agentSaveArtifactsFixture) {
	t.Helper()
	rows, err := f.store.ListMemoryExtractionRows(t.Context(), f.stream.OwnerID, f.stream.ProjectID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("unexpected Memory rows=%+v err=%v", rows, err)
	}
	cursor, err := f.store.LastExtractMessageIndex(t.Context(), f.stream.FrameID)
	if err != nil || cursor != 0 {
		t.Fatalf("unexpected cursor=%d err=%v", cursor, err)
	}
	var receipts int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM memory_extraction_receipts`).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("unexpected receipts=%d err=%v", receipts, err)
	}
}

func TestMemoryExtractionDurableDeliveryRestoresForkedViewAndReplaysReceipt(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{
		Messages:  []agentruntime.Message{{Role: "user", Content: "ORIGINAL_VIEW durable reporting convention"}},
		MaxTokens: 1234, UseProviderDefaultOutputBudget: true, ReasoningMode: agentruntime.ReasoningModeDisabled,
		Headers: map[string]string{"secret": "DO_NOT_PERSIST_HEADER"}, Metadata: map[string]any{"secret": "DO_NOT_PERSIST_METADATA"},
	})
	f.server.memoryExtraction.tasks = nil
	result, err := settleDurableMemoryFixture(t, f, "Retain explicit random seeds in reproducible reports.", nil)
	if err != nil || result.Status != "completed" {
		t.Fatalf("settlement=%+v err=%v", result, err)
	}
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repo, err := reopened.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Workspace: reopened, Transcript: repo, FileRoot: f.server.fileRoot})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = restarted.Close(ctx)
	})
	restarted.memoryConfig = f.server.memoryConfig
	// A later local configuration must not replace the persisted extraction mode.
	restarted.memoryConfig.ExtractMode = "compact"
	calls := 0
	restarted.memoryExtraction.factory = func(_ memorytools.Scope, mode string, view *agentruntime.ModelRequest) memoryextract.Extractor {
		calls++
		if mode != "forked" || view == nil || len(view.Messages) != 1 || !strings.Contains(view.Messages[0].Content, "ORIGINAL_VIEW") ||
			view.MaxTokens != 1234 || !view.UseProviderDefaultOutputBudget || view.ReasoningMode != agentruntime.ReasoningModeDisabled || len(view.Headers) != 0 || len(view.Metadata) != 0 {
			t.Fatalf("original forked request not restored: mode=%q view=%+v", mode, view)
		}
		return memoryExtractorFunc(func(_ context.Context, input memoryextract.ExtractRequest) (map[string]any, error) {
			if input.Model != "original-fixture-model" {
				t.Fatalf("substituted model=%q", input.Model)
			}
			return durableMemoryFixtureAppend(), nil
		})
	}
	event := claimDurableMemoryFixture(t, reopened, "restart-worker")
	if err := restarted.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("delivery=%v", err)
	}
	saved, err := reopened.GetOutboxEvent(t.Context(), event.ID)
	if err != nil || saved.Status != workspace.OutboxStatusDelivered {
		t.Fatalf("atomic ACK=%+v err=%v", saved, err)
	}
	value, err := workspace.DecodeMemoryExtractionDispatch(event)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found, err := reopened.ReadMemoryExtractionReceipt(t.Context(), value.Source)
	if err != nil || !found || len(receipt.Applied.Appended) != 1 || !receipt.CursorAdvanced || calls != 1 {
		t.Fatalf("receipt=%+v found=%t calls=%d err=%v", receipt, found, calls, err)
	}
	// Recreate only the historical/uncertain apply-before-ACK transport seam;
	// receipt replay must remain pure even after opt-out and loss of RAM view.
	if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_outbox SET status='pending',delivered_at_ms=NULL,available_at_ms=0 WHERE event_id=?`, event.ID); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SetMemoryEnabled(t.Context(), f.stream.OwnerID, false); err != nil {
		t.Fatal(err)
	}
	restarted.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		t.Fatal("committed replay reran the model")
		return nil
	}
	takeover := claimDurableMemoryFixture(t, reopened, "receipt-replay-worker")
	if takeover.ClaimToken == event.ClaimToken {
		t.Fatal("takeover reused a claim token")
	}
	if err := restarted.deliverMemoryExtraction(t.Context(), takeover); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("receipt replay=%v", err)
	}
	rows, err := reopened.ListMemoryExtractionRows(t.Context(), f.stream.OwnerID, f.stream.ProjectID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("replayed effects=%+v err=%v", rows, err)
	}
}

func TestMemoryExtractionDurableCompletionIsAtomicAndQualified(t *testing.T) {
	t.Run("enqueue failure rolls back completion", func(t *testing.T) {
		f := prepareDurableMemoryFixture(t)
		rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Retain explicit random seeds."}}})
		if _, err := f.db.ExecContext(t.Context(), `CREATE TRIGGER memory_enqueue_failure BEFORE INSERT ON workspace_outbox WHEN NEW.topic='memory.extraction' BEGIN SELECT RAISE(ABORT,'synthetic memory enqueue failure'); END`); err != nil {
			t.Fatal(err)
		}
		result, err := settleDurableMemoryFixture(t, f, "Recorded the durable reporting convention.", nil)
		if err == nil || !strings.Contains(err.Error(), "synthetic memory enqueue failure") || result.FinishEventID != 0 {
			t.Fatalf("partial settlement=%+v err=%v", result, err)
		}
		state, err := f.repo.GetRunnerRuntimeState(t.Context(), f.stream.UID, f.stream.OwnerID, f.claim.Attempt)
		if err != nil || state.Status != "running" {
			t.Fatalf("terminal escaped rollback: %+v %v", state, err)
		}
		var ordinal int64
		if err := f.db.QueryRowContext(t.Context(), `SELECT COALESCE(metadata.memory_completion_ordinal,0) FROM projects project LEFT JOIN project_runtime_metadata metadata ON metadata.project_id=project.id WHERE project.id=?`, f.stream.ProjectID).Scan(&ordinal); err != nil || ordinal != 0 {
			t.Fatalf("cadence escaped rollback: ordinal=%d err=%v", ordinal, err)
		}
		requireNoDurableMemoryEffects(t, f)
	})
	for _, test := range []struct {
		name, reply             string
		disabled, aside, cancel bool
	}{
		{name: "empty assistant"}, {name: "explicit opt out", reply: "Recorded.", disabled: true}, {name: "aside", reply: "Recorded.", aside: true}, {name: "cancelled", reply: "Must not extract.", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := prepareDurableMemoryFixture(t)
			rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Retain reporting conventions."}}})
			if test.disabled {
				if err := f.store.SetMemoryEnabled(t.Context(), f.stream.OwnerID, false); err != nil {
					t.Fatal(err)
				}
			}
			if test.aside {
				if _, err := f.db.ExecContext(t.Context(), `UPDATE frame_runtime_metadata SET input_data='{"_aside_parent":"parent-fixture"}' WHERE frame_id=?`, f.stream.FrameID); err != nil {
					t.Fatal(err)
				}
			}
			var runErr error
			if test.cancel {
				runErr = context.Canceled
			}
			if _, err := settleDurableMemoryFixture(t, f, test.reply, runErr); err != nil {
				t.Fatal(err)
			}
			events, err := f.store.ClaimOutbox(t.Context(), workspace.ClaimOutboxInput{WorkerID: "qualification-worker", Topics: []string{workspace.MemoryExtractionOutboxTopic}, Limit: 1, Lease: time.Minute})
			if err != nil || len(events) != 0 {
				t.Fatalf("ineligible Memory dispatch=%+v err=%v", events, err)
			}
			requireNoDurableMemoryEffects(t, f)
		})
	}
}

func TestMemoryExtractionDurableDeliveryFencesExpiredClaimAndAllowsTakeover(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Use explicit random seeds."}}})
	if _, err := settleDurableMemoryFixture(t, f, "Retain explicit random seeds.", nil); err != nil {
		t.Fatal(err)
	}
	event := claimDurableMemoryFixture(t, f.store, "old-worker")
	calls := 0
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			calls++
			if calls == 1 {
				if _, err := f.db.ExecContext(t.Context(), `UPDATE workspace_outbox SET lease_expires_at_ms=0 WHERE event_id=?`, event.ID); err != nil {
					t.Fatal(err)
				}
			}
			return durableMemoryFixtureAppend(), nil
		})
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, workspace.ErrOutboxClaimLost) {
		t.Fatalf("expired writer=%v", err)
	}
	requireNoDurableMemoryEffects(t, f)
	takeover := claimDurableMemoryFixture(t, f.store, "new-worker")
	if takeover.ClaimToken == event.ClaimToken || string(takeover.Payload) != string(event.Payload) {
		t.Fatal("takeover changed immutable source/view or reused claim")
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), takeover); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("takeover=%v", err)
	}
	if calls != 2 {
		t.Fatalf("provider calls=%d", calls)
	}
	rows, err := f.store.ListMemoryExtractionRows(t.Context(), f.stream.OwnerID, f.stream.ProjectID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("takeover effects=%+v err=%v", rows, err)
	}
	if err := f.store.SettleMemoryExtractionDelivery(t.Context(), event, ""); !errors.Is(err, workspace.ErrOutboxClaimLost) {
		t.Fatalf("old worker ACK=%v", err)
	}
}

func TestMemoryExtractionDurableDeliveryPreservesFailuresAndPolicyChanges(t *testing.T) {
	for _, test := range []string{"extractor failure", "opt out during model", "cancel during model", "oversize view", "unsupported media", "missing view"} {
		t.Run(test, func(t *testing.T) {
			f := prepareDurableMemoryFixture(t)
			request := agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Retain explicit random seeds."}}}
			if test == "oversize view" {
				request.Messages[0].Content = strings.Repeat("x", workspace.MemoryExtractionViewMaxBytes+1)
			}
			if test == "unsupported media" {
				request.Messages[0].Parts = []agentruntime.ContentPart{{Type: agentruntime.ContentPartImage, Media: &agentruntime.MediaContent{Source: agentruntime.MediaSource{Type: agentruntime.MediaSourceFile, Path: "PRIVATE_MEDIA_PATH_MUST_NOT_PERSIST"}}}}
			}
			if test != "missing view" {
				rememberDurableMemoryFixture(t, f, request)
			}
			if _, err := settleDurableMemoryFixture(t, f, "Recorded reporting convention.", nil); err != nil {
				t.Fatalf("optional view prevented main completion: %v", err)
			}
			event := claimDurableMemoryFixture(t, f.store, "failure-worker")
			calls := 0
			f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
				return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
					calls++
					switch test {
					case "extractor failure":
						return nil, errors.Join(context.DeadlineExceeded, errors.New("PRIVATE_PROVIDER_BODY token=synthetic-secret"))
					case "opt out during model":
						if err := f.store.SetMemoryEnabled(t.Context(), f.stream.OwnerID, false); err != nil {
							t.Fatal(err)
						}
					case "cancel during model":
						if _, err := f.db.ExecContext(t.Context(), `UPDATE frames SET status='cancelled' WHERE id=?`, f.stream.FrameID); err != nil {
							t.Fatal(err)
						}
					}
					return durableMemoryFixtureAppend(), nil
				})
			}
			err := f.server.deliverMemoryExtraction(t.Context(), event)
			if test == "extractor failure" {
				if err == nil || errors.Is(err, outbox.ErrDeliverySettled) {
					t.Fatalf("nil-error extractor gate was ACKed: %v", err)
				}
				if _, retryErr := f.store.RetryOutbox(t.Context(), event.ID, event.ClaimToken, err.Error(), 0); retryErr != nil {
					t.Fatal(retryErr)
				}
			} else if !errors.Is(err, outbox.ErrDeliverySettled) {
				t.Fatalf("explicit failure settlement=%v", err)
			}
			if strings.Contains(string(event.Payload), "PRIVATE_MEDIA_PATH_MUST_NOT_PERSIST") {
				t.Fatal("media host path persisted")
			}
			if (test == "oversize view" || test == "unsupported media" || test == "missing view") && calls != 0 {
				t.Fatal("diagnostic job called a replacement model")
			}
			requireNoDurableMemoryEffects(t, f)
			saved, getErr := f.store.GetOutboxEvent(t.Context(), event.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if test == "extractor failure" {
				if saved.Status != workspace.OutboxStatusPending || !strings.Contains(saved.LastError, "gate=extractor-failed cause=deadline_exceeded") || strings.Contains(saved.LastError, "PRIVATE_PROVIDER_BODY") || strings.Contains(saved.LastError, "synthetic-secret") {
					t.Fatalf("lost or unsafe retry diagnostic: %+v", saved)
				}
			} else if saved.Status != workspace.OutboxStatusDeadLetter || saved.LastError == "" {
				t.Fatalf("lost terminal diagnostic: %+v", saved)
			}
		})
	}
}

func TestMemoryExtractionRequestViewIsBoundedAndRetainsOnlyPromptFields(t *testing.T) {
	temperature := 0.3
	request := agentruntime.ModelRequest{
		Messages: []agentruntime.Message{{Role: "assistant", Content: "Context", Parts: []agentruntime.ContentPart{{Type: agentruntime.ContentPartText, Text: "Part"}},
			ReasoningContent: "synthetic provider prompt continuation", ContextUsageSource: agentruntime.ContextUsageSkills,
			ToolCalls: []agentruntime.ToolCall{{ID: "call-one", Name: "synthetic_tool", Arguments: json.RawMessage(" \n{ \"z\": 2, \"a\": 1 }\t"), VerifiedEvidence: true}}}},
		Tools:   []agentruntime.ToolSchema{{Name: "synthetic_tool", Description: "Synthetic", Parameters: map[string]any{"type": "object"}, Capabilities: []string{"HOST_CAPABILITY"}, OutputSchema: map[string]any{"host": "HOST_SCHEMA"}}},
		Headers: map[string]string{"key": "HOST_HEADER"}, Metadata: map[string]any{"host": "HOST_METADATA"}, MediaPolicy: agentruntime.MediaPolicy{AllowedFileRoots: []string{"HOST_ROOT"}},
		MaxTokens: 2345, UseProviderDefaultOutputBudget: true, Temperature: &temperature, ToolChoice: map[string]any{"type": "auto"}, ReasoningMode: agentruntime.ReasoningModeDisabled,
	}
	raw, diagnostic := encodeMemoryForkedRequestView(&request)
	if diagnostic != "" {
		t.Fatal(diagnostic)
	}
	for _, forbidden := range []string{"HOST_CAPABILITY", "HOST_SCHEMA", "HOST_HEADER", "HOST_METADATA", "HOST_ROOT", "VerifiedEvidence"} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("persisted host-only field %s", forbidden)
		}
	}
	// Pass the private view through the real existing outbox canonical envelope,
	// not merely a DTO-only roundtrip. Argument bytes must remain untouched.
	f := prepareDurableMemoryFixture(t)
	rememberDurableMemoryFixture(t, f, request)
	if _, err := settleDurableMemoryFixture(t, f, "Recorded the provider request context.", nil); err != nil {
		t.Fatal(err)
	}
	event := claimDurableMemoryFixture(t, f.store, "view-roundtrip-worker")
	dispatch, err := workspace.DecodeMemoryExtractionDispatch(event)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeMemoryForkedRequestView(dispatch.Completion.RequestView)
	if err != nil || len(restored.Messages) != 1 || len(restored.Messages[0].ToolCalls) != 1 || restored.Messages[0].ToolCalls[0].ID != "call-one" ||
		restored.Messages[0].ToolCalls[0].VerifiedEvidence || string(restored.Messages[0].ToolCalls[0].Arguments) != string(request.Messages[0].ToolCalls[0].Arguments) || restored.Messages[0].ReasoningContent != request.Messages[0].ReasoningContent || len(restored.Tools) != 1 || restored.Tools[0].Parameters["type"] != "object" || restored.MaxTokens != 2345 || restored.Temperature == nil || *restored.Temperature != temperature {
		t.Fatalf("prompt-view roundtrip=%+v err=%v", restored, err)
	}
	// One record either fits in full or becomes an explicit diagnostic. It is
	// never partially truncated into a different supposedly exact request.
	request.Messages[0].Content = strings.Repeat("x", workspace.MemoryExtractionViewMaxBytes)
	if raw, diagnostic = encodeMemoryForkedRequestView(&request); len(raw) != 0 || diagnostic != "original-request-view-oversize" {
		t.Fatalf("view bound bytes=%d diagnostic=%q", len(raw), diagnostic)
	}
}

func TestMemoryExtractionDeadLetterDoesNotBlockLaterCompletedSource(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "First convention."}}})
	if _, err := settleDurableMemoryFixture(t, f, "Recorded the first convention.", nil); err != nil {
		t.Fatal(err)
	}
	first := claimDurableMemoryFixture(t, f.store, "failed-worker")
	appendSkillControlInput(t, f, "next-memory-input", "Retain explicit random seeds in reports.", false)
	claimSkillControlInput(t, f)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Second convention."}}})
	if _, err := settleDurableMemoryFixture(t, f, "Recorded the second convention.", nil); err != nil {
		t.Fatal(err)
	}
	blocked, err := f.store.ClaimOutbox(t.Context(), workspace.ClaimOutboxInput{WorkerID: "later-worker", Topics: []string{workspace.MemoryExtractionOutboxTopic}, Limit: 1, Lease: time.Minute})
	if err != nil || len(blocked) != 0 {
		t.Fatalf("same-root Memory ordering was bypassed: %+v %v", blocked, err)
	}
	for attempt := 1; attempt <= first.MaxAttempts; attempt++ {
		dead, err := f.store.RetryOutbox(t.Context(), first.ID, first.ClaimToken, "synthetic bounded provider failure", 0)
		if err != nil || dead != (attempt == first.MaxAttempts) {
			t.Fatalf("retry %d dead=%t err=%v", attempt, dead, err)
		}
		if !dead {
			first = claimDurableMemoryFixture(t, f.store, "failed-worker")
		}
	}
	later := claimDurableMemoryFixture(t, f.store, "later-worker")
	if later.ID == first.ID || later.PartitionKey != first.PartitionKey || !strings.HasPrefix(later.PartitionKey, "memory-extraction:") {
		t.Fatalf("dead letter blocked or crossed transport partition: first=%+v later=%+v", first, later)
	}
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			return durableMemoryFixtureAppend(), nil
		})
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), later); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("later completion blocked: %v", err)
	}
}

func TestMemoryExtractionDurableCadenceDecisionSurvivesFailureAndRestart(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	f.server.memoryConfig.ExtractEveryN = 2
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "First reporting convention."}}})
	if _, err := settleDurableMemoryFixture(t, f, "Recorded the first convention.", nil); err != nil {
		t.Fatal(err)
	}
	first := claimDurableMemoryFixture(t, f.store, "cadence-worker")
	firstValue, err := workspace.DecodeMemoryExtractionDispatch(first)
	if err != nil || firstValue.Cadence.Ordinal != 1 || firstValue.Cadence.EveryN != 2 || firstValue.Cadence.Eligible {
		t.Fatalf("first decision=%+v err=%v", firstValue.Cadence, err)
	}
	calls := 0
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			calls++
			return nil, context.DeadlineExceeded
		})
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), first); !errors.Is(err, outbox.ErrDeliverySettled) || calls != 0 {
		t.Fatalf("N=2 first decision called model: calls=%d err=%v", calls, err)
	}
	// Actual outbox retention is allowed to remove the old transport row; it
	// must not reset the project's private durable completion counter.
	if _, err := f.db.ExecContext(t.Context(), `DELETE FROM workspace_outbox WHERE event_id=? AND status='delivered'`, first.ID); err != nil {
		t.Fatal(err)
	}
	appendSkillControlInput(t, f, "second-cadence-input", "Use explicit random seeds for reproducible reports.", false)
	claimSkillControlInput(t, f)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Second reporting convention."}}})
	if _, err := settleDurableMemoryFixture(t, f, "Recorded explicit random seeds.", nil); err != nil {
		t.Fatal(err)
	}
	second := claimDurableMemoryFixture(t, f.store, "cadence-worker")
	secondValue, err := workspace.DecodeMemoryExtractionDispatch(second)
	if err != nil || secondValue.Cadence.Ordinal != 2 || secondValue.Cadence.EveryN != 2 || !secondValue.Cadence.Eligible {
		t.Fatalf("second decision=%+v err=%v", secondValue.Cadence, err)
	}
	deliveryErr := f.server.deliverMemoryExtraction(t.Context(), second)
	if deliveryErr == nil || errors.Is(deliveryErr, outbox.ErrDeliverySettled) || calls != 1 {
		t.Fatalf("initial eligible failure calls=%d err=%v", calls, deliveryErr)
	}
	if _, err := f.store.RetryOutbox(t.Context(), second.ID, second.ClaimToken, deliveryErr.Error(), 0); err != nil {
		t.Fatal(err)
	}
	requireNoDurableMemoryEffects(t, f)
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repo, err := reopened.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	restarted := New(Options{Workspace: reopened, Transcript: repo, FileRoot: f.server.fileRoot})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = restarted.Close(ctx)
	})
	restarted.memoryConfig = f.server.memoryConfig
	restarted.memoryConfig.ExtractEveryN = 19
	restarted.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		return memoryExtractorFunc(func(context.Context, memoryextract.ExtractRequest) (map[string]any, error) {
			calls++
			return durableMemoryFixtureAppend(), nil
		})
	}
	retry := claimDurableMemoryFixture(t, reopened, "restart-cadence-worker")
	if retry.ClaimToken == second.ClaimToken || string(retry.Payload) != string(second.Payload) {
		t.Fatal("retry changed its original decision/source/view")
	}
	if err := restarted.deliverMemoryExtraction(t.Context(), retry); !errors.Is(err, outbox.ErrDeliverySettled) || calls != 2 {
		t.Fatalf("eligible retry was skipped after restart: calls=%d err=%v", calls, err)
	}
	receipt, found, err := reopened.ReadMemoryExtractionReceipt(t.Context(), secondValue.Source)
	if err != nil || !found || !receipt.CursorAdvanced || len(receipt.Applied.Appended) != 1 {
		t.Fatalf("retry receipt=%+v found=%t err=%v", receipt, found, err)
	}
	var ordinal int64
	if err := f.db.QueryRowContext(t.Context(), `SELECT memory_completion_ordinal FROM project_runtime_metadata WHERE project_id=?`, f.stream.ProjectID).Scan(&ordinal); err != nil || ordinal != 2 {
		t.Fatalf("retry incremented original cadence: ordinal=%d err=%v", ordinal, err)
	}
}

func TestMemoryExtractionPreparationFailureDoesNotBlockMainCompletion(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "Retain reproducible reporting conventions."}}})
	// A directory is not a settings JSON file: this exercises the existing
	// optional preparation error without injecting a production failure hook.
	f.server.settingsStore = settingsstore.New(t.TempDir())
	if _, err := f.server.memoryAutoExtractionEnabled(f.stream.OwnerID); err == nil {
		t.Fatal("fixture did not produce a real optional settings-read failure")
	}
	result, err := settleDurableMemoryFixture(t, f, "Completed the requested task.", nil)
	if err != nil || result.Status != "completed" || result.FinishEventID <= 0 {
		t.Fatalf("optional Memory preparation blocked main finish: %+v %v", result, err)
	}
	if _, err := f.repo.ReadCompletedMemorySource(t.Context(), f.stream.UID, f.stream.OwnerID, result.FinishEventID); err != nil {
		t.Fatal(err)
	}
	event := claimDurableMemoryFixture(t, f.store, "preparation-failure-worker")
	value, err := workspace.DecodeMemoryExtractionDispatch(event)
	if err != nil || value.Completion.Diagnostic != "memory-preparation-unavailable" {
		t.Fatalf("missing failed disposition: %+v %v", value.Completion, err)
	}
	f.server.memoryExtraction.factory = func(memorytools.Scope, string, *agentruntime.ModelRequest) memoryextract.Extractor {
		t.Fatal("failed preparation authorized a model call")
		return nil
	}
	if err := f.server.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("preparation diagnostic settlement=%v", err)
	}
	requireNoDurableMemoryEffects(t, f)
	saved, err := f.store.GetOutboxEvent(t.Context(), event.ID)
	if err != nil || saved.Status != workspace.OutboxStatusDeadLetter || saved.LastError != "memory-preparation-unavailable" {
		t.Fatalf("failed preparation disposition=%+v %v", saved, err)
	}
}

func TestMemoryExtractionExistingRootDeletionRemovesPrivateDispatch(t *testing.T) {
	f := prepareDurableMemoryFixture(t)
	rememberDurableMemoryFixture(t, f, agentruntime.ModelRequest{Messages: []agentruntime.Message{{Role: "user", Content: "PRIVATE_SYNTHETIC_MEMORY_CONTEXT"}}})
	if _, err := settleDurableMemoryFixture(t, f, "Completed the reporting task.", nil); err != nil {
		t.Fatal(err)
	}
	event := claimDurableMemoryFixture(t, f.store, "deletion-worker")
	value, err := workspace.DecodeMemoryExtractionDispatch(event)
	if err != nil || event.AggregateType != "frame" || !strings.Contains(string(event.Payload), "PRIVATE_SYNTHETIC_MEMORY_CONTEXT") {
		t.Fatalf("missing frame-owned private fixture: %+v %v", event, err)
	}
	if _, err := f.store.DeleteCompatibilityFrameTree(f.stream.FrameID, f.stream.OwnerID, value.Source.Transcript.FrameIncarnationID); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := f.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM workspace_outbox WHERE event_id=? OR instr(payload_json,'PRIVATE_SYNTHETIC_MEMORY_CONTEXT')>0`, event.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("deleted root retained private dispatch: rows=%d err=%v", rows, err)
	}
	if err := f.store.SettleMemoryExtractionDelivery(t.Context(), event, ""); !errors.Is(err, workspace.ErrOutboxClaimLost) {
		t.Fatalf("deleted inflight job accepted late settlement: %v", err)
	}
}

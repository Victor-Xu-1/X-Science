package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	"x-science/internal/outbox"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/providers"
)

const forkedResponseSourceView = "ORIGINAL_FORKED_REQUEST_FOR_DURABLE_ACK"
const forkedResponsePrivateBody = "PRIVATE_UNACKNOWLEDGED_MEMORY_RESPONSE"

type forkedResponseDeliveryFixture struct {
	base     *agentSaveArtifactsFixture
	provider *httptest.Server
	reply    atomic.Value
	calls    atomic.Int64
	event    workspace.OutboxEvent
	value    workspace.MemoryExtractionDispatch
}

func newForkedResponseDeliveryFixture(t *testing.T, response string) *forkedResponseDeliveryFixture {
	t.Helper()
	f := &forkedResponseDeliveryFixture{base: prepareDurableMemoryFixture(t)}
	f.reply.Store(response)
	f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || r.Method != http.MethodPost ||
			r.URL.Path != "/v1/messages" || payload["model"] != memoryExtractionTestModel {
			t.Errorf("fixture did not reach its selected local provider: method=%s path=%s model=%v err=%v", r.Method, r.URL.Path, payload["model"], err)
			http.Error(w, "invalid local provider fixture request", http.StatusBadRequest)
			return
		}
		encoded, _ := json.Marshal(payload)
		if !strings.Contains(string(encoded), forkedResponseSourceView) {
			t.Error("default forked adapter did not consume the original recorded request view")
		}
		f.calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_durable_memory_ack", "role": "assistant", "model": memoryExtractionTestModel,
			"content":     []any{map[string]any{"type": "text", "text": f.reply.Load().(string)}},
			"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 11, "output_tokens": 3},
		})
	}))
	t.Cleanup(f.provider.Close)
	const providerID = "durable-forked-response-provider"
	enabled := true
	if _, err := f.base.store.RegisterModelProvider(workspace.ModelProviderInput{
		ID: providerID, UserID: f.base.stream.OwnerID, Name: "Durable response fixture provider",
		Type: "anthropic", BaseURL: f.provider.URL + "/v1", Model: memoryExtractionTestModel, Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	f.base.server.httpClient = f.provider.Client()
	f.base.server.memoryConfig.ExtractMode = "forked"
	selection := "provider:" + providerID
	profile, err := f.base.server.resolveUserModelSelectionProfileWithContext(t.Context(), f.base.stream.OwnerID, selection,
		providers.ResolutionInput{Context: t.Context(), ProjectID: f.base.stream.ProjectID})
	if err != nil {
		t.Fatal(err)
	}
	identity := memoryExtractionIdentityFromResolved(sessionRunnerResolvedModelClient{
		model: profile.Model, selection: selection, contextProfile: &profile,
		identity: strings.Join([]string{strings.TrimSpace(profile.Provider.ID), strings.TrimSpace(profile.Provider.Endpoint), strings.TrimSpace(profile.Model)}, "\x00"),
	})
	request := f.base.server.memoryExtraction.recordLastRequest(f.base.stream.SessionID, f.base.claim.Attempt, agentruntime.ModelRequest{
		Messages: []agentruntime.Message{
			{Role: "system", Content: forkedResponseSourceView},
			{Role: "user", Content: "Retain explicit random seeds for reproducible reports."},
		},
	})
	f.base.server.memoryExtraction.bindRecordedModel(f.base.stream.SessionID, f.base.claim.Attempt, request, identity)
	result, err := settleDurableMemoryFixture(t, f.base, "The reporting convention is retained.", nil)
	if err != nil || result.Status != "completed" {
		t.Fatalf("optional extraction changed main completion: result=%+v err=%v", result, err)
	}
	f.event = claimDurableMemoryFixture(t, f.base.store, "forked-response-first-worker")
	f.value, err = workspace.DecodeMemoryExtractionDispatch(f.event)
	if err != nil || f.value.Source.Transcript == nil || f.value.Completion.Mode != "forked" ||
		f.value.Completion.Model != identity || f.value.Completion.Diagnostic != "" || len(f.value.Completion.RequestView) == 0 ||
		f.event.AttemptCount != 1 || f.event.MaxAttempts != 8 {
		t.Fatalf("fixture did not capture the canonical completed source and original view: dispatch=%+v event=%+v err=%v", f.value, f.event, err)
	}
	return f
}

func (f *forkedResponseDeliveryFixture) reopenServer(t *testing.T) *Server {
	t.Helper()
	store, err := workspace.Open(f.base.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	server := New(Options{Workspace: store, Transcript: repo, FileRoot: f.base.server.fileRoot})
	server.memoryConfig = f.base.server.memoryConfig
	server.settingsStore = f.base.server.settingsStore
	server.httpClient = f.provider.Client()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := server.Close(ctx); err != nil {
			t.Errorf("close reopened fixture server: %v", err)
		}
	})
	// No replacement factory or remembered RAM request: use the actual default
	// adapter, restored durable view and persisted original model selection.
	return server
}

func (f *forkedResponseDeliveryFixture) commitEmptyAndReplay(t *testing.T, server *Server, event workspace.OutboxEvent, wantCalls int64) {
	t.Helper()
	if err := server.deliverMemoryExtraction(t.Context(), event); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("explicit empty acknowledgement did not settle: %v", err)
	}
	if f.calls.Load() != wantCalls {
		t.Fatalf("wrong actual provider call count: got=%d want=%d", f.calls.Load(), wantCalls)
	}
	receipt, found, err := server.workspaceStore.ReadMemoryExtractionReceipt(t.Context(), f.value.Source)
	if err != nil || !found || receipt.Gate != "done" || !receipt.CursorAdvanced || receipt.CursorAfter <= 0 || receipt.InputSHA256 == "" {
		t.Fatalf("valid no-op did not commit its source-bound receipt/cursor: receipt=%+v found=%t err=%v", receipt, found, err)
	}
	rows, err := server.workspaceStore.ListMemoryExtractionRows(t.Context(), f.base.stream.OwnerID, f.base.stream.ProjectID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("explicit empty acknowledgement created Memory rows: rows=%+v err=%v", rows, err)
	}
	ack, err := server.workspaceStore.GetOutboxEvent(t.Context(), event.ID)
	if err != nil || ack.Status != workspace.OutboxStatusDelivered || ack.LastError != "" {
		t.Fatalf("valid no-op did not atomically ACK its job: event=%+v err=%v", ack, err)
	}
	// Existing fixture-only lost-ACK seam. Keep the real source/receipt intact;
	// expose the same envelope for reclaim, never create another extraction.
	if _, err := f.base.db.ExecContext(t.Context(), `UPDATE workspace_outbox SET status='pending',delivered_at_ms=NULL,available_at_ms=0 WHERE event_id=?`, event.ID); err != nil {
		t.Fatal(err)
	}
	restarted := f.reopenServer(t)
	takeover := claimDurableMemoryFixture(t, restarted.workspaceStore, "forked-response-replay-worker")
	if takeover.ID != event.ID || takeover.IdempotencyKey != event.IdempotencyKey ||
		!bytes.Equal(takeover.Payload, event.Payload) || takeover.ClaimToken == event.ClaimToken {
		t.Fatal("receipt replay changed the original source/envelope or reused its claim")
	}
	if err := restarted.deliverMemoryExtraction(t.Context(), takeover); !errors.Is(err, outbox.ErrDeliverySettled) {
		t.Fatalf("committed no-op did not replay: %v", err)
	}
	replayed, found, err := restarted.workspaceStore.ReadMemoryExtractionReceipt(t.Context(), f.value.Source)
	if err != nil || !found || !reflect.DeepEqual(replayed, receipt) || f.calls.Load() != wantCalls {
		t.Fatalf("receipt replay changed its outcome or called the model: receipt=%+v found=%t calls=%d err=%v", replayed, found, f.calls.Load(), err)
	}
	cursor, err := restarted.workspaceStore.LastExtractMessageIndex(t.Context(), f.base.stream.FrameID)
	if err != nil || cursor != receipt.CursorAfter {
		t.Fatalf("replay changed the committed cursor: cursor=%d err=%v", cursor, err)
	}
	var count int
	if err := f.base.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM memory_extraction_receipts`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("same source committed more than once: receipts=%d err=%v", count, err)
	}
	ack, err = restarted.workspaceStore.GetOutboxEvent(t.Context(), event.ID)
	if err != nil || ack.Status != workspace.OutboxStatusDelivered {
		t.Fatalf("receipt replay left the job unsettled: event=%+v err=%v", ack, err)
	}
}

func TestMemoryForkedDurableMalformedOutcomeRetriesSameSource(t *testing.T) {
	for name, response := range map[string]string{
		"no object":    forkedResponsePrivateBody,
		"truncated":    forkedResponsePrivateBody + ` before {"append": [`,
		"invalid JSON": forkedResponsePrivateBody + ` before {append: []} after`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newForkedResponseDeliveryFixture(t, response)
			err := f.base.server.deliverMemoryExtraction(t.Context(), f.event)
			if f.calls.Load() != 1 {
				t.Fatalf("fixture did not reach exactly one real default adapter/provider call: calls=%d err=%v", f.calls.Load(), err)
			}
			var failure *memoryExtractionDeliveryError
			if err == nil || errors.Is(err, outbox.ErrDeliverySettled) || !errors.As(err, &failure) || failure.gate != "extractor-failed" {
				t.Fatalf("malformed response was acknowledged instead of retaining the source: %v", err)
			}
			if strings.Contains(err.Error(), forkedResponsePrivateBody) || failure.Unwrap() == nil || strings.Contains(failure.Unwrap().Error(), forkedResponsePrivateBody) {
				t.Fatalf("unbounded provider response escaped into diagnostics: %v", err)
			}
			requireNoDurableMemoryEffects(t, f.base)
			if _, found, receiptErr := f.base.store.ReadMemoryExtractionReceipt(t.Context(), f.value.Source); receiptErr != nil || found {
				t.Fatalf("invalid response acquired an immutable success receipt: found=%t err=%v", found, receiptErr)
			}
			pending, getErr := f.base.store.GetOutboxEvent(t.Context(), f.event.ID)
			if getErr != nil || pending.Status != workspace.OutboxStatusInflight || pending.ClaimToken != f.event.ClaimToken {
				t.Fatalf("invalid extraction settled or lost its original claim: event=%+v err=%v", pending, getErr)
			}
			// Exercise one original RetryOutbox transition without a clock sleep;
			// production backoff/attempt limits and scheduler are not changed.
			if _, retryErr := f.base.store.RetryOutbox(t.Context(), f.event.ID, f.event.ClaimToken, err.Error(), 0); retryErr != nil {
				t.Fatal(retryErr)
			}
			pending, getErr = f.base.store.GetOutboxEvent(t.Context(), f.event.ID)
			if getErr != nil || pending.Status != workspace.OutboxStatusPending || pending.AttemptCount != 1 || pending.MaxAttempts != 8 ||
				!strings.Contains(pending.LastError, "gate=extractor-failed") || strings.Contains(pending.LastError, forkedResponsePrivateBody) {
				t.Fatalf("original bounded retry/privacy contract changed: event=%+v err=%v", pending, getErr)
			}
			f.reply.Store("{}")
			restarted := f.reopenServer(t)
			retry := claimDurableMemoryFixture(t, restarted.workspaceStore, "forked-response-valid-worker")
			if retry.ID != f.event.ID || retry.IdempotencyKey != f.event.IdempotencyKey || !bytes.Equal(retry.Payload, f.event.Payload) ||
				retry.ClaimToken == f.event.ClaimToken || retry.AttemptCount != 2 || retry.MaxAttempts != 8 {
				t.Fatal("retry did not preserve the original source/view or obtain a new claim")
			}
			f.commitEmptyAndReplay(t, restarted, retry, 2)
		})
	}
}

func TestMemoryForkedDurableExplicitEmptyOutcomeReplaysWithoutModel(t *testing.T) {
	f := newForkedResponseDeliveryFixture(t, "{}")
	f.commitEmptyAndReplay(t, f.base.server, f.event, 1)
}

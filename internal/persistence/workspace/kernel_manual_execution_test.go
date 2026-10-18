package workspace

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newManualKernelStoreFixture(t *testing.T) (*Store, KernelFrameAccess, AdmitManualKernelExecutionInput) {
	t.Helper()
	store, _, stream := newTranscriptCancellationFixtureWithoutRunnerClaim(t)
	access, found, err := store.GetKernelFrameAccess(stream.FrameID)
	if err != nil || !found {
		t.Fatalf("access found=%t err=%v", found, err)
	}
	request := ManualKernelRequest{RequestID: uuid.NewString(), Source: "print('frozen')", Language: "python", Environment: "python", TimeoutMillis: 1000}
	binding := manualKernelAccessBinding(access)
	binding.ExecutionID = ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID)
	binding.KernelID, binding.KernelGeneration, binding.KernelKind, binding.ServerBootID = "manual-kernel", 7, "analysis", "original-boot"
	return store, access, AdmitManualKernelExecutionInput{Binding: binding, Request: request}
}

func TestManualKernelAdmissionReplaysExactRequestWithoutPlaceholderLog(t *testing.T) {
	store, access, input := newManualKernelStoreFixture(t)
	first, created, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil || !created || first.State != "queued" {
		t.Fatalf("admit=%#v created=%t err=%v", first, created, err)
	}
	again, created, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil || created || again.ExecutionID != first.ExecutionID || again.StateVersion != first.StateVersion {
		t.Fatalf("replay=%#v created=%t err=%v", again, created, err)
	}
	lookup, found, err := store.LookupManualKernelExecution(context.Background(), access, input.Request)
	if err != nil || !found || lookup.RequestSHA256 != first.RequestSHA256 {
		t.Fatalf("lookup=%#v found=%t err=%v", lookup, found, err)
	}
	for _, mutate := range []func(*AdmitManualKernelExecutionInput){
		func(v *AdmitManualKernelExecutionInput) { v.Request.Source = "print('changed')" },
		func(v *AdmitManualKernelExecutionInput) { v.Request.TimeoutMillis++ },
		func(v *AdmitManualKernelExecutionInput) { v.Request.Environment = "other" },
		func(v *AdmitManualKernelExecutionInput) { v.Binding.KernelGeneration++ },
	} {
		changed := input
		mutate(&changed)
		if _, _, err := store.AdmitManualKernelExecution(context.Background(), changed); !errors.Is(err, ErrManualKernelExecutionConflict) {
			t.Fatalf("changed admission accepted: %v", err)
		}
	}
	page, found, err := store.ListCompatibilityExecutionLogPage(context.Background(), access.UserID, access.Frame.ID, "", 20)
	if err != nil || !found || page.Total != 0 || len(page.Records) != 0 {
		t.Fatalf("placeholder log: %#v %v", page, err)
	}
}

func TestManualKernelIdentityAndCASRejectScopeIncarnationAndChangedHandle(t *testing.T) {
	store, access, input := newManualKernelStoreFixture(t)
	record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	wrong := access
	wrong.UserID = "different-owner"
	if _, _, err := store.GetManualKernelExecution(context.Background(), wrong, record.ExecutionID); err == nil {
		t.Fatal("foreign owner read receipt")
	}
	changed := record
	changed.KernelGeneration++
	if _, err := store.RequestManualKernelExecutionCancellation(context.Background(), changed); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("changed handle accepted: %v", err)
	}
	started, err := store.MarkManualKernelExecutionStarted(context.Background(), record, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestManualKernelExecutionCancellation(context.Background(), record); !errors.Is(err, ErrManualKernelExecutionStale) {
		t.Fatalf("stale transition accepted: %v", err)
	}
	if _, err := store.db.Exec(`UPDATE frames SET incarnation_id='replacement' WHERE id=?`, access.Frame.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RequestManualKernelExecutionCancellation(context.Background(), started); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("old incarnation cancelled replacement: %v", err)
	}
	if _, _, err := store.GetManualKernelExecution(context.Background(), access, record.ExecutionID); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("old incarnation read accepted: %v", err)
	}
}

func TestManualKernelQueuedCancelAndUnknownNeverBecomeExecutedLog(t *testing.T) {
	store, access, input := newManualKernelStoreFixture(t)
	record, _, err := store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	record, err = store.RequestManualKernelExecutionCancellation(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := store.CompleteManualKernelExecution(context.Background(), ManualKernelCompletionInput{Expected: record, Disposition: "cancelled_before_start", FinishedAt: time.Now().UTC()})
	if err != nil || terminal.StartedAt != nil || terminal.ExecutionLogID != "" {
		t.Fatalf("queued cancellation=%#v err=%v", terminal, err)
	}
	input.Request.RequestID = uuid.NewString()
	input.Binding.ExecutionID = ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, input.Request.RequestID)
	record, _, err = store.AdmitManualKernelExecution(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := store.MarkManualKernelExecutionUnknown(context.Background(), record, "worker_outcome_unconfirmed")
	if err != nil || unknown.State != "outcome_unknown" || unknown.FinishedAt != nil {
		t.Fatalf("unknown=%#v err=%v", unknown, err)
	}
	if _, err := store.CompleteManualKernelExecution(context.Background(), ManualKernelCompletionInput{Expected: unknown, Disposition: "rejected_before_start", FinishedAt: time.Now().UTC()}); !errors.Is(err, ErrManualKernelExecutionStale) {
		t.Fatalf("unknown became stopped: %v", err)
	}
	input.Binding.ServerBootID = "new-boot"
	if _, _, err := store.AdmitManualKernelExecution(context.Background(), input); !errors.Is(err, ErrManualKernelExecutionConflict) {
		t.Fatalf("new boot re-admitted unknown: %v", err)
	}
	logs, err := store.ListExecutionLog(access.Frame.ID, "")
	if err != nil || len(logs) != 0 {
		t.Fatalf("nonexecuted log=%#v err=%v", logs, err)
	}
}

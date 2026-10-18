package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	workspace "x-science/internal/persistence/workspace"
)

func TestDelegatedStopOldOwnerExitPreservesAndStartsNewQueuedInput(t *testing.T) {
	store, manager, app, identity := newKernelHostTestRuntime(t, filepath.Join(t.TempDir(), "workspace.db"), true, kernelHostTestRuntimeOptions{CanonicalTranscript: true})
	defer closeKernelHostTestRuntime(t, app, manager, store)
	providerStarted, releaseProvider := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	var calls atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		raw, _ := json.Marshal(body["messages"])
		if !strings.Contains(string(raw), "New explicitly queued task") {
			t.Error("replacement provider did not receive the new queued input")
		}
		startedOnce.Do(func() { close(providerStarted) })
		select {
		case <-releaseProvider:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"new task finished"}}]}`))
	}))
	defer provider.Close()
	defer releaseOnce.Do(func() { close(releaseProvider) })
	configureKernelHostProvider(t, app, identity.access, "handoff-provider", "openai", provider.URL+"/v1", "handoff-model", "handoff-secret", "handoff-key")
	children, err := store.CreateKernelSupervisedChildren(t.Context(), workspace.CreateKernelDelegatesInput{
		ParentFrameID: identity.access.Frame.ID, OwnerUserID: identity.access.UserID, ToolUseID: "stop-handoff",
		Requests: []workspace.KernelDelegateRequest{{Task: "Original cancelled task", Model: "handoff-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	child := children[0]
	if err := app.ensureKernelChildSession(child); err != nil {
		t.Fatal(err)
	}
	app.startKernelChildRun(child, make(chan struct{}))
	old := kernelSupervisionRunAtBoundary(t, app, child.FrameID)
	stopEntered, releaseStop := make(chan struct{}), make(chan struct{})
	var releaseStopOnce sync.Once
	defer releaseStopOnce.Do(func() { close(releaseStop) })
	old.targetMu.Lock()
	originalStop := old.stop
	old.stop = func() {
		close(stopEntered)
		<-releaseStop
		originalStop()
	}
	old.targetMu.Unlock()
	scope := workspace.DelegatedStopScope{ParentFrameID: identity.access.Frame.ID, ParentFrameIncarnationID: identity.access.Frame.IncarnationID,
		RootFrameID: identity.access.Frame.RootFrameID, RootFrameIncarnationID: identity.access.RootFrameIncarnationID}
	receipt, err := store.AdmitKernelChildStop(t.Context(), identity.access.UserID, scope, child.FrameID, "controlled stop", "stop-before-new-queue")
	if err != nil {
		t.Fatal(err)
	}
	signalled := make(chan error, 1)
	go func() {
		_, err := app.signalDelegatedStop(t.Context(), identity.access.UserID, &receipt)
		signalled <- err
	}()
	select {
	case <-stopEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("old exact supervisor signal did not arrive")
	}
	queued, err := app.sendKernelChildMessage(context.Background(), identity.access, child.FrameID, "New explicitly queued task", "info", "new-after-canonical-stop", nil)
	if err != nil || queued["status"] != "resumed" {
		t.Fatalf("new input receipt=%#v err=%v", queued, err)
	}
	releaseStopOnce.Do(func() { close(releaseStop) })
	if err := <-signalled; err != nil {
		t.Fatal(err)
	}
	waitForKernelSupervisionRun(t, old, "old cancelled owner exit")
	frame, found, err := store.GetFrame(child.FrameID)
	if err != nil || !found || frame.Status == "cancelled" {
		t.Errorf("old owner overwrote new input frame=%#v found=%t err=%v", frame, found, err)
	}
	events, err := store.ListFrameEvents(identity.access.Frame.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	cancelledNotifications := 0
	for _, event := range events {
		payload, _ := event.Payload["payload"].(map[string]any)
		if event.Type == "notification" && event.Payload["notification_type"] == "child_landed" && payload["status"] == "cancelled" {
			cancelledNotifications++
		}
	}
	if cancelledNotifications != 1 {
		t.Errorf("old cancellation projected %d completion notifications, want 1", cancelledNotifications)
	}
	waitForKernelSupervisionSignal(t, providerStarted, "new input owner handoff")
	next := kernelSupervisionRunAtBoundary(t, app, child.FrameID)
	if next == old {
		t.Fatal("cancelled supervisor reused for new input")
	}
	releaseOnce.Do(func() { close(releaseProvider) })
	waitForKernelSupervisionRun(t, next, "new input completion")
	if calls.Load() != 1 {
		t.Fatalf("new input executed %d times", calls.Load())
	}
	if pending, err := store.KernelChildHasPendingMessages(t.Context(), child.FrameID, identity.access.UserID); err != nil || pending {
		t.Fatalf("new queue not consumed exactly once pending=%t err=%v", pending, err)
	}
}

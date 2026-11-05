package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestTranscriptWebOrdinaryToolHistoryDoesNotWaitForUnrelatedWriter(t *testing.T) {
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "ordinary-history-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim ordinary tool attempt: claimed=%t err=%v", claim.Claimed, err)
	}
	appendRunnerToolCheckpoint(t, fixture.repository, claim.Claim, "ordinary-history-tool", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolCallId": "ordinary-history-call",
		"toolName": "read_file", "toolInput": map[string]any{"path": "synthetic.txt"},
		"toolResult": map[string]any{"content": "synthetic tool result"},
	})
	if err := fixture.server.runTranscriptWebReadModelCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	fence := requireTranscriptWebReadModelFence(t, fixture.readModel, fixture.stream, fixture.fence.BranchID)
	if !transcriptWebFenceReady(fence) {
		t.Fatal("ordinary tool fixture did not reach a verified read-model fence")
	}
	base := "/api/conversations/" + transcriptWebServingFrameID + "/messages"
	warm := p3JSONRequest(t, fixture.server, http.MethodGet, base+"?limit=80&content_mode=compact", nil, "")
	if warm.Code != http.StatusOK {
		t.Fatalf("warm exact compact history: HTTP%d", warm.Code)
	}
	tool := findProjectedMessageByCallID(transcriptWebServingItems(t, p3DecodeObject(t, warm), "items"), "ordinary-history-call")
	messageID := webString(tool["id"])
	if messageID == "" {
		t.Fatal("real tool checkpoint did not project its history message")
	}
	for _, endpoint := range []struct{ name, path string }{
		{"page", base + "?limit=80&content_mode=compact"},
		{"detail", base + "/" + url.PathEscape(messageID)},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fence.BranchID)
			releaseWriter := holdTranscriptWebUnrelatedWriter(t, fixture)
			defer releaseWriter()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := newLoopbackTestRequest(http.MethodGet, endpoint.path, nil).WithContext(ctx)
			request.RemoteAddr = "127.0.0.1:12345"
			response := httptest.NewRecorder()
			completed := make(chan struct{})
			go func() {
				fixture.server.Handler().ServeHTTP(response, request)
				close(completed)
			}()
			// Observe the actual SQL pool wait, not a machine-speed threshold. The
			// held transaction executes no SQL and leaves all read connections free.
			deadline := time.NewTimer(2 * time.Second)
			defer deadline.Stop()
			stack := make([]byte, 128<<10)
			for {
				select {
				case <-completed:
					goto settled
				case <-deadline.C:
					t.Error("ordinary tool history did not settle while an unrelated writer connection was held")
					for _, goroutine := range strings.Split(string(stack[:runtime.Stack(stack, true)]), "\n\n") {
						if strings.Contains(goroutine, "TestTranscriptWebOrdinaryToolHistoryDoesNotWaitForUnrelatedWriter.func1.1") {
							t.Logf("synthetic HTTP blocked stack:\n%s", goroutine)
						}
					}
					releaseWriter()
					goto awaitReleased
				default:
				}
				runtime.Gosched()
			}
		awaitReleased:
			select {
			case <-completed:
			case <-time.After(2 * time.Second):
				t.Fatal("history did not settle after writer cleanup")
			}
		settled:
			if response.Code != http.StatusOK {
				t.Fatalf("ordinary history HTTP%d", response.Code)
			}
			releaseWriter()
			if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, fence.BranchID); after != before {
				t.Fatal("ready history read rewrote its projection")
			}
		})
	}
}

func holdTranscriptWebUnrelatedWriter(t *testing.T, fixture transcriptWebReadModelServingFixture) func() {
	t.Helper()
	held, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	go func() {
		done <- fixture.store.WithTransaction(t.Context(), func(*sql.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	releaseWriter := func() {
		once.Do(func() {
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("release unrelated writer: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Error("unrelated writer did not finish")
			}
		})
	}
	select {
	case <-held:
	case <-time.After(2 * time.Second):
		releaseWriter()
		t.Fatal("unrelated writer barrier did not start")
	}
	return releaseWriter
}

package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"x-science/internal/agentruntime"
)

func TestProviderPlanningFailureRetainsTypedIdentityAcrossRealStream(t *testing.T) {
	for _, prefix := range []string{"", "Accepted prefix."} {
		name := "private-only"
		if prefix != "" {
			name = "accepted-public-prefix"
		}
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if prefix != "" {
					fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", prefix)
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private\"}}]}\n\n")
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"private\"}}]}\n\n")
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer server.Close()
			client, err := NewRuntimeModelClient(ModelProfile{
				Provider: ProviderProfile{ID: "planning-identity", Protocol: ProtocolOpenAICompatible, Endpoint: server.URL},
				Model:    "model", Request: RequestProfile{Timeout: 100 * time.Millisecond, MaxAttempts: 3, MaxResponseBytes: 64 << 10},
			}, server.Client(), nil)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var accepted strings.Builder
			_, err = client.(agentruntime.StreamingModelClient).CompleteStream(ctx, agentruntime.ModelRequest{
				Messages: []agentruntime.Message{{Role: "user", Content: "Perform the next analysis."}},
				Tools:    []agentruntime.ToolSchema{{Name: "analysis", Parameters: map[string]any{"type": "object"}}},
			}, func(event agentruntime.ModelStreamEvent) error {
				accepted.WriteString(event.ContentDelta)
				return nil
			})
			if !IsProviderPlanningNoProgress(err) || !errors.Is(err, errProviderPlanningNoProgress) {
				t.Fatalf("action deadline lost its typed cause: %v", err)
			}
			if IsProviderEmptyResponse(err) != (prefix == "") || IsRecoverableStreamInterruption(err) != (prefix != "") {
				t.Fatalf("typed cause changed the existing recovery contract: %v", err)
			}
			if IsProviderPlanningNoNativeProgress(err) != (prefix == "") {
				t.Fatalf("action-window native evidence was not preserved: %v", err)
			}
			if accepted.String() != prefix || requests.Load() != 1 || ctx.Err() != nil {
				t.Fatalf("private output leaked or accepted output was replayed: text=%q requests=%d parent=%v", accepted.String(), requests.Load(), ctx.Err())
			}
		})
	}
}

func TestProviderPlanningFailureIdentityRejectsOtherErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{"generic-empty-same-text", newProviderEmptyResponseError(errProviderPlanningNoProgress.Error())},
		{"plain-same-text", errors.New(errProviderPlanningNoProgress.Error())},
		{"header-timeout", errOpenAIChatStreamFirstByteTimeout},
		{"http-503", newResponseHTTPError(http.StatusServiceUnavailable, errProviderPlanningNoProgress.Error(), nil)},
		{"eof", io.EOF},
		{"unexpected-eof", io.ErrUnexpectedEOF},
		{"cancelled", context.Canceled},
		{"deadline", context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, emitted := range []bool{false, true} {
				got := classifyProviderStreamInterruption(context.Background(), context.Background(), emitted, test.err)
				if IsProviderPlanningNoProgress(got) || IsProviderPlanningNoNativeProgress(got) || !errors.Is(got, test.err) {
					t.Fatalf("unrelated failure acquired planning authority or lost identity: %v", got)
				}
			}
		})
	}
}

func TestProviderPlanningNoNativeProgressRequiresTimerObservation(t *testing.T) {
	// Old/general empty wrappers have no observation of tool-boundary absence.
	legacy := &providerEmptyResponseError{message: errProviderPlanningNoProgress.Error(), cause: errProviderPlanningNoProgress}
	if !IsProviderPlanningNoProgress(legacy) || IsProviderPlanningNoNativeProgress(legacy) {
		t.Fatal("a deadline label without native evidence acquired immediate-park authority")
	}
}

func TestProviderPlanningDeadlineRetainsBoundaryRacingCancellation(t *testing.T) {
	parent, stopParent := context.WithTimeout(context.Background(), time.Second)
	defer stopParent()
	ctx, cancel, emit, planning := toolPlanningStreamContext(parent,
		agentruntime.ModelRequest{Tools: []agentruntime.ToolSchema{{Name: "lookup"}}}, 10*time.Millisecond, nil)
	defer cancel(nil)
	defer planning.Stop()
	planning.HeadersReceived()
	<-ctx.Done()
	if parent.Err() != nil {
		t.Fatalf("operation timer did not fire: %v", parent.Err())
	}
	failure := classifyProviderStreamInterruption(parent, ctx, false, context.Cause(ctx))
	if !IsProviderPlanningNoNativeProgress(failure) {
		t.Fatalf("missing timer observation: %v", failure)
	}
	// A decoder may already hold this event when cancellation wins the timer
	// race. It is still not a complete tool call or accepted public content.
	if err := emit(agentruntime.ModelStreamEvent{Kind: agentruntime.ModelStreamEventToolCallBoundary}); err != nil {
		t.Fatal(err)
	}
	if !IsProviderEmptyResponse(failure) || !IsProviderPlanningNoProgress(failure) || IsProviderPlanningNoNativeProgress(failure) {
		t.Fatalf("timer snapshot erased a native boundary: %v", failure)
	}
}

func TestProviderPlanningFailureIdentityPreservesCallerCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	request, cancelRequest := context.WithCancelCause(parent)
	defer cancelRequest(nil)
	// Even if the operation deadline already fired, caller cancellation wins.
	cancelRequest(errProviderPlanningNoProgress)
	cancelParent()
	for _, emitted := range []bool{false, true} {
		got := classifyProviderStreamInterruption(parent, request, emitted, errProviderPlanningNoProgress)
		if !errors.Is(got, context.Canceled) || IsProviderPlanningNoProgress(got) || IsProviderEmptyResponse(got) || IsRecoverableStreamInterruption(got) {
			t.Fatalf("caller cancellation was reclassified: %v", got)
		}
	}
}

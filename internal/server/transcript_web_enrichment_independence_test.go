package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/persistence/workspace"
)

func TestTranscriptWebOrdinaryToolEnrichmentDoesNotLoadUnrelatedLineage(t *testing.T) {
	fixture := newTranscriptWebReadModelServingFixture(t, 1)
	claim, err := fixture.repository.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: fixture.stream.UID, OwnerID: fixture.stream.OwnerID, RunnerID: "ordinary-lineage-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claim.Claimed {
		t.Fatalf("claim ordinary tool attempt: claimed=%t err=%v", claim.Claimed, err)
	}
	appendRunnerToolCheckpoint(t, fixture.repository, claim.Claim, "ordinary-lineage-tool", map[string]any{
		"status": "completed", "toolPhase": "completed", "toolCallId": "ordinary-lineage-call",
		"toolName": "synthetic_nested_reader", "toolInput": map[string]any{"section": "summary"},
		"toolResult": map[string]any{"content": "complete output\n\nsecond paragraph", "records": []any{
			map[string]any{"label": "synthetic", "values": []any{1, true, nil}},
		}},
	})
	if err := fixture.server.runTranscriptWebReadModelCycle(t.Context()); err != nil {
		t.Fatal(err)
	}
	active, found, err := fixture.server.activatedTranscriptWebReadModel(t.Context(), fixture.stream.OwnerID,
		transcriptWebServingFrameID, fixture.fence.BranchID)
	if err != nil || !found || !transcriptWebFenceReady(active.fence) {
		t.Fatalf("real tool source is not ready: active=%t err=%v", found, err)
	}
	base := "/api/conversations/" + transcriptWebServingFrameID + "/messages"
	warm := p3JSONRequest(t, fixture.server, http.MethodGet, base+"?limit=80", nil, "")
	if warm.Code != http.StatusOK {
		t.Fatalf("warm source page HTTP%d", warm.Code)
	}
	tool := findProjectedMessageByCallID(transcriptWebServingItems(t, p3DecodeObject(t, warm), "items"), "ordinary-lineage-call")
	messageID := webString(tool["id"])
	record, found, err := fixture.readModel.GetTranscriptWebMessageByID(t.Context(),
		active.transcriptWebMessageIdentity(fixture.stream.OwnerID, messageID))
	if err != nil || !found || messageID == "" {
		t.Fatalf("exact canonical tool read: found=%t err=%v", found, err)
	}
	messages, err := transcriptWebReadModelMessageMaps([]transcriptstore.TranscriptWebMessageRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	expected := cloneTranscriptWebProjectionMap(messages[0])
	raw, err := json.Marshal(expected)
	if err != nil || !strings.Contains(string(raw), "second paragraph") || !strings.Contains(string(raw), "records") {
		t.Fatalf("fixture lost its complete nested tool result: %s err=%v", raw, err)
	}
	before := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, active.snapshot.BranchID)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	t.Cleanup(finish)
	load := func(ctx context.Context, frameID string) (workspace.Frame, []workspace.Frame, error) {
		close(entered)
		select {
		case <-release:
			return fixture.server.loadTranscriptWebMessageLineage(ctx, frameID)
		case <-ctx.Done():
			return workspace.Frame{}, nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- fixture.server.enrichTranscriptWebConversationMessagesWithLineage(ctx,
			transcriptWebServingFrameID, active.snapshot.BranchID, messages, load)
	}()
	select {
	case <-entered:
		finish()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("original real lineage loader did not settle after release: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("lineage loader did not settle after release")
		}
		t.Fatal("ordinary canonical tool entered unrelated full-root lineage loader")
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary enrichment did not settle at its controlled dependency boundary")
	}
	if !reflect.DeepEqual(messages[0], expected) {
		t.Fatal("ordinary enrichment changed the complete canonical message")
	}
	if after := readTranscriptWebProjectionClock(t, fixture.db, fixture.stream.UID, active.snapshot.BranchID); after != before {
		t.Fatal("ordinary enrichment rewrote the ready projection")
	}
	// The same production adapter also remains connected to the real HTTP detail
	// consumer. The barrier above proves the enrichment dependency, not timing of
	// a live request or an injected whole-HTTP path.
	detail := p3JSONRequest(t, fixture.server, http.MethodGet, base+"/"+url.PathEscape(messageID), nil, "")
	var expectedHTTP map[string]any
	if err := json.Unmarshal(raw, &expectedHTTP); err != nil {
		t.Fatal(err)
	}
	if detail.Code != http.StatusOK || !reflect.DeepEqual(p3DecodeObject(t, detail), expectedHTTP) {
		t.Fatalf("HTTP detail did not preserve the complete canonical message: HTTP%d", detail.Code)
	}
}

func TestTranscriptWebToolLineageAdmissionUsesOriginalCollectors(t *testing.T) {
	use := func(id, name string, input any) map[string]any {
		return map[string]any{"type": "tool_use", "id": id, "name": name, "input": input}
	}
	result := func(id string, output any) map[string]any {
		return map[string]any{"type": "tool_result", "tool_use_id": id, "content": output}
	}
	for _, test := range []struct {
		name           string
		block          map[string]any
		harnessNotice  bool
		calls, results int
	}{
		{"ordinary", use("call", "synthetic_reader", nil), false, 0, 0},
		{"bare_agent", use("call", "Agent", nil), false, 0, 0},
		{"delegate", use("call", "namespace/Delegate", nil), false, 1, 0},
		{"named_agent", use("call", "namespace:Agent", map[string]any{"delegate_name": "synthetic"}), false, 1, 0},
		{"directed_message", use("call", "namespace.Send_Message", map[string]any{"child_frame_id": "child"}), false, 1, 0},
		{"undirected_message", use("call", "send_message", nil), false, 0, 0},
		{"missing_call_id", use(" ", "delegate", nil), false, 0, 0},
		{"harness_notice", use("call", "delegate", nil), true, 0, 0},
		{"ordinary_nested_output", result("call", map[string]any{"data": map[string]any{"notifications": []any{}}}), false, 0, 0},
		{"invalid_notification_type", result("call", map[string]any{"notifications": "untrusted"}), false, 0, 0},
		{"notifications", result("call", map[string]any{"notifications": []any{}}), false, 0, 1},
		{"collected", result("call", map[string]any{"_collected": []any{}}), false, 0, 1},
		{"text_array_wrapper", result("call", `[{"text":"{\"notifications\":[]}"}]`), false, 0, 1},
		{"notification_missing_call", result(" ", map[string]any{"notifications": []any{}}), false, 0, 0},
		// Notices exclude delegation tool_use only; their result envelopes have
		// always retained the original notification interpretation.
		{"notice_result", result("call", map[string]any{"_collected": []any{}}), true, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := planRichWebSubagentEnrichment([]map[string]any{{
				"_harness_notice": test.harnessNotice, "content": []any{test.block},
			}})
			if len(plan.calls) != test.calls || len(plan.results) != test.results {
				t.Fatalf("lineage plan calls=%d results=%d, want %d/%d", len(plan.calls), len(plan.results), test.calls, test.results)
			}
		})
	}
	// The original payload-only fallback remains visible, but a hidden or
	// unrelated child must not supply its name or ordinal to that decoration.
	for _, child := range []workspace.Frame{
		{ID: "child", ParentFrameID: "parent", IsHidden: true, DelegateName: "excluded-child-name", RootSequence: 91},
		{ID: "child", ParentFrameID: "other-parent", DelegateName: "excluded-child-name", RootSequence: 91},
	} {
		plan := planRichWebSubagentEnrichment([]map[string]any{{"content": []any{result("call", map[string]any{
			"notifications": []any{map[string]any{"notification_type": "child_message", "payload": map[string]any{
				"sender_frame_id": "child", "kind": "question", "text": "canonical synthetic notification",
			}}},
		})}}})
		want := map[string][]map[string]any{"call": {{
			"kind": "question", "frameId": "child", "ordinal": 1,
			"childName": "X-Science", "text": "canonical synthetic notification",
		}}}
		if events := collectRichWebSubagentEvents(workspace.Frame{ID: "parent"}, plan, []workspace.Frame{child}); !reflect.DeepEqual(events, want) {
			t.Fatalf("payload-only fallback used excluded child metadata or changed its canonical text: %#v", events)
		}
	}
}

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

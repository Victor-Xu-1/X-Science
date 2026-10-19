package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
	"x-science/internal/sessionagent"
)

const (
	specialistPolicyConnectorA   = "custom:policy-source-a"
	specialistPolicyConnectorB   = "custom:policy-current-b"
	specialistPolicyMissingAgent = "SYNTHETIC_MISSING_POLICY_TARGET"
)

type specialistPolicyContextFixture struct {
	app       *Server
	repo      *transcriptstore.Repository
	access    workspace.KernelFrameAccess
	stream    transcriptstore.Stream
	claimA    transcriptstore.RunnerClaim
	claimB    transcriptstore.RunnerClaim
	snapshotA *sessionagent.Snapshot
	snapshotB *sessionagent.Snapshot
	source    context.Context
	binding   kernelTranscriptExecutionBinding
	dialCalls *atomic.Int32
}

func newSpecialistPolicyContextFixture(t *testing.T, sourceDefault, currentDefault bool) specialistPolicyContextFixture {
	t.Helper()
	app, store, frame := newSessionSpecialistAPIFixture(t)
	dialCalls := &atomic.Int32{}
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		dialCalls.Add(1)
		return nil, errors.New("synthetic policy fixture forbids network dialing")
	}}
	t.Cleanup(transport.CloseIdleConnections)
	app.httpClient = &http.Client{Transport: transport}
	// The fixture has no listener or external endpoint. The literal destination
	// is rejected synchronously by the production outbound preflight, before
	// any DNS lookup, request or dial; the transport is an additional tripwire.
	const blockedURL = "https://127.0.0.1:1/mcp"
	if _, err := app.publicSourceHTTPClient(t.Context(), blockedURL, app.httpClient); err == nil || !strings.Contains(err.Error(), "must not target a loopback") {
		t.Fatalf("fixture destination did not fail its local preflight: %v", err)
	}
	enabled := true
	for _, connector := range []struct{ id, name string }{
		{specialistPolicyConnectorA, "policy-source-a"},
		{specialistPolicyConnectorB, "policy-current-b"},
	} {
		if _, err := store.CreateMCPServer(workspace.MCPServerInput{
			ID: connector.id, UserID: specialistFixtureOwner, Name: connector.name,
			URL: blockedURL, Transport: "streamable-http", Enabled: &enabled,
		}); err != nil {
			t.Fatal(err)
		}
		if err := store.AttachAgentConnector(specialistFixtureOwner, "SYNTHETIC_A", connector.id, "custom"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.AttachAgentConnector(specialistFixtureOwner, "SYNTHETIC_B", specialistPolicyConnectorB, "custom"); err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	stream, err := repo.CreateStream(t.Context(), transcriptstore.CreateStreamInput{
		UID: "frame:" + frame.ID, OwnerID: specialistFixtureOwner, ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.RootFrameID, FrameID: frame.ID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	prepare := func(name, command string, useDefault bool) sessionagent.Mutation {
		if useDefault {
			name = ""
		}
		return specialistPrepareFixture(t, app, frame.ID, name, command, useDefault)
	}
	appendInput := func(selection sessionagent.Mutation, id, connector string) {
		input := specialistInputFixture(selection, stream, id)
		input.InputData["inject_mcp_server_ids"] = []string{connector}
		if _, _, created, err := repo.AppendFrameUserEvent(t.Context(), input); err != nil || !created {
			t.Fatalf("append %s: created=%t err=%v", id, created, err)
		}
	}
	first := prepare("SYNTHETIC_A", "policy-prepare-a", sourceDefault)
	appendInput(first, "policy-input-a", specialistPolicyConnectorA)
	claimA := specialistClaimFixture(t, repo, stream, "policy-runner-a")
	snapshotA, err := repo.ClaimedRunnerAgentSnapshot(t.Context(), claimA)
	if err != nil || snapshotA == nil || snapshotA.IsDefault != sourceDefault {
		t.Fatalf("source snapshot=%#v err=%v", snapshotA, err)
	}
	if !containsAgentToolName(snapshotA.Settings.AllowedMCPIDs, specialistPolicyConnectorA) ||
		!containsAgentToolName(snapshotA.Settings.AllowedMCPIDs, specialistPolicyConnectorB) {
		t.Fatalf("source fixture must allow both connectors, so its admitted input is the narrowing fence: %#v", snapshotA.Settings.AllowedMCPIDs)
	}
	_, sourceEvent, _, err := repo.AppendRunnerCheckpoint(t.Context(), transcriptstore.AppendRunnerCheckpointInput{
		Claim: claimA, ClientMessageID: "policy-source-boundary", Phase: transcriptstore.RunnerPhasePlanning, Resumable: true,
		PayloadJSON: []byte(`{"status":"running","message":"synthetic source boundary"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: claimA, ClientMessageID: "policy-finish-a", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	second := prepare("SYNTHETIC_B", "policy-prepare-b", currentDefault)
	appendInput(second, "policy-input-b", specialistPolicyConnectorB)
	claimB := specialistClaimFixture(t, repo, stream, "policy-runner-b")
	snapshotB, err := repo.ClaimedRunnerAgentSnapshot(t.Context(), claimB)
	if err != nil || snapshotB == nil || snapshotB.IsDefault != currentDefault || snapshotB.ID == snapshotA.ID {
		t.Fatalf("current snapshot=%#v err=%v", snapshotB, err)
	}
	run := &sessionRunnerChatRun{SessionID: stream.SessionID,
		Transcript:         &transcriptRunnerAuthority{Stream: stream, Claim: claimB},
		KernelOperationIDs: map[string]string{"policy-outer-tool": "policy-original-operation"},
	}
	current := withTranscriptRunnerChatRun(t.Context(), run)
	source, captured, err := app.specialistOperationContext(current, run, workspace.KernelLocalOperation{
		OwnerUserID: stream.OwnerID, FrameID: frame.ID, StreamUID: stream.UID,
		SourceEventID: sourceEvent.EventID, SourceRunnerAttempt: claimA.Attempt,
	})
	if err != nil || captured == nil || captured.ID != snapshotA.ID {
		t.Fatalf("original operation source=%#v err=%v", captured, err)
	}
	access, found, err := store.GetKernelFrameAccessContext(t.Context(), frame.ID)
	if err != nil || !found {
		t.Fatalf("frame access found=%t err=%v", found, err)
	}
	binding := kernelTranscriptExecutionBindingFromContext(source, "policy-outer-tool", "synthetic-policy-kernel", 1, nil)
	// No real kernel is started. The real callback still validates the actual
	// SQLite live claim, frame incarnations and its bound generation.
	binding.currentKernelGeneration = func() uint64 { return 1 }
	return specialistPolicyContextFixture{app: app, repo: repo, access: access, stream: stream,
		claimA: claimA, claimB: claimB, snapshotA: snapshotA, snapshotB: snapshotB,
		source: source, binding: binding, dialCalls: dialCalls}
}

func (f specialistPolicyContextFixture) policy(ctx context.Context) *kernelruntime.HostCallPolicy {
	return f.app.agentKernelExecutionHostPolicy(ctx, f.access, f.access.ProjectPath, []string{"mcp"}, false, f.binding)
}

func (f specialistPolicyContextFixture) assertCurrentClaimUnchanged(t *testing.T) {
	t.Helper()
	if _, err := f.repo.ValidateLiveRunnerClaim(t.Context(), f.claimA); err == nil {
		t.Fatal("finished source attempt regained live execution ownership")
	}
	snapshot, err := f.repo.ClaimedRunnerAgentSnapshot(t.Context(), f.claimB)
	if err != nil || snapshot == nil || snapshot.ID != f.snapshotB.ID {
		t.Fatalf("callback changed or lost the current live B binding: snapshot=%#v err=%v", snapshot, err)
	}
	if calls := f.dialCalls.Load(); calls != 0 {
		t.Fatalf("isolated host policy fixture attempted %d network dials", calls)
	}
}

func assertSpecialistPolicyHostError(t *testing.T, err error, code, message string) {
	t.Helper()
	var hostError *kernelruntime.HostCallError
	if !errors.As(err, &hostError) || hostError.Code != code || !strings.Contains(hostError.Message, message) {
		t.Fatalf("host error=%#v (%v), want code=%q containing %q", hostError, err, code, message)
	}
}

func TestSessionSpecialistExecutionHostPolicyKeepsSourceInputAndDetachedLifetime(t *testing.T) {
	f := newSpecialistPolicyContextFixture(t, false, true)
	foreground, cancelForeground := context.WithCancel(f.source)
	policy := f.policy(foreground)
	cancelForeground()
	result, err := policy.Handler(t.Context(), kernelruntime.HostCall{ID: "hc-00000000000000000000000000000001", Method: "mcp.catalog"})
	if err != nil {
		t.Fatalf("foreground cancellation cancelled the authorized policy: %v", err)
	}
	catalog, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("catalog=%#v", result)
	}
	unavailable, ok := catalog["unavailable_connectors"].([]map[string]any)
	if !ok || len(unavailable) != 1 || unavailable[0]["server"] != specialistPolicyConnectorA {
		t.Fatalf("nested callback lost source A snapshot or original A-only input: %#v", catalog)
	}
	f.assertCurrentClaimUnchanged(t)
	cancelledCall, cancelCall := context.WithCancel(t.Context())
	cancelCall()
	if _, err := policy.Handler(cancelledCall, kernelruntime.HostCall{ID: "hc-00000000000000000000000000000002", Method: "mcp.catalog"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("callback cancellation was detached too: %v", err)
	}
	reviewerContext := withSessionReviewerEvidenceScope(foreground, &sessionReviewerEvidenceScope{sessionID: f.access.Frame.ID})
	reviewer := f.policy(reviewerContext)
	if want := []string{"host.archive.search", "host.archive.page", "host.artifact_path"}; !reflect.DeepEqual(reviewer.AllowedMethods, want) {
		t.Fatalf("execution factory dropped reviewer restriction: methods=%#v want=%#v", reviewer.AllowedMethods, want)
	}
	f.assertCurrentClaimUnchanged(t)
}

func TestSessionSpecialistExecutionHostPolicyUsesOriginalMainRole(t *testing.T) {
	for _, test := range []struct {
		name                          string
		sourceDefault, currentDefault bool
		explicitLegacyNil             bool
		wantCode, wantMessage         string
	}{
		{"source-specialist-current-default", false, true, false, "permission_denied", "current specialist cannot switch"},
		{"source-default-current-specialist", true, false, false, "selection_unavailable", "selection could not be prepared"},
		{"explicit-legacy-nil-current-specialist", false, false, true, "selection_unavailable", "selection could not be prepared"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newSpecialistPolicyContextFixture(t, test.sourceDefault, test.currentDefault)
			source := f.source
			if test.explicitLegacyNil {
				legacy := source.Value(specialistExecutionSourceContextKey{}).(specialistExecutionSource)
				legacy.Source.Snapshot = nil
				source = context.WithValue(source, specialistExecutionSourceContextKey{}, legacy)
			}
			// The absent target ends a permitted main-role request before any
			// approval is queued. A wrong current-B role takes a different path.
			_, err := f.policy(source).Handler(t.Context(), kernelruntime.HostCall{
				ID: "hc-00000000000000000000000000000003", Method: "host.agents.switch", Args: []any{specialistPolicyMissingAgent},
			})
			assertSpecialistPolicyHostError(t, err, test.wantCode, test.wantMessage)
			f.assertCurrentClaimUnchanged(t)
		})
	}
}

func TestSessionSpecialistExecutionHostPolicyRequiresCurrentLiveOwner(t *testing.T) {
	f := newSpecialistPolicyContextFixture(t, false, true)
	staleBinding := f.binding
	staleBinding.claim = f.claimA
	stalePolicy := f.app.agentKernelExecutionHostPolicy(f.source, f.access, f.access.ProjectPath, []string{"mcp"}, false, staleBinding)
	call := kernelruntime.HostCall{ID: "hc-00000000000000000000000000000004", Method: "host.agents.switch", Args: []any{specialistPolicyMissingAgent}}
	if _, err := stalePolicy.Handler(t.Context(), call); err == nil {
		t.Fatal("immutable A source was accepted as a replacement live execution owner")
	} else {
		assertSpecialistPolicyHostError(t, err, "permission_denied", "runner claim is stale")
	}
	f.assertCurrentClaimUnchanged(t)
	policy := f.policy(f.source)
	if _, _, _, err := f.repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: f.claimB, ClientMessageID: "policy-finish-b", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Handler(t.Context(), call); err == nil {
		t.Fatal("host callback retained authority after its unique B owner finished")
	} else {
		assertSpecialistPolicyHostError(t, err, "permission_denied", "runner claim is stale")
	}
	if calls := f.dialCalls.Load(); calls != 0 {
		t.Fatalf("isolated host policy fixture attempted %d network dials", calls)
	}
}

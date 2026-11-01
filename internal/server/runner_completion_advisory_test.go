package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	sessionstore "x-science/internal/persistence/sessions"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestCompletionTypedAdvisoriesPreserveUnknownTextAndAllFindings(t *testing.T) {
	claim := transcriptstore.RunnerClaim{Attempt: 3, ClaimedInputRevision: 2}
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Claim: claim}}
	const body = "合成结果 🧪.\n\n> Result quality is unverified. Existing outputs are preserved."
	one := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryResultQuality, Findings: []string{"first", "same"}}
	two := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryResultQuality, Findings: []string{"same", "second"}}
	content := appendSessionRunnerCompletionAdvisoriesForRun(body, "en", run, "artifact_completion", one)
	content = appendSessionRunnerCompletionAdvisoriesForRun(content, "en", run, "task_expectations", two)
	wire := run.completionAdvisorySnapshot().envelope(content, claim)
	if wire == nil || len(wire.Items) != 1 || len(wire.Spans) != 1 || len(wire.Items[0].Sources) != 2 ||
		!reflect.DeepEqual(wire.Items[0].Findings, []string{"first", "same", "second"}) ||
		!reflect.DeepEqual(wire.Items[0].Sources[0].FindingIndexes, []int{0, 1}) ||
		!reflect.DeepEqual(wire.Items[0].Sources[1].FindingIndexes, []int{1, 2}) {
		t.Fatal("same-code findings or source identities were dropped")
	}
	payload := map[string]any{"text": content, "completion_advisories": wire}
	decoded := sessionRunnerCompletionAdvisoriesFromPayload(payload, claim.Attempt)
	if decoded == nil || sessionRunnerCompletionAdvisoryModelBody(content, decoded) != body {
		t.Fatal("host provenance did not preserve the exact unknown-origin model paragraph")
	}
	if sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": content + "changed", "completion_advisories": wire}, claim.Attempt) != nil ||
		sessionRunnerCompletionAdvisoriesFromPayload(payload, claim.Attempt+1) != nil {
		t.Fatal("host metadata crossed a body or attempt boundary")
	}
	// An exact canonical notice already in model prose is not a host append.
	english, _ := sessionRunnerCompletionAdvisoryText(completionAdvisoryResultQuality)
	unknown := "Model-authored quotation: " + english
	other := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Claim: claim}}
	unchanged := appendSessionRunnerCompletionAdvisoriesForRun(unknown, "en", other, "artifact_completion", one)
	projection := other.completionAdvisorySnapshot().envelope(unchanged, claim)
	if len(projection.Spans) != 0 || sessionRunnerCompletionAdvisoryModelBody(unchanged, projection) != unknown {
		t.Fatal("wording alone fabricated host ownership")
	}
	if sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": `{"completion_advisories":{"schema":"x-science.completion-advisories.v1"}}`}, claim.Attempt) != nil {
		t.Fatal("model text granted a host advisory projection")
	}
}

func TestCompletionTypedAdvisoriesRejectSplitUTF16Spans(t *testing.T) {
	const body = "A😀B"
	const attempt = int64(3)
	for _, span := range []completionAdvisorySpan{{Start: 1, End: 2, Text: "\ufffd"}, {Start: 2, End: 3, Text: "\ufffd"}} {
		wire := &completionAdvisoryEnvelope{Schema: "x-science.completion-advisories.v1", Attempt: attempt, InputRevision: 2,
			TextSHA256: completionAdvisorySHA(body), TextLength: 4, Spans: []completionAdvisorySpan{span},
			Items: []completionAdvisoryItem{{Code: completionAdvisoryResultQuality, Findings: []string{"observed"},
				Sources: []completionAdvisorySource{{Stage: "artifact_completion", Attempt: attempt, InputRevision: 2,
					CandidateSHA256: completionAdvisorySHA(body), FindingIndexes: []int{0}}}}}}
		if sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": body, "completion_advisories": wire}, attempt) != nil {
			t.Fatal("split surrogate pair granted host ownership")
		}
		if sessionRunnerCompletionAdvisoryModelBody(body, wire) != body {
			t.Fatal("invalid UTF16 span changed the original model body")
		}
	}
}

func TestCompletionTypedAdvisoriesPersistAcrossFreshLiveHistoryAndReplay(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	intent, found, err := f.repo.EnsureActiveFrameTaskIntent(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil || !found {
		t.Fatalf("task intent: found=%t err=%v", found, err)
	}
	run := &sessionRunnerChatRun{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt), ClaimToken: f.claim.ClaimToken, Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}}
	if err := applySessionRunnerTaskIntent(run, intent.Text, intent.Language, intent.ID, intent.Revision); err != nil {
		t.Fatal(err)
	}
	const path = "out/typed_validation.json"
	write := writeAgentSaveArtifactsFile(t, f.projectPath, path, `{"checks":{"valid":false},"status":"failed"}`)
	f.saveExecution(t, f.identity.access, f.projectPath, "typed-validation-write", 1, write)
	input := map[string]any{"files": []any{path}, "language": "text", "human_description": "Save observed synthetic validation"}
	startPayload, err := json.Marshal(map[string]any{"toolName": "save_artifacts", "toolPhase": "start", "status": "running", "toolCallId": "typed-validation-save", "toolInput": input})
	if err != nil {
		t.Fatal(err)
	}
	_, source, _, err := f.repo.AppendRunnerCheckpoint(ctx, transcriptstore.AppendRunnerCheckpointInput{Claim: f.claim,
		ClientMessageID: "typed-validation-start", Phase: transcriptstore.RunnerPhaseExecuting, PayloadJSON: startPayload, Destinations: []string{"ws"}})
	if err != nil {
		t.Fatal(err)
	}
	toolContext := withTranscriptArtifactRun(ctx, run.Transcript, source.EventID)
	saved, err := f.server.executeAgentSaveArtifacts(toolContext, f.identity, "typed-validation-save", input)
	if err != nil || saved["errors"] != nil {
		t.Fatalf("save fixture: %v %v", saved, err)
	}
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "typed-validation-completed", map[string]any{"toolName": "save_artifacts", "toolPhase": "completed", "status": "completed", "toolCallId": "typed-validation-save", "toolInput": input, "toolResult": saved})
	session := sessionstore.Session{ID: f.stream.SessionID, Project: &sessionstore.Project{ID: f.stream.ProjectID}}
	const body = "Existing synthetic result 🧪.\n\n> Result quality is unverified. Existing outputs are preserved."
	model := &artifactReferenceRepairModel{content: body, testing: t}
	result, err := f.server.runSessionAgentWithArtifactReferenceRepair(ctx, session, agentruntime.Engine{Model: model},
		agentruntime.RunRequest{Messages: []agentruntime.Message{{Role: "user", Content: run.TaskIntent}}}, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	final, err := f.server.persistTranscriptRunnerFinalization(ctx, run.Transcript, transcriptRunnerFinalization{
		Content: result.FinalMessage.Content, Status: "completed", Detail: "runner chat completed", SegmentOrdinal: 1, CompletionAdvisories: run.completionAdvisorySnapshot(),
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var expected *completionAdvisoryEnvelope
	for _, event := range events {
		if event.Event.EventID != final.Assistant.EventID {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		expected = sessionRunnerCompletionAdvisoriesFromPayload(payload, f.claim.Attempt)
		if expected == nil || len(expected.Items[0].Findings) == 0 || sessionRunnerCompletionAdvisoryModelBody(stringValue(payload["text"]), expected) != body {
			t.Fatal("fresh atomic finalization lost the model body or actual host findings")
		}
	}
	if expected == nil {
		t.Fatal("missing canonical assistant")
	}
	full, handled, err := f.server.loadTranscriptWebHistory(ctx, stream.OwnerID, stream.FrameID)
	if err != nil || !handled {
		t.Fatalf("full history: handled=%t err=%v cause=%v", handled, err, errors.Unwrap(err))
	}
	foundHistory := false
	for _, message := range full {
		content := mapValue(message["content"])
		if content["completionAdvisories"] != nil {
			foundHistory = true
		}
	}
	if !foundHistory {
		t.Fatal("full history dropped typed host facts")
	}
	if err := f.server.runTranscriptWebDeliveryCycle(ctx); err != nil {
		t.Fatal(err)
	}
	foundLive := false
	for _, event := range transcriptWebEvents(t, f.store, stream.OwnerID) {
		if event.Type == "message.stream" && event.Payload["completion_advisories"] != nil {
			foundLive = true
		}
	}
	if !foundLive {
		t.Fatal("live content publication dropped typed host facts")
	}
	for reload := 0; reload < 2; reload++ {
		page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+stream.FrameID+"/messages?limit=80", nil, stream.OwnerID)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"completionAdvisories"`) {
			t.Fatalf("materialized/reloaded history lost typed host facts: HTTP%d", page.Code)
		}
	}
	replay, err := f.server.loadTranscriptRunnerReplay(ctx, run.Transcript, 80, 80)
	if err != nil {
		t.Fatal(err)
	}
	foundReplay := false
	for _, entry := range replay {
		text := stringValue(entry.Message["text"])
		if strings.Contains(text, `"host_completion_observations"`) {
			foundReplay = true
			if !strings.HasPrefix(text, body) || strings.Contains(text, "Some consistency or evidence checks remain unresolved") {
				t.Fatal("provider replay lost the model body or copied presentation boilerplate")
			}
		}
	}
	if !foundReplay {
		t.Fatal("provider replay dropped typed host findings")
	}
	// Memory and compaction consume the same canonical completion, not a
	// hand-built journal or a second presentation-generated answer.
	facts, err := json.Marshal(map[string]any{"host_completion_observations": expected.Items})
	if err != nil {
		t.Fatal(err)
	}
	wantReplay := body + "\n\n" + string(facts)
	proof, err := f.repo.ReadCompletedMemorySource(ctx, stream.UID, stream.OwnerID, final.Terminal.EventID)
	if err != nil {
		t.Fatal(err)
	}
	memory := &memoryExtractionRuntime{server: f.server}
	snapshot, err := memory.readMemoryExtractionSnapshot(ctx, stream.OwnerID, stream.SessionID,
		memoryExtractionSource{proof: &proof, finishEventID: final.Terminal.EventID, captured: true})
	if err != nil || snapshot.TerminalStatus != "completed" || snapshot.FinalResponseText != wantReplay {
		t.Fatalf("memory snapshot lost model prose, exact findings or source identities: status=%q err=%v", snapshot.TerminalStatus, err)
	}
	coordinate, err := f.repo.GetProjectionSnapshot(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	compaction, err := f.server.contextCompactionSource(ctx, transcriptstore.ContextCompactionRequest{
		Expected: transcriptstore.ContextCompactionTarget{StreamUID: stream.UID, BranchID: coordinate.BranchID,
			BranchGeneration: coordinate.BranchGeneration, ThroughPublicationSeq: coordinate.ThroughPublicationSequence},
	}, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	foundCompaction := false
	for _, entry := range compaction {
		if entry.EventID == final.Assistant.EventID {
			foundCompaction = true
			if entry.Message["text"] != wantReplay || entry.Message["role"] != "assistant" {
				t.Fatal("compaction source lost exact typed observations or changed their role")
			}
		}
	}
	if !foundCompaction {
		t.Fatal("compaction source dropped the final assistant")
	}
	for _, event := range events {
		if event.Event.EventID != final.Assistant.EventID {
			continue
		}
		var legacy map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &legacy); err != nil {
			t.Fatal(err)
		}
		delete(legacy, "completion_advisories")
		prior := legacy["text"]
		projectSessionRunnerCompletionAdvisoryReplay(legacy, event.Event)
		if legacy["text"] != prior {
			t.Fatal("untyped historical answer was rewritten")
		}
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &legacy); err != nil {
			t.Fatal(err)
		}
		imported := event.Event
		imported.Type = "history_assistant_message"
		projectSessionRunnerCompletionAdvisoryReplay(legacy, imported)
		if legacy["text"] != prior {
			t.Fatal("imported metadata granted canonical host ownership")
		}
	}
}

func TestCompletionAdvisoryIsLocalizedIdempotentAndCannotReplaceAnAnswer(t *testing.T) {
	advice := &sessionRunnerCompletionAdvisory{Code: completionAdvisoryStructurePreview}
	for _, language := range []string{"en", "zh", "zh-CN", "de", ""} {
		t.Run(language, func(t *testing.T) {
			base := "Result 12.5; [file]({{artifact:00000000-0000-4000-8000-000000000001}})."
			text := appendSessionRunnerCompletionAdvisories(base, language, advice, advice)
			english, chinese := sessionRunnerCompletionAdvisoryText(advice.Code)
			want := english
			if sessionRunnerRequiresChinese(language) {
				want = chinese
			}
			if !strings.HasPrefix(text, base) || strings.Count(text, want) != 1 ||
				appendSessionRunnerCompletionAdvisories(text, language, advice) != text {
				t.Fatalf("advisory changed the answer or duplicated: %q", text)
			}
			if got := appendSessionRunnerCompletionAdvisories(" \n", language, advice); got != " \n" {
				t.Fatalf("notice fabricated a missing final answer: %q", got)
			}
		})
	}
}

func TestCompletionRecoveryPersistsSceneNoticeOnceWithoutRewritingPriorCandidate(t *testing.T) {
	ctx := context.Background()
	srv, session, priorAuthority := completionIntentRecoveryFixtureWithSetup(t, "Explain the result in chat", false,
		func(server *Server, stream transcriptstore.Stream, claim transcriptstore.RunnerClaim) {
			access, found, err := server.workspaceStore.GetKernelFrameAccessContext(ctx, stream.FrameID)
			if err != nil || !found {
				t.Fatalf("recovery output access: found=%t error=%v", found, err)
			}
			projectPath := t.TempDir()
			if _, err := server.upsertHostGrant(stream.OwnerID, projectPath, "read_write"); err != nil {
				t.Fatal(err)
			}
			fixture := &agentSaveArtifactsFixture{
				server: server, store: server.workspaceStore, repo: server.transcriptStore,
				stream: stream, claim: claim, projectPath: projectPath,
				identity: &agentKernelContext{access: access, workspaceDir: projectPath},
			}
			const structure = "ATOM      1  N   ALA A   1       0.000   0.000   0.000  1.00  0.00           N\nEND\n"
			saveRoundPresentationFile(t, fixture, "recovered-scene-base", "base.pdb", structure)
			saveRoundPresentationFile(t, fixture, "recovered-scene-derived", "derived.pdb", structure)
			current, err := server.transcriptStore.CurrentRoundArtifactCommitSnapshot(ctx, stream.UID, stream.OwnerID, claim.Attempt)
			if err != nil || len(current.References) != 2 {
				t.Fatalf("original live input did not produce two structures: %#v error=%v", current, err)
			}
			for _, reference := range current.References {
				if reference.Relation != transcriptstore.ArtifactRelationProduced {
					t.Fatalf("comparison input is not a produced receipt: %#v", reference)
				}
			}
		})
	prior, found, err := srv.loadTranscriptCompletionRecoveryCandidate(ctx, priorAuthority)
	if err != nil || !found {
		t.Fatalf("prior candidate: found=%t error=%v", found, err)
	}
	// Obtain real Continue and dispatch authority; never use the read-only
	// fixture's synthetic next-attempt claim to write a response.
	if _, err := srv.workspaceStore.ResumeCompatibilityFrameConversation(session.ID, workspace.ResumeCompatibilityFrameInput{}); err != nil {
		t.Fatal(err)
	}
	dispatch, found, err := srv.workspaceStore.ClaimNextCompatibilityFrameResumeDispatch("scene-notice-worker", time.Minute)
	if err != nil || !found {
		t.Fatalf("resume dispatch: found=%t error=%v", found, err)
	}
	claimed, err := srv.transcriptStore.ClaimRunner(ctx, transcriptstore.ClaimRunnerInput{
		StreamUID: priorAuthority.Stream.UID, OwnerID: priorAuthority.Stream.OwnerID,
		RunnerID: frameResumeRunnerID(dispatch.ResumeEvent.ID), TTL: time.Minute,
		ResumeSource: transcriptstore.ResumeSourceCheckpoint, ResumeCheckpoint: priorAuthority.Claim.ResumeCheckpoint,
	})
	if err != nil || !claimed.Claimed {
		t.Fatalf("real recovery claim=%#v error=%v", claimed, err)
	}
	stream, err := srv.transcriptStore.GetStream(ctx, priorAuthority.Stream.UID, priorAuthority.Stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	run := &sessionRunnerChatRun{SessionID: session.ID, Transcript: &transcriptRunnerAuthority{Stream: stream, Claim: claimed.Claim}}
	for replay := 0; replay < 2; replay++ {
		status, _, _, handled, err := consumeCompletionRecoveryFixture(ctx, srv, session, run)
		if err != nil || !handled || status != "completed" {
			t.Fatalf("replay=%d status=%s handled=%t error=%v", replay, status, handled, err)
		}
	}
	stream, err = srv.transcriptStore.GetStream(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := srv.transcriptStore.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{
		StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	answers, sourceDeltas := 0, 0
	for _, event := range events {
		var payload map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &payload); err != nil {
			t.Fatal(err)
		}
		switch event.Event.Type {
		case "content_delta":
			sourceDeltas++
			if stringValue(payload["text"]) != prior.Content {
				t.Fatal("old immutable candidate was rewritten")
			}
		case "assistant_message":
			answers++
			text := stringValue(payload["text"])
			if !strings.HasPrefix(text, prior.Content) || strings.Count(text, "Structure preview is unverified") != 1 {
				t.Fatalf("persisted public answer lost its exact candidate or notice: %q", text)
			}
			typed := mapValue(payload["completion_advisories"])
			items, _ := typed["items"].([]any)
			spans, _ := typed["spans"].([]any)
			if typed["schema"] != "x-science.completion-advisories.v1" || len(items) != 1 || len(spans) != 1 ||
				numberValue(typed["attempt"]) != claimed.Claim.Attempt ||
				numberValue(typed["input_revision"]) != claimed.Claim.ClaimedInputRevision {
				t.Fatal("canonical completion lost typed host finding provenance")
			}
		}
	}
	if answers != 1 || sourceDeltas != 1 {
		t.Fatalf("answers=%d old_deltas=%d", answers, sourceDeltas)
	}
}

func TestToolChoiceViolationDoesNotReviveAdvisorySceneCondition(t *testing.T) {
	f := newCorrectionRouteFixture(t)
	cause := newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "old auxiliary scene",
		transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{UnboundNames: []string{"scene unavailable"}}})
	payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	appendRunnerToolCheckpoint(t, f.repo, f.claim, "old-scene", map[string]any{
		"status": "interrupted", "reason_code": cause.ReasonCode, "resume_detail": cause.Detail,
		"recovery_contract_revision": sessionRunnerRecoveryContractRevision, transcriptstore.RunnerInterruptionCauseField: payload,
	})
	entries, err := f.server.loadTranscriptRunnerReplay(context.Background(), f.run.Transcript, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	var chatErr error = &agentruntime.InitialToolChoiceViolationError{}
	result := &SessionRunnerCycleResult{SessionID: f.stream.SessionID, Attempt: int(f.claim.Attempt)}
	handled, err := f.server.handleSessionRunnerChatInterruption(context.Background(),
		SessionRunnerChatOptions{SessionID: f.stream.SessionID, RunnerID: f.claim.RunnerID}, result,
		&activeSessionRun{}, sessionstore.RunnerMutationClaim{}, f.run.Transcript, f.run, entries, &chatErr, nil)
	if err != nil || !handled || result.InterruptionReasonCode != sessionRunnerRequiredToolChoiceUnsatisfiedReasonCode {
		t.Fatalf("provider violation revived scene condition: result=%#v handled=%t error=%v", result, handled, err)
	}
}

func TestNewInputDoesNotInheritAnOldSceneCorrection(t *testing.T) {
	cause := newRunnerCorrection(sessionRunnerVisualArtifactValidationReasonCode, "old auxiliary scene",
		transcriptstore.RunnerCorrectionCondition{Visual: &transcriptstore.RunnerVisualCondition{UnboundNames: []string{"scene unavailable"}}})
	payload, err := transcriptstore.RunnerInterruptionCausePayload(cause)
	if err != nil {
		t.Fatal(err)
	}
	entries := []eventjournal.Entry{{Message: eventjournal.Message{
		"type": "runner_checkpoint", "status": "interrupted", "reason_code": cause.ReasonCode,
		"resume_detail": cause.Detail, transcriptstore.RunnerInterruptionCauseField: payload,
	}}, {SourceEventType: "user_message", Message: eventjournal.Message{"type": "message", "role": "user", "text": "Explain a different result"}}}
	if _, found := latestRunnerCorrection(entries); found {
		t.Fatal("new user task inherited the previous scene condition")
	}
	if advisory, err := (&Server{}).recoveredCompletionCorrectionIsAdvisory("", nil, recoveredRunnerCorrection{}); err != nil || advisory {
		t.Fatal("absence of a condition was treated as an advisory obligation")
	}
}

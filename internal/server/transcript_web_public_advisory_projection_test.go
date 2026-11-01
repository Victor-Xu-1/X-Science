package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	transcriptstore "x-science/internal/persistence/transcript"
)

func publicAdvisoryProjectionFixture(t *testing.T, claim transcriptstore.RunnerClaim) (string, string, *completionAdvisoryEnvelope, *sessionRunnerChatRun) {
	t.Helper()
	structure, _ := sessionRunnerCompletionAdvisoryText(completionAdvisoryStructurePreview)
	body := " \n实验结果 🧪。\n\n> " + structure + "\n\n内部执行 `web_fetch`。\n<think>private fixture</think>\n保留对照 in_vitro。\n"
	wantModel := "实验结果 🧪。\n> " + structure + "\n保留对照 in_vitro。"
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Claim: claim}}
	text := appendSessionRunnerCompletionAdvisoriesForRun(body, "en", run, "artifact_completion",
		&sessionRunnerCompletionAdvisory{Code: completionAdvisoryStructurePreview, Findings: []string{"original quoted observation"}},
		&sessionRunnerCompletionAdvisory{Code: completionAdvisoryResultQuality, Findings: []string{"observed 🧪", "second observation"}},
		&sessionRunnerCompletionAdvisory{Code: completionAdvisoryTaskExpectations, Findings: []string{"recorded execution"}})
	wire := run.completionAdvisorySnapshot().envelope(text, claim)
	if wire == nil || len(wire.Items) != 3 || len(wire.Spans) != 2 {
		t.Fatal("fixture did not retain a model quotation plus two actual host appends")
	}
	return text, wantModel, wire, run
}

func publicAdvisoryProjectionMessage(frame, text string, attempt int64, wire any) map[string]any {
	identity := transcriptAssistantMessageID(frame, attempt, 1)
	return map[string]any{
		"id": identity, "conversation_id": frame, "type": "text", "position": "left", "terminal_status": "completed",
		"content": map[string]any{"content": text, "assistant_attempt_id": identity, "completionAdvisories": wire},
	}
}

func publicAdvisoryProjectionJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertPublicAdvisoryProjection(t *testing.T, message map[string]any, attempt int64, wantModel string, items []completionAdvisoryItem) {
	t.Helper()
	content := mapValue(message["content"])
	text := webString(content["content"])
	wire := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": text, "completion_advisories": content["completionAdvisories"]}, attempt)
	if wire == nil {
		t.Fatal("public body and typed envelope failed the original strict decoder")
	}
	if !reflect.DeepEqual(wire.Items, items) {
		t.Fatal("public projection changed normal findings, source hashes or finding indexes")
	}
	if got := sessionRunnerCompletionAdvisoryModelBody(text, wire); got != wantModel {
		t.Fatalf("owned ranges changed model quotation or separators: got %q want %q", got, wantModel)
	}
	quality, _ := sessionRunnerCompletionAdvisoryText(completionAdvisoryResultQuality)
	expectations, _ := sessionRunnerCompletionAdvisoryText(completionAdvisoryTaskExpectations)
	if text != wantModel+"\n> "+quality+"\n> "+expectations {
		t.Fatal("public projection changed the original privacy cleanup output")
	}
	if len(wire.Spans) != 2 || wire.Spans[0].Text != "\n> "+quality || wire.Spans[1].Text != "\n> "+expectations {
		t.Fatal("partially removed leading newlines were not mapped through their retained source intervals")
	}
}

func TestPublicCompletionAdvisoryProjectionRetainsSourceOwnership(t *testing.T) {
	claim := transcriptstore.RunnerClaim{Attempt: 3, ClaimedInputRevision: 2}
	text, wantModel, original, _ := publicAdvisoryProjectionFixture(t, claim)
	originalJSON := publicAdvisoryProjectionJSON(t, original)
	for _, shape := range []string{"typed", "read-model-json"} {
		t.Run(shape, func(t *testing.T) {
			message := publicAdvisoryProjectionMessage("projection-frame", text, claim.Attempt, original)
			if shape == "read-model-json" {
				var decoded map[string]any
				if err := json.Unmarshal(publicAdvisoryProjectionJSON(t, message), &decoded); err != nil {
					t.Fatal(err)
				}
				message = decoded
			}
			mapValue(message["content"])["nested"] = map[string]any{"thinking": "Public observation.\nInternal `runner_private_checkpoint`."}
			sanitizeTranscriptWebPublicMessagesWithCodes([]map[string]any{message}, nil)
			assertPublicAdvisoryProjection(t, message, claim.Attempt, wantModel, original.Items)
			if webString(mapValue(mapValue(message["content"])["nested"])["thinking"]) != "Public observation." {
				t.Fatal("typed presentation bypassed existing recursive privacy cleanup")
			}
			first := string(publicAdvisoryProjectionJSON(t, message))
			sanitizeTranscriptWebPublicMessagesWithCodes([]map[string]any{message}, nil)
			if string(publicAdvisoryProjectionJSON(t, message)) != first {
				t.Fatal("repeated public projection was not idempotent")
			}
			if string(publicAdvisoryProjectionJSON(t, original)) != string(originalJSON) {
				t.Fatal("response projection mutated canonical host findings or owned source ranges")
			}
		})
	}
}

func TestPublicTextProjectionPreservesOriginalCleaningStages(t *testing.T) {
	for _, row := range []struct{ source, privateOnly, historical string }{
		{" 公开\n\nordinary in_vitro。 ", " 公开\n\nordinary in_vitro。 ", " 公开\n\nordinary in_vitro。 "},
		{"A<think>private<thinking>nested</thinking>hidden</think>B", "AB", "AB"},
		{"A</think>B<thi", "AB", "AB"},
		{"A<think>unclosed", "A", "A"},
		{"A<think>private</think>\nweb_fetch\n\nB", "A\nweb_fetch\n\nB", "A\nB"},
		{"\u2003A\r\n\r\nweb_fetch\r\n\r\nB\u2003", "\u2003A\r\n\r\nweb_fetch\r\n\r\nB\u2003", "A\r\nB"},
	} {
		if got := sessionRunnerStripPrivateThinkMarkers(row.source); got != row.privateOnly {
			t.Fatalf("private-only publication semantics changed: got %q want %q", got, row.privateOnly)
		}
		if got := sessionRunnerSanitizeHistoricalPublicTextWithCodes(row.source, nil); got != row.historical {
			t.Fatalf("historical cleanup semantics changed: got %q want %q", got, row.historical)
		}
	}
}

func TestPublicCompletionAdvisoryProjectionDoesNotSearchDuplicateText(t *testing.T) {
	const prefix = "引用 🧪。\n> same-note\n内部执行 `web_fetch`。\n\n"
	const owned = "> same-note"
	const text = prefix + owned
	wire := &completionAdvisoryEnvelope{Schema: "x-science.completion-advisories.v1", Attempt: 3, InputRevision: 2,
		TextSHA256: completionAdvisorySHA(text), TextLength: len(utf16.Encode([]rune(text))),
		Items: []completionAdvisoryItem{{Code: completionAdvisoryResultQuality, Findings: []string{"recorded"},
			Sources: []completionAdvisorySource{{Stage: "artifact_completion", Attempt: 3, InputRevision: 2,
				CandidateSHA256: completionAdvisorySHA(prefix), FindingIndexes: []int{0}}}}},
		Spans: []completionAdvisorySpan{{Start: len(utf16.Encode([]rune(prefix))), End: len(utf16.Encode([]rune(text))), Text: owned}}}
	message := publicAdvisoryProjectionMessage("duplicate-frame", text, 3, wire)
	sanitizeTranscriptWebPublicMessagesWithCodes([]map[string]any{message}, nil)
	content := mapValue(message["content"])
	publicText := webString(content["content"])
	decoded := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": publicText, "completion_advisories": content["completionAdvisories"]}, 3)
	if decoded == nil || len(decoded.Spans) != 1 || sessionRunnerCompletionAdvisoryModelBody(publicText, decoded) != "引用 🧪。\n> same-note\n" {
		t.Fatal("mapping assigned a same-wording legacy quotation to the host")
	}
}

func TestPublicCompletionAdvisoryProjectionRejectsInvalidPreimages(t *testing.T) {
	claim := transcriptstore.RunnerClaim{Attempt: 3, ClaimedInputRevision: 2}
	text, _, original, _ := publicAdvisoryProjectionFixture(t, claim)
	for _, name := range []string{"future-public-hash", "wrong-attempt", "wrong-identity", "unknown-field", "wrong-length", "split-utf16", "invalid-source", "null-spans"} {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(publicAdvisoryProjectionJSON(t, original), &value); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "future-public-hash":
				clean := sessionRunnerSanitizeHistoricalPublicTextWithCodes(text, nil)
				value["text_sha256"] = completionAdvisorySHA(clean)
				value["text_length"] = len(utf16.Encode([]rune(clean)))
				value["spans"] = []any{}
			case "wrong-attempt":
				value["attempt"] = float64(claim.Attempt + 1)
			case "unknown-field":
				value["extra"] = map[string]any{"thinking": "internal `runner_private_checkpoint`"}
			case "wrong-length":
				value["text_length"] = value["text_length"].(float64) + 1
			case "split-utf16":
				start := len(utf16.Encode([]rune(text[:strings.Index(text, "🧪")]))) + 1
				value["spans"] = []any{map[string]any{"start": start, "end": start + 1, "text": "\ufffd"}}
			case "invalid-source":
				value["items"].([]any)[0].(map[string]any)["sources"].([]any)[0].(map[string]any)["candidate_sha256"] = "invalid"
			case "null-spans":
				value["spans"] = nil
			}
			message := publicAdvisoryProjectionMessage("invalid-frame", text, claim.Attempt, value)
			if name == "wrong-identity" {
				mapValue(message["content"])["assistant_attempt_id"] = transcriptAssistantMessageID("other-frame", claim.Attempt, 1)
			}
			sanitizeTranscriptWebPublicMessagesWithCodes([]map[string]any{message}, nil)
			content := mapValue(message["content"])
			if _, present := content["completionAdvisories"]; present {
				t.Fatal("invalid original optional metadata was rebound into a public trust claim")
			}
			if strings.Contains(webString(content["content"]), "web_fetch") || strings.Contains(webString(content["content"]), "private fixture") {
				t.Fatal("invalid metadata bypassed the existing public privacy boundary")
			}
		})
	}
}

func TestPublicCompletionAdvisoryProjectionHTTPReadModel(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	text, wantModel, original, run := publicAdvisoryProjectionFixture(t, f.claim)
	run.Transcript.Stream = f.stream
	// Use the same real host append receipts as the direct fixtures, then the
	// production atomic finalizer and actual materialized /messages endpoint.
	final, err := f.server.persistTranscriptRunnerFinalization(ctx, run.Transcript, transcriptRunnerFinalization{
		Content: text, Status: "completed", Detail: "runner chat completed", SegmentOrdinal: 1, CompletionAdvisories: run.completionAdvisorySnapshot(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	branch, err := f.repo.GetBranchState(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	fence := requireTranscriptWebReadModelFence(t, f.server.transcriptWebReadModel, f.stream, branch.ActiveBranchID)
	if fence.StateStatus != "ready" || fence.StateThroughPublicationSequence != fence.ThroughPublicationSequence || fence.StateSourceRevision != fence.SourceRevision {
		t.Fatalf("public advisory fixture did not activate the materialized read model: %#v", fence)
	}
	for reload := 0; reload < 2; reload++ {
		page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, f.stream.OwnerID)
		if page.Code != http.StatusOK {
			t.Fatalf("public history HTTP%d", page.Code)
		}
		var response struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(page.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range response.Items {
			if mapValue(message["content"])["completionAdvisories"] == nil {
				continue
			}
			found = true
			assertPublicAdvisoryProjection(t, message, f.claim.Attempt, wantModel, original.Items)
		}
		if !found {
			t.Fatal("materialized response dropped valid typed host observations")
		}
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
	found := false
	for _, event := range events {
		if event.Event.EventID != final.Assistant.EventID {
			continue
		}
		found = true
		var canonical map[string]any
		if err := json.Unmarshal(event.ResolvedPayloadJSON, &canonical); err != nil {
			t.Fatal(err)
		}
		if webString(canonical["text"]) != text || sessionRunnerCompletionAdvisoriesFromPayload(canonical, f.claim.Attempt) == nil {
			t.Fatal("public projection rewrote canonical completion or source facts")
		}
	}
	if !found {
		t.Fatal("canonical final assistant was not retained")
	}
}

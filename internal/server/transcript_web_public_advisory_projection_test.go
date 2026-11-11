package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
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
	text, _, original, run := publicAdvisoryProjectionFixture(t, f.claim)
	structure, _ := sessionRunnerCompletionAdvisoryText(completionAdvisoryStructurePreview)
	wantModel := " \n实验结果 🧪。\n\n> " + structure + "\n\n内部执行 `web_fetch`。\n\n保留对照 in_vitro。\n"
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
			content := mapValue(message["content"])
			public := webString(content["content"])
			wire := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": public, "completion_advisories": content["completionAdvisories"]}, f.claim.Attempt)
			if wire == nil || !reflect.DeepEqual(wire.Items, original.Items) || sessionRunnerCompletionAdvisoryModelBody(public, wire) != wantModel || len(wire.Spans) != len(original.Spans) {
				t.Fatal("canonical policy changed model content or actual host provenance")
			}
			for index, span := range wire.Spans {
				if span.Text != original.Spans[index].Text {
					t.Fatal("canonical policy changed host-owned notice bytes")
				}
			}
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

func TestPublicCompletionObservationReferencesLiveHistoryAndCanonical(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	claim := f.claim
	const before = "Measured synthetic result 🧪."
	const after = "Independent uncertainty remains."
	quoted := completionAdvisoryItem{Code: completionAdvisoryResultQuality, Findings: []string{"quoted diagnostic is not a host finding"}, Sources: []completionAdvisorySource{{Stage: "artifact_completion", Attempt: claim.Attempt, InputRevision: claim.ClaimedInputRevision, CandidateSHA256: completionAdvisorySHA("quoted input"), FindingIndexes: []int{0}}}}
	other := quoted
	other.Code = completionAdvisoryStructurePreview
	repeated := quoted
	repeated.Findings = []string{"another quoted diagnostic is not a host finding"}
	quote, err := json.MarshalIndent(map[string]any{"host_completion_observations": []completionAdvisoryItem{quoted, other, repeated}}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	body := before + "\n```json\n" + string(quote) + "\n```\n" + after
	run := &sessionRunnerChatRun{Transcript: &transcriptRunnerAuthority{Stream: f.stream, Claim: claim}}
	text := appendSessionRunnerCompletionAdvisoriesForRun(body, "en", run, "task_expectations", &sessionRunnerCompletionAdvisory{Code: completionAdvisoryTaskExpectations, Findings: []string{"actual canonical uncertainty"}})
	original := run.completionAdvisorySnapshot().envelope(text, claim)
	final, err := f.server.persistTranscriptRunnerFinalization(ctx, run.Transcript, transcriptRunnerFinalization{Content: text, Status: "completed", Detail: "completed", CompletionAdvisories: run.completionAdvisorySnapshot()})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.runTranscriptWebDeliveryCycle(ctx); err != nil {
		t.Fatal(err)
	}
	live := false
	liveText := ""
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type != "message.stream" || event.Payload["completion_advisories"] == nil {
			continue
		}
		live = true
		public := webString(event.Payload["data"])
		liveText = public
		if strings.Contains(public, "host_completion_observations") {
			t.Error("live publication exposed an internal diagnostic quote")
		}
		wire := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": public, "completion_advisories": event.Payload["completion_advisories"]}, claim.Attempt)
		if wire == nil || !reflect.DeepEqual(wire.Items, original.Items) {
			t.Fatal("live publication lost original host facts or imported quoted facts")
		}
		if sessionRunnerCompletionAdvisoryModelBody(public, wire) != before+"\n"+after {
			t.Fatal("live publication swallowed surrounding scientific prose")
		}
	}
	if !live {
		t.Fatal("missing real live advisory publication")
	}
	if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	for reload := 0; reload < 2; reload++ {
		page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, f.stream.OwnerID)
		if page.Code != http.StatusOK {
			t.Fatalf("history HTTP%d", page.Code)
		}
		var response struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(page.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range response.Items {
			content := mapValue(message["content"])
			if content["completionAdvisories"] == nil {
				continue
			}
			found = true
			if strings.Contains(webString(content["content"]), "host_completion_observations") {
				t.Error("reloaded history exposed an internal diagnostic quote")
			}
			public := webString(content["content"])
			wire := sessionRunnerCompletionAdvisoriesFromPayload(map[string]any{"text": public, "completion_advisories": content["completionAdvisories"]}, claim.Attempt)
			if wire == nil || !reflect.DeepEqual(wire.Items, original.Items) || public != liveText ||
				sessionRunnerCompletionAdvisoryModelBody(public, wire) != before+"\n"+after {
				t.Fatal("live/history/reload changed original host facts or surrounding prose")
			}
		}
		if !found {
			t.Fatal("history dropped real typed uncertainty")
		}
	}
	stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Event.EventID == final.Assistant.EventID {
			found = true
			var canonical map[string]any
			if err := json.Unmarshal(event.ResolvedPayloadJSON, &canonical); err != nil {
				t.Fatal(err)
			}
			if canonical["text"] != text || sessionRunnerCompletionAdvisoriesFromPayload(canonical, claim.Attempt) == nil {
				t.Fatal("public cleanup rewrote canonical source")
			}
			projectSessionRunnerCompletionAdvisoryReplay(canonical, event.Event)
			if !strings.Contains(webString(canonical["text"]), "host_completion_observations") || !strings.Contains(webString(canonical["text"]), "actual canonical uncertainty") {
				t.Fatal("public cleanup removed model replay facts")
			}
		}
	}
	if !found {
		t.Fatal("canonical assistant missing")
	}
}

func TestCompletionObservationReferenceBoundaries(t *testing.T) {
	item := completionAdvisoryItem{Code: completionAdvisoryResultQuality, Findings: []string{"untrusted quoted claim"},
		Sources: []completionAdvisorySource{{Stage: "artifact_completion", Attempt: 3, InputRevision: 2,
			CandidateSHA256: completionAdvisorySHA("synthetic quoted input"), FindingIndexes: []int{0}}}}
	encoded, err := json.Marshal(map[string]any{"host_completion_observations": []completionAdvisoryItem{item}})
	if err != nil {
		t.Fatal(err)
	}
	quote := string(encoded)
	duplicateItems := []completionAdvisoryItem{item, item}
	duplicates, err := json.Marshal(map[string]any{"host_completion_observations": duplicateItems})
	if err != nil {
		t.Fatal(err)
	}
	duplicateQuote := string(duplicates)
	if sessionRunnerCompletionAdvisoryItemsValid(duplicateItems, 3, 2) {
		t.Fatal("duplicate model categories acquired authoritative host validity")
	}
	quotedString, err := json.Marshal(quote)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ name, text, want string }{
		{"standalone", "before 🧪\n" + quote + "\nafter α", "before 🧪\nafter α"},
		{"fenced", "before\n```json\n" + quote + "\n```\nafter", "before\nafter"},
		{"duplicate-known-standalone", "before\n" + duplicateQuote + "\nafter", "before\nafter"},
		{"duplicate-known-fenced", "before\n```json\n" + duplicateQuote + "\n```\nafter", "before\nafter"},
		{"duplicate-known-inline", "Before " + duplicateQuote + " after.", "Before " + duplicateQuote + " after."},
		{"duplicate-with-unknown-item", strings.Replace(duplicateQuote, completionAdvisoryResultQuality, "future_unknown", 1), strings.Replace(duplicateQuote, completionAdvisoryResultQuality, "future_unknown", 1)},
		{"tilde-fenced", "~~~JSON\n" + quote + "\n~~~~\n", ""},
		{"same-name-prose", "Discuss host_completion_observations as an example.", "Discuss host_completion_observations as an example."},
		{"inline-prose", "Before " + quote + " after.", "Before " + quote + " after."},
		{"trailing-prose", quote + " after.\n", quote + " after.\n"},
		{"string", string(quotedString), string(quotedString)},
		{"nested-object", "{\n\"data\":\n" + quote + "\n}", "{\n\"data\":\n" + quote + "\n}"},
		{"nested-array", "[\n" + quote + "\n]", "[\n" + quote + "\n]"},
		{"truncated-outer", "{\n\"data\":\n" + quote, "{\n\"data\":\n" + quote},
		{"truncated-reference", quote[:len(quote)-1], quote[:len(quote)-1]},
		{"code-string", "```python\n" + quote + "\n```", "```python\n" + quote + "\n```"},
		{"unclosed-fence", "```json\n" + quote, "```json\n" + quote},
		{"fenced-extra-content", "```json\n" + quote + "\nscientific note\n```", "```json\n" + quote + "\nscientific note\n```"},
		{"unknown-code", strings.ReplaceAll(quote, completionAdvisoryResultQuality, "future_unknown"), strings.ReplaceAll(quote, completionAdvisoryResultQuality, "future_unknown")},
		{"unknown-source", strings.ReplaceAll(quote, "artifact_completion", "future_unknown"), strings.ReplaceAll(quote, "artifact_completion", "future_unknown")},
		{"reserved-key-alias", strings.Replace(quote, "host_completion_observations", "HOST_completion_observations", 1), strings.Replace(quote, "host_completion_observations", "HOST_completion_observations", 1)},
		{"item-key-alias", strings.Replace(quote, `"code":`, `"Code":`, 1), strings.Replace(quote, `"code":`, `"Code":`, 1)},
		{"unknown-item-field", strings.Replace(quote, `"code":`, `"future":true,"code":`, 1), strings.Replace(quote, `"code":`, `"future":true,"code":`, 1)},
		{"duplicate-field", strings.Replace(quote, `"code":`, `"code":"ignored","code":`, 1), strings.Replace(quote, `"code":`, `"code":"ignored","code":`, 1)},
		{"empty-observations", `{"host_completion_observations":[]}`, `{"host_completion_observations":[]}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			projected := sessionRunnerPublicTextIdentity(row.text).withoutCompletionObservationReferences()
			if projected.text != row.want {
				t.Fatalf("public reference boundary changed ordinary/unknown content: got %q want %q", projected.text, row.want)
			}
		})
	}
	// Strictly recognized machine prose still cannot supply host authority.
	for _, payload := range []map[string]any{
		{"text": quote},
		{"text": quote, "completion_advisories": map[string]any{"schema": "x-science.completion-advisories.v1", "attempt": 3}},
	} {
		stream := map[string]any{}
		projectTranscriptWebCompletionObservationReferences(stream, payload, 3)
		if stream["data"] != "" || stream["completion_advisories"] != nil {
			t.Fatal("a quoted or malformed model object supplied host provenance")
		}
	}
}

func TestCanonicalBusinessJSONPreservesLiveHistoryAndReload(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	const business = "# Synthetic business result\n\n```json\n{\n  \"artifact_note\": \"ordinary business field\",\n  \"value\": 1\n}\n```\n\n**Keep this Markdown** and `analysis_report`."
	const body = business + "<think>synthetic private content</think>"
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	final, err := f.server.persistTranscriptRunnerFinalization(ctx, authority, transcriptRunnerFinalization{Content: body, Status: "completed", Detail: "completed", SegmentOrdinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.runTranscriptWebDeliveryCycle(ctx); err != nil {
		t.Fatal(err)
	}
	liveCount := 0
	liveEqual := false
	for _, event := range transcriptWebEvents(t, f.store, f.stream.OwnerID) {
		if event.Type != "message.stream" {
			continue
		}
		kind := firstNonEmpty(webString(event.Payload["stream_type"]), webString(event.Payload["type"]))
		if kind != "content" {
			continue
		}
		text, ok := event.Payload["data"].(string)
		if !ok {
			continue
		}
		liveCount++
		liveEqual = text == business
	}
	if liveCount != 1 || !liveEqual {
		t.Error("actual WS did not preserve canonical synthetic business JSON")
	}
	if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	for reload := 0; reload < 2; reload++ {
		page := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, f.stream.OwnerID)
		if page.Code != http.StatusOK {
			t.Fatalf("history HTTP%d", page.Code)
		}
		var response struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(page.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range response.Items {
			if message["terminal_status"] != "completed" || message["position"] != "left" {
				continue
			}
			found = true
			projected := webString(mapValue(message["content"])["content"])
			if projected != business {
				t.Errorf("history reload%d removed ordinary canonical JSON/Markdown", reload)
			}
		}
		if !found {
			t.Fatal("canonical assistant was absent from materialized API")
		}
	}
	stream, err := f.repo.GetStream(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := f.repo.ListProjectedEvents(ctx, transcriptstore.ListProjectedEventsInput{StreamUID: stream.UID, OwnerID: stream.OwnerID, ThroughPublicationSequence: stream.NextPublication - 1, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	canonicalUnchanged := false
	source := ""
	attemptBound := false
	for _, event := range events {
		if event.Event.EventID == final.Assistant.EventID {
			var original map[string]any
			if json.Unmarshal(event.ResolvedPayloadJSON, &original) != nil {
				t.Fatal("canonical payload invalid")
			}
			canonicalUnchanged = webString(original["text"]) == body
			source = string(event.Event.Source)
			attemptBound = event.Event.RunnerAttempt != nil && *event.Event.RunnerAttempt == f.claim.Attempt
		}
	}
	if !canonicalUnchanged || source != "payload" || !attemptBound {
		t.Fatal("canonical assistant source/attempt was not preserved")
	}
}

func TestCanonicalPublicPageSourceProofIsConservative(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	const body = "{\n  \"artifact_note\": \"ordinary field\"\n}"
	authority := &transcriptRunnerAuthority{Stream: f.stream, Claim: f.claim}
	final, err := f.server.persistTranscriptRunnerFinalization(ctx, authority, transcriptRunnerFinalization{Content: body, Status: "completed", Detail: "completed", SegmentOrdinal: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	fence := requireTranscriptWebReadModelFence(t, f.server.transcriptWebReadModel, f.stream, snapshot.BranchID)
	page, found, err := f.server.transcriptWebReadModel.GetTranscriptWebMessageRange(ctx, transcriptstore.TranscriptWebMessagePageInput{
		OwnerID: f.stream.OwnerID, StreamUID: f.stream.UID, BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence, SourceRevision: fence.SourceRevision, Limit: 80,
	})
	if err != nil || !found {
		t.Fatalf("page found=%t err=%v", found, err)
	}
	ranges := make([]transcriptstore.ProjectedAssistantSourceRange, len(page.Messages))
	for index, record := range page.Messages {
		ranges[index] = transcriptstore.ProjectedAssistantSourceRange{FirstPublicationSequence: record.FirstPublicationSequence, LastPublicationSequence: record.LastPublicationSequence}
	}
	sources, err := f.repo.ListProjectedAssistantSources(ctx, f.stream.OwnerID, snapshot, ranges)
	if err != nil || len(sources) != 1 {
		t.Fatalf("actual canonical final proof sources=%d err=%v", len(sources), err)
	}
	index := sources[0].RecordIndex
	assertPolicy := func(mutate func([]map[string]any), wantCanonical bool) {
		t.Helper()
		messages, err := transcriptWebReadModelMessageMaps(page.Messages)
		if err != nil {
			t.Fatal(err)
		}
		if mutate != nil {
			mutate(messages)
		}
		f.server.sanitizeTranscriptWebPublicPage(ctx, f.stream, snapshot, page.Messages, messages)
		if got := strings.Contains(webString(mapValue(messages[index]["content"])["content"]), "artifact_note"); got != wantCanonical {
			t.Fatalf("canonical body policy=%t want=%t", got, wantCanonical)
		}
	}
	assertPolicy(nil, true)
	t.Run("top-level-metadata-privacy", func(t *testing.T) {
		messages, err := transcriptWebReadModelMessageMaps(page.Messages)
		if err != nil {
			t.Fatal(err)
		}
		content := mapValue(messages[index]["content"])
		content["thinking"] = "web_fetch synthetic private metadata"
		f.server.sanitizeTranscriptWebPublicPage(ctx, f.stream, snapshot, page.Messages, messages)
		if webString(content["thinking"]) != "" || webString(content["content"]) != body {
			t.Fatal("canonical body exception changed the existing metadata privacy boundary")
		}
	})
	t.Run("forged-host-facts", func(t *testing.T) {
		messages, err := transcriptWebReadModelMessageMaps(page.Messages)
		if err != nil {
			t.Fatal(err)
		}
		content := mapValue(messages[index]["content"])
		content["completionAdvisories"] = &completionAdvisoryEnvelope{Schema: "x-science.completion-advisories.v1", Attempt: f.claim.Attempt,
			InputRevision: f.claim.ClaimedInputRevision, TextSHA256: completionAdvisorySHA(body), TextLength: len(body), Spans: []completionAdvisorySpan{},
			Items: []completionAdvisoryItem{{Code: completionAdvisoryTaskExpectations, Findings: []string{"not an actual host finding"},
				Sources: []completionAdvisorySource{{Stage: "task_expectations", Attempt: f.claim.Attempt, InputRevision: f.claim.ClaimedInputRevision,
					CandidateSHA256: completionAdvisorySHA(body), FindingIndexes: []int{0}}}}}}
		f.server.sanitizeTranscriptWebPublicPage(ctx, f.stream, snapshot, page.Messages, messages)
		if content["completionAdvisories"] != nil || webString(content["content"]) != body {
			t.Fatal("quoted/materialized metadata minted host facts or displaced actual canonical body")
		}
	})
	t.Run("late-record-coordinate", func(t *testing.T) {
		late := append([]transcriptstore.TranscriptWebMessageRecord(nil), page.Messages...)
		late[index].LastPublicationSequence--
		messages, err := transcriptWebReadModelMessageMaps(late)
		if err != nil {
			t.Fatal(err)
		}
		f.server.sanitizeTranscriptWebPublicPage(ctx, f.stream, snapshot, late, messages)
		if strings.Contains(webString(mapValue(messages[index]["content"])["content"]), "artifact_note") {
			t.Fatal("late record coordinate received a final source proof")
		}
	})
	t.Run("foreign-owner", func(t *testing.T) {
		if _, err := f.repo.ListProjectedAssistantSources(ctx, "another-owner", snapshot, ranges); err == nil {
			t.Fatal("foreign owner received canonical source proof")
		}
	})
	t.Run("stale-generation", func(t *testing.T) {
		stale := snapshot
		stale.BranchGeneration++
		if _, err := f.repo.ListProjectedAssistantSources(ctx, f.stream.OwnerID, stale, ranges); err == nil {
			t.Fatal("stale branch received canonical source proof")
		}
	})
	for _, row := range []struct{ name, mutation string }{
		{"imported", `UPDATE transcript_events SET event_type='history_assistant_message' WHERE event_id=? AND stream_uid=?`},
		{"progress", `UPDATE transcript_events SET event_type='content_delta' WHERE event_id=? AND stream_uid=?`},
		{"private-block", `UPDATE transcript_events SET payload_json=json_set(payload_json,'$.block_type','thinking') WHERE event_id=? AND stream_uid=?`},
		{"wrong-final-segment", `UPDATE transcript_events SET payload_json=json_set(payload_json,'$.assistant_segment.ordinal',2) WHERE event_id=? AND stream_uid=?`},
	} {
		t.Run(row.name, func(t *testing.T) {
			if _, err := f.db.Exec(row.mutation, final.Assistant.EventID, f.stream.UID); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := f.db.Exec(`UPDATE transcript_events SET event_type='assistant_message',payload_json=? WHERE event_id=? AND stream_uid=?`, final.Assistant.PayloadJSON, final.Assistant.EventID, f.stream.UID); err != nil {
					t.Error(err)
				}
			}()
			assertPolicy(nil, false)
		})
	}
	t.Run("failed-terminal", func(t *testing.T) {
		if _, err := f.db.Exec(`UPDATE transcript_runner_receipts SET status='failed' WHERE stream_uid=? AND attempt=?`, f.stream.UID, f.claim.Attempt); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = f.db.Exec(`UPDATE transcript_runner_receipts SET status='completed' WHERE stream_uid=? AND attempt=?`, f.stream.UID, f.claim.Attempt)
		}()
		assertPolicy(nil, false)
	})
	for _, name := range []string{"body-mismatch", "forged-id", "wrong-role"} {
		t.Run(name, func(t *testing.T) {
			assertPolicy(func(messages []map[string]any) {
				switch name {
				case "body-mismatch":
					mapValue(messages[index]["content"])["content"] = body + "\nchanged"
				case "forged-id":
					messages[index]["id"] = "assistant-forged-1"
				case "wrong-role":
					messages[index]["position"] = "right"
				}
			}, false)
		})
	}
	t.Run("unavailable-proof", func(t *testing.T) {
		original := f.server.transcriptStore
		f.server.transcriptStore = transcriptstore.NewRepository(nil)
		defer func() { f.server.transcriptStore = original }()
		assertPolicy(nil, false)
	})
	assertPolicy(nil, true)
}

func TestCanonicalPublicPagePreservesOrdinaryUserText(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	const body = "Please inspect the harness logs.\n\nKeep the first paragraph and its blank separator.\n\n```text\nweb_fetch is an example in my code.\n\nretain_this_line\n```\n\nThen explain the observed result."
	input, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "ordinary-user-input",
		FrameEventID: "ordinary-user-event", MessageUUID: "ordinary-user-message", Text: body,
	})
	if err != nil || !created {
		t.Fatalf("append ordinary input created=%t err=%v", created, err)
	}
	if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	for reload := 0; reload < 2; reload++ {
		if reload == 1 {
			reopened, err := workspace.Open(f.databasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			f.server.transcriptStore, err = reopened.TranscriptRepository(ctx)
			if err != nil {
				t.Fatal(err)
			}
			f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
			if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
				t.Fatal(err)
			}
		}
		response := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, f.stream.OwnerID)
		if response.Code != http.StatusOK {
			t.Fatalf("history HTTP%d", response.Code)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range page.Items {
			if message["msg_id"] != input.ClientMessageID {
				continue
			}
			found = true
			if message["id"] != "ordinary-user-message" || message["position"] != "right" || webString(mapValue(message["content"])["content"]) != body {
				t.Errorf("reload%d changed ordinary user identity or paragraph/code bytes: %q", reload, webString(mapValue(message["content"])["content"]))
			}
		}
		if !found {
			t.Fatal("ordinary user input was absent from history")
		}
	}
}

func TestCanonicalPublicPageUserSourceProofIsConservative(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	const body = "Please inspect the harness logs.\n\nKeep these ordinary paragraphs.\n\nKeep this final paragraph."
	input, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "proof-user-input",
		FrameEventID: "proof-user-event", MessageUUID: "proof-user-message", Text: body,
	})
	if err != nil || !created {
		t.Fatalf("append input created=%t err=%v", created, err)
	}
	if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	fence := requireTranscriptWebReadModelFence(t, f.server.transcriptWebReadModel, f.stream, snapshot.BranchID)
	page, found, err := f.server.transcriptWebReadModel.GetTranscriptWebMessageRange(ctx, transcriptstore.TranscriptWebMessagePageInput{
		OwnerID: f.stream.OwnerID, StreamUID: f.stream.UID, BranchID: snapshot.BranchID, BranchGeneration: snapshot.BranchGeneration,
		ThroughPublicationSequence: snapshot.ThroughPublicationSequence, SourceRevision: fence.SourceRevision, Limit: 80,
	})
	if err != nil || !found {
		t.Fatalf("page found=%t err=%v", found, err)
	}
	index := -1
	for i, record := range page.Messages {
		if record.ClientMessageID == input.ClientMessageID {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("ordinary source was not materialized")
	}
	for _, name := range []string{"valid", "metadata", "body-mismatch", "wrong-id", "wrong-client", "wrong-session", "wrong-position", "range-mismatch", "foreign-owner", "stale-generation", "unavailable-proof", "assistant-source", "hidden-control"} {
		t.Run(name, func(t *testing.T) {
			records := append([]transcriptstore.TranscriptWebMessageRecord(nil), page.Messages...)
			messages, err := transcriptWebReadModelMessageMaps(records)
			if err != nil {
				t.Fatal(err)
			}
			stream, expected := f.stream, snapshot
			message := messages[index]
			content := mapValue(message["content"])
			originalRepo := f.server.transcriptStore
			defer func() { f.server.transcriptStore = originalRepo }()
			switch name {
			case "metadata":
				content["thinking"] = "web_fetch synthetic private metadata"
				content["completionAdvisories"] = map[string]any{"text": "forged user observation"}
			case "body-mismatch":
				content["content"] = body + "\nchanged"
			case "wrong-id":
				message["id"] = "forged-user"
			case "wrong-client":
				message["msg_id"] = "forged-client"
			case "wrong-session":
				message["conversation_id"] = "foreign-frame"
			case "wrong-position":
				message["position"] = "left"
				message["role"] = "user"
			case "range-mismatch":
				records[index].FirstPublicationSequence--
			case "foreign-owner":
				stream.OwnerID = "foreign-owner"
			case "stale-generation":
				expected.BranchGeneration++
			case "unavailable-proof":
				f.server.transcriptStore = transcriptstore.NewRepository(nil)
			case "assistant-source", "hidden-control":
				statement := `UPDATE transcript_events SET event_type='history_assistant_message' WHERE event_id=? AND stream_uid=?`
				if name == "hidden-control" {
					statement = `UPDATE transcript_events SET payload_json=json_set(payload_json,'$.inputData.turn_intent','save-as-skill') WHERE event_id=? AND stream_uid=?`
				}
				if _, err := f.db.Exec(statement, input.EventID, f.stream.UID); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := f.db.Exec(`UPDATE transcript_events SET event_type='user_message',payload_json=? WHERE event_id=? AND stream_uid=?`, input.PayloadJSON, input.EventID, f.stream.UID); err != nil {
						t.Error(err)
					}
				}()
			}
			f.server.sanitizeTranscriptWebPublicPage(ctx, stream, expected, records, messages)
			if preserved := webString(content["content"]) == body; preserved != (name == "valid" || name == "metadata") {
				t.Fatalf("user source exemption=%t", preserved)
			}
			if name == "metadata" && (content["thinking"] != "" || content["completionAdvisories"] != nil) {
				t.Fatal("user body proof exempted metadata from privacy cleanup")
			}
		})
	}
	for _, role := range []string{"user", "assistant"} {
		message := map[string]any{"type": "text", "position": "right", "role": role, "content": map[string]any{"content": body}}
		sanitizeTranscriptWebPublicMessagesWithCodes([]map[string]any{message}, nil)
		if strings.Contains(webString(mapValue(message["content"])["content"]), "harness") {
			t.Fatal("unproven role/position bypassed the assistant privacy boundary")
		}
	}
}

func TestRichHistoryPreservesOrdinaryUserText(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	const body = "Please inspect the harness logs.\n\nFirst paragraph.\n\n```text\nweb_fetch example\n```\n\nLast paragraph."
	for _, role := range []string{"user", "assistant"} {
		t.Run(role, func(t *testing.T) {
			messages, err := f.server.projectRichWebConversationMessages(f.stream.FrameID, []map[string]any{
				{"role": role, "_uuid": "legacy-input", "content": []any{map[string]any{"type": "text", "text": body}}},
			}, "")
			if err != nil || len(messages) != 1 {
				t.Fatalf("messages=%d err=%v", len(messages), err)
			}
			if preserved := webString(mapValue(messages[0]["content"])["content"]) == body; preserved != (role == "user") {
				t.Fatalf("legacy role %s preservation=%t", role, preserved)
			}
		})
	}
}

func TestCanonicalUserTextSurvivesContinuationAndBranchEdit(t *testing.T) {
	f := newAgentSaveArtifactsFixture(t)
	ctx := context.Background()
	f.server.transcriptWebReadModel = transcriptstore.NewWebReadModelRepository(f.db, f.db)
	const body = "Please inspect the harness logs.\n\nKeep the original user paragraphs."
	input, _, created, err := f.repo.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, ClientMessageID: "continuation-original",
		FrameEventID: "continuation-original-event", MessageUUID: "continuation-original-message", Text: body,
	})
	if err != nil || !created {
		t.Fatalf("append input created=%t err=%v", created, err)
	}
	if _, err := f.store.SetFrameRuntimeMetadata(f.stream.FrameID, workspace.FrameRuntimeMetadata{ContextData: map[string]any{
		"_pending_input_requests": []any{map[string]any{"tool_id": "network-ordinary", "kind": "network"}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.AppendFrameEvent(workspace.FrameEventInput{FrameID: f.stream.FrameID, Type: "user_message", Payload: map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "network-ordinary", "content": `{"status":"awaiting_user_response"}`}},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE frames SET status='awaiting_user_response' WHERE id=?`, f.stream.FrameID); err != nil {
		t.Fatal(err)
	}
	resolution := []workspace.CompatibilityInputResolution{{ToolID: "network-ordinary", Content: `{"approved":true,"scope":"once"}`}}
	for retry := 0; retry < 2; retry++ {
		result, err := f.store.ResolveCompatibilityPendingInputsWithTranscriptConfig(ctx, f.stream.FrameID, resolution, nil)
		if err != nil || (retry == 0 && result.Status != "accepted") || (retry == 1 && !result.AlreadyResolved) {
			t.Fatalf("approval retry%d result=%#v err=%v", retry, result, err)
		}
	}
	assertHistory := func(client, text string, absent string) {
		t.Helper()
		if err := f.server.runTranscriptWebReadModelCycle(ctx); err != nil {
			t.Fatal(err)
		}
		response := p3JSONRequest(t, f.server, http.MethodGet, "/api/conversations/"+f.stream.FrameID+"/messages?limit=80", nil, f.stream.OwnerID)
		if response.Code != http.StatusOK {
			t.Fatalf("history HTTP%d", response.Code)
		}
		var page struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, message := range page.Items {
			if absent != "" && message["msg_id"] == absent {
				t.Fatal("branch reused the edited source identity")
			}
			if message["msg_id"] == client {
				found = true
				if webString(mapValue(message["content"])["content"]) != text {
					t.Fatal("continuation or branch changed the user body")
				}
			}
		}
		if !found {
			t.Fatal("expected user event identity was absent")
		}
	}
	assertHistory(input.ClientMessageID, body, "")
	snapshot, err := f.repo.GetProjectionSnapshot(ctx, f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	const replacement = "Please inspect the harness logs again.\n\nThis is an explicit edited user event."
	fork, err := f.repo.ForkFrameUserMessageAtIndex(ctx, transcriptstore.ForkFrameUserMessageAtIndexInput{
		StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID, SourceBranchID: snapshot.BranchID,
		ExpectedActiveBranchID: snapshot.BranchID, ExpectedGeneration: snapshot.BranchGeneration,
		ClientMutationID: "ordinary-user-explicit-edit", SourceMessageIndex: 1, ExpectedSourceClientID: input.ClientMessageID,
		ReplacementText: replacement, Destinations: []string{"ws"},
	})
	if err != nil || !fork.Created || fork.ForkEventID != input.EventID || fork.ReplacementEvent.EventID == input.EventID {
		t.Fatalf("explicit edit fork=%#v err=%v", fork, err)
	}
	assertHistory(fork.ReplacementEvent.ClientMessageID, replacement, input.ClientMessageID)
	var original []byte
	if err := f.db.QueryRow(`SELECT payload_json FROM transcript_events WHERE stream_uid=? AND event_id=?`, f.stream.UID, input.EventID).Scan(&original); err != nil || string(original) != string(input.PayloadJSON) {
		t.Fatalf("approval/edit rewrote original source err=%v", err)
	}
}

func TestCanonicalPublicPagePreservesMoreThanEventBatchOfUserText(t *testing.T) {
	ctx := context.Background()
	store, repository, db := newTranscriptWebFixture(t)
	seedTranscriptWebFrame(t, store, transcriptWebServingOwnerID, transcriptWebServingProjectID, transcriptWebServingFrameID)
	stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{
		UID: transcriptWebServingStreamUID, OwnerID: transcriptWebServingOwnerID,
		ExternalID: transcriptWebServingFrameID, SessionID: transcriptWebServingFrameID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: transcriptWebServingProjectID,
		RootFrameID: transcriptWebServingFrameID, FrameID: transcriptWebServingFrameID, Epoch: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	const count = 1001
	if err := repository.RunImmediate(ctx, func(tx *transcriptstore.ImmediateTransaction) error {
		for index := 0; index < count; index++ {
			_, _, created, err := tx.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
				StreamUID: stream.UID, OwnerID: stream.OwnerID,
				ClientMessageID: fmt.Sprintf("large-page-client-%d", index),
				FrameEventID:    fmt.Sprintf("large-page-event-%d", index),
				MessageUUID:     fmt.Sprintf("large-page-message-%d", index),
				Text:            fmt.Sprintf("Please inspect the harness logs for input %d.\n\nKeep this paragraph and its separator.", index),
			})
			if err != nil {
				return err
			}
			if !created {
				return fmt.Errorf("input %d was not created", index)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	server, _, _ := newTranscriptWebReadModelServingServer(t, store, repository, db, stream)
	response := p3JSONRequest(t, server, http.MethodGet,
		fmt.Sprintf("/api/conversations/%s/messages?limit=%d", stream.FrameID, maxWebMessagePageSize), nil, stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("history HTTP%d: %s", response.Code, response.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != count {
		t.Fatalf("history items=%d want=%d", len(page.Items), count)
	}
	for index, message := range page.Items {
		body := fmt.Sprintf("Please inspect the harness logs for input %d.\n\nKeep this paragraph and its separator.", index)
		if message["id"] != fmt.Sprintf("large-page-message-%d", index) ||
			message["msg_id"] != fmt.Sprintf("large-page-client-%d", index) ||
			webString(mapValue(message["content"])["content"]) != body {
			t.Fatalf("large history page changed user input %d identity or paragraph bytes", index)
		}
	}
}

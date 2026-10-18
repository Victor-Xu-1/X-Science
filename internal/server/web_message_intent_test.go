package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

func TestWebMessageIntentCancelBeforePostIsDurableAndSideEffectFree(t *testing.T) {
	f := newAsideAcceptanceFixture(t)
	const loadingID = "pending-web-message"
	path := "/api/conversations/" + f.stream.FrameID
	// Cancellation binds original attachments without copying or opening them.
	body := map[string]any{"content": "Frozen followup", "loading_id": loadingID, "files": []string{"/missing/original-upload"}, "session_options": map[string]any{"memory_mode": "off"}}
	response := p3JSONRequest(t, f.server, http.MethodDelete, path+"/message-intents/"+loadingID, body, f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
	receipt := p3DecodeObject(t, response)
	canonical := transcriptstore.FrameMessageIntentID(f.stream.FrameID, loadingID, "")
	if receipt["frame_id"] != f.stream.FrameID || receipt["loading_id"] != loadingID || receipt["input_message_id"] != canonical || receipt["accepted"] != false || receipt["cancelled"] != true || receipt["cancellation_status"] != "cancelled_before_acceptance" {
		t.Fatalf("receipt: %#v", receipt)
	}
	late := p3JSONRequest(t, f.server, http.MethodPost, path+"/messages", body, f.stream.OwnerID)
	if late.Code != http.StatusConflict || p3DecodeObject(t, late)["code"] != "MESSAGE_INTENT_CANCELLED" {
		t.Fatalf("late post: %d %s", late.Code, late.Body.String())
	}
	repeat := p3JSONRequest(t, f.server, http.MethodDelete, path+"/message-intents/"+loadingID, body, f.stream.OwnerID)
	if repeat.Code != http.StatusOK || p3DecodeObject(t, repeat)["replayed"] != true {
		t.Fatalf("lost receipt: %d %s", repeat.Code, repeat.Body.String())
	}
	changed := copyMapAny(body)
	changed["session_options"] = map[string]any{"memory_mode": "on"}
	conflict := p3JSONRequest(t, f.server, http.MethodDelete, path+"/message-intents/"+loadingID, changed, f.stream.OwnerID)
	if conflict.Code != http.StatusConflict || p3DecodeObject(t, conflict)["code"] != "MESSAGE_INTENT_CONFLICT" {
		t.Fatalf("changed body: %d %s", conflict.Code, conflict.Body.String())
	}
	for _, owner := range []string{"", "another-owner"} {
		denied := p3JSONRequest(t, f.server, http.MethodDelete, path+"/message-intents/"+loadingID, body, owner)
		if denied.Code != http.StatusUnauthorized && denied.Code != http.StatusNotFound {
			t.Fatalf("owner boundary: %d %s", denied.Code, denied.Body.String())
		}
	}
	var inputs, cancellations int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM transcript_events WHERE stream_uid=? AND event_type IN ('user_message','user_input_response')`, f.stream.UID).Scan(&inputs); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM frame_events WHERE event_type='frame_cancelled'`).Scan(&cancellations); err != nil {
		t.Fatal(err)
	}
	if inputs != 1 || cancellations != 0 {
		t.Fatalf("preaccept changed task: inputs=%d cancellations=%d", inputs, cancellations)
	}
	reopened, err := workspace.Open(f.databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	record, found, err := reopened.GetCompatibilityMessageIntent(canonical)
	if err != nil || !found {
		t.Fatalf("restart lost receipt: %t %v", found, err)
	}
	raw, _ := json.Marshal(record.DeliveryPayload["message_intent_cancellation"])
	var persisted transcriptstore.FrameMessageIntentCancellation
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.CancelFrameMessageIntent(context.Background(), persisted.Intent)
	if err != nil || !replayed.Replayed || replayed.Receipt != persisted {
		t.Fatalf("restart retry: %#v %v", replayed, err)
	}
}

func TestWebMessageIntentPostThenCancelUsesAcceptedCanonicalID(t *testing.T) {
	for _, branchMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "selected-branch"}[branchMode], func(t *testing.T) {
			f := newAsideAcceptanceFixture(t)
			const loadingID = "accepted-web-message"
			options := map[string]any{"memory_mode": "off"}
			target := ""
			if branchMode {
				state, err := f.repo.GetBranchState(context.Background(), f.stream.UID, f.stream.OwnerID)
				if err != nil {
					t.Fatal(err)
				}
				target = state.ActiveBranchID
				options["target_branch_id"], options["expected_branch_id"], options["expected_generation"] = target, target, state.Generation
			}
			body := map[string]any{"content": "Frozen accepted followup", "loading_id": loadingID, "files": []string{}, "session_options": options}
			path := "/api/conversations/" + f.stream.FrameID
			post := p3JSONRequest(t, f.server, http.MethodPost, path+"/messages", body, f.stream.OwnerID)
			if post.Code != http.StatusAccepted {
				t.Fatalf("post: %d %s", post.Code, post.Body.String())
			}
			acceptedID := p3DecodeObject(t, post)["msg_id"]
			if acceptedID == loadingID || acceptedID != transcriptstore.FrameMessageIntentID(f.stream.FrameID, loadingID, target) {
				t.Fatalf("canonical input: %#v", acceptedID)
			}
			cancel := p3JSONRequest(t, f.server, http.MethodDelete, path+"/message-intents/"+loadingID, body, f.stream.OwnerID)
			if cancel.Code != http.StatusOK {
				t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String())
			}
			receipt := p3DecodeObject(t, cancel)
			if receipt["accepted"] != true || receipt["cancelled"] != true || receipt["input_message_id"] != acceptedID || receipt["cancellation_status"] != "cancelled" {
				t.Fatalf("receipt: %#v", receipt)
			}
			late := p3JSONRequest(t, f.server, http.MethodPost, path+"/messages", body, f.stream.OwnerID)
			if late.Code != http.StatusConflict || p3DecodeObject(t, late)["code"] != "MESSAGE_INTENT_CANCELLED" {
				t.Fatalf("resurrected post: %d %s", late.Code, late.Body.String())
			}
		})
	}
}

func TestWebFrozenMessageDigestBindsCompleteNormalizedBody(t *testing.T) {
	base := webConversationSendInput{Content: "Frozen input", LoadingID: "frozen-loading", SessionOptions: map[string]any{"expected_generation": json.Number("1")}}
	want, err := webFrozenMessageDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*webConversationSendInput){
		"content": func(v *webConversationSendInput) { v.Content = "changed" },
		"files":   func(v *webConversationSendInput) { v.Files = []string{"original-upload"} },
		"artifact_refs": func(v *webConversationSendInput) {
			v.ArtifactRefs = []webConversationArtifactReferenceInput{{ArtifactID: "a", VersionID: "v"}}
		},
		"message_context":       func(v *webConversationSendInput) { v.MessageContext = "onboarding_first_task" },
		"inject_skills":         func(v *webConversationSendInput) { v.InjectSkills = []string{"Customize"} },
		"inject_mcp_server_ids": func(v *webConversationSendInput) { v.InjectMCPServerIDs = []string{"mcp"} },
		"session_options": func(v *webConversationSendInput) {
			v.SessionOptions = map[string]any{"expected_generation": json.Number("2")}
		},
		"turn_intent": func(v *webConversationSendInput) { v.TurnIntent = "save-as-skill" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := base
			mutate(&candidate)
			got, err := webFrozenMessageDigest(candidate)
			if err != nil || got == want {
				t.Fatalf("field not bound: %s %v", got, err)
			}
		})
	}
	equivalent := base
	equivalent.Files = []string{}
	equivalent.ArtifactRefs = []webConversationArtifactReferenceInput{}
	equivalent.Content = "  Frozen input  "
	if got, err := webFrozenMessageDigest(equivalent); err != nil || got != want {
		t.Fatalf("empty/default normalization: %s %v", got, err)
	}
}

func TestScopedSessionCancellationDoesNotSignalForPreacceptReceipt(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	s := &Server{sessionRuns: map[string]*activeSessionRun{"frame": {cancel: cancel}}}
	if err := s.commitScopedSessionRunCancellation([]string{"frame"}, func(map[string]*transcriptRunnerAuthority) ([]string, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("preaccept cancellation stopped unrelated run")
	}
	if err := s.commitScopedSessionRunCancellation([]string{"frame"}, func(map[string]*transcriptRunnerAuthority) ([]string, error) { return []string{"frame"}, nil }); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("committed exact cancellation did not signal owned run")
	}
}

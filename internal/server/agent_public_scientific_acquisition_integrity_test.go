package server

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestNativeScientificAcquisitionCheckpointPreservesNumbers(t *testing.T) {
	fixture, gateway, call, ctx, requests, _ := scientificAcquisitionFixture(t, true)
	ctx, parent, input, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
	phase, err := fixture.server.newNativeScientificAcquisition(ctx, gateway.taskRun, parent, call.ID, input, request)
	if err != nil {
		t.Fatal(err)
	}
	// This isolated failed checkpoint grants no source authority. It tests that
	// untrusted 64-bit transport metadata survives canonicalization exactly.
	phase.ToolResult, err = json.Marshal(normalizeNativeScientificSourceResult(json.RawMessage(`{"ok":false,"result":{"contentLength":9007199254740993}}`)))
	if err != nil {
		t.Fatal(err)
	}
	phase.ResultSHA256 = scientificSourceDigest(phase.ToolResult)
	if err := fixture.server.checkpointNativeScientificAcquisition(ctx, parent, phase, "source_acquisition_failed"); err != nil {
		t.Fatal(err)
	}
	_, events := scientificAcquisitionEvents(t, fixture)
	for _, event := range events {
		var stored nativeScientificAcquisition
		_ = json.Unmarshal(event.Event.PayloadJSON, &stored)
		if stored.ToolCallID != phase.ToolCallID {
			continue
		}
		if !bytes.Contains(stored.ToolResult, []byte("9007199254740993")) || scientificSourceDigest(stored.ToolResult) != stored.ResultSHA256 || requests.Load() != 0 {
			t.Fatalf("checkpoint changed actual result numbers or digest: %s", event.Event.PayloadJSON)
		}
		return
	}
	t.Fatal("missing exact failed checkpoint")
}

func TestNativeScientificAcquisitionReceiptIntegrity(t *testing.T) {
	fixture, gateway, call, ctx, requests, content := scientificAcquisitionFixture(t, true)
	ctx, parent, input, request := scientificAcquisitionParent(t, fixture, gateway, call, ctx)
	if err := fixture.server.acquireNativeScientificSource(ctx, parent, call.ID, input, request); err != nil {
		t.Fatal(err)
	}
	snapshot, events := scientificAcquisitionEvents(t, fixture)
	var completed transcriptstore.ProjectedEvent
	var original nativeScientificAcquisition
	for _, event := range events {
		var phase nativeScientificAcquisition
		_ = json.Unmarshal(event.Event.PayloadJSON, &phase)
		if phase.Schema == agentPublicScientificAcquisitionSchema && phase.ToolPhase == "source_acquisition_completed" {
			completed, original = event, phase
		}
	}
	if completed.Event.EventID == 0 || requests.Load() != 1 {
		t.Fatal("missing real source completion")
	}
	if binding, _, accepted, err := fixture.server.nativeScientificAcquisitionSource(ctx, snapshot, completed, request.SourceURL); err != nil || !accepted || binding.ToolCallID != original.ToolCallID {
		t.Fatalf("real persisted source failed its own validator: accepted=%t binding=%#v err=%v", accepted, binding, err)
	}
	var envelope struct {
		Result struct {
			BytesRead     int    `json:"bytesRead"`
			BodySHA256    string `json:"bodySha256"`
			BodyHashScope string `json:"bodyHashScope"`
			Partial       bool   `json:"partial"`
			Complete      bool   `json:"complete"`
		} `json:"result"`
	}
	if err := json.Unmarshal(original.ToolResult, &envelope); err != nil {
		t.Fatal(err)
	}
	observed := envelope.Result
	if observed.BytesRead != nativeScientificSourceLimit || !observed.Partial || observed.Complete ||
		observed.BodyHashScope != "returned_response_bytes" || observed.BodySHA256 != scientificSourceDigest([]byte(content[:nativeScientificSourceLimit])) ||
		observed.BodySHA256 == scientificSourceDigest([]byte(content)) {
		t.Fatalf("bounded preview became whole-file checksum/completion: %#v", observed)
	}
	for _, field := range []string{"parent_id", "parent_event", "original_hash", "executed_hash", "source_url", "request", "result", "owner", "branch", "generation", "failed", "corrupt_start"} {
		t.Run(field, func(t *testing.T) {
			phase := original
			switch field {
			case "parent_id":
				phase.ParentToolCallID = "foreign-parent"
			case "parent_event":
				phase.ParentSourceEventID++
			case "original_hash":
				phase.ParentArgumentsSHA256 = strings.Repeat("a", 64)
			case "executed_hash":
				phase.ParentExecutedInput = json.RawMessage(`{"url":"https://source.example.org/other.csv"}`)
			case "source_url":
				phase.SourceURL = "https://source.example.org/records.csv?release=two&part=1"
			case "request":
				phase.ToolInput = json.RawMessage(`{"limit":4096,"url":"https://source.example.org/records.csv?release=two&part=1"}`)
				phase.RequestSHA256 = scientificSourceDigest(phase.ToolInput)
			case "result":
				phase.ToolResult = json.RawMessage(`{"ok":true,"result":{"url":"https://source.example.org/forged.csv"}}`)
			case "owner":
				phase.OwnerID = "foreign-owner"
			case "branch":
				phase.BranchID = "foreign-branch"
			case "generation":
				phase.BranchGeneration++
			case "failed":
				phase.ToolPhase = "source_acquisition_failed"
			case "corrupt_start":
				phase.ToolPhase = "source_acquisition_start"
				phase.ParentSourceEventID++
				phase.ToolResult, phase.ResultSHA256 = nil, ""
			}
			// Even recomputing the self-described phase ID does not repair its
			// immutable parent, exact request or real result binding.
			phase.ToolCallID = phase.identity()
			event := completed
			event.Event.PayloadJSON = mustMarshalRawMessage(phase)
			if _, _, accepted, err := fixture.server.nativeScientificAcquisitionSource(ctx, snapshot, event, request.SourceURL); err == nil || accepted {
				t.Fatalf("accepted forged %s receipt: accepted=%t err=%v", field, accepted, err)
			}
		})
	}
}

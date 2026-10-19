package server

import (
	"net/http"
	"reflect"
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
	"x-science/internal/sessionagent"
)

func asideSpecialistCounts(t *testing.T, f *agentSaveArtifactsFixture) []int {
	t.Helper()
	var result []int
	for _, table := range []string{"frames", "frame_events", "transcript_streams", "transcript_events", "queued_user_messages"} {
		var count int
		if err := f.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		result = append(result, count)
	}
	return result
}

func TestAsideSpecialistFailureRollsBackEveryArtifact(t *testing.T) {
	f, prepared := newAsideSpecialistFixture(t)
	body := asideSpecialistBody(prepared, "fb7389a9-e28a-4816-b295-51c656251fb9")
	path := "/api/frames/" + f.stream.FrameID + "/aside"
	before := asideSpecialistCounts(t, f)
	if _, err := f.db.Exec(`CREATE TRIGGER fail_specialist_aside_input BEFORE INSERT ON transcript_events
		WHEN NEW.event_type='user_message' AND NEW.stream_uid<>'frame:frame-save'
		BEGIN SELECT RAISE(ABORT,'synthetic first input failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if failed.Code < 400 {
		t.Fatalf("failed append was accepted: %d %s", failed.Code, failed.Body.String())
	}
	if after := asideSpecialistCounts(t, f); !reflect.DeepEqual(before, after) {
		t.Fatalf("transaction left child/private/history/receipt: before=%v after=%v", before, after)
	}
	if _, err := f.db.Exec(`DROP TRIGGER fail_specialist_aside_input`); err != nil {
		t.Fatal(err)
	}
	retry := p3JSONRequest(t, f.server, http.MethodPost, path, body, f.stream.OwnerID)
	if retry.Code != http.StatusOK || decodeAsideReference(t, p3DecodeObject(t, retry), "specialist_binding").SnapshotID != prepared.Receipt.SnapshotID {
		t.Fatalf("same nonce after rollback: %d %s", retry.Code, retry.Body.String())
	}
}

func TestAsideSpecialistRejectsWrongScopeBranchAndRevocation(t *testing.T) {
	for _, name := range []string{"owner", "scope", "branch", "stale-head", "revoked", "empty-model-maximum"} {
		t.Run(name, func(t *testing.T) {
			f, prepared := newAsideSpecialistFixture(t)
			body := asideSpecialistBody(prepared, "da574aaa-4b7b-46e7-bf10-1cec827c9d3e")
			owner, expected := f.stream.OwnerID, http.StatusConflict
			reference := asideSpecialistReference(prepared)
			switch name {
			case "owner":
				owner, expected = "other-owner", http.StatusNotFound
			case "scope":
				reference.Scope.FrameIncarnationID = "different-incarnation"
				body["source_specialist"] = reference
			case "branch":
				reference.Branch.Generation++
				body["source_specialist"] = reference
			case "stale-head":
				_ = prepareAsideSpecialist(t, f, "ASIDE_SYNTHETIC_B", "newer-head")
			case "revoked":
				if _, err := f.db.Exec(`UPDATE user_agents SET enabled=0 WHERE user_id=? AND name=?`, owner, prepared.Snapshot.Desired.SelectionName); err != nil {
					t.Fatal(err)
				}
			case "empty-model-maximum":
				body["model"], expected = "not-captured-model", http.StatusForbidden
			}
			before := asideSpecialistCounts(t, f)
			response := p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", body, owner)
			if response.Code != expected {
				t.Fatalf("%s: %d %s want %d", name, response.Code, response.Body.String(), expected)
			}
			if after := asideSpecialistCounts(t, f); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s rejection left work: %v => %v", name, before, after)
			}
		})
	}
}

func TestAsideSpecialistDoesNotInheritMutableParentModel(t *testing.T) {
	f, _ := newAsideSpecialistFixture(t)
	base, err := f.repo.GetBranchState(t.Context(), f.stream.UID, f.stream.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.repo.ForkFrameUserMessageBranch(t.Context(), transcriptstore.ForkFrameUserMessageBranchInput{StreamUID: f.stream.UID, OwnerID: f.stream.OwnerID,
		SourceBranchID: base.ActiveBranchID, ExpectedActiveBranchID: base.ActiveBranchID, ExpectedGeneration: base.Generation,
		ClientMutationID: "mutable-parent-model", SourceClientMessageID: "save-user", SourceMessageIndex: 0, ReplacementText: "New source with a mutable model",
		RuntimeConfig: map[string]any{"model": "mutable-parent-model", "reviewer_model": "mutable-parent-reviewer"},
		Destinations:  []string{transcriptWebDestination}})
	if err != nil {
		t.Fatal(err)
	}
	prepared := prepareAsideSpecialist(t, f, "ASIDE_SYNTHETIC_A", "prepare-after-model-change")
	response := p3JSONRequest(t, f.server, http.MethodPost, "/api/frames/"+f.stream.FrameID+"/aside", asideSpecialistBody(prepared, "7c6c2cc6-b671-46a5-b330-52c03644af94"), f.stream.OwnerID)
	if response.Code != http.StatusOK {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	child := decodeAsideReference(t, p3DecodeObject(t, response), "specialist_binding")
	stream, found, err := f.repo.GetFrameStreamBySession(t.Context(), f.stream.OwnerID, child.Scope.FrameID)
	if err != nil || !found {
		t.Fatalf("child stream: %t %v", found, err)
	}
	config, found, err := f.repo.LatestFrameRuntimeConfig(t.Context(), stream.UID, f.stream.OwnerID)
	if err != nil || !found || config["model"] == "mutable-parent-model" || config["reviewer_model"] != nil {
		t.Fatalf("mutable parent model escaped: %#v %t %v", config, found, err)
	}
	definition, err := f.store.SessionSpecialistDefinition(t.Context(), f.stream.OwnerID, child.Scope.FrameID, &sessionagent.InputReference{Scope: child.Scope, Branch: child.Branch, Revision: child.Revision, SnapshotID: child.SnapshotID})
	if err != nil || definition == nil || webString(config["model"]) != definition.Settings.Model {
		t.Fatalf("not frozen default: %#v definition=%#v err=%v", config, definition, err)
	}
}

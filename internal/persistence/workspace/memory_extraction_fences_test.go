package workspace

import (
	"errors"
	"strings"
	"testing"
)

func TestMemoryExtractionCommitFencesSourceCursorAndScope(t *testing.T) {
	for _, field := range []string{"owner", "project", "incarnation", "cursor"} {
		t.Run(field, func(t *testing.T) {
			store, _, input := newExtractionCommitFixture(t)
			want := ErrExtractionSourceChanged
			switch field {
			case "owner":
				input.Source.OwnerUserID = "another-owner"
			case "project":
				input.Source.ProjectID = "another-project"
			case "incarnation":
				legacy := *input.Source.Legacy
				legacy.FrameIncarnationID += "-new"
				input.Source.Legacy = &legacy
			case "cursor":
				input.CursorBefore = 1
				want = ErrExtractionCursorChanged
			}
			if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, want) {
				t.Fatalf("fence error=%v want=%v", err, want)
			}
			assertExtractionCounts(t, store, 0, 0, 0)
		})
	}
	store, _, input := newExtractionCommitFixture(t)
	if _, err := store.CreateFrame(CreateFrameInput{ID: "child", ProjectID: input.Source.ProjectID, ParentFrameID: input.Source.FrameID, AgentName: "GENERAL", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	input.Source.FrameID = "child"
	if err := store.db.QueryRow(`SELECT incarnation_id FROM frames WHERE id='child'`).Scan(&input.Source.Legacy.FrameIncarnationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, ErrExtractionSourceChanged) {
		t.Fatalf("child was accepted as root: %v", err)
	}
	assertExtractionCounts(t, store, 0, 0, 0)
}

func TestMemoryExtractionCanonicalSourceRevalidatedInsideCommit(t *testing.T) {
	for _, coordinate := range []string{"branch", "publication", "attempt", "input", "active generation"} {
		t.Run(coordinate, func(t *testing.T) {
			store, _, input := canonicalExtractionCommitFixture(t)
			proof := *input.Source.Transcript
			switch coordinate {
			case "branch":
				proof.BranchID += "-stale"
			case "publication":
				proof.PublicationSeq++
			case "attempt":
				proof.Attempt++
			case "input":
				proof.InputRevision++
			case "active generation":
				if _, err := store.db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, proof.StreamUID); err != nil {
					t.Fatal(err)
				}
			}
			input.Source.Transcript = &proof
			if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, ErrExtractionSourceChanged) {
				t.Fatalf("changed canonical coordinate accepted: %v", err)
			}
			assertExtractionCounts(t, store, 0, 0, 0)
		})
	}
}

func TestMemoryExtractionCanonicalReceiptSurvivesBranchChangeWithoutFreshApply(t *testing.T) {
	store, _, input := canonicalExtractionCommitFixture(t)
	if _, err := store.CommitMemoryExtraction(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE transcript_branch_state SET generation=generation+1 WHERE stream_uid=?`, input.Source.Transcript.StreamUID); err != nil {
		t.Fatal(err)
	}
	if saved, found, err := store.ReadMemoryExtractionReceipt(t.Context(), input.Source); err != nil || !found || !saved.Replayed {
		t.Fatalf("original receipt was revoked by later generation: saved=%+v found=%t err=%v", saved, found, err)
	}
	if saved, err := store.CommitMemoryExtraction(t.Context(), input); err != nil || !saved.Replayed {
		t.Fatalf("original receipt commit did not replay: saved=%+v err=%v", saved, err)
	}
	proof := *input.Source.Transcript
	proof.BranchGeneration++
	input.Source.Transcript = &proof
	if saved, found, err := store.ReadMemoryExtractionReceipt(t.Context(), input.Source); err != nil || !found || !saved.Replayed {
		t.Fatalf("recaptured branch coordinates could not read original outcome: saved=%+v found=%t err=%v", saved, found, err)
	}
	if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, ErrExtractionSourceChanged) {
		t.Fatalf("changed source was treated as a new fresh completion: %v", err)
	}
	for _, field := range []string{"input revision", "publication", "attempt", "frame incarnation"} {
		t.Run(field, func(t *testing.T) {
			tampered := input
			proof := *input.Source.Transcript
			switch field {
			case "input revision":
				proof.InputRevision++
			case "publication":
				proof.PublicationSeq++
			case "attempt":
				proof.Attempt++
			case "frame incarnation":
				proof.FrameIncarnationID += "-changed"
			}
			tampered.Source.Transcript = &proof
			if result, found, err := store.ReadMemoryExtractionReceipt(t.Context(), tampered.Source); found || err == nil && result.Replayed {
				t.Fatalf("changed immutable %s read original result: result=%+v found=%t err=%v", field, result, found, err)
			}
		})
	}
	assertExtractionCounts(t, store, 1, 2, 1)
}

func TestMemoryExtractionFreshCommitRechecksPersistedPolicy(t *testing.T) {
	for _, policy := range []string{"user", "project", "session off", "aside"} {
		t.Run(policy, func(t *testing.T) {
			store, _, input := newExtractionCommitFixture(t)
			var err error
			switch policy {
			case "user":
				err = store.SetMemoryEnabled(t.Context(), input.Source.OwnerUserID, false)
			case "project":
				err = store.SetProjectMemoryEnabled(t.Context(), input.Source.ProjectID, input.Source.OwnerUserID, false)
			case "session off":
				_, err = store.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,context_data) VALUES(?,?)`, input.Source.FrameID, `{"_original_input":{"memory_mode":"off"}}`)
			case "aside":
				_, err = store.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,input_data) VALUES(?,?)`, input.Source.FrameID, `{"_aside_parent":"parent"}`)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, ErrAgentMemoryMutationForbidden) {
				t.Fatalf("disabled extraction policy accepted: %v", err)
			}
			assertExtractionCounts(t, store, 0, 0, 0)
		})
	}
}

func TestMemoryExtractionPlanSkipsStaleFactsAndKeepsCategoryFallback(t *testing.T) {
	store, _, input := newExtractionCommitFixture(t)
	old := extractionFixtureMemory(t, store, "changed-fact")
	input.Plan.Replace = []ExtractionReplacement{{Expected: old, NewID: "stale-successor", Body: "Stale correction", Evidence: "stated"}}
	input.Plan.Remove = []ExtractionRemoval{{Expected: old}}
	newBody := "Newer user-edited fact must survive."
	if _, err := store.UpdateMemoryOwned(t.Context(), old.ID, old.UserID, UpdateMemoryInput{Body: &newBody}); err != nil {
		t.Fatal(err)
	}
	category, err := store.CreateMemoryCategory(t.Context(), old.UserID, "Temporary", "Classify reproducibility preferences.", true)
	if err != nil {
		t.Fatal(err)
	}
	input.Plan.Append[0].Input.CategoryID = category.ID
	if _, err := store.db.Exec(`DELETE FROM memory_categories WHERE id=?`, category.ID); err != nil {
		t.Fatal(err)
	}
	result, err := store.CommitMemoryExtraction(t.Context(), input)
	if err != nil || len(result.Applied.Failures) != 2 || len(result.Applied.Replaced)+len(result.Applied.Removed) != 0 || len(result.Applied.Appended) != 1 {
		t.Fatalf("stale plan outcome=%+v err=%v", result, err)
	}
	for _, failure := range result.Applied.Failures {
		if !strings.Contains(failure.Error, ErrAgentMemoryChanged.Error()) {
			t.Fatalf("missing stale diagnostic: %+v", failure)
		}
	}
	if current, found, err := store.GetMemoryOwned(t.Context(), old.UserID, old.ID); err != nil || !found || current.Body != newBody || current.SupersededBy != "" {
		t.Fatalf("newer fact changed: current=%+v found=%t err=%v", current, found, err)
	}
	if appendRow, found, err := store.GetMemoryOwned(t.Context(), old.UserID, "extracted-append"); err != nil || !found || appendRow.CategoryID != "" {
		t.Fatalf("missing-category fallback=%+v found=%t err=%v", appendRow, found, err)
	}
	assertExtractionCounts(t, store, 2, 2, 1)
}

func TestMemoryExtractionRejectsUserAndCrossProjectPlansAtomically(t *testing.T) {
	for _, kind := range []string{"user", "cross project"} {
		t.Run(kind, func(t *testing.T) {
			store, _, input := newExtractionCommitFixture(t)
			old := extractionFixtureMemory(t, store, "protected")
			if kind == "user" {
				old.Origin = "user"
				if _, err := store.db.Exec(`UPDATE memories SET origin='user' WHERE id=?`, old.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := store.CreateProject(CreateProjectInput{ID: "other-project", UserID: input.Source.OwnerUserID, Name: "Other"}); err != nil {
					t.Fatal(err)
				}
				old.SubjectProjectID = "other-project"
				if _, err := store.db.Exec(`UPDATE memories SET subject_project_id=? WHERE id=?`, old.SubjectProjectID, old.ID); err != nil {
					t.Fatal(err)
				}
			}
			input.Plan.Remove = []ExtractionRemoval{{Expected: old}}
			if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, ErrAgentMemoryMutationForbidden) {
				t.Fatalf("protected plan accepted: %v", err)
			}
			assertExtractionCounts(t, store, 1, 0, 0)
		})
	}
}

func TestMemoryExtractionPolicyProjectionKeepsExistingPreferencePriority(t *testing.T) {
	store, _, input := newExtractionCommitFixture(t)
	for _, scenario := range []struct {
		name, input, context string
		denied               bool
	}{
		{"original camel off", `{}`, `{"_original_input":{"memoryMode":"off"}}`, true},
		{"input snake off", `{"memory_mode":"off"}`, `{}`, true},
		{"input camel off", `{"memoryMode":"off"}`, `{}`, true},
		{"original wins input", `{"memory_mode":"off"}`, `{"_original_input":{"memoryMode":"on"}}`, false},
		{"snake key wins camel", `{}`, `{"_original_input":{"memory_mode":"on","memoryMode":"off"}}`, false},
		{"existing null key keeps priority", `{"memory_mode":"off"}`, `{"_original_input":{"memory_mode":null}}`, false},
		{"non-map original falls back", `{"memoryMode":"off"}`, `{"_original_input":"legacy-label"}`, true},
		{"context aside", `{}`, `{"_aside_parent":"parent"}`, true},
		{"null aside is absent", `{"_aside_parent":null}`, `{}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := store.db.Exec(`INSERT INTO frame_runtime_metadata(frame_id,input_data,context_data) VALUES(?,?,?)
				ON CONFLICT(frame_id) DO UPDATE SET input_data=excluded.input_data,context_data=excluded.context_data`, input.Source.FrameID, scenario.input, scenario.context); err != nil {
				t.Fatal(err)
			}
			err := requireExtractionPolicyTx(t.Context(), store.db, input.Source)
			if scenario.denied && !errors.Is(err, ErrAgentMemoryMutationForbidden) || !scenario.denied && err != nil {
				t.Fatalf("preference projection denied=%t err=%v", scenario.denied, err)
			}
		})
	}
}

func TestMemoryExtractionRejectsUnclassifiedPlan(t *testing.T) {
	store, _, input := newExtractionCommitFixture(t)
	input.Plan.Prepared.PIUnavailable = true
	if _, err := store.CommitMemoryExtraction(t.Context(), input); err == nil {
		t.Fatal("unclassified plan was committed")
	}
	assertExtractionCounts(t, store, 0, 0, 0)
}

func TestMemoryExtractionLegitimateOvershootAndDisabledCursorRemainSupported(t *testing.T) {
	for _, advance := range []bool{false, true} {
		name := "no cursor advance"
		if advance {
			name = "overshoot recovery"
		}
		t.Run(name, func(t *testing.T) {
			store, _, input := newExtractionCommitFixture(t)
			if _, err := store.SetLastExtractMessageIndex(t.Context(), input.Source.FrameID, 8, nil); err != nil {
				t.Fatal(err)
			}
			input.CursorBefore, input.AdvanceCursor, input.Overshoot = 8, advance, advance
			result, err := store.CommitMemoryExtraction(t.Context(), input)
			wantCursor := 8
			if advance {
				wantCursor = 2
			}
			if err != nil || result.CursorAfter != wantCursor || result.CursorAdvanced != advance {
				t.Fatalf("bounded cursor behavior=%+v err=%v", result, err)
			}
			assertExtractionCounts(t, store, 1, wantCursor, 1)
		})
	}
}

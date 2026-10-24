package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestMemoryExtractionCoverageReaderUsesScopedBoundedTail(t *testing.T) {
	store, _, own := newExtractionCommitFixture(t)
	if _, err := store.CommitMemoryExtraction(t.Context(), own); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProject(CreateProjectInput{ID: "other-project", UserID: "other-owner", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateFrame(CreateFrameInput{ID: "other-frame", ProjectID: "other-project", AgentName: "GENERAL", Status: "completed", ConversationType: "agent"}); err != nil {
		t.Fatal(err)
	}
	frame, found, err := store.GetFrame("other-frame")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	other := own
	other.Source.OwnerUserID, other.Source.ProjectID, other.Source.FrameID = "other-owner", "other-project", frame.ID
	other.Plan = ExtractionMutationPlan{}
	other.CursorBefore = 0
	for index := range 129 {
		other.Source.Legacy = &LegacyExtractionSource{SessionID: frame.ID, FrameIncarnationID: frame.IncarnationID,
			FinishEventID: int64(index + 1), PrefixSHA256: extractionDigest([]byte(fmt.Sprintf("controlled prefix %d", index)))}
		other.InputSHA256 = extractionDigest([]byte(fmt.Sprintf("controlled input %d", index)))
		if _, err := store.CommitMemoryExtraction(t.Context(), other); err != nil {
			t.Fatal(err)
		}
		other.CursorBefore = 2
	}
	// These Store fixtures exercise the DB boundary; no legacy journal/runtime
	// projection is claimed by their caller-bound synthetic source values.
	other.Source.Legacy = &LegacyExtractionSource{SessionID: frame.ID, FrameIncarnationID: frame.IncarnationID,
		FinishEventID: 1000, PrefixSHA256: extractionDigest([]byte("future other prefix"))}
	candidates, err := store.ReadMemoryExtractionCoverageReceipts(t.Context(), other.Source)
	if err != nil || len(candidates) != 128 {
		t.Fatalf("tail candidates=%d err=%v", len(candidates), err)
	}
	for index, candidate := range candidates {
		if candidate.Source.OwnerUserID != "other-owner" || candidate.Source.Legacy.FinishEventID != int64(index+2) {
			t.Fatalf("scope/source order changed: %+v", candidate.Source)
		}
	}
	legacy := *own.Source.Legacy
	legacy.FinishEventID, legacy.PrefixSHA256 = 1000, extractionDigest([]byte("future own prefix"))
	own.Source.Legacy = &legacy
	if candidates, err := store.ReadMemoryExtractionCoverageReceipts(t.Context(), own.Source); err != nil || len(candidates) != 0 {
		t.Fatalf("foreign rows escaped bounded tail: candidates=%+v err=%v", candidates, err)
	}
	foreign := own.Source
	foreign.OwnerUserID = "other-owner"
	if _, err := store.ReadMemoryExtractionCoverageReceipts(t.Context(), foreign); !errors.Is(err, ErrExtractionSourceChanged) {
		t.Fatalf("foreign root read original payloads: %v", err)
	}
}

func TestMemoryExtractionCoverageReaderPreservesIntegrityAndCancellationErrors(t *testing.T) {
	store, _, input := newExtractionCommitFixture(t)
	target := input.Source
	legacy := *target.Legacy
	legacy.FinishEventID++
	target.Legacy = &legacy
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.ReadMemoryExtractionCoverageReceipts(ctx, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled coverage became empty success: %v", err)
	}
	identity, err := identifyExtractionSource(input.Source)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately bypass Commit validation only to seed a malformed stored
	// result through the original private writer in this isolated DB fixture.
	if err := repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
		return store.insertExtractionReceiptTx(t.Context(), tx, identity, input.InputSHA256,
			extractionDigest([]byte("controlled invalid plan")), ExtractionCommitResult{
				Gate: "done", InputSHA256: input.InputSHA256, DeltaCount: -1,
			})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadMemoryExtractionCoverageReceipts(t.Context(), target); !errors.Is(err, ErrExtractionPlanConflict) {
		t.Fatalf("malformed receipt became unknown coverage: %v", err)
	}
}

func nextCoverageOrderCompletion(t *testing.T, repo *transcriptstore.Repository, prior ExtractionCommitInput, key string) ExtractionCommitInput {
	t.Helper()
	proof := prior.Source.Transcript
	if _, _, _, err := repo.AppendFrameUserEvent(t.Context(), transcriptstore.AppendFrameUserEventInput{
		StreamUID: proof.StreamUID, OwnerID: prior.Source.OwnerUserID, ClientMessageID: key + "-input",
		FrameEventID: key + "-event", MessageUUID: key + "-message", Text: "Continue the retained reporting task.",
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repo.ClaimRunner(t.Context(), transcriptstore.ClaimRunnerInput{
		StreamUID: proof.StreamUID, OwnerID: prior.Source.OwnerUserID, RunnerID: key + "-runner",
		TTL: time.Minute, ResumeSource: transcriptstore.ResumeSourceFresh,
	})
	if err != nil || !claimed.Claimed {
		t.Fatal(claimed, err)
	}
	finish, _, _, err := repo.FinishRunner(t.Context(), transcriptstore.FinishRunnerInput{
		Claim: claimed.Claim, ClientMessageID: key + "-finish", Status: "completed", PayloadJSON: []byte(`{"status":"completed"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := repo.ReadCompletedMemorySource(t.Context(), proof.StreamUID, prior.Source.OwnerUserID, finish.EventID)
	if err != nil {
		t.Fatal(err)
	}
	next := prior
	next.Source.Transcript = &actual
	next.CursorBefore, next.CursorNext, next.DeltaCount = 2, 2, 2
	next.InputSHA256 = extractionDigest([]byte(key))
	next.Plan.Append = append([]AgentMemoryAppendMutation(nil), prior.Plan.Append...)
	next.Plan.Append[0].Input.ID = key
	return next
}

func TestMemoryExtractionOlderSelectedCompletionCannotWriteAtSameScalar(t *testing.T) {
	store, repo, initial := canonicalExtractionCommitFixture(t)
	if _, err := store.CommitMemoryExtraction(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	older := nextCoverageOrderCompletion(t, repo, initial, "older-same-cursor")
	newer := nextCoverageOrderCompletion(t, repo, older, "newer-same-cursor")
	result, err := store.CommitMemoryExtraction(t.Context(), newer)
	if err != nil || result.CursorAdvanced || result.CursorAfter != 2 {
		t.Fatalf("newer controlled equal-view commit=%+v err=%v", result, err)
	}
	identity, err := identifyExtractionSource(newer.Source)
	if err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := store.db.QueryRow(`SELECT result_json FROM memory_extraction_receipts WHERE source_key=?`, identity.key).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitMemoryExtraction(t.Context(), older); !errors.Is(err, ErrExtractionSourceChanged) {
		t.Fatalf("late older write borrowed the unchanged scalar: %v", err)
	}
	if err := store.db.QueryRow(`SELECT result_json FROM memory_extraction_receipts WHERE source_key=?`, identity.key).Scan(&after); err != nil || after != before {
		t.Fatalf("late write changed newer outcome: %v", err)
	}
	if _, found, err := store.ReadMemoryExtractionReceipt(t.Context(), older.Source); err != nil || found {
		t.Fatalf("fenced source committed a receipt: found=%t err=%v", found, err)
	}
	assertExtractionCounts(t, store, 2, 2, 2)
}

func TestMemoryExtractionCoverageReaderBindsSelectedRowAndAuthority(t *testing.T) {
	for _, mode := range []string{"source-key-alias", "noncanonical-source", "cross-kind-duplicate-key"} {
		t.Run(mode, func(t *testing.T) {
			var store *Store
			var input ExtractionCommitInput
			if mode == "cross-kind-duplicate-key" {
				store, _, input = canonicalExtractionCommitFixture(t)
			} else {
				store, _, input = newExtractionCommitFixture(t)
			}
			identity, err := identifyExtractionSource(input.Source)
			if err != nil {
				t.Fatal(err)
			}
			current := input.Source
			if current.Legacy == nil {
				current.Transcript = nil
				current.Legacy = &LegacyExtractionSource{SessionID: current.FrameID, FrameIncarnationID: identity.incarnation,
					FinishEventID: 3, PrefixSHA256: extractionDigest([]byte("legacy coverage source"))}
			}
			legacy := *current.Legacy
			legacy.FinishEventID = 100
			current.Legacy = &legacy
			switch mode {
			case "source-key-alias":
				if _, err := store.CommitMemoryExtraction(t.Context(), input); err != nil {
					t.Fatal(err)
				}
				identity.key = extractionDigest([]byte("corrupt-reference-to-" + identity.key))
			case "noncanonical-source":
				identity.encoded = " " + identity.encoded
				identity.digest = extractionDigest([]byte(identity.encoded))
			case "cross-kind-duplicate-key":
				// SQLite reads the first Legacy value; Go sees the final null.
				// The selected row may never redirect to another authority's key.
				priorLegacy := *current.Legacy
				priorLegacy.FinishEventID = 3
				raw, err := json.Marshal(priorLegacy)
				if err != nil {
					t.Fatal(err)
				}
				identity.encoded = strings.Replace(identity.encoded, `"Legacy":null`, `"Legacy":`+string(raw)+`,"Legacy":null`, 1)
				identity.digest = extractionDigest([]byte(identity.encoded))
				currentIdentity, err := identifyExtractionSource(current)
				if err != nil {
					t.Fatal(err)
				}
				plain, err := identifyExtractionSource(input.Source)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := identifyStoredExtractionSource(currentIdentity, extractionStoredSource{
					key: plain.key, raw: plain.encoded, digest: plain.digest,
				}); !errors.Is(err, ErrExtractionPlanConflict) {
					t.Fatalf("cross-kind canonical source accepted: %v", err)
				}
			}
			repo, err := store.TranscriptRepository(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			// Malformed-row injection is limited to this isolated Store fixture.
			if err := repo.RunImmediate(t.Context(), func(tx *transcriptstore.ImmediateTransaction) error {
				return store.insertExtractionReceiptTx(t.Context(), tx, identity, input.InputSHA256,
					extractionDigest([]byte("controlled plan")), ExtractionCommitResult{
						Gate: "done", InputSHA256: input.InputSHA256, DeltaCount: 2,
					})
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.ReadMemoryExtractionCoverageReceipts(t.Context(), current); !errors.Is(err, ErrExtractionPlanConflict) {
				t.Fatalf("malformed selected row became successful coverage: %v", err)
			}
		})
	}
}

func TestMemoryExtractionLegacyCheckerIsHostOnlyAndRequiredForFreshDelta(t *testing.T) {
	store, _, input := newExtractionCommitFixture(t)
	input.LegacyOrderCheck = nil
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digestBefore, err := extractionPlanDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	input.LegacyOrderCheck = func(CompletedExtractionSource) (bool, error) { return false, ErrExtractionPlanConflict }
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	digestAfter, err := extractionPlanDigest(input)
	if err != nil || digestBefore != digestAfter || string(before) != string(after) {
		t.Fatalf("host checker entered encoded fields/digest: %v", err)
	}
	first, err := store.CommitMemoryExtraction(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.LegacyOrderCheck = nil
	if result, err := store.CommitMemoryExtraction(t.Context(), input); err != nil || !result.Replayed ||
		result.CursorBefore != first.CursorBefore || result.CursorAfter != first.CursorAfter {
		t.Fatalf("immutable replay acquired a new host requirement: %+v %v", result, err)
	}
	legacy := *input.Source.Legacy
	legacy.FinishEventID++
	legacy.PrefixSHA256 = extractionDigest([]byte("next fresh prefix"))
	input.Source.Legacy, input.CursorBefore = &legacy, 2
	input.Plan = ExtractionMutationPlan{}
	if _, err := store.CommitMemoryExtraction(t.Context(), input); !errors.Is(err, ErrExtractionPlanConflict) {
		t.Fatalf("new legacy delta silently omitted checker: %v", err)
	}
	if _, found, err := store.ReadMemoryExtractionReceipt(t.Context(), input.Source); err != nil || found {
		t.Fatalf("missing checker left a receipt: %t %v", found, err)
	}
}

func TestMemoryExtractionLegacyOrderCannotUseCoverageTailAsAbsence(t *testing.T) {
	store, _, older := newExtractionCommitFixture(t)
	newer := older
	legacy := *older.Source.Legacy
	legacy.FinishEventID = 6
	legacy.PrefixSHA256 = extractionDigest([]byte("selected later prefix"))
	newer.Source.Legacy = &legacy
	newer.AdvanceCursor, newer.Plan = false, ExtractionMutationPlan{}
	if _, err := store.CommitMemoryExtraction(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	for index := range 129 {
		stale := newer
		proof := *newer.Source.Legacy
		proof.FinishEventID = int64(index + 100)
		proof.PrefixSHA256 = extractionDigest([]byte(fmt.Sprintf("replaced prefix %d", index)))
		stale.Source.Legacy = &proof
		stale.InputSHA256 = extractionDigest([]byte(fmt.Sprintf("input %d", index)))
		if _, err := store.CommitMemoryExtraction(t.Context(), stale); err != nil {
			t.Fatal(err)
		}
	}
	// A caller-bound Store-only checker marks exactly one later prefix. The
	// genuine journal race/renumbering checks live in the server tests.
	older.LegacyOrderCheck = func(candidate CompletedExtractionSource) (bool, error) {
		return candidate.Legacy != nil && candidate.Legacy.PrefixSHA256 == legacy.PrefixSHA256, nil
	}
	if _, err := store.CommitMemoryExtraction(t.Context(), older); !errors.Is(err, ErrExtractionSourceChanged) {
		t.Fatalf("newest 128 hid a committed selected later source: %v", err)
	}
	if _, found, err := store.ReadMemoryExtractionReceipt(t.Context(), older.Source); err != nil || found {
		t.Fatalf("late source wrote after incomplete order proof: %t %v", found, err)
	}
}

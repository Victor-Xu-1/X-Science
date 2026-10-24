package memoryextract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	workspace "x-science/internal/persistence/workspace"
)

func coverageMessages(count int) []Message {
	messages := make([]Message, count)
	for index := range messages {
		messages[index] = Message{Role: "user", Content: []Block{{Type: BlockText, Text: strings.Repeat("x", index+1)}}}
	}
	return messages
}

func coverageResult(t *testing.T, messages []Message, delta int) workspace.ExtractionCommitResult {
	t.Helper()
	digest, err := ExtractionSnapshotDigest(messages, "original final")
	if err != nil {
		t.Fatal(err)
	}
	return workspace.ExtractionCommitResult{Gate: "done", InputSHA256: digest, DeltaCount: delta}
}

func TestMemoryExtractionCoverageRequiresExaminedPrefixClosure(t *testing.T) {
	current := coverageMessages(5)
	for _, test := range []struct {
		name                         string
		covered, length, delta, want int
	}{
		{"full", 0, 2, 2, 2},
		{"retained-prefix", 2, 4, 2, 4},
		{"old-longer-rewrite-gap", 0, 4, 2, 0},
		{"gap-after-retained-prefix", 2, 5, 2, 2},
		{"zero-interval", 2, 4, 0, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			prior := current[:test.length]
			saved := coverageResult(t, prior, test.delta)
			// The old scalar can belong to another longer branch. It is not n.
			saved.CursorBefore, saved.CursorAfter = 99, 99
			got, err := AdvanceExtractionCoverage(current, test.covered, prior, "original final", saved)
			if err != nil || got != test.want {
				t.Fatalf("covered=%d want=%d err=%v", got, test.want, err)
			}
		})
	}
	prior := coverageMessages(2)
	saved := coverageResult(t, prior, 2)
	changed := coverageMessages(5)
	changed[0].Ignore = true
	if got, err := AdvanceExtractionCoverage(changed, 0, prior, "original final", saved); err != nil || got != 0 {
		t.Fatalf("changed Ignore flag borrowed coverage=%d err=%v", got, err)
	}
	saved.DeltaCount = 3
	if _, err := AdvanceExtractionCoverage(current, 0, prior, "original final", saved); !errors.Is(err, workspace.ErrExtractionPlanConflict) {
		t.Fatalf("invalid examined interval was accepted: %v", err)
	}
	covered := 0
	for _, interval := range [][2]int{{2, 2}, {4, 1}, {5, 5}} {
		prior := current[:interval[0]]
		var err error
		covered, err = AdvanceExtractionCoverage(current, covered, prior, "original final", coverageResult(t, prior, interval[1]))
		if err != nil {
			t.Fatal(err)
		}
	}
	if covered != 5 {
		t.Fatalf("full current result did not close a prior gap: %d", covered)
	}
}

func TestMemoryExtractionSnapshotDigestKeepsOriginalEncoding(t *testing.T) {
	want := sha256.Sum256([]byte(`{"Messages":null,"Final":"unchanged"}`))
	got, err := ExtractionSnapshotDigest(nil, "unchanged")
	if err != nil || got != hex.EncodeToString(want[:]) {
		t.Fatalf("original input digest changed: got=%s err=%v", got, err)
	}
}

func TestMemoryExtractionRunnerKeepsScalarCASSeparateFromCoverage(t *testing.T) {
	for _, mode := range []string{"missing", "zero", "read-error", "invalid-prefix", "delta-disabled"} {
		t.Run(mode, func(t *testing.T) {
			store := openExtractionStore(t)
			prepareExtractionScope(t, store)
			if err := store.SetMemoryEnabled(t.Context(), "user", true); err != nil {
				t.Fatal(err)
			}
			cursor := 2
			config := DefaultConfig()
			if mode == "delta-disabled" {
				config.ExtractDelta, cursor = false, 9
			}
			if changed, err := store.SetLastExtractMessageIndex(t.Context(), "frame", cursor, nil); err != nil || !changed {
				t.Fatalf("prepare scalar=%t err=%v", changed, err)
			}
			input := bindExtractionSource(t, store, RunInput{
				Scope: ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"},
				Messages: []Message{{Role: "user", Content: []Block{{Type: BlockText, Text: "retain explicit reporting conventions"}}},
					{Role: "assistant", Content: []Block{{Type: BlockText, Text: "Recorded."}}}}, Config: config,
			})
			switch mode {
			case "zero":
				input.CoveredPrefix = func(context.Context) (int, error) { return 0, nil }
			case "read-error":
				input.CoveredPrefix = func(context.Context) (int, error) { return 0, context.Canceled }
			case "invalid-prefix":
				input.CoveredPrefix = func(context.Context) (int, error) { return 3, nil }
			case "delta-disabled":
				input.CoveredPrefix = func(context.Context) (int, error) { t.Fatal("disabled delta read coverage"); return 0, nil }
			}
			calls := 0
			runner := NewRunner(store, NewService(store, nil, nil), extractorFunc(func(_ context.Context, request ExtractRequest) (map[string]any, error) {
				calls++
				if len(request.Delta) != 2 {
					t.Errorf("unproven scalar omitted prose: delta=%d", len(request.Delta))
				}
				return map[string]any{}, nil
			}))
			result, err := runner.Run(t.Context(), input)
			if mode == "read-error" || mode == "invalid-prefix" {
				want := error(context.Canceled)
				if mode == "invalid-prefix" {
					want = workspace.ErrExtractionPlanConflict
				}
				if !errors.Is(err, want) || result.Gate != "coverage-failed" || calls != 0 {
					t.Fatalf("coverage failure became success: result=%+v calls=%d err=%v", result, calls, err)
				}
				if _, found, err := store.ReadMemoryExtractionReceipt(t.Context(), *input.Source); err != nil || found {
					t.Fatalf("failed coverage committed: found=%t err=%v", found, err)
				}
				return
			}
			if err != nil || result.Gate != "done" || calls != 1 || result.CursorBefore != cursor || result.CursorAfter != cursor {
				t.Fatalf("coverage replaced scalar CAS: result=%+v calls=%d err=%v", result, calls, err)
			}
			saved, found, err := store.ReadMemoryExtractionReceipt(t.Context(), *input.Source)
			if err != nil || !found {
				t.Fatalf("original receipt missing: found=%t err=%v", found, err)
			}
			if got, err := AdvanceExtractionCoverage(input.Messages, 0, input.Messages, input.FinalResponseText, saved); err != nil || got != 2 {
				t.Fatalf("receipt scalar was mistaken for snapshot length: covered=%d err=%v", got, err)
			}
			input.CoveredPrefix = func(context.Context) (int, error) { t.Fatal("immutable replay read coverage"); return 0, nil }
			again, err := runner.Run(t.Context(), input)
			if err != nil || !again.Replayed || calls != 1 {
				t.Fatalf("original receipt was re-extracted: result=%+v calls=%d err=%v", again, calls, err)
			}
		})
	}
}

func TestMemoryExtractionCoveredPrefixRequiresBoundSourceAndLegacyChecker(t *testing.T) {
	for _, mode := range []string{"missing-source", "foreign-scope", "missing-guard", "missing-checker", "guard-did-not-run", "valid-binding"} {
		t.Run(mode, func(t *testing.T) {
			store := openExtractionStore(t)
			prepareExtractionScope(t, store)
			if err := store.SetMemoryEnabled(t.Context(), "user", true); err != nil {
				t.Fatal(err)
			}
			input := bindExtractionSource(t, store, RunInput{
				Scope:    ApplyScope{UserID: "user", ProjectID: "project", SourceFrameID: "frame"},
				Messages: coverageMessages(2), Config: DefaultConfig(),
			})
			original := *input.Source
			input.CoveredPrefix = func(context.Context) (int, error) { return len(input.Messages), nil }
			want := error(workspace.ErrExtractionPlanConflict)
			switch mode {
			case "missing-source":
				input.Source = nil
				want = workspace.ErrExtractionSourceChanged
			case "foreign-scope":
				changed := *input.Source
				changed.OwnerUserID = "other-user"
				input.Source = &changed
				want = workspace.ErrExtractionSourceChanged
			case "missing-guard":
				input.CommitGuard = nil
			case "missing-checker":
				input.CommitGuard = func(_ context.Context, next func(workspace.LegacyExtractionOrderCheck) error) error { return next(nil) }
			case "guard-did-not-run":
				input.CommitGuard = func(context.Context, func(workspace.LegacyExtractionOrderCheck) error) error { return nil }
			}
			calls := 0
			runner := NewRunner(store, NewService(store, nil, nil), extractorFunc(func(context.Context, ExtractRequest) (map[string]any, error) {
				calls++
				return map[string]any{}, nil
			}))
			result, err := runner.Run(t.Context(), input)
			if mode == "valid-binding" {
				if err != nil || result.Gate != "no-delta" {
					t.Fatalf("valid host binding was rejected: %+v %v", result, err)
				}
			} else if !errors.Is(err, want) || result.Gate == "no-delta" {
				t.Fatalf("invalid host binding acknowledged: %+v %v", result, err)
			}
			if calls != 0 || result.CursorAdvanced {
				t.Fatalf("coverage validation performed extraction/effects: %+v calls=%d", result, calls)
			}
			if _, found, err := store.ReadMemoryExtractionReceipt(t.Context(), original); err != nil || found {
				t.Fatalf("coverage-only check committed: %t %v", found, err)
			}
		})
	}
}

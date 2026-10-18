package workspace

import (
	"context"
	"reflect"
	"testing"
)

func TestManualKernelGenericCloneExcludesAuthorityBeforeCoordinates(t *testing.T) {
	for _, mode := range []string{FrameBranchAside, FrameBranchEdit} {
		t.Run(mode, func(t *testing.T) {
			store, access, input := newManualKernelStoreFixture(t)
			original, _, err := store.AdmitManualKernelExecution(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			last, err := store.AppendFrameEvent(FrameEventInput{
				ID: "after-manual-control", FrameID: access.Frame.ID, Type: "user_message",
				Payload: map[string]any{"text": "ordinary last input", "role": "user", "metadata": map[string]any{"preserve": true}},
			})
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.ListFrameEvents(access.Frame.ID, 0, 100)
			if err != nil {
				t.Fatal(err)
			}
			historyCount := 0
			for _, event := range before {
				if event.Type != ManualKernelExecutionEventType {
					historyCount++
				}
			}
			branch, err := store.CreateFrameBranch(CreateFrameBranchInput{
				ID: "manual-clone-" + mode, RootFrameID: access.Frame.RootFrameID, SourceFrameID: access.Frame.ID,
				Mode: mode, MessageIndex: historyCount - 1, EditedContent: "edited ordinary input", Request: "new aside input",
			})
			if err != nil {
				t.Fatal(err)
			}
			wantKeep := historyCount
			if mode == FrameBranchEdit {
				wantKeep--
			}
			if branch.RuntimeKeepEventCount != wantKeep {
				t.Fatalf("control event changed clone runtime coordinate: got=%d want=%d", branch.RuntimeKeepEventCount, wantKeep)
			}
			foundLast := false
			for _, event := range branch.Events {
				if event.Type == ManualKernelExecutionEventType {
					t.Fatalf("clone copied manual execution authority: %#v", event)
				}
				if event.Payload["sourceEventId"] == last.ID {
					foundLast = true
					want := "ordinary last input"
					if mode == FrameBranchEdit {
						want = "edited ordinary input"
					}
					if event.Payload["text"] != want || event.Payload["sourceSequence"] != float64(last.Sequence) {
						// The returned in-memory projection may retain int64; the
						// persisted source coordinate must still be the original one.
						if event.Payload["text"] != want || event.Payload["sourceSequence"] != last.Sequence {
							t.Fatalf("ordinary clone target changed: %#v", event)
						}
					}
				}
			}
			if !foundLast {
				t.Fatal("source selection removed or replaced the ordinary target")
			}
			after, err := store.ListFrameEvents(access.Frame.ID, 0, 100)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("clone modified original event history: err=%v", err)
			}
			receipt, found, err := store.GetManualKernelExecution(context.Background(), access, original.ExecutionID)
			if err != nil || !found || !reflect.DeepEqual(receipt, original) {
				t.Fatalf("clone modified original authority: %#v err=%v", receipt, err)
			}
		})
	}
}

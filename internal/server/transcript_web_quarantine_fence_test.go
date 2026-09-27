package server

import (
	"context"
	"errors"
	"reflect"
	"testing"

	transcriptstore "synon-go/internal/persistence/transcript"
)

func TestTranscriptWebQuarantineRejectsAdvancedSourceFence(t *testing.T) {
	for _, advance := range []string{"none", "publication", "source_revision", "branch_generation", "same_source_rebuilt"} {
		t.Run(advance, func(t *testing.T) {
			ctx := context.Background()
			store, repository, db := newTranscriptWebFixture(t)
			seedTranscriptWebFrame(t, store, "owner-fence", "project-fence", "frame-fence")
			stream, err := repository.CreateStream(ctx, transcriptstore.CreateStreamInput{
				UID: "stream-fence", OwnerID: "owner-fence", ExternalID: "frame-fence",
				SessionID: "frame-fence", Kind: transcriptstore.StreamKindFrameRef,
				ProjectID: "project-fence", RootFrameID: "frame-fence", FrameID: "frame-fence", Epoch: 1,
			})
			if err != nil {
				t.Fatal(err)
			}
			branch, err := repository.GetBranchState(ctx, stream.UID, stream.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			appendUser := func(suffix string) transcriptstore.Event {
				t.Helper()
				event, _, created, err := repository.AppendFrameUserEvent(ctx, transcriptstore.AppendFrameUserEventInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID, ClientMessageID: "fence-client-" + suffix,
					FrameEventID: "fence-event-" + suffix, MessageUUID: "fence-message-" + suffix,
					Text: "message " + suffix, Destinations: []string{transcriptWebDestination},
				})
				if err != nil || !created {
					t.Fatalf("append created=%t err=%v", created, err)
				}
				return event
			}
			first := appendUser("first")
			readModel := transcriptstore.NewWebReadModelRepository(db, db)
			server := &Server{workspaceStore: store, transcriptStore: repository, transcriptWebReadModel: readModel}
			if err := server.runTranscriptWebReadModelCycle(ctx); err != nil {
				t.Fatal(err)
			}
			ready := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
			if ready.StateStatus != "ready" || ready.VisibleMessageCount != 1 {
				t.Fatalf("initial fence=%#v", ready)
			}
			appendUser("second")
			work, found, err := readModel.GetTranscriptWebProjectionWork(ctx, stream.OwnerID, stream.UID, branch.ActiveBranchID)
			if err != nil || !found {
				t.Fatalf("pending work found=%t err=%v", found, err)
			}

			// A rebuild can fail after selecting its source coordinates while a
			// different writer advances that source before quarantine is persisted.
			switch advance {
			case "same_source_rebuilt":
				if err := server.rebuildTranscriptWebReadModel(ctx, work); err != nil {
					t.Fatal(err)
				}
			case "publication":
				appendUser("third")
			case "source_revision":
				// Fault injection stays inside this temporary database; the real
				// invalidation trigger must leave newer dirty work pending.
				if _, err := db.ExecContext(ctx, `UPDATE transcript_events SET payload_json=?
					WHERE stream_uid=? AND event_id=?`,
					[]byte(`{"messageUuid":"fence-message-first","text":"revised source","role":"user","messageOrigin":"task_intent"}`),
					stream.UID, first.EventID); err != nil {
					t.Fatal(err)
				}
			case "branch_generation":
				fork, err := repository.ForkFrameUserMessageBranch(ctx, transcriptstore.ForkFrameUserMessageBranchInput{
					StreamUID: stream.UID, OwnerID: stream.OwnerID,
					SourceBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: branch.ActiveBranchID,
					ExpectedGeneration: branch.Generation, ClientMutationID: "fence-fork",
					SourceClientMessageID: first.ClientMessageID, SourceMessageIndex: 0,
					ReplacementText: "replacement", Destinations: []string{transcriptWebDestination},
				})
				if err != nil || !fork.Created {
					t.Fatalf("fork created=%t err=%v", fork.Created, err)
				}
				continued, err := repository.AppendFrameUserEventToBranch(ctx, transcriptstore.AppendFrameUserEventToBranchInput{
					AppendFrameUserEventInput: transcriptstore.AppendFrameUserEventInput{
						StreamUID: stream.UID, OwnerID: stream.OwnerID,
						ClientMessageID: "branch-continue:fence-return", MessageUUID: "branch-continue:fence-return",
						FrameEventID: "frame-branch-continue:" + stream.FrameID + ":" + branch.ActiveBranchID + ":fence-return",
						Text:         "continue original branch", Destinations: []string{transcriptWebDestination},
					},
					TargetBranchID: branch.ActiveBranchID, ExpectedActiveBranchID: fork.BranchID,
					ExpectedGeneration: fork.Generation, ClientMutationID: "fence-return",
				})
				if err != nil || !continued.Created || !continued.Switched {
					t.Fatalf("continue created=%t switched=%t err=%v", continued.Created, continued.Switched, err)
				}
			}
			before := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
			if advance == "publication" && before.ThroughPublicationSequence <= work.ThroughPublicationSequence ||
				advance == "source_revision" && before.SourceRevision <= work.SourceRevision ||
				advance == "branch_generation" && before.BranchGeneration <= work.BranchGeneration {
				t.Fatalf("source did not advance: work=%#v fence=%#v", work, before)
			}
			if advance == "same_source_rebuilt" && !transcriptWebFenceReady(before) {
				t.Fatalf("concurrent rebuild must publish current ready state, status=%s", before.StateStatus)
			}
			dirtyCount := func() int {
				t.Helper()
				var count int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcript_web_projection_dirty
					WHERE stream_uid=? AND branch_id=?`, stream.UID, branch.ActiveBranchID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				return count
			}
			dirtyBefore := dirtyCount()
			if advance == "source_revision" && dirtyBefore != 1 {
				t.Fatalf("source revision must enqueue dirty work, count=%d", dirtyBefore)
			}
			quarantineErr := server.quarantineTranscriptWebReadModel(ctx, work, transcriptstore.ErrEventConflict)
			after := requireTranscriptWebReadModelFence(t, readModel, stream, branch.ActiveBranchID)
			if advance == "none" {
				if quarantineErr != nil || after.StateStatus != "quarantined" || after.VisibleMessageCount != 1 ||
					after.StateThroughPublicationSequence != work.ThroughPublicationSequence ||
					after.StateSourceRevision != work.SourceRevision {
					t.Fatalf("current conflicting work must retain quarantine: err=%v fence=%#v", quarantineErr, after)
				}
				return
			}
			if !errors.Is(quarantineErr, transcriptstore.ErrBranchStateStale) &&
				!errors.Is(quarantineErr, transcriptstore.ErrTranscriptWebProjectionStale) {
				t.Errorf("obsolete failure must report stale coordinates, got %v", quarantineErr)
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("obsolete failure changed projection: status=%s->%s revision=%d->%d generation=%d->%d through=%d->%d source=%d->%d visible=%d->%d",
					before.StateStatus, after.StateStatus, before.StateProjectionRevision, after.StateProjectionRevision,
					before.StateBranchGeneration, after.StateBranchGeneration,
					before.StateThroughPublicationSequence, after.StateThroughPublicationSequence,
					before.StateSourceRevision, after.StateSourceRevision, before.VisibleMessageCount, after.VisibleMessageCount)
			}
			if dirtyAfter := dirtyCount(); dirtyAfter != dirtyBefore {
				t.Errorf("obsolete failure consumed newer dirty work: before=%d after=%d", dirtyBefore, dirtyAfter)
			}
		})
	}
}

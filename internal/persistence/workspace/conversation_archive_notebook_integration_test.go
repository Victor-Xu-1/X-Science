package workspace

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	transcriptstore "x-science/internal/persistence/transcript"
)

func archiveNotebookFrame(t *testing.T, store *Store, root Frame, descendant bool) Frame {
	t.Helper()
	if !descendant {
		return root
	}
	child, err := store.CreateFrame(CreateFrameInput{
		ID: "archive-notebook-child", ProjectID: root.ProjectID, ParentFrameID: root.ID,
		AgentName: "agent", Status: FrameStatusCompleted, ConversationType: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	return child
}

func archiveNotebookInput(t *testing.T, store *Store, frame Frame) (KernelFrameAccess, AdmitManualKernelExecutionInput) {
	t.Helper()
	access, found, err := store.GetKernelFrameAccess(frame.ID)
	if err != nil || !found {
		t.Fatalf("manual access found=%t err=%v", found, err)
	}
	request := ManualKernelRequest{
		RequestID: uuid.NewString(), Source: "print('archive lifecycle')", Language: "python", Environment: "python",
		KernelID: "archive-notebook-kernel", ExpectedKernelGeneration: 7, TimeoutMillis: 1000,
	}
	binding := manualKernelAccessBinding(access)
	binding.ExecutionID = ManualKernelExecutionID(access.UserID, access.Frame.IncarnationID, request.RequestID)
	binding.KernelID, binding.KernelGeneration = request.KernelID, request.ExpectedKernelGeneration
	binding.KernelKind, binding.ServerBootID = "analysis", "archive-notebook-boot"
	return access, AdmitManualKernelExecutionInput{Binding: binding, Request: request}
}

// These are execution-side facts only. Presentation archive/restore is allowed
// to change its own metadata, but must not manufacture execution or dispatch.
func archiveNotebookExecutionCounts(t *testing.T, store *Store) [4]int {
	t.Helper()
	queries := []string{
		"SELECT COUNT(*) FROM frame_events WHERE event_type='kernel_manual_execution'",
		"SELECT COUNT(*) FROM execution_log",
		"SELECT COUNT(*) FROM workspace_outbox WHERE event_type='execution_cell_update'",
		"SELECT COUNT(*) FROM transcript_runner_attempts",
	}
	var counts [4]int
	for index, query := range queries {
		if err := store.db.QueryRow(query).Scan(&counts[index]); err != nil {
			t.Fatal(err)
		}
	}
	return counts
}

func TestConversationArchiveNotebookRejectsNewAdmissionOnRootAndDescendant(t *testing.T) {
	for _, descendant := range []bool{false, true} {
		t.Run(map[bool]string{false: "root", true: "descendant"}[descendant], func(t *testing.T) {
			store, _ := newConversationArchiveFixture(t)
			root := conversationArchiveFixtureFrame(t, store, "archive-notebook-root", 1, false, false)
			frame := archiveNotebookFrame(t, store, root, descendant)
			// Capture real access while active, then archive through the command.
			// The admission transaction must reject this now-stale availability.
			_, input := archiveNotebookInput(t, store, frame)
			archived, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(root, true, 7))
			if err != nil || !archived.Changed {
				t.Fatalf("archive changed=%t err=%v", archived.Changed, err)
			}
			before := archiveNotebookExecutionCounts(t, store)
			_, created, err := store.AdmitManualKernelExecution(context.Background(), input)
			if !errors.Is(err, transcriptstore.ErrConversationArchived) || created {
				t.Errorf("archived manual admission created=%t err=%v", created, err)
			}
			if after := archiveNotebookExecutionCounts(t, store); after != before {
				t.Errorf("rejected admission changed execution facts: before=%v after=%v", before, after)
			}
		})
	}
}

func TestConversationArchiveNotebookNonterminalFactsRemainBusyDespiteCompletedFrame(t *testing.T) {
	for _, descendant := range []bool{false, true} {
		for _, state := range []string{"queued", "started", "outcome_unknown"} {
			t.Run(map[bool]string{false: "root", true: "descendant"}[descendant]+"/"+state, func(t *testing.T) {
				store, _ := newConversationArchiveFixture(t)
				root := conversationArchiveFixtureFrame(t, store, "archive-notebook-busy-root", 1, false, false)
				frame := archiveNotebookFrame(t, store, root, descendant)
				access, input := archiveNotebookInput(t, store, frame)
				record, created, err := store.AdmitManualKernelExecution(context.Background(), input)
				if err != nil || !created {
					t.Fatalf("admit created=%t err=%v", created, err)
				}
				if state != "queued" {
					record, err = store.MarkManualKernelExecutionStarted(context.Background(), record, store.now().UTC())
					if err != nil {
						t.Fatal(err)
					}
				}
				if state == "outcome_unknown" {
					record, err = store.MarkManualKernelExecutionUnknown(context.Background(), record, "worker_outcome_unconfirmed")
					if err != nil {
						t.Fatal(err)
					}
				}
				if record.State != state || record.FinishedAt != nil {
					t.Fatalf("expected genuine nonterminal receipt: %#v", record)
				}
				// Manual admission does not own the old frame display status.
				// Never rewrite that status to make the archive guard pass.
				current, found, err := store.GetFrame(frame.ID)
				if err != nil || !found || current.Status != FrameStatusCompleted {
					t.Fatalf("completed display fixture changed: frame=%#v found=%t err=%v", current, found, err)
				}
				metadata, _, err := store.GetFrameRuntimeMetadata(root.ID)
				if err != nil {
					t.Fatal(err)
				}
				before := archiveNotebookExecutionCounts(t, store)
				result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(root, true, 7))
				if !errors.Is(err, ErrConversationArchiveBusy) || result.Changed {
					t.Errorf("nonterminal manual fact hidden: state=%s changed=%t err=%v", state, result.Changed, err)
				}
				after, _, err := store.GetFrameRuntimeMetadata(root.ID)
				if err != nil || !reflect.DeepEqual(metadata, after) {
					t.Errorf("busy archive changed presentation: before=%#v after=%#v err=%v", metadata, after, err)
				}
				if counts := archiveNotebookExecutionCounts(t, store); counts != before {
					t.Errorf("busy archive changed execution facts: before=%v after=%v", before, counts)
				}
				stored, found, err := store.GetManualKernelExecution(context.Background(), access, record.ExecutionID)
				if err != nil || !found || !reflect.DeepEqual(record, stored) {
					t.Errorf("archive altered pending receipt: before=%#v after=%#v found=%t err=%v", record, stored, found, err)
				}
			})
		}
	}
}

func TestConversationArchiveNotebookTerminalAllowsArchiveAndRestoreWithoutRunning(t *testing.T) {
	for _, descendant := range []bool{false, true} {
		for _, disposition := range []string{"completed", "cancelled_before_start"} {
			t.Run(map[bool]string{false: "root", true: "descendant"}[descendant]+"/"+disposition, func(t *testing.T) {
				store, repo := newConversationArchiveFixture(t)
				root := conversationArchiveFixtureFrame(t, store, "archive-notebook-terminal-root", 1, false, false)
				frame := archiveNotebookFrame(t, store, root, descendant)
				access, input := archiveNotebookInput(t, store, frame)
				record, created, err := store.AdmitManualKernelExecution(context.Background(), input)
				if err != nil || !created {
					t.Fatalf("admit created=%t err=%v", created, err)
				}
				completion := ManualKernelCompletionInput{Disposition: disposition, FinishedAt: store.now().UTC().Add(time.Millisecond)}
				if disposition == "completed" {
					record, err = store.MarkManualKernelExecutionStarted(context.Background(), record, store.now().UTC())
					if err != nil {
						t.Fatal(err)
					}
					completion.Log = &ExecutionLogRecord{
						ID: record.ExecutionID, FrameID: record.FrameID, CellIndex: 1, KernelID: record.KernelID,
						KernelKind: record.KernelKind, CondaEnv: record.Request.Environment, Language: record.Request.Language,
						Source: record.Request.Source, Stdout: "archive lifecycle\n", ExitStatus: "ok", Origin: "user", ExecutedAt: *record.StartedAt,
					}
				} else {
					record, err = store.RequestManualKernelExecutionCancellation(context.Background(), record)
					if err != nil {
						t.Fatal(err)
					}
				}
				completion.Expected = record
				terminal, err := store.CompleteManualKernelExecution(context.Background(), completion)
				if err != nil || terminal.State != "terminal" || terminal.FinishedAt == nil {
					t.Fatalf("completion=%#v err=%v", terminal, err)
				}
				before := archiveNotebookExecutionCounts(t, store)
				stream, err := repo.GetStream(context.Background(), "frame:"+root.ID, "archive-owner")
				if err != nil {
					t.Fatal(err)
				}
				if result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(root, true, 7)); err != nil || !result.Changed {
					t.Fatalf("terminal manual blocked archive: changed=%t err=%v", result.Changed, err)
				}
				// Archive prevents new work, not recovery or inspection of an
				// already accepted immutable request and its terminal evidence.
				archivedReceipt, found, err := store.GetManualKernelExecution(context.Background(), access, terminal.ExecutionID)
				if err != nil || !found || !reflect.DeepEqual(terminal, archivedReceipt) {
					t.Fatalf("archive hid terminal evidence: receipt=%#v found=%t err=%v", archivedReceipt, found, err)
				}
				replayed, created, err := store.AdmitManualKernelExecution(context.Background(), input)
				if err != nil || created || !reflect.DeepEqual(terminal, replayed) {
					t.Fatalf("archive replay created new work or lost its receipt: receipt=%#v created=%t err=%v", replayed, created, err)
				}
				if result, err := store.UpdateConversationArchive(context.Background(), conversationArchiveUpdate(root, false, 8)); err != nil || !result.Changed {
					t.Fatalf("restore changed=%t err=%v", result.Changed, err)
				}
				if claimed, err := repo.ClaimNextRunner(context.Background(), transcriptstore.ClaimNextRunnerInput{
					RunnerID: "archive-notebook-restore-observer", TTL: time.Minute,
				}); err != nil || claimed.Claimed {
					t.Fatalf("restore dispatched execution: claimed=%#v err=%v", claimed, err)
				}
				afterStream, err := repo.GetStream(context.Background(), stream.UID, stream.OwnerID)
				if err != nil || !reflect.DeepEqual(stream, afterStream) {
					t.Errorf("restore changed runner input: before=%#v after=%#v err=%v", stream, afterStream, err)
				}
				stored, found, err := store.GetManualKernelExecution(context.Background(), access, terminal.ExecutionID)
				if err != nil || !found || !reflect.DeepEqual(terminal, stored) {
					t.Errorf("restore changed terminal receipt: before=%#v after=%#v found=%t err=%v", terminal, stored, found, err)
				}
				if counts := archiveNotebookExecutionCounts(t, store); counts != before {
					t.Errorf("archive/restore manufactured execution facts: before=%v after=%v", before, counts)
				}
			})
		}
	}
}

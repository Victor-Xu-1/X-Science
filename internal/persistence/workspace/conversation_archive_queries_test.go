package workspace

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	transcriptstore "x-science/internal/persistence/transcript"
)

func newConversationArchiveFixture(t *testing.T) (*Store, *transcriptstore.Repository) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CreateProject(CreateProjectInput{ID: "archive-project", UserID: "archive-owner", Name: "Archive project"}); err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return store, repo
}

func conversationArchiveFixtureFrame(t *testing.T, store *Store, id string, minute int, pinned, archived bool) Frame {
	t.Helper()
	now := time.Date(2026, time.October, 18, 0, minute, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	frame, err := store.CreateFrame(CreateFrameInput{
		ID: id, ProjectID: "archive-project", AgentName: "agent", Status: "completed", ConversationType: "agent", Name: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := store.TranscriptRepository(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateStream(context.Background(), transcriptstore.CreateStreamInput{
		UID: "frame:" + frame.ID, OwnerID: "archive-owner", ExternalID: frame.ID, SessionID: frame.ID,
		Kind: transcriptstore.StreamKindFrameRef, ProjectID: frame.ProjectID, RootFrameID: frame.ID, FrameID: frame.ID, Epoch: 1,
	}); err != nil {
		t.Fatal(err)
	}
	extra := map[string]any{"pinned": pinned, "_presentation_revision": int64(7)}
	if pinned {
		extra["pinned_at"] = now.Add(-time.Minute).UnixMilli()
		extra["sortOrder"] = float64(1000)
	}
	if archived {
		extra["archived_at"] = now.UnixMilli()
	}
	// Seed a durable presentation snapshot, not an archive command or a fake
	// runner lease. Each frame remains terminal and has never received input.
	if _, err := store.SetFrameRuntimeMetadata(frame.ID, FrameRuntimeMetadata{
		FrameID: frame.ID, ContextData: map[string]any{"web_extra": extra, "runtime_owned": "retain"},
	}); err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestConversationArchiveQueriesFilterBeforeLimitAndKeepSectionsIndependent(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	conversationArchiveFixtureFrame(t, store, "active-recent", 1, false, false)
	conversationArchiveFixtureFrame(t, store, "active-pinned", 2, true, false)
	conversationArchiveFixtureFrame(t, store, "archived-recent", 3, false, true)
	conversationArchiveFixtureFrame(t, store, "archived-pinned", 4, true, true)
	for _, test := range []struct {
		section string
		want    string
	}{
		{"", "active-pinned"}, {"recent", "active-recent"}, {"pinned", "active-pinned"}, {"archived", "archived-pinned"},
	} {
		t.Run("section-"+test.section, func(t *testing.T) {
			rows, err := store.ListVisibleRootCompatibilityFrameSummaries(context.Background(), "archive-owner", 1, nil, test.section)
			if err != nil || len(rows) != 1 || rows[0].Frame.ID != test.want {
				t.Fatalf("section=%q limit=1 rows=%#v err=%v", test.section, rows, err)
			}
		})
	}
	rows, err := store.ListVisibleRootCompatibilityFrameSummaries(context.Background(), "archive-owner", 1, nil, "archived")
	if err != nil || len(rows) != 1 {
		t.Fatalf("archived first page=%#v err=%v", rows, err)
	}
	cursor := CompatibilityFrameSummaryCursor{UpdatedAt: rows[0].Frame.UpdatedAt, FrameID: rows[0].Frame.ID}
	next, err := store.ListVisibleRootCompatibilityFrameSummaries(context.Background(), "archive-owner", 1, &cursor, "archived")
	if err != nil || len(next) != 1 || next[0].Frame.ID != "archived-recent" {
		t.Fatalf("archived second page=%#v err=%v", next, err)
	}
	foreign, err := store.ListVisibleRootCompatibilityFrameSummaries(context.Background(), "other-owner", 10, nil, "archived")
	if err != nil || len(foreign) != 0 {
		t.Fatalf("archived owner filter rows=%#v err=%v", foreign, err)
	}
}

func TestConversationArchiveProjectQueriesExcludeArchivedCountsAndLatest(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	active := conversationArchiveFixtureFrame(t, store, "active-conversation", 1, false, false)
	conversationArchiveFixtureFrame(t, store, "newer-archived", 2, true, true)
	rows, err := store.ListVisibleCompatibilityFrames("archive-owner", active.ProjectID, true, 1)
	if err != nil || len(rows) != 1 || rows[0].ID != active.ID {
		t.Errorf("project limit=1 rows=%#v err=%v", rows, err)
	}
	projects, err := store.ListCompatibilityProjects("archive-owner", 10, 0)
	if err != nil || len(projects) != 1 {
		t.Fatalf("projects=%#v err=%v", projects, err)
	}
	if projects[0].ConversationCount != 1 || projects[0].LatestConversationID != active.ID {
		t.Fatalf("archived conversation affected project projection: %#v", projects[0])
	}
}

func TestConversationArchiveProjectionPinAndCloneBoundaries(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	archived := conversationArchiveFixtureFrame(t, store, "archived-pinned", 1, true, true)
	metadata, _, err := store.GetFrameRuntimeMetadata(archived.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := ConversationPresentation(metadata.ContextData)
	if err != nil || view["archived_at"] != metadata.ContextData["web_extra"].(map[string]any)["archived_at"] {
		t.Errorf("archive missing from canonical projection: %#v err=%v", view, err)
	}
	revision := int64(7)
	if changed, err := store.UpdateConversationPresentation(context.Background(), ConversationPresentationUpdate{
		OwnerID: "archive-owner", FrameID: archived.ID, IncarnationID: archived.IncarnationID,
		ExpectedRevision: &revision, Extra: map[string]any{"pinned": false},
	}); err == nil || changed {
		t.Errorf("pin mutation accepted on archived conversation: changed=%t err=%v", changed, err)
	}
	stored, _, err := store.GetFrameRuntimeMetadata(archived.ID)
	if err != nil || !reflect.DeepEqual(stored.ContextData, metadata.ContextData) {
		t.Errorf("rejected archived pin changed presentation: %#v err=%v", stored, err)
	}
	cloned := cloneConversationPresentationContext(metadata.ContextData)
	extra := cloned["web_extra"].(map[string]any)
	for _, key := range []string{"archived_at", "pinned", "pinned_at", "sortOrder", presentationRevisionKey} {
		if _, present := extra[key]; present {
			t.Errorf("new conversation inherited %q: %#v", key, extra)
		}
	}
	if cloned["runtime_owned"] != "retain" || metadata.ContextData["web_extra"].(map[string]any)["archived_at"] == nil {
		t.Fatal("clone erased source or unrelated metadata")
	}
}

func TestConversationArchiveReorderBatchRollsBackAtArchivedMember(t *testing.T) {
	store, _ := newConversationArchiveFixture(t)
	active := conversationArchiveFixtureFrame(t, store, "active-pinned", 1, true, false)
	archived := conversationArchiveFixtureFrame(t, store, "archived-pinned", 2, true, true)
	before, _, err := store.GetFrameRuntimeMetadata(active.ID)
	if err != nil {
		t.Fatal(err)
	}
	items := []ConversationPinnedOrderItem{
		{ID: active.ID, IncarnationID: active.IncarnationID, ExpectedRevision: 7, SortOrder: 2000},
		{ID: archived.ID, IncarnationID: archived.IncarnationID, ExpectedRevision: 7, SortOrder: 3000},
	}
	if _, err := store.ReorderPinnedConversations(context.Background(), "archive-owner", active.ID, items); err == nil {
		t.Error("reorder accepted archived member")
	}
	after, _, err := store.GetFrameRuntimeMetadata(active.ID)
	if err != nil || !reflect.DeepEqual(before.ContextData, after.ContextData) {
		t.Fatalf("rejected reorder partially committed: before=%#v after=%#v err=%v", before, after, err)
	}
}

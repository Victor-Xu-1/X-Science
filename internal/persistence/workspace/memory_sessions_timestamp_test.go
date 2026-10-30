package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMemorySessionsReadDriverTimestampsWithoutLosingPrecision(t *testing.T) {
	t.Run("ordinary_create_memory", func(t *testing.T) {
		store := newMemorySessionsTimestampStore(t)
		written := time.Date(2026, 1, 2, 12, 34, 56, 987654321, time.FixedZone("test-offset", 5*3600+45*60))
		memory := createMemorySessionTimestampFact(t, store, "ordinary", "owner-a", "frame-a", written)
		var column, aggregate any
		if err := store.db.QueryRow(`SELECT updated_at FROM memories WHERE id=?`, memory.ID).Scan(&column); err != nil {
			t.Fatal(err)
		}
		if err := store.db.QueryRow(`SELECT MAX(updated_at) FROM memories WHERE id=?`, memory.ID).Scan(&aggregate); err != nil {
			t.Fatal(err)
		}
		typed, typedOK := column.(time.Time)
		text, textOK := aggregate.(string)
		if !typedOK || !typed.Equal(written) || !textOK || text != written.UTC().String() {
			t.Fatalf("locked SQLite driver representation changed: column=%T aggregate=%T", column, aggregate)
		}
		sessions, err := store.ListMemorySessions(context.Background(), "owner-a", "")
		if err != nil {
			t.Fatalf("ordinary persisted memory must remain readable: %v", err)
		}
		if len(sessions) != 1 || sessions[0].FrameID != "frame-a" || sessions[0].RowCount != 1 {
			t.Fatalf("session summaries=%#v", sessions)
		}
		assertMemorySessionNewest(t, sessions[0], written.UTC())
	})
	t.Run("counts_order_active_owner_and_project", func(t *testing.T) {
		store := newMemorySessionsTimestampStore(t)
		base := time.Date(2026, 1, 2, 3, 4, 5, 123456000, time.UTC)
		latestA := base.Add(987 * time.Nanosecond)
		createMemorySessionTimestampFact(t, store, "a-old", "owner-a", "frame-a", base)
		createMemorySessionTimestampFact(t, store, "a-new", "owner-a", "frame-a", latestA)
		createMemorySessionTimestampFact(t, store, "a-new-tie", "owner-a", "frame-a", latestA)
		createMemorySessionTimestampFact(t, store, "b-tie", "owner-a", "frame-b", latestA)
		latestOther := base.Add(time.Second + 321*time.Nanosecond)
		createMemorySessionTimestampFact(t, store, "other-project", "owner-a", "frame-other", latestOther)
		createMemorySessionTimestampFact(t, store, "foreign", "owner-b", "frame-foreign", base.Add(time.Hour))
		createMemorySessionTimestampFact(t, store, "superseded", "owner-a", "frame-a", base.Add(2*time.Hour))
		createMemorySessionTimestampFact(t, store, "inactive-only", "owner-a", "frame-inactive", base.Add(2*time.Hour))
		for _, id := range []string{"superseded", "inactive-only"} {
			if err := store.SupersedeMemory(id, "a-new"); err != nil {
				t.Fatal(err)
			}
		}
		createMemorySessionTimestampFact(t, store, "profile", "owner-a", "", base.Add(3*time.Hour))
		sessions, err := store.ListMemorySessions(context.Background(), "owner-a", "")
		if err != nil {
			t.Fatalf("multi-row summaries: %v", err)
		}
		if len(sessions) != 3 || sessions[0].FrameID != "frame-other" || sessions[1].FrameID != "frame-a" ||
			sessions[2].FrameID != "frame-b" || sessions[0].RowCount != 1 || sessions[1].RowCount != 3 || sessions[2].RowCount != 1 {
			t.Fatalf("newest/count/tie/active/owner order=%#v", sessions)
		}
		assertMemorySessionNewest(t, sessions[0], latestOther)
		assertMemorySessionNewest(t, sessions[1], latestA)
		assertMemorySessionNewest(t, sessions[2], latestA)
		project, err := store.ListMemorySessions(context.Background(), " owner-a ", " project-a ")
		if err != nil || len(project) != 2 || project[0].FrameID != "frame-a" || project[1].FrameID != "frame-b" ||
			project[0].Label != "Alpha" || project[1].Label != "Beta" {
			t.Fatalf("project/label/trim contract=%#v err=%v", project, err)
		}
		foreign, err := store.ListMemorySessions(context.Background(), "owner-b", "project-a")
		if err != nil || len(foreign) != 0 {
			t.Fatalf("foreign project leaked=%#v err=%v", foreign, err)
		}
		foreign, err = store.ListMemorySessions(context.Background(), "owner-b", "")
		if err != nil || len(foreign) != 1 || foreign[0].FrameID != "frame-foreign" {
			t.Fatalf("other owner's own summaries=%#v err=%v", foreign, err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		store := newMemorySessionsTimestampStore(t)
		sessions, err := store.ListMemorySessions(context.Background(), "owner-a", "project-a")
		if err != nil || sessions == nil || len(sessions) != 0 {
			t.Fatalf("empty summaries=%#v err=%v", sessions, err)
		}
	})
	t.Run("invalid_timestamp_remains_error", func(t *testing.T) {
		store := newMemorySessionsTimestampStore(t)
		memory := createMemorySessionTimestampFact(t, store, "invalid", "owner-a", "frame-a", time.Now())
		// Simulate corrupt historical storage only in this disposable Store.
		if _, err := store.db.Exec(`UPDATE memories SET updated_at=? WHERE id=?`, "not-a-timestamp", memory.ID); err != nil {
			t.Fatal(err)
		}
		sessions, err := store.ListMemorySessions(context.Background(), "owner-a", "")
		if err == nil || !strings.Contains(err.Error(), "scan memory session") || sessions != nil {
			t.Fatalf("invalid stored time became a successful/partial result: sessions=%#v err=%v", sessions, err)
		}
	})
}

func newMemorySessionsTimestampStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, input := range []CreateProjectInput{
		{ID: "project-a", UserID: "owner-a", Name: "A"},
		{ID: "project-other", UserID: "owner-a", Name: "Other"},
		{ID: "project-foreign", UserID: "owner-b", Name: "Foreign"},
	} {
		if _, err := store.CreateProject(input); err != nil {
			t.Fatal(err)
		}
	}
	for _, input := range []CreateFrameInput{
		{ID: "frame-a", ProjectID: "project-a", Name: "Alpha"},
		{ID: "frame-b", ProjectID: "project-a", Name: "Beta"},
		{ID: "frame-inactive", ProjectID: "project-a", Name: "Inactive"},
		{ID: "frame-other", ProjectID: "project-other", Name: "Other"},
		{ID: "frame-foreign", ProjectID: "project-foreign", Name: "Foreign"},
	} {
		input.AgentName, input.Status, input.ConversationType = "GENERAL", FrameStatusProcessing, "agent"
		if _, err := store.CreateFrame(input); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func createMemorySessionTimestampFact(t *testing.T, store *Store, id, owner, frame string, at time.Time) Memory {
	t.Helper()
	store.now = func() time.Time { return at }
	memory, err := store.CreateMemory(CreateMemoryInput{ID: id, UserID: owner, SubjectFrameID: frame,
		Body: "A synthetic session fact.", Origin: "user", Evidence: "stated"})
	if err != nil {
		t.Fatal(err)
	}
	return memory
}

func assertMemorySessionNewest(t *testing.T, session MemorySessionSummary, expected time.Time) {
	t.Helper()
	if session.NewestAt == nil || !session.NewestAt.Equal(expected) || session.NewestAt.Nanosecond() != expected.Nanosecond() || session.NewestAt.Location() != time.UTC {
		t.Fatalf("session %s newest=%v want precise UTC %v", session.FrameID, session.NewestAt, expected)
	}
}

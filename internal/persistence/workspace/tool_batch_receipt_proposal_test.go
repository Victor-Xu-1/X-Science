package workspace

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
	transcriptstore "x-science/internal/persistence/transcript"
)

func TestToolCallReceiptProposalExactIndexedCoordinates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipt.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, statement := range []string{
		`CREATE TABLE transcript_streams(stream_uid TEXT PRIMARY KEY,owner_id TEXT)`,
		`CREATE TABLE transcript_tool_call_batches(batch_id TEXT PRIMARY KEY,stream_uid TEXT,owner_user_id TEXT,source_event_id INTEGER)`,
		`CREATE TABLE transcript_tool_call_items(batch_id TEXT,stream_uid TEXT,terminal_event_id INTEGER,tool_call_id TEXT,tool_name TEXT,state TEXT)`,
		`CREATE UNIQUE INDEX transcript_tool_call_items_terminal_event_unique ON transcript_tool_call_items(stream_uid,terminal_event_id) WHERE terminal_event_id IS NOT NULL`,
		`CREATE TABLE transcript_branch_state(stream_uid TEXT PRIMARY KEY,active_branch_id TEXT,generation INTEGER)`,
		`CREATE TABLE transcript_branch_events(stream_uid TEXT,branch_id TEXT,event_id INTEGER,ordinal INTEGER,PRIMARY KEY(stream_uid,branch_id,event_id))`,
		`CREATE TABLE transcript_events(stream_uid TEXT,event_id INTEGER,publication_seq INTEGER,PRIMARY KEY(stream_uid,event_id))`,
		`INSERT INTO transcript_streams VALUES('stream','owner')`,
		`INSERT INTO transcript_branch_state VALUES('stream','branch',1)`,
		`INSERT INTO transcript_tool_call_batches VALUES('old','stream','owner',10),('new','stream','owner',30)`,
		`INSERT INTO transcript_tool_call_items VALUES('old','stream',20,'same-call','repl','completed'),('new','stream',40,'same-call','repl','completed')`,
		`INSERT INTO transcript_branch_events VALUES('stream','branch',10,1),('stream','branch',20,2),('stream','branch',30,3),('stream','branch',40,4)`,
		`INSERT INTO transcript_events VALUES('stream',10,10),('stream',20,20),('stream',30,30),('stream',40,40)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := transcriptstore.ProjectionSnapshot{StreamUID: "stream", BranchID: "branch", BranchGeneration: 1, ThroughPublicationSequence: 40}
	lookup := func(owner, call, name string, terminal int64, scope transcriptstore.ProjectionSnapshot) (int64, bool, error) {
		return lookupToolCallReceiptProposal(context.Background(), db, owner, call, name, terminal, scope)
	}
	for _, test := range []struct{ terminal, want int64 }{{20, 10}, {40, 30}} {
		if proposal, found, err := lookup("owner", "same-call", "repl", test.terminal, snapshot); err != nil || !found || proposal != test.want {
			t.Fatalf("exact receipt %d: %d %t %v", test.terminal, proposal, found, err)
		}
	}
	for _, test := range []struct {
		name, owner, call, tool string
		terminal                int64
		mutate                  func(*transcriptstore.ProjectionSnapshot)
	}{
		{"other owner", "foreign", "same-call", "repl", 20, nil},
		{"other call", "owner", "foreign", "repl", 20, nil},
		{"other tool", "owner", "same-call", "python", 20, nil},
		{"missing terminal", "owner", "same-call", "repl", 21, nil},
		{"other stream", "owner", "same-call", "repl", 20, func(s *transcriptstore.ProjectionSnapshot) { s.StreamUID = "foreign" }},
		{"other branch", "owner", "same-call", "repl", 20, func(s *transcriptstore.ProjectionSnapshot) { s.BranchID = "foreign" }},
		{"stale generation", "owner", "same-call", "repl", 20, func(s *transcriptstore.ProjectionSnapshot) { s.BranchGeneration++ }},
		{"after snapshot", "owner", "same-call", "repl", 40, func(s *transcriptstore.ProjectionSnapshot) { s.ThroughPublicationSequence = 20 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope := snapshot
			if test.mutate != nil {
				test.mutate(&scope)
			}
			if _, found, err := lookup(test.owner, test.call, test.tool, test.terminal, scope); err != nil || found {
				t.Fatalf("foreign or missing receipt selected: found=%t err=%v", found, err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := lookupToolCallReceiptProposal(cancelled, db, "owner", "same-call", "repl", 20, snapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel ignored: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if proposal, found, err := lookup("owner", "same-call", "repl", 20, snapshot); err != nil || !found || proposal != 10 {
		t.Fatalf("reopen selected latest instead of exact receipt: %d %t %v", proposal, found, err)
	}
	// A damaged/legacy database without the uniqueness invariant must fail
	// closed rather than selecting an arbitrary source proposal.
	if _, err := db.Exec(`DROP INDEX transcript_tool_call_items_terminal_event_unique`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_tool_call_items VALUES('old','stream',40,'same-call','repl','completed')`); err != nil {
		t.Fatal(err)
	}
	if _, found, err := lookup("owner", "same-call", "repl", 40, snapshot); err != nil || found {
		t.Fatalf("ambiguous terminal selected: %t %v", found, err)
	}
}

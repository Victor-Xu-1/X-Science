package transcript

import (
	"context"
	"strings"
	"testing"
)

func TestContextCompactionArchiveWorkUsesIndexedBoundaryLookup(t *testing.T) {
	_, db, _ := newTranscriptRepository(t)
	if _, err := db.Exec(`CREATE TABLE compaction_archives(id TEXT PRIMARY KEY,frame_id TEXT,source_event_id INTEGER,UNIQUE(frame_id,source_event_id));
 CREATE TABLE realtime_events(id TEXT PRIMARY KEY,user_id TEXT,project_id TEXT,frame_id TEXT,event_type TEXT);`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+pendingContextCompactionArchiveSQL, true, true, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
		if strings.Contains(detail, "SCAN event") {
			t.Fatalf("archive recovery scanned transcript bodies: %s", detail)
		}
		if strings.Contains(detail, "SEARCH event USING INDEX") && strings.Contains(detail, "stream_uid=? AND client_message_id>?") {
			indexed = true
		}
	}
	if err := rows.Err(); err != nil || !indexed {
		t.Fatalf("missing owned-stream/client-boundary range lookup: indexed=%t err=%v", indexed, err)
	}
}

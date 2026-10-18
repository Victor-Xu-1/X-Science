package workspace

import (
	"strings"
	"testing"
)

func TestManualKernelPendingWindowUsesExistingSequenceIndex(t *testing.T) {
	store, access, _ := newManualKernelPendingStoreFixture(t)
	rows, err := store.db.Query(`SELECT name FROM pragma_index_list('frame_events')`)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var sequenceIndexes []string
	for _, name := range indexes {
		columns, err := store.db.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, name)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for columns.Next() {
			var column string
			if err := columns.Scan(&column); err != nil {
				t.Fatal(err)
			}
			names = append(names, column)
		}
		if err := columns.Err(); err != nil {
			t.Fatal(err)
		}
		columns.Close()
		if strings.Join(names, ",") == "frame_id,sequence" {
			sequenceIndexes = append(sequenceIndexes, name)
		}
	}
	if len(sequenceIndexes) == 0 {
		t.Fatal("existing frame/sequence index missing")
	}
	for _, before := range []int64{130, 2} {
		plan, err := store.db.Query("EXPLAIN QUERY PLAN "+manualKernelPendingWindowSQL, access.Frame.ID, 129, before, manualKernelPendingWindow+1)
		if err != nil {
			t.Fatal(err)
		}
		usedIndex := false
		for plan.Next() {
			var id, parent, unused int
			var detail string
			if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Logf("pending window before=%d: %s", before, detail)
			if strings.Contains(detail, "SCAN frame_events") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatalf("unbounded/sorted plan: %s", detail)
			}
			for _, name := range sequenceIndexes {
				if strings.Contains(detail, name) && strings.Contains(detail, "SEARCH frame_events") && strings.Contains(detail, "frame_id=?") && strings.Contains(detail, "sequence<?") {
					usedIndex = true
				}
			}
		}
		if err := plan.Err(); err != nil {
			t.Fatal(err)
		}
		plan.Close()
		if !usedIndex {
			t.Fatal("pending metadata query did not use existing frame/sequence range index")
		}
	}
}

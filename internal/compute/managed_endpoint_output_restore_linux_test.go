//go:build linux

package compute

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestManagedEndpointOutputReopenPreservesRingAndRejectsUnknownCursor(t *testing.T) {
	policy := ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2}
	t.Run("warm_partial_segment", func(t *testing.T) {
		dir := managedEndpointClosedOutputRing(t, policy, strings.Repeat("a", 4096), strings.Repeat("b", 1024))
		output, err := reopenManagedEndpointOutput(dir, policy)
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		managedEndpointAssertOutputCursor(t, output, 1, 1024, 2, 0, 5120)
		if _, err := output.Write([]byte("c")); err != nil {
			t.Fatal(err)
		}
		managedEndpointAssertOutputCursor(t, output, 1, 1025, 3, 0, 5121)
		managedEndpointAssertOutputSegments(t, dir, strings.Repeat("a", 4096), strings.Repeat("b", 1024)+"c")
	})
	t.Run("wrapped_full_ring", func(t *testing.T) {
		// Both segments have the same size after wrapping. Their sizes cannot
		// identify which is newest, and the older dropped bytes must survive
		// reconstruction as facts rather than resetting to zero.
		dir := managedEndpointClosedOutputRing(t, policy,
			strings.Repeat("a", 4096), strings.Repeat("b", 4096),
			strings.Repeat("c", 4096), strings.Repeat("d", 4096))
		output, err := reopenManagedEndpointOutput(dir, policy)
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		managedEndpointAssertOutputCursor(t, output, 1, 4096, 4, 8192, 16384)
		managedEndpointAssertOutputSegments(t, dir, strings.Repeat("c", 4096), strings.Repeat("d", 4096))
		if _, err := output.Write([]byte(strings.Repeat("e", 128))); err != nil {
			t.Fatal(err)
		}
		managedEndpointAssertOutputCursor(t, output, 0, 128, 5, 12288, 16512)
		managedEndpointAssertOutputSegments(t, dir, strings.Repeat("e", 128), strings.Repeat("d", 4096))
		if err := output.Close(); err != nil {
			t.Fatal(err)
		}
		// A second warm reopen must append to the latest partial segment,
		// not select an older full segment or lose the accumulated counters.
		output, err = reopenManagedEndpointOutput(dir, policy)
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		managedEndpointAssertOutputCursor(t, output, 0, 128, 5, 12288, 16512)
		if _, err := output.Write([]byte("f")); err != nil {
			t.Fatal(err)
		}
		managedEndpointAssertOutputCursor(t, output, 0, 129, 6, 12288, 16513)
		managedEndpointAssertOutputSegments(t, dir, strings.Repeat("e", 128)+"f", strings.Repeat("d", 4096))
	})

	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"missing_cursor", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "cursor.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}},
		{"malformed_cursor", func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "cursor.json"), []byte(`{"version":`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"pending_write_cursor", func(t *testing.T, dir string) {
			// A durable write-in-progress fence is deliberately not a clean
			// cursor, even when all segment lengths happen to match. This
			// covers a crash while overwriting a previously full segment.
			path := filepath.Join(dir, "cursor.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no durable cursor to fence: %v", err)
			}
			var cursor map[string]any
			if err := json.Unmarshal(raw, &cursor); err != nil {
				t.Fatal(err)
			}
			cursor["pending"] = true
			raw, err = json.Marshal(cursor)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"segment_size_disagrees_with_cursor", func(t *testing.T, dir string) {
			file, err := os.OpenFile(filepath.Join(dir, "output-1"), os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.WriteString("uncommitted"); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := managedEndpointClosedOutputRing(t, policy, strings.Repeat("a", 4096), strings.Repeat("b", 1024))
			tc.mutate(t, dir)
			before := managedEndpointOutputDirectoryBytes(t, dir)
			output, err := reopenManagedEndpointOutput(dir, policy)
			if !errors.Is(err, ErrManagedEndpointOutcomeUnknown) {
				t.Errorf("unproven cursor returned writable output instead of explicit unknown: output=%t err=%v", output != nil, err)
			}
			if output != nil {
				_ = output.Close()
			}
			if after := managedEndpointOutputDirectoryBytes(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatal("unknown cursor recovery changed retained bytes or rewrote its evidence")
			}
		})
	}
}

func managedEndpointClosedOutputRing(t *testing.T, policy ManagedEndpointOutputPolicy, values ...string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "output")
	output, err := newManagedEndpointOutput(dir, policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if _, err := output.Write([]byte(value)); err != nil {
			_ = output.Close()
			t.Fatal(err)
		}
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	return dir
}

func managedEndpointAssertOutputCursor(t *testing.T, output *managedEndpointOutput, segment int, used int64, sequence, dropped, total uint64) {
	t.Helper()
	_, actualSequence, actualDropped, closed, err := output.snapshot()
	if err != nil || closed || output.segment != segment || output.used != used || actualSequence != sequence || actualDropped != dropped || output.total != total {
		t.Errorf("cursor=(segment %d used %d sequence %d dropped %d total %d closed %t), want=(%d %d %d %d %d false), err=%v", output.segment, output.used, actualSequence, actualDropped, output.total, closed, segment, used, sequence, dropped, total, err)
	}
}

func managedEndpointAssertOutputSegments(t *testing.T, dir string, expected ...string) {
	t.Helper()
	for index, want := range expected {
		path := filepath.Join(dir, "output-"+strconv.Itoa(index))
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("segment %d lost or reordered retained bytes: got %d bytes, want %d", index, len(got), len(want))
		}
	}
}

func managedEndpointOutputDirectoryBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		values[entry.Name()] = string(raw)
	}
	return values
}

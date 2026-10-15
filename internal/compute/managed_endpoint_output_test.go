package compute

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedEndpointOutputCountsActualRingOverwriteAndClosedWrites(t *testing.T) {
	output, err := newManagedEndpointOutput(filepath.Join(t.TempDir(), "output"), ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if _, err := output.Write([]byte(strings.Repeat("a", 8192))); err != nil {
		t.Fatal(err)
	}
	_, sequence, dropped, _, err := output.snapshot()
	if err != nil || dropped != 0 {
		t.Fatalf("full ring sequence=%d dropped=%d err=%v", sequence, dropped, err)
	}
	if _, err := output.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	_, sequence, dropped, _, err = output.snapshot()
	if err != nil || dropped != 4096 {
		t.Fatalf("first overwritten byte sequence=%d dropped=%d err=%v", sequence, dropped, err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := output.Write([]byte("closed")); n != 0 || !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed write=%d %v", n, err)
	}
	_, after, afterDrop, closed, _ := output.snapshot()
	if after != sequence || afterDrop != dropped || !closed {
		t.Fatalf("closed write changed accepted facts: %d/%d %d/%d", sequence, after, dropped, afterDrop)
	}
}

func TestManagedEndpointOutputRecordsRealDiskWriteFailureWithoutBreakingServicePipe(t *testing.T) {
	output, err := newManagedEndpointOutput(filepath.Join(t.TempDir(), "output"), ManagedEndpointOutputPolicy{SegmentBytes: 4096, Segments: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := output.file.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := output.Write([]byte("unpersisted")); n != 11 || err != nil {
		t.Fatalf("drain stopped instead of recording failure: %d %v", n, err)
	}
	_, _, dropped, _, err := output.snapshot()
	if err == nil || dropped != 11 {
		t.Fatalf("unpersisted output was reported retained: dropped=%d err=%v", dropped, err)
	}
	if _, err := output.Write([]byte("later")); err != nil {
		t.Fatal(err)
	}
	_, _, dropped, _, err = output.snapshot()
	if err == nil || dropped != 16 {
		t.Fatalf("subsequent discarded output: dropped=%d err=%v", dropped, err)
	}
}

func TestManagedEndpointPrivateReceiptSurvivesOldStagingAndRepeatedPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action.json")
	// This is a private test-owned crash residue, not an accepted action receipt.
	if err := os.WriteFile(path+".pending", []byte("interrupted"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, generation := range []int{1, 2} {
		value := struct {
			Generation int `json:"generation"`
		}{generation}
		if err := writeManagedEndpointPrivateJSON(path, value); err != nil {
			t.Fatal(err)
		}
		var reopened struct {
			Generation int `json:"generation"`
		}
		if err := readManagedEndpointPrivateJSON(path, &reopened); err != nil || reopened.Generation != generation {
			t.Fatalf("reopened=%#v err=%v", reopened, err)
		}
	}
	if raw, err := os.ReadFile(path + ".pending"); err != nil || string(raw) != "interrupted" {
		t.Fatalf("unrelated crash residue was deleted or changed: %q %v", raw, err)
	}
}

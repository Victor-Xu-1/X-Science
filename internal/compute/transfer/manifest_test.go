package transfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
)

func inventoryRecord(kind, name string, size int64) string {
	return fmt.Sprintf("%s\x00%s\x00%d\x001800000000.1234567890\x00", kind, name, size)
}

func selectInventory(t *testing.T, source string, policy Policy, capacity Capacity) (Selection, string, string, error) {
	t.Helper()
	var manifest, selected bytes.Buffer
	sha := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	summary, err := BuildSelection(context.Background(), strings.NewReader(source), sha, policy, capacity, &manifest, &selected, t.TempDir())
	return summary, manifest.String(), selected.String(), err
}

func TestInventorySelectsSmallResultWithoutReadingLargeUnselectedPayload(t *testing.T) {
	source := inventoryRecord("d", "out", 4096) + inventoryRecord("f", "out/trajectory.bin", 40<<30) +
		inventoryRecord("f", "out/table.csv", 5) + inventoryRecord("f", "stdout.log", 0) + inventoryRecord("f", "stderr.log", 0)
	result, manifest, selected, err := selectInventory(t, source, Policy{Outputs: []Output{{Glob: "*.csv"}}}, Capacity{BytesKnown: true, Bytes: 1024})
	if err != nil || result.SelectedFiles != 3 || result.SelectedBytes != 5 || result.RemoteFiles != 1 || result.ObservedFiles != 4 {
		t.Fatalf("selection = %#v, %v", result, err)
	}
	if selected != "out/table.csv\x00stdout.log\x00stderr.log\x00" || !strings.Contains(manifest, `"reason":"not_selected"`) {
		t.Fatalf("selected payload/remote manifest mismatch: %q %q", selected, manifest)
	}
	if result.ManifestSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(manifest))) {
		t.Fatal("published manifest digest does not bind the complete manifest")
	}
}

func TestInventoryKeepsCapacityDeferralExplicitWithoutShrinkingTheComputation(t *testing.T) {
	source := inventoryRecord("f", "out/large.bin", 100) + inventoryRecord("f", "out/small.csv", 5)
	result, manifest, selected, err := selectInventory(t, source, Policy{}, Capacity{BytesKnown: true, Bytes: 10})
	if err != nil || result.RemoteFiles != 1 || result.SelectedBytes != 5 || selected != "out/small.csv\x00" ||
		!strings.Contains(manifest, `"reason":"storage_capacity"`) {
		t.Fatalf("capacity was hidden or turned into a dataset ceiling: %#v %q %q %v", result, manifest, selected, err)
	}
	_, _, _, err = selectInventory(t, source, Policy{}, Capacity{})
	if err == nil {
		t.Fatal("unknown storage was treated as unlimited storage")
	}
}

func TestInventoryStreamsBeyondTheFormerEntryCapWithExactCounts(t *testing.T) {
	var source strings.Builder
	for index := 0; index < 10017; index++ {
		source.WriteString(inventoryRecord("f", fmt.Sprintf("out/result-%05d.csv", index), 0))
	}
	payload := source.String()
	sha := fmt.Sprintf("%x", sha256.Sum256([]byte(payload)))
	result, err := BuildSelection(context.Background(), strings.NewReader(payload), sha, Policy{}, Capacity{BytesKnown: true, Bytes: math.MaxInt64}, io.Discard, io.Discard, t.TempDir())
	if err != nil || result.SelectedFiles != 10017 || result.ObservedFiles != 10017 || result.RemoteFiles != 0 {
		t.Fatalf("entry count was capped: %#v %v", result, err)
	}
}

func TestInventoryRejectsDuplicateAliasesTraversalAndMalformedFields(t *testing.T) {
	for _, source := range []string{
		inventoryRecord("f", "out/a", 1) + inventoryRecord("f", "out/a", 1),
		inventoryRecord("f", "out/a", 1) + inventoryRecord("f", "./out/a", 1),
		inventoryRecord("f", "out/../../private", 1),
		inventoryRecord("f", "/private", 1),
		inventoryRecord("f", "out/a", -1),
		"f\x00out/a\x001\x00",
		"f\x00" + strings.Repeat("x", 65537),
	} {
		if _, _, _, err := selectInventory(t, source, Policy{}, Capacity{BytesKnown: true, Bytes: 100}); err == nil {
			t.Fatalf("malformed inventory admitted: bytes=%d", len(source))
		}
	}
}

func TestInventoryPreservesNulSafeNamesAndDoesNotPublishExcludedNames(t *testing.T) {
	source := inventoryRecord("f", "out/with\nnewline.csv", 1) + inventoryRecord("f", "out/private/secret.csv", 2)
	result, manifest, selected, err := selectInventory(t, source, Policy{Outputs: []Output{{Glob: "**/*.csv"}}, Exclude: []string{"private/**"}}, Capacity{BytesKnown: true, Bytes: 100})
	if err != nil || result.ExcludedFiles != 1 || result.SelectedFiles != 1 || selected != "out/with\nnewline.csv\x00" {
		t.Fatalf("NUL-safe name/selection changed: %#v %q %v", result, selected, err)
	}
	if strings.Contains(manifest, "secret.csv") {
		t.Fatal("excluded filename leaked into the public delivery manifest")
	}
}

func TestInventoryDoesNotFollowNonRegularOutputsOrInventInodes(t *testing.T) {
	source := inventoryRecord("l", "out/link", 4) + inventoryRecord("f", "out/a", 1) + inventoryRecord("f", "out/b", 1)
	result, manifest, selected, err := selectInventory(t, source, Policy{}, Capacity{BytesKnown: true, Bytes: 100, FilesKnown: true, Files: 1})
	if err != nil || result.UnsupportedFiles != 1 || result.SelectedFiles != 1 || result.RemoteFiles != 2 || selected != "out/a\x00" ||
		!strings.Contains(manifest, `"reason":"non_regular"`) || !strings.Contains(manifest, `"reason":"storage_capacity"`) {
		t.Fatalf("non-regular/inode admission changed: %#v %q %q %v", result, manifest, selected, err)
	}
}

func TestInventoryRefusesChangedObservationAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	source := inventoryRecord("f", "out/a", 1)
	sha := fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
	for _, test := range []struct {
		ctx context.Context
		sha string
	}{{context.Background(), strings.Repeat("0", 64)}, {ctx, sha}} {
		if _, err := BuildSelection(test.ctx, strings.NewReader(source), test.sha, Policy{}, Capacity{BytesKnown: true, Bytes: 100}, io.Discard, io.Discard, t.TempDir()); err == nil {
			t.Fatal("changed or cancelled observation produced a successful selection")
		}
	}
}

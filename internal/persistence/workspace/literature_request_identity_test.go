package workspace

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func literatureRequestIdentityTestHash(t *testing.T, value any) string {
	t.Helper()
	hash, err := mutationRequestHash(value)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func literatureRequestIdentityTestPatchHash(t *testing.T, id string, input LiteraturePatch) string {
	t.Helper()
	return literatureRequestIdentityTestHash(t, struct {
		ID    string
		Input LiteraturePatch
	}{id, input})
}

func TestLiteratureRequestIdentityEncodingDispatch(t *testing.T) {
	exact, legacy, projected := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	cases := []struct {
		name, stored string
		want         bool
		calls        int
	}{
		{"v2 exact", "v2:" + exact, true, 0},
		{"v2 changed", "v2:" + projected, false, 0},
		{"v2 prefixed legacy exact", "v2:" + legacy, false, 0},
		{"unknown version", "v3:" + projected, false, 0},
		{"truncated v2", "v2:" + exact[:63], false, 0},
		{"empty encoding", "", false, 0},
		{"uppercase legacy", strings.ToUpper(legacy), false, 0},
		{"nonhex legacy", strings.Repeat("z", 64), false, 0},
		{"short legacy", legacy[:63], false, 0},
		{"legacy exact", legacy, true, 0},
		{"legacy projected", projected, true, 1},
		{"legacy different", strings.Repeat("d", 64), false, 1},
		{"new digest is not legacy exact", exact, false, 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			identity := literatureRequestIdentity{exact: exact, legacyExact: legacy, legacy: func() (string, error) {
				calls++
				return projected, nil
			}}
			if got := identity.matches(test.stored); got != test.want || calls != test.calls {
				t.Fatalf("match=%t calls=%d, want %t/%d", got, calls, test.want, test.calls)
			}
		})
	}
	calls := 0
	identity := literatureRequestIdentity{exact: exact, legacyExact: legacy, legacy: func() (string, error) {
		calls++
		return projected, errors.New("projection unavailable")
	}}
	if identity.matches(projected) || calls != 1 {
		t.Fatal("failed projection became an alias")
	}
	if !identity.matches(legacy) || !identity.matches("v2:"+exact) || calls != 1 {
		t.Fatal("exact matches unnecessarily evaluated a failing projection")
	}
}

func TestLiteratureRequestIdentityExactLegacyAndMetadataPresence(t *testing.T) {
	metadata := map[string]any{"number": json.Number("1e400")}
	raw, err := literatureMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	imported := LiteratureImport{OperationID: "import", ArtifactID: "artifact", VersionID: "version", Title: "Paper", Metadata: metadata}
	patched := LiteraturePatch{OperationID: "patch", ExpectedRevision: 9007199254740993, Metadata: metadata}
	importIdentity, err := literatureImportRequestIdentity(imported, raw)
	if err != nil {
		t.Fatal(err)
	}
	patchIdentity, err := literaturePatchRequestIdentity("item", patched, raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		identity literatureRequestIdentity
		expected string
	}{
		{"import", importIdentity, literatureRequestIdentityTestHash(t, imported)},
		{"patch", patchIdentity, literatureRequestIdentityTestPatchHash(t, "item", patched)},
	} {
		t.Run(test.name, func(t *testing.T) {
			identity := test.identity
			projection := identity.legacy
			if _, err := projection(); err == nil {
				t.Fatal("1e400 unexpectedly has a legacy float projection")
			}
			calls := 0
			identity.legacy = func() (string, error) { calls++; return projection() }
			if identity.legacyExact != test.expected || !identity.matches(test.expected) || !identity.matches(identity.requestHash()) || calls != 0 {
				t.Fatal("exact legacy/v2 1e400 depended on unavailable projection")
			}
		})
	}
	leave := LiteraturePatch{OperationID: "presence", ExpectedRevision: 7}
	clear := leave
	clear.Metadata = map[string]any{}
	left, err := literaturePatchRequestIdentity("item", leave, "{}")
	if err != nil {
		t.Fatal(err)
	}
	right, err := literaturePatchRequestIdentity("item", clear, "{}")
	if err != nil {
		t.Fatal(err)
	}
	old := literatureRequestIdentityTestPatchHash(t, "item", leave)
	if old != literatureRequestIdentityTestPatchHash(t, "item", clear) || left.legacyExact != old || right.legacyExact != old {
		t.Fatal("historical nil/empty typed identity changed")
	}
	for _, test := range []struct {
		identity literatureRequestIdentity
		present  bool
	}{{left, false}, {right, true}} {
		expected := literatureRequestIdentityTestHash(t, struct {
			TypedRequestHash string `json:"typed_request_hash"`
			MetadataPresent  bool   `json:"metadata_present"`
		}{old, test.present})
		if test.identity.requestHash() != "v2:"+expected || !test.identity.matches(old) {
			t.Fatal("Patch presence envelope or legacy replay identity changed")
		}
	}
	if left.requestHash() == right.requestHash() || left.matches(right.requestHash()) || right.matches(left.requestHash()) {
		t.Fatal("new nil/empty Patch intents alias")
	}
}

func TestLiteratureRequestIdentitySnapshotsCallerInputs(t *testing.T) {
	nested := map[string]any{"number": json.Number("9007199254740993")}
	array := []any{json.Number("9007199254740993")}
	metadata := map[string]any{"number": json.Number("9007199254740993"), "nested": nested, "array": array}
	metadata["typed"] = struct {
		Z string `json:"z"`
		A string `json:"a"`
	}{"first", "second"}
	raw, err := literatureMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var projected map[string]any
	if err := json.Unmarshal([]byte(raw), &projected); err != nil {
		t.Fatal(err)
	}
	title, deleted := "Original title", false
	input := LiteraturePatch{OperationID: "patch", ExpectedRevision: 9007199254740993, Title: &title, Deleted: &deleted, Metadata: metadata}
	imported := LiteratureImport{OperationID: "import", ArtifactID: "artifact", VersionID: "version", Title: "Original import", Metadata: metadata, PageCount: 2}
	exactPatch, exactImport := literatureRequestIdentityTestPatchHash(t, "item", input), literatureRequestIdentityTestHash(t, imported)
	projectedPatch, projectedImport := input, imported
	projectedPatch.Metadata, projectedImport.Metadata = projected, projected
	wantPatch, wantImport := literatureRequestIdentityTestPatchHash(t, "item", projectedPatch), literatureRequestIdentityTestHash(t, projectedImport)
	patchIdentity, err := literaturePatchRequestIdentity("item", input, raw)
	if err != nil {
		t.Fatal(err)
	}
	importIdentity, err := literatureImportRequestIdentity(imported, raw)
	if err != nil {
		t.Fatal(err)
	}
	imported.PageCount = 99
	pageIdentity, err := literatureImportRequestIdentity(imported, raw)
	if err != nil || pageIdentity.requestHash() != importIdentity.requestHash() {
		t.Fatal("server-derived PageCount entered request identity")
	}
	metadata["number"], nested["number"], array[0] = json.Number("7"), json.Number("8"), json.Number("9")
	title, deleted = "Mutated title", true
	input.ExpectedRevision++
	imported.Title = "Mutated import"
	if patchIdentity.legacyExact != exactPatch || importIdentity.legacyExact != exactImport {
		t.Fatal("exact typed identity was not frozen")
	}
	for _, test := range []struct {
		name     string
		identity literatureRequestIdentity
		want     string
	}{{"patch", patchIdentity, wantPatch}, {"import", importIdentity, wantImport}} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.identity.legacy()
			if err != nil || got != test.want || !test.identity.matches(test.want) {
				t.Fatalf("lazy typed projection retained mutable input or rounded coordinates: got %s want %s err=%v", got, test.want, err)
			}
		})
	}
}

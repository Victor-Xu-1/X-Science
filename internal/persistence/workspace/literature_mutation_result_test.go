package workspace

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiteratureMutationResultActualRowMatching(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "workspace.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exact, legacy, projected := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	const owner, operation = "identity-owner", "literature.patch"
	for _, test := range []struct {
		name, stored string
		found        bool
		conflict     bool
		calls        int
	}{
		{"no-row", "", false, false, 0},
		{"v2-exact", "v2:" + exact, true, false, 0},
		{"v2-no-legacy-alias", "v2:" + projected, false, true, 0},
		{"unknown-encoding", "v3:" + projected, false, true, 0},
		{"legacy-exact", legacy, true, false, 0},
		{"legacy-projected", projected, true, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			witness := LiteratureReceipt{Schema: "x-science.literature-receipt.v1", OperationID: test.name,
				Item: LiteratureItem{ID: "identity-item", Title: "Stored witness", MetadataRevision: 4, Metadata: map[string]any{"marker": "unchanged"}}}
			if test.stored != "" {
				// Synthetic row witness for the existing matcher only; this is not
				// an invented business-mutation success or a legacy-binary run.
				if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
					return insertMutationResultTx(t.Context(), tx, owner, test.name, operation, test.stored, witness, time.Unix(1, 0).UTC())
				}); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			identity := literatureRequestIdentity{exact: exact, legacyExact: legacy, legacy: func() (string, error) {
				calls++
				return projected, nil
			}}
			assertResult := func(found bool, err error, got LiteratureReceipt) {
				t.Helper()
				if found != test.found || errors.Is(err, ErrMutationIdempotencyConflict) != test.conflict || (!test.conflict && err != nil) || calls != test.calls {
					t.Fatalf("found=%t err=%v calls=%d, want %t/conflict=%t/%d", found, err, calls, test.found, test.conflict, test.calls)
				}
				if found && (got.OperationID != witness.OperationID || got.Item.ID != witness.Item.ID || got.Item.MetadataRevision != 4) {
					t.Fatalf("wrong stored witness: %#v", got)
				}
			}
			var outer LiteratureReceipt
			found, err := store.lookupLiteratureMutationResult(t.Context(), owner, test.name, operation, identity, &outer)
			assertResult(found, err, outer)
			calls = 0
			if err := store.WithTransaction(t.Context(), func(tx *sql.Tx) error {
				var inner LiteratureReceipt
				found, readErr := lookupLiteratureMutationResultTx(t.Context(), tx, owner, test.name, operation, identity, &inner)
				assertResult(found, readErr, inner)
				if test.stored != "" {
					var generic LiteratureReceipt
					found, genericErr := lookupMutationResultTx(t.Context(), tx, owner, test.name, operation, strings.Repeat("d", 64), &generic)
					if found || !errors.Is(genericErr, ErrMutationIdempotencyConflict) {
						t.Fatalf("generic exact reader acquired Library aliases: found=%t err=%v", found, genericErr)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			calls = 0
			var foreign LiteratureReceipt
			if found, err := store.lookupLiteratureMutationResult(t.Context(), "foreign-owner", test.name, operation, identity, &foreign); found || err != nil || calls != 0 {
				t.Fatalf("missing owned row evaluated legacy projection: found=%t err=%v calls=%d", found, err, calls)
			}
		})
	}
}

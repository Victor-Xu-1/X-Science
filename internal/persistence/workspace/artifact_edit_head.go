package workspace

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

var ErrInvalidArtifactExpectedHead = errors.New("expected artifact head version must be a non-empty identity")
var ErrArtifactExpectedHeadRequired = errors.New("a confirmed artifact head is required for a new manual version")

// ArtifactHeadConflictError reports a stale editor baseline, independently of
// the selected historical parent used for the new version's lineage.
type ArtifactHeadConflictError struct {
	ArtifactID            string
	ExpectedHeadVersionID string
	CurrentHeadVersionID  string
}

func (err *ArtifactHeadConflictError) Error() string {
	return "artifact head changed since editing began"
}

func requireArtifactExpectedHeadTx(ctx context.Context, tx *sql.Tx, input WriteArtifactVersionInput, currentVersion int) error {
	if input.ExpectedHeadVersionID == nil {
		if input.RequireExpectedHeadForNewMutation {
			return ErrArtifactExpectedHeadRequired
		}
		return nil
	}
	expected := strings.TrimSpace(*input.ExpectedHeadVersionID)
	if expected == "" {
		return ErrInvalidArtifactExpectedHead
	}
	actual := ""
	if currentVersion > 0 {
		if err := tx.QueryRowContext(ctx, `SELECT id FROM artifact_versions WHERE artifact_id=? AND version_number=?`, input.ArtifactID, currentVersion).Scan(&actual); err != nil {
			return err
		}
	}
	if actual != expected {
		return &ArtifactHeadConflictError{ArtifactID: input.ArtifactID, ExpectedHeadVersionID: expected, CurrentHeadVersionID: actual}
	}
	return nil
}

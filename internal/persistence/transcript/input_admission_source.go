package transcript

import (
	"context"
	"database/sql"
)

// RecoveryAdmissionSource contains exact immutable coordinates from the
// admission ledger. A verifier checks them, never discovers a different/latest
// source or derives an input revision from operation history.
type RecoveryAdmissionSource struct {
	StreamUID, OwnerID, ProjectID, FrameID, BranchID, OperationID string
	Epoch, BranchGeneration, InputRevision, StateVersion          int64
}

type InputAdmissionSourceQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// RecoveryAdmissionVerifier is bound by the workspace owner at construction.
// It receives the caller's current read/Immediate connection, preserving one
// transaction snapshot without a transcript -> workspace dependency.
type RecoveryAdmissionVerifier func(context.Context, InputAdmissionSourceQuery, RecoveryAdmissionSource) error

func NewRepositoryWithRecoveryVerifier(writeDB, readDB *sql.DB, verifier RecoveryAdmissionVerifier) *Repository {
	repository := NewRepositoryWithReadPool(writeDB, readDB)
	repository.recoveryAdmissionVerifier = verifier
	return repository
}

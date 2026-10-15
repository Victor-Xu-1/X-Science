package transcript

import (
	"context"
	"database/sql"
)

func validateInputAdmissionContract(ctx context.Context, tx *sql.Tx, required bool) error {
	var installed int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema
		WHERE type='table' AND name='transcript_input_admissions'`).Scan(&installed); err != nil {
		return err
	}
	if installed == 0 && !required {
		return nil
	}
	if installed != 1 {
		return ErrSchemaUnavailable
	}
	names := []string{"transcript_input_admissions", "transcript_input_admission_current",
		"transcript_input_admissions_immutable", "transcript_input_admissions_retained"}
	for index, expected := range InputAdmissionV77Statements() {
		var observed string
		if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name=?`, names[index]).Scan(&observed); err != nil {
			return err
		}
		if normalizeContractSQL(observed) != normalizeContractSQL(expected) {
			return ErrSchemaUnavailable
		}
	}
	return nil
}

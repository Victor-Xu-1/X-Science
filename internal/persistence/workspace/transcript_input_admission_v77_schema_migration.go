package workspace

import (
	"context"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

const (
	transcriptInputAdmissionV77CallbackID        = "transcript-input-admission-v77-noop"
	transcriptInputAdmissionV77PreflightIdentity = "transcript-input-admission-v77-prospective-v1"
	transcriptInputAdmissionV77RuleSpec          = "x-science.workspace.input-admission.v77"
)

var transcriptInputAdmissionV77Migration = versionedSchemaMigration{
	version: 77, name: "transcript-exact-input-admission",
	identityV2: &schemaMigrationIdentityV2{CallbackID: transcriptInputAdmissionV77CallbackID,
		PreflightIdentity: transcriptInputAdmissionV77PreflightIdentity, RuleSpec: transcriptInputAdmissionV77RuleSpec},
	statements: append(transcriptstore.InputAdmissionV77Statements(),
		`CREATE TRIGGER transcript_kernel_recovery_admission_source BEFORE INSERT ON transcript_input_admissions
		WHEN NEW.source_kind='kernel_recovery' AND NOT EXISTS (
			SELECT 1 FROM kernel_local_operation_transitions t JOIN kernel_local_operations o ON o.operation_id=t.operation_id
			WHERE t.operation_id=NEW.operation_id AND t.state_version=NEW.operation_state_version
			AND t.transition_seq=t.state_version AND t.admitted_input_revision=NEW.input_revision
			AND t.admitted_input_revision>COALESCE((SELECT p.admitted_input_revision FROM kernel_local_operation_transitions p
				WHERE p.operation_id=t.operation_id AND p.state_version=t.state_version-1),0)
			AND o.stream_uid=NEW.stream_uid AND o.branch_id=NEW.branch_id AND o.branch_generation=NEW.branch_generation
			AND ((t.from_state='prepared' AND t.to_state='approved') OR
				(t.from_state='started' AND t.to_state='outcome_unknown')))
		BEGIN SELECT RAISE(ABORT,'kernel recovery admission has no exact transition'); END`),
}

func preflightTranscriptInputAdmissionV77(ctx context.Context, query schemaMigrationQueryExecutor) error {
	var count int
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema
		WHERE name IN ('transcript_input_admissions','transcript_input_admission_current',
		'transcript_input_admissions_immutable','transcript_input_admissions_retained',
		'transcript_kernel_recovery_admission_source')`).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return errors.New("input admission migration target is not empty")
	}
	if err := query.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('kernel_local_operation_transitions')
		WHERE name IN ('operation_id','transition_seq','state_version','from_state','to_state','admitted_input_revision')`).Scan(&count); err != nil {
		return err
	}
	if count != 6 {
		return errors.New("input admission migration requires the canonical recovery transition ledger")
	}
	return nil
}

package transcript

import "context"

// Legacy repositories can be read and migrated before prospective admission
// exists. Absence is not admission evidence: keep their original ordinary/auto
// behavior, but fail closed if they contain a manual-maintenance input whose
// authority cannot be checked. Current-contract validation remains mandatory.
func contextCompactionLedgerForStream(ctx context.Context, query InputAdmissionSourceQuery, stream string) (bool, error) {
	var installed bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema
		WHERE type='table' AND name='transcript_input_admissions')`).Scan(&installed); err != nil {
		return false, err
	}
	if installed {
		return true, nil
	}
	var unsupported bool
	if err := query.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM transcript_events
		WHERE stream_uid=? AND event_type='user_message' AND source='payload'
		AND CASE WHEN json_valid(payload_json)
		 THEN json_extract(payload_json,'$.inputData.turn_intent') END='compact-context')`, stream).Scan(&unsupported); err != nil {
		return false, err
	}
	if unsupported {
		return false, ErrSchemaUnavailable
	}
	return false, nil
}

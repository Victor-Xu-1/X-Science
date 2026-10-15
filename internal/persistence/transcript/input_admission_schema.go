package transcript

// InputAdmissionV77Statements records prospective admission, not publication
// order. Existing history is intentionally not backfilled. Settled copied
// prefixes remain historical, and subsequent revisions use this exact ledger.
func InputAdmissionV77Statements() []string {
	return []string{
		`CREATE TABLE transcript_input_admissions (
			stream_uid TEXT NOT NULL REFERENCES transcript_streams(stream_uid) ON DELETE CASCADE,
			input_revision INTEGER NOT NULL CHECK(input_revision>0),
			source_kind TEXT NOT NULL CHECK(source_kind IN ('event','kernel_recovery')),
			input_kind TEXT NOT NULL CHECK(input_kind IN ('message','control','recovery')),
			classification_version INTEGER NOT NULL CHECK(classification_version=1),
			event_id INTEGER,
			operation_id TEXT,
			operation_state_version INTEGER,
			branch_id TEXT NOT NULL,
			branch_generation INTEGER NOT NULL CHECK(branch_generation>0),
			PRIMARY KEY(stream_uid,input_revision),
			UNIQUE(stream_uid,event_id),
			UNIQUE(stream_uid,operation_id,operation_state_version),
			FOREIGN KEY(stream_uid,event_id) REFERENCES transcript_events(stream_uid,event_id) ON DELETE CASCADE,
			CHECK((source_kind='event' AND input_kind IN ('message','control') AND event_id>0
				AND operation_id IS NULL AND operation_state_version IS NULL) OR
				(source_kind='kernel_recovery' AND input_kind='recovery' AND event_id IS NULL
				AND length(operation_id)>0 AND operation_state_version>0))
		) STRICT`,
		`CREATE TRIGGER transcript_input_admission_current BEFORE INSERT ON transcript_input_admissions
		WHEN NOT EXISTS (SELECT 1 FROM transcript_streams s JOIN transcript_branch_state b ON b.stream_uid=s.stream_uid
			WHERE s.stream_uid=NEW.stream_uid AND s.input_revision=NEW.input_revision
			AND b.active_branch_id=NEW.branch_id AND b.generation=NEW.branch_generation)
		OR (NEW.source_kind='event' AND NOT EXISTS (SELECT 1 FROM transcript_events e
			WHERE e.stream_uid=NEW.stream_uid AND e.event_id=NEW.event_id
			AND e.event_type IN ('user_message','user_input_response') AND e.runner_attempt IS NULL))
		BEGIN SELECT RAISE(ABORT,'input admission source is not current'); END`,
		`CREATE TRIGGER transcript_input_admissions_immutable BEFORE UPDATE ON transcript_input_admissions
		BEGIN SELECT RAISE(ABORT,'input admission is immutable'); END`,
		`CREATE TRIGGER transcript_input_admissions_retained BEFORE DELETE ON transcript_input_admissions
		WHEN EXISTS (SELECT 1 FROM transcript_streams s WHERE s.stream_uid=OLD.stream_uid)
		BEGIN SELECT RAISE(ABORT,'input admission belongs to retained history'); END`,
	}
}

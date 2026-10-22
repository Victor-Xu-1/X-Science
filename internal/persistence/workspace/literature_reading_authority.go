package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Accepted journal payloads, not mutable Library membership or model-authored
// IDs, authorize a task's retained/tombstoned Library version reads.
func (s *Store) AcceptedLiteratureReadingBinding(ctx context.Context, owner, frame, version string) (transcriptstore.PDFReadingBinding, error) {
	var binding transcriptstore.PDFReadingBinding
	var raw []byte
	err := s.readDatabase().QueryRowContext(ctx, `SELECT binding.value FROM transcript_events event
	 JOIN transcript_streams stream ON stream.stream_uid=event.stream_uid
	 JOIN frames frame ON frame.id=stream.frame_id JOIN projects project ON project.id=frame.project_id
	 JOIN frames root ON root.id=frame.root_frame_id AND root.project_id=frame.project_id
	 JOIN json_each(CASE WHEN event.source='payload' AND event.event_type IN ('user_message','user_input_response') THEN event.payload_json ELSE '{}' END,'$.inputData.pdf_reading_context.bindings') binding
	 WHERE stream.owner_id=? AND frame.id=? AND project.user_id=? AND stream.project_id=frame.project_id
	 AND event.source='payload' AND event.event_type IN ('user_message','user_input_response') AND json_extract(event.payload_json,'$.role')='user'
	 AND json_extract(event.payload_json,'$.inputData.pdf_reading_context.scope.frame_id')=frame.id
	 AND json_extract(event.payload_json,'$.inputData.pdf_reading_context.scope.frame_incarnation_id')=frame.incarnation_id
	 AND json_extract(event.payload_json,'$.inputData.pdf_reading_context.scope.root_frame_id')=root.id
	 AND json_extract(event.payload_json,'$.inputData.pdf_reading_context.scope.root_frame_incarnation_id')=root.incarnation_id
	 AND json_extract(event.payload_json,'$.inputData.pdf_reading_context.scope.project_id')=frame.project_id
	 AND json_extract(binding.value,'$.source_kind')='literature-attachment-version'
	 AND json_extract(binding.value,'$.version_id')=? ORDER BY event.event_id DESC LIMIT 1`, owner, frame, owner, version).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return binding, ErrLiteratureUnavailable
	}
	if err != nil {
		return binding, err
	}
	if json.Unmarshal(raw, &binding) != nil || !binding.Valid() {
		return binding, ErrLiteratureUnavailable
	}
	return binding, nil
}

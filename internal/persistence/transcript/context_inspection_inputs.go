package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
)

func contextInspectionInputsConn(ctx context.Context, conn *sql.Conn, stream Stream, scope ContextInspectionScope) ([]ContextInspectionInput, bool, error) {
	rows, err := conn.QueryContext(ctx, `SELECT input.event_id,input.input_revision,event.publication_seq,event.client_message_id,
		input.branch_id,input.branch_generation,event.event_type,event.source,event.runner_attempt,
		CASE WHEN event.source='payload' THEN event.payload_json ELSE frame.payload END
		FROM transcript_input_admissions input
		JOIN transcript_events event ON event.stream_uid=input.stream_uid AND event.event_id=input.event_id
		JOIN transcript_branch_events member ON member.stream_uid=input.stream_uid AND member.event_id=input.event_id AND member.branch_id=?
		LEFT JOIN frame_events frame ON event.source='frame_ref' AND frame.id=event.frame_event_id AND frame.frame_id=? AND frame.event_type=event.event_type
		WHERE input.stream_uid=? AND input.classification_version=? AND input.source_kind='event' AND input.input_kind='message'
		AND event.publication_seq<=? AND EXISTS(SELECT 1 FROM transcript_branch_events original WHERE original.stream_uid=input.stream_uid AND original.branch_id=input.branch_id AND original.event_id=input.event_id)
		ORDER BY input.input_revision DESC LIMIT ?`, scope.BranchID, stream.FrameID, stream.UID, AdmissionClassificationV1, scope.ThroughPublicationSequence, contextInspectionInputLimit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []ContextInspectionInput{}
	truncated := false
	for rows.Next() {
		var input ContextInspectionInput
		var eventType, source string
		var runner sql.NullInt64
		var payload []byte
		if err := rows.Scan(&input.EventID, &input.InputRevision, &input.PublicationSequence, &input.InputMessageID,
			&input.AdmissionBranchID, &input.AdmissionBranchGeneration, &eventType, &source, &runner, &payload); err != nil {
			return nil, false, err
		}
		if len(result) == contextInspectionInputLimit {
			truncated = true
			break
		}
		kind, err := admittedEventBytesKind(eventType, input.InputMessageID, payload)
		if err != nil || kind != "message" || runner.Valid || len(payload) > maxEventPayloadBytes ||
			(source != string(EventSourcePayload) && source != string(EventSourceFrameRef)) ||
			input.InputRevision <= 0 || input.AdmissionBranchGeneration <= 0 || input.AdmissionBranchGeneration > scope.BranchGeneration {
			return nil, false, ErrEventConflict
		}
		input.InputMessageID = contextInputMessageID(payload, input.InputMessageID)
		result = append(result, input)
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, truncated, rows.Err()
}

func contextInputMessageID(payload []byte, clientMessageID string) string {
	var message struct {
		MessageUUID     string `json:"messageUuid"`
		UUID            string `json:"_uuid"`
		ClientMessageID string `json:"clientMessageId"`
	}
	if json.Unmarshal(payload, &message) != nil {
		return ""
	}
	// Same explicit identity order as the canonical Frame task/message
	// projection. Client mutation IDs and rendered message UUIDs can differ.
	return firstNonEmptyString(message.MessageUUID, message.UUID, message.ClientMessageID, clientMessageID)
}

func contextInspectionAncestorsConn(ctx context.Context, conn *sql.Conn, scope ContextInspectionScope) (map[string]bool, bool, error) {
	rows, err := conn.QueryContext(ctx, `WITH RECURSIVE lineage(branch_id,parent_branch_id,depth) AS (
		SELECT branch_id,parent_branch_id,1 FROM transcript_branches WHERE stream_uid=? AND branch_id=?
		UNION ALL SELECT branch.branch_id,branch.parent_branch_id,lineage.depth+1
		FROM transcript_branches branch JOIN lineage ON branch.branch_id=lineage.parent_branch_id
		WHERE branch.stream_uid=? AND lineage.depth<?)
		SELECT branch_id,COALESCE(parent_branch_id,'') FROM lineage`, scope.StreamUID, scope.BranchID, scope.StreamUID, contextInspectionInputLimit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := map[string]bool{}
	lastParent := ""
	for rows.Next() {
		var branch string
		if err := rows.Scan(&branch, &lastParent); err != nil {
			return nil, false, err
		}
		if result[branch] {
			return nil, false, ErrEventConflict
		}
		result[branch] = true
	}
	return result, lastParent != "", rows.Err()
}

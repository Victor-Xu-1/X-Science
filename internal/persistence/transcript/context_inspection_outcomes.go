package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"time"
)

type ContextInspectionOutcome struct {
	InputEventID, Attempt, TerminalEventID int64
	Status                                 string
	CompletedAt                            time.Time
	Retired                                bool
}

func contextInspectionOutcomesConn(ctx context.Context, conn *sql.Conn, scope ContextInspectionScope, attempts []ContextInspectionAttempt, inputs []ContextInspectionInput) ([]ContextInspectionOutcome, bool, bool, error) {
	result := []ContextInspectionOutcome{}
	byAttempt := map[int64]ContextInspectionAttempt{}
	for _, attempt := range attempts {
		byAttempt[attempt.Attempt] = attempt
		if attempt.TerminalEventID > 0 && attempt.CompletedAt != nil {
			result = append(result, ContextInspectionOutcome{InputEventID: attempt.InputEventID, Attempt: attempt.Attempt,
				TerminalEventID: attempt.TerminalEventID, Status: attempt.Status, CompletedAt: *attempt.CompletedAt})
		}
	}
	// Revival retires the reclaimed receipt, not the reserved canonical event.
	// Require the existing anchored claim-generation chain to attribute it.
	rows, err := conn.QueryContext(ctx, `SELECT event.event_id,event.runner_attempt,event.client_message_id,event.source,event.created_at,event.payload_json
		FROM transcript_events event JOIN transcript_branch_events member ON member.stream_uid=event.stream_uid AND member.event_id=event.event_id
		JOIN transcript_runner_attempts attempt ON attempt.stream_uid=event.stream_uid AND attempt.attempt=event.runner_attempt
		WHERE event.stream_uid=? AND member.branch_id=? AND event.event_type='runner_reclaimed' AND event.publication_seq<=?
		AND event.event_id!=COALESCE(attempt.finished_event_id,0) ORDER BY event.publication_seq LIMIT ?`,
		scope.StreamUID, scope.BranchID, scope.ThroughPublicationSequence, contextInspectionAttemptLimit+1)
	if err != nil {
		return nil, false, false, err
	}
	defer rows.Close()
	incomplete, truncated, count := false, false, 0
	for rows.Next() {
		count++
		if count > contextInspectionAttemptLimit {
			truncated = true
			break
		}
		var eventID, attemptID int64
		var clientID, source string
		var created time.Time
		var raw []byte
		if err := rows.Scan(&eventID, &attemptID, &clientID, &source, &created, &raw); err != nil {
			return nil, false, false, err
		}
		var payload struct {
			Status string `json:"status"`
			Reason string `json:"reason_code"`
		}
		attempt, found := byAttempt[attemptID]
		inputID, proven := contextInspectionRetiredInput(attempt, eventID, inputs)
		if !found || !proven || source != string(EventSourcePayload) || len(raw) > maxEventPayloadBytes ||
			clientID != "runner-reclaimed-"+strconv.FormatInt(attemptID, 10) || json.Unmarshal(raw, &payload) != nil ||
			payload.Status != "reclaimed" || payload.Reason != "lease_expired" || created.IsZero() {
			incomplete = true
			continue
		}
		result = append(result, ContextInspectionOutcome{InputEventID: inputID, Attempt: attemptID, TerminalEventID: eventID,
			Status: "reclaimed", CompletedAt: created.UTC(), Retired: true})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TerminalEventID < result[j].TerminalEventID })
	return result, incomplete, truncated, rows.Err()
}

func contextInspectionRetiredInput(attempt ContextInspectionAttempt, terminalID int64, inputs []ContextInspectionInput) (int64, bool) {
	for index, record := range attempt.resumeFacts {
		if record.eventID <= terminalID {
			continue
		}
		if index == 0 && record.fact.LegacyPrefixUnknown {
			return 0, false
		}
		// Reserved events are ordered within this stream and attempt. The next
		// proven generation fixes its previous bound: no wall-clock join.
		var selected ContextInspectionInput
		for _, input := range inputs {
			if input.InputRevision <= record.fact.PreviousInputRevision && input.InputRevision > selected.InputRevision {
				selected = input
			}
		}
		return selected.EventID, selected.EventID > 0 && selected.EventID == attempt.InputEventID
	}
	return 0, false
}

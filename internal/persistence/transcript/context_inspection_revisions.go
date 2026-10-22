package transcript

import (
	"context"
	"database/sql"
	"encoding/json"
)

// Checkpoint continuation can rotate an attempt's claim in place. Preserve
// earlier captured input bounds only through the existing immutable generation
// chain, anchored to that attempt's current claim digest. No timestamp join or
// <= approximation can establish an earlier claim's accepted input boundary.
func contextInspectionRevisionsConn(ctx context.Context, conn *sql.Conn, stream Stream, attempts []ContextInspectionAttempt) (bool, error) {
	indices := map[int64]int{}
	for index, attempt := range attempts {
		indices[attempt.Attempt] = index
	}
	if len(indices) == 0 {
		return false, nil
	}
	rows, err := conn.QueryContext(ctx, runnerTimingFactsQuery+" LIMIT ?", stream.UID,
		runnerTimingResumeClientPrefix, "runner-timing-resume;", RunnerTimingResumeEventType, contextInspectionAttemptLimit+1)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	facts := map[int64][]runnerTimingFactRecord{}
	count := 0
	for rows.Next() {
		count++
		if count > contextInspectionAttemptLimit {
			return true, nil // Current bounds remain proven; historical ones do not.
		}
		var record runnerTimingFactRecord
		var attempt sql.NullInt64
		var clientID, source string
		var createdRaw any
		var raw []byte
		if err := rows.Scan(&record.eventID, &attempt, &clientID, &source, &createdRaw, &raw); err != nil {
			return false, err
		}
		if _, found := indices[attempt.Int64]; !attempt.Valid || !found {
			continue
		}
		created, _, createdErr := runnerTimingTimeValue(createdRaw, true)
		fact := &record.fact
		record.valid = createdErr == nil && json.Unmarshal(raw, fact) == nil && fact.Schema == runnerTimingResumeSchema &&
			fact.OwnerID == stream.OwnerID && fact.StreamUID == stream.UID && fact.Attempt == attempt.Int64 &&
			clientID == runnerTimingResumeClientPrefix+fact.Generation && source == string(EventSourcePayload) && fact.ResumedAt.Equal(created)
		facts[attempt.Int64] = append(facts[attempt.Int64], record)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for attempt, records := range facts {
		index := indices[attempt]
		if revisions, valid := contextInspectionClaimRevisions(attempts[index], records); valid {
			attempts[index].ClaimedRevisions = revisions
			attempts[index].resumeFacts = records
		}
	}
	return false, nil
}

func contextInspectionClaimRevisions(attempt ContextInspectionAttempt, records []runnerTimingFactRecord) (map[int64]bool, bool) {
	result := map[int64]bool{attempt.ClaimedInputRevision: true}
	var generation string
	var revision int64
	for index, record := range records {
		fact := record.fact
		if !record.valid || !validRunnerTimingGeneration(fact.Generation) || !validRunnerTimingGeneration(fact.PreviousGeneration) ||
			fact.Generation == fact.PreviousGeneration || fact.PreviousInputRevision <= 0 || fact.InputRevision < fact.PreviousInputRevision ||
			index > 0 && (fact.PreviousGeneration != generation || fact.PreviousInputRevision != revision) {
			return nil, false
		}
		result[fact.PreviousInputRevision], result[fact.InputRevision] = true, true
		generation, revision = fact.Generation, fact.InputRevision
	}
	return result, len(records) > 0 && generation == attempt.generation && revision == attempt.ClaimedInputRevision
}

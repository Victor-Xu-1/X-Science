package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Every confirmation query shares the Transcript reader's single snapshot. It
// cannot append a missing event, bind newer artifacts, finish, enqueue or rewrite
// Aside state. Missing/mismatched facts remain an unknown acknowledgement.
func (s *Store) confirmRunnerTranscriptFinalization(ctx context.Context, input RunnerFinalizationInput, expected RunnerFinalizationResult, repository *transcriptstore.Repository) (RunnerFinalizationResult, bool, error) {
	result := expected
	receipt, found, err := repository.ReadRunnerFinalization(ctx, input.Transcript, &expected.Transcript,
		func(ctx context.Context, read transcriptstore.RunnerFinalizationRead, receipt transcriptstore.RunnerFinalizationReceipt) error {
			if expected.EnqueuedMemory != nil {
				actual, err := scanOutboxEvent(read.QueryRowContext(ctx, outboxSelect+` WHERE event_id=?`, expected.EnqueuedMemory.ID))
				if err != nil {
					return err
				}
				if !sameFinalizationOutbox(actual, *expected.EnqueuedMemory) {
					return transcriptstore.ErrEventConflict
				}
				if _, err := DecodeMemoryExtractionDispatch(actual); err != nil {
					return err
				}
				result.EnqueuedMemory = &actual
			}
			if expected.Aside != nil {
				var raw sql.NullString
				if err := read.QueryRowContext(ctx, `SELECT output_data FROM frame_runtime_metadata WHERE frame_id=?`, expected.Aside.FrameID).Scan(&raw); err != nil {
					return err
				}
				if !raw.Valid || !bytes.Equal([]byte(raw.String), expected.Aside.Payload) {
					return transcriptstore.ErrEventConflict
				}
			}
			return nil
		})
	if err != nil || !found {
		return RunnerFinalizationResult{}, found, err
	}
	result.Transcript = receipt
	return result, true, nil
}

func sameFinalizationOutbox(left, right OutboxEvent) bool {
	return left.Sequence == right.Sequence && left.ID == right.ID && left.IdempotencyKey == right.IdempotencyKey && left.Topic == right.Topic &&
		left.PartitionKey == right.PartitionKey && left.Type == right.Type && left.AggregateType == right.AggregateType && left.AggregateID == right.AggregateID &&
		bytes.Equal(left.Payload, right.Payload) && reflect.DeepEqual(left.Headers, right.Headers) && left.MaxAttempts == right.MaxAttempts && left.OccurredAt.Equal(right.OccurredAt)
}

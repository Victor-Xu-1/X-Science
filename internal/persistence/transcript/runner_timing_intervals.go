package transcript

import (
	"encoding/hex"
	"time"
)

type runnerActiveInterval struct {
	start, end time.Time
	revision   int64
	unknown    bool
}

func runnerTimingIntervals(attempt runnerTimingAttempt, observedAt time.Time) ([]runnerActiveInterval, error) {
	if attempt.observationErr != nil {
		return nil, attempt.observationErr
	}
	if attempt.attempt <= 0 || attempt.revision <= 0 || attempt.claimedAt.IsZero() || attempt.expiresAt.IsZero() ||
		attempt.status == "running" && attempt.finishedAt != nil || attempt.status != "running" && attempt.finishedAt == nil {
		return nil, ErrEventConflict
	}
	end := observedAt
	if attempt.finishedAt != nil && attempt.finishedAt.Before(end) {
		end = *attempt.finishedAt
	}
	// Terminal settlement can happen after a paused/expired lease. Its real
	// audit timestamp is retained, but cannot retroactively create active work.
	if attempt.expiresAt.Before(end) {
		end = attempt.expiresAt
	}
	if end.Before(attempt.claimedAt) {
		return nil, ErrEventConflict
	}
	if len(attempt.facts) == 0 {
		return []runnerActiveInterval{{start: attempt.claimedAt, end: end, revision: attempt.revision, unknown: attempt.legacyResume}}, nil
	}
	start := attempt.claimedAt
	revision := attempt.facts[0].fact.PreviousInputRevision
	var generation string
	intervals := make([]runnerActiveInterval, 0, len(attempt.facts)+1)
	for i, record := range attempt.facts {
		if record.observationErr != nil {
			return nil, record.observationErr
		}
		fact := record.fact
		if !record.valid || !validRunnerTimingGeneration(fact.Generation) || !validRunnerTimingGeneration(fact.PreviousGeneration) ||
			fact.Generation == fact.PreviousGeneration || revision <= 0 || fact.PreviousInputRevision != revision || fact.InputRevision < revision ||
			fact.PreviousExpiresAt.IsZero() || fact.ResumedAt.IsZero() ||
			fact.PreviousExpiresAt.Before(start) || fact.ResumedAt.Before(fact.PreviousExpiresAt) || fact.ResumedAt.After(end) ||
			i > 0 && fact.PreviousGeneration != generation {
			// Missing/foreign/old-reader generations cannot be repaired by
			// guessing pause gaps. The current input may also cross the gap.
			return []runnerActiveInterval{{start: attempt.claimedAt, end: end, revision: attempt.revision, unknown: true}}, nil
		}
		intervals = append(intervals, runnerActiveInterval{start: start, end: fact.PreviousExpiresAt, revision: revision, unknown: i == 0 && fact.LegacyPrefixUnknown})
		start, revision, generation = fact.ResumedAt, fact.InputRevision, fact.Generation
	}
	if generation != attempt.generation || revision != attempt.revision {
		return []runnerActiveInterval{{start: attempt.claimedAt, end: end, revision: attempt.revision, unknown: true}}, nil
	}
	return append(intervals, runnerActiveInterval{start: start, end: end, revision: revision}), nil
}

func validRunnerTimingGeneration(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32
}

func addRunnerActiveInterval(elapsed *time.Duration, interval runnerActiveInterval) error {
	if interval.unknown {
		return nil
	}
	duration := interval.end.Sub(interval.start)
	if duration < 0 || *elapsed > time.Duration(1<<63-1)-duration {
		return ErrEventConflict
	}
	*elapsed += duration
	return nil
}

func reduceRunnerTiming(streamUID string, attempts []runnerTimingAttempt, observedAt time.Time) (RunnerTimingSummary, RunnerTaskTimingSummary, bool, error) {
	if len(attempts) == 0 {
		return RunnerTimingSummary{}, RunnerTaskTimingSummary{}, false, nil
	}
	last := attempts[len(attempts)-1]
	active := last.status == "running" && last.finishedAt == nil && last.expiresAt.After(observedAt)
	latest := RunnerTimingSummary{StreamUID: streamUID, Attempt: last.attempt, InputRevision: last.revision, Status: last.status,
		FinishedAt: last.finishedAt, ObservedAt: observedAt, Active: active, FinishedEventID: last.finishedEventID}
	task := RunnerTaskTimingSummary{StreamUID: streamUID, Attempt: last.attempt, InputRevision: last.revision, Status: last.status,
		FinishedAt: last.finishedAt, ObservedAt: observedAt, Active: active}
	for _, attempt := range attempts {
		intervals, err := runnerTimingIntervals(attempt, observedAt)
		if attempt.attempt != last.attempt && attempt.status == "running" {
			err = ErrEventConflict
		}
		if err != nil {
			// A broken clock boundary is not permission to hide a conversation,
			// change execution status, or fabricate a zero duration. Keep the
			// original typed diagnostic while isolating only its timing scope.
			task.ElapsedUnknown, task.ObservationError = true, err
			if !attempt.claimedAt.IsZero() && (task.StartedAt.IsZero() || attempt.claimedAt.Before(task.StartedAt)) {
				task.StartedAt = attempt.claimedAt
			}
			if attempt.revision == last.revision {
				latest.ElapsedUnknown, latest.ObservationError = true, err
				if !attempt.claimedAt.IsZero() && (latest.StartedAt.IsZero() || attempt.claimedAt.Before(latest.StartedAt)) {
					latest.StartedAt = attempt.claimedAt
				}
			}
			continue
		}
		for _, interval := range intervals {
			if task.StartedAt.IsZero() || interval.start.Before(task.StartedAt) {
				task.StartedAt = interval.start
			}
			task.ElapsedUnknown = task.ElapsedUnknown || interval.unknown
			if err := addRunnerActiveInterval(&task.Elapsed, interval); err != nil {
				task.ElapsedUnknown, task.ObservationError = true, err
			}
			if interval.revision == last.revision {
				if latest.StartedAt.IsZero() || interval.start.Before(latest.StartedAt) {
					latest.StartedAt = interval.start
				}
				latest.ElapsedUnknown = latest.ElapsedUnknown || interval.unknown
				if err := addRunnerActiveInterval(&latest.Elapsed, interval); err != nil {
					latest.ElapsedUnknown, latest.ObservationError = true, err
				}
			}
		}
	}
	if latest.StartedAt.IsZero() {
		latest.ElapsedUnknown, latest.ObservationError = true, ErrEventConflict
	}
	if task.StartedAt.IsZero() {
		task.ElapsedUnknown, task.ObservationError = true, ErrEventConflict
	}
	return latest, task, true, nil
}

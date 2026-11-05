package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

const runnerCommunicationScheduleNamespace = "session-runner-communication-schedule"

// This is cadence metadata, never generated prose or scientific state. Scope
// includes branch and input revision so unrelated turns cannot inherit silence.
type sessionRunnerCommunicationSchedule struct {
	mu             sync.Mutex
	LastPublished  time.Time `json:"last_published"`
	CompletedSteps int       `json:"completed_steps"`
	now            func() time.Time
	persist        func(*sessionRunnerCommunicationSchedule) error
}

func (schedule *sessionRunnerCommunicationSchedule) due() bool {
	schedule.mu.Lock()
	defer schedule.mu.Unlock()
	return schedule.LastPublished.IsZero() || schedule.CompletedSteps > 0 && schedule.now().Sub(schedule.LastPublished) >= 90*time.Second
}

func (schedule *sessionRunnerCommunicationSchedule) published() {
	schedule.mu.Lock()
	defer schedule.mu.Unlock()
	schedule.LastPublished = schedule.now().UTC()
	schedule.CompletedSteps = 0
	schedule.save()
}

func (schedule *sessionRunnerCommunicationSchedule) settled() {
	schedule.mu.Lock()
	defer schedule.mu.Unlock()
	if schedule.CompletedSteps < 4 {
		schedule.CompletedSteps++
	}
	schedule.save()
}

func (schedule *sessionRunnerCommunicationSchedule) save() {
	if schedule.persist != nil {
		if err := schedule.persist(schedule); err != nil {
			log.Printf("communication cadence persistence skipped: %v", err)
		}
	}
}

func (s *Server) loadSessionRunnerCommunicationSchedule(ctx context.Context, run *sessionRunnerChatRun) (*sessionRunnerCommunicationSchedule, error) {
	schedule := &sessionRunnerCommunicationSchedule{now: time.Now}
	if s.runtimeStore == nil || run == nil || run.Transcript == nil {
		return schedule, nil
	}
	stream := run.Transcript.Stream
	branch, err := s.transcriptStore.GetBranchState(ctx, stream.UID, stream.OwnerID)
	if err != nil {
		return nil, err
	}
	key := fmt.Sprintf("%s:%s:%d", stream.UID, branch.ActiveBranchID, run.Transcript.Claim.ClaimedInputRevision)
	schedule.persist = func(current *sessionRunnerCommunicationSchedule) error {
		return s.persistSessionRunnerCommunicationMetadata(ctx, runnerCommunicationScheduleNamespace, key, current)
	}
	readCtx, cancel := sessionRunnerCommunicationMetadataContext(ctx)
	entry, found, err := s.runtimeStore.GetContext(readCtx, runnerCommunicationScheduleNamespace, key)
	cancel()
	if err != nil {
		log.Printf("communication cadence restore skipped: %v", err)
		return schedule, nil
	}
	if found {
		encoded, err := json.Marshal(entry.Value)
		if err != nil {
			log.Printf("communication cadence restore skipped: %v", err)
			return schedule, nil
		}
		var restored sessionRunnerCommunicationSchedule
		if err := json.Unmarshal(encoded, &restored); err != nil {
			log.Printf("communication cadence restore skipped: %v", err)
			return schedule, nil
		}
		schedule.LastPublished, schedule.CompletedSteps = restored.LastPublished, restored.CompletedSteps
	}
	return schedule, nil
}

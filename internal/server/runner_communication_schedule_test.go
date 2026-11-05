package server

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCommunicationScheduleRequestsUpdateAfterOneLongOperation(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	schedule := &sessionRunnerCommunicationSchedule{now: func() time.Time { return now }}
	schedule.published()
	now = now.Add(5 * time.Minute)
	if schedule.due() {
		t.Fatal("elapsed time alone must not invent progress")
	}
	schedule.settled()
	if !schedule.due() {
		t.Fatal("a completed long operation must not wait for three more tool calls")
	}
}

func TestCommunicationScheduleSurvivesRestoreWithoutRepeatingIntroduction(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	schedule := &sessionRunnerCommunicationSchedule{now: func() time.Time { return now }}
	if !schedule.due() {
		t.Fatal("initial explanation not due")
	}
	schedule.published()
	for range 4 {
		schedule.settled()
	}
	encoded, err := json.Marshal(schedule)
	if err != nil {
		t.Fatal(err)
	}
	restored := &sessionRunnerCommunicationSchedule{now: func() time.Time { return now }}
	if err := json.Unmarshal(encoded, restored); err != nil {
		t.Fatal(err)
	}
	if restored.due() {
		t.Fatal("restore restarted cadence")
	}
	now = now.Add(30 * time.Second)
	if restored.due() {
		t.Fatal("optional transition cadence mismatch")
	}
	now = now.Add(60 * time.Second)
	if !restored.due() {
		t.Fatal("progress update not due after evidence and time")
	}
	restored.published()
	now = now.Add(time.Hour)
	if restored.due() {
		t.Fatal("idle time without completed actions created progress")
	}
}

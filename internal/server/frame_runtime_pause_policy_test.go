package server

import (
	"testing"

	transcriptstore "x-science/internal/persistence/transcript"
)

func TestRunnerInterruptionPauseProjectionUsesResumeAuthority(t *testing.T) {
	for _, test := range []struct {
		name                     string
		autoResume, resumable    bool
		matching, modelSelection bool
		want                     bool
	}{
		{name: "manual nonresumable interruption is not a pause"},
		{name: "manual mismatched checkpoint is not a pause", resumable: true},
		{name: "manual matching resumable checkpoint remains paused", resumable: true, matching: true, want: true},
		{name: "model selection retains fresh input recovery", modelSelection: true, want: true},
		{name: "model selection matching checkpoint remains paused", modelSelection: true, resumable: true, matching: true, want: true},
		{name: "automatic matching checkpoint remains scheduled", autoResume: true, resumable: true, matching: true},
		{name: "automatic missing checkpoint keeps legacy attention", autoResume: true, want: true},
		{name: "automatic mismatched checkpoint keeps legacy attention", autoResume: true, resumable: true, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			interruption := transcriptstore.RunnerInterruption{CheckpointSequence: 7, AutoResume: test.autoResume}
			checkpoint := transcriptstore.RunnerCheckpoint{Sequence: 6}
			if test.matching {
				checkpoint.Sequence = 7
			}
			if got := runnerInterruptionProjectsPaused(interruption, checkpoint, test.resumable, test.modelSelection); got != test.want {
				t.Fatalf("pause=%t, want %t", got, test.want)
			}
		})
	}
}

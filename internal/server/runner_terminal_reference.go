package server

import transcriptstore "x-science/internal/persistence/transcript"

func (result SessionRunnerCycleResult) transcriptTerminalReference() *transcriptstore.RunnerTerminalReference {
	if result.transcriptAuthority == nil || result.FinishEventID <= 0 {
		return nil
	}
	return &transcriptstore.RunnerTerminalReference{
		Claim: result.transcriptAuthority.Claim, EventID: result.FinishEventID,
	}
}

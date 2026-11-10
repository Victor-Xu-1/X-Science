package server

import (
	"fmt"
	"strings"
	"time"
)

// Fixed internal stages only: timing never keys storage by user, resource,
// SQL, or payload data. The owning diagnostic's mutex protects accumulation.
var transcriptWebDiagnosticStageOrder = [...]transcriptWebReadStage{
	webReadAuthentication, webReadIdentity, webReadAccess, webReadFence,
	webReadCatchupLeader, webReadCatchupWait, webReadIncremental, webReadFull,
	webReadMetadata, webReadRecords, webReadCommit, webReadPage,
	webReadSanitize, webReadEnrich, webReadDetail, webReadResponse,
}

type transcriptWebStageTimings [len(transcriptWebDiagnosticStageOrder)]time.Duration

func (timings *transcriptWebStageTimings) add(stage transcriptWebReadStage, elapsed time.Duration) {
	if elapsed <= 0 {
		return
	}
	for index, known := range transcriptWebDiagnosticStageOrder {
		if stage == known {
			timings[index] += elapsed
			return
		}
	}
}

func (timings transcriptWebStageTimings) milliseconds(stage transcriptWebReadStage) float64 {
	for index, known := range transcriptWebDiagnosticStageOrder {
		if stage == known {
			return float64(timings[index].Microseconds()) / 1000
		}
	}
	return 0
}

func (timings transcriptWebStageTimings) requestFields() string {
	var fields strings.Builder
	for index, stage := range transcriptWebDiagnosticStageOrder {
		if index > 0 {
			fields.WriteByte(' ')
		}
		fmt.Fprintf(&fields, "%s_ms=%.3f", stage, float64(timings[index].Microseconds())/1000)
	}
	return fields.String()
}

func (timings transcriptWebStageTimings) flightFields() string {
	return fmt.Sprintf("lookup_ms=%.3f incremental_ms=%.3f full_ms=%.3f metadata_ms=%.3f records_ms=%.3f commit_ms=%.3f",
		timings.milliseconds(webReadCatchupLeader), timings.milliseconds(webReadIncremental),
		timings.milliseconds(webReadFull), timings.milliseconds(webReadMetadata),
		timings.milliseconds(webReadRecords), timings.milliseconds(webReadCommit))
}

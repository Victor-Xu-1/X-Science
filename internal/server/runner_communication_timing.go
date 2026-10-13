package server

import "time"

// Timing belongs to one observer delegate call. It is not provider dispatch,
// response-header arrival, or browser paint. No response data is retained here.
type runnerCommunicationTiming struct {
	now               func() time.Time
	started           time.Time
	nativeContent     time.Time
	toolBoundary      time.Time
	responseFinished  time.Time
	progressReady     time.Time
	presentationReady time.Time
	publication       time.Time
	progressSource    string
}

func newRunnerCommunicationTiming(now func() time.Time) *runnerCommunicationTiming {
	if now == nil {
		now = time.Now
	}
	return &runnerCommunicationTiming{now: now, started: now().UTC()}
}

func (timing *runnerCommunicationTiming) first(destination *time.Time) {
	if destination.IsZero() {
		*destination = timing.now().UTC()
	}
}

func (timing *runnerCommunicationTiming) noteNativeContent() {
	if timing != nil {
		timing.first(&timing.nativeContent)
	}
}

func (timing *runnerCommunicationTiming) noteToolBoundary() {
	if timing != nil {
		timing.first(&timing.toolBoundary)
	}
}

func (timing *runnerCommunicationTiming) noteResponseFinished() {
	if timing != nil {
		timing.first(&timing.responseFinished)
	}
}

// These hooks observe the existing synchronous publication path. Invalid or
// absent sources never acquire a readiness timestamp; timestamps authorize no
// publication, translation, tool execution, or model retry.
func (observer *sessionRunnerCommunicationObserver) noteProgressReady(source string) {
	if observer == nil || observer.timing == nil || !observer.timing.progressReady.IsZero() {
		return
	}
	if source != "native" && source != "structured" {
		return
	}
	observer.timing.first(&observer.timing.progressReady)
	observer.timing.progressSource = source
}

func (observer *sessionRunnerCommunicationObserver) notePresentationReady() {
	if observer != nil && observer.timing != nil {
		observer.timing.first(&observer.timing.presentationReady)
	}
}

// Call only after the durable publication boundary succeeds. This confirms
// persistence, not WebSocket delivery or a browser rendering the content.
func (observer *sessionRunnerCommunicationObserver) notePublication() {
	if observer != nil && observer.timing != nil {
		observer.timing.first(&observer.timing.publication)
	}
}

func (timing *runnerCommunicationTiming) addTo(record map[string]any) {
	if timing == nil || record == nil {
		return
	}
	for name, observed := range map[string]time.Time{
		"observer_request_started_at": timing.started,
		"first_native_content_at":     timing.nativeContent,
		"first_tool_boundary_at":      timing.toolBoundary,
		"response_finished_at":        timing.responseFinished,
		"first_progress_ready_at":     timing.progressReady,
		"presentation_ready_at":       timing.presentationReady,
		"first_publication_at":        timing.publication,
	} {
		if !observed.IsZero() {
			record[name] = observed.UTC().Format(time.RFC3339Nano)
		}
	}
	if !timing.progressReady.IsZero() {
		record["progress_ready_source"] = timing.progressSource
	}
}

package workspace

import "errors"

var (
	ErrFrameResumePrecondition = errors.New("resume command and original target are required")
	ErrFrameResumeTargetStale  = errors.New("resume target changed; refresh before a new command")
	ErrFrameResumeCommandUsed  = errors.New("resume command conflicts with its accepted request")
	ErrFrameResumeCommandBusy  = errors.New("resume dispatch is owned; retry the original command")
)

// CompatibilityFrameResumeTarget is a derived optimistic precondition, not
// permission to run. Null IDs describe an unfenced or not-yet-created dispatch.
type CompatibilityFrameResumeTarget struct {
	ResumeEventID *string `json:"resume_event_id"`
	WaitRef       *string `json:"wait_ref"`
	StateRef      string  `json:"state_ref"`
}

// ActorID comes only from authenticated server context. The client supplies
// one command ID and retains its exact target and controls across retries.
type CompatibilityFrameContinueCommand struct {
	ActorID         string
	ClientCommandID string
	Target          CompatibilityFrameResumeTarget
}

type CompatibilityFrameContinueReceipt struct {
	ID            string                         `json:"id"`
	Target        CompatibilityFrameResumeTarget `json:"target"`
	ResumeEventID string                         `json:"resume_event_id"`
	WakeEventID   string                         `json:"wake_event_id,omitempty"`
	Outcome       string                         `json:"outcome"`
	Replayed      bool                           `json:"replayed"`
}

package server

// An optional immutable admission fence shared by maintenance dispatch and
// delegated child dispatch. The final canonical claim transaction validates it.
type runnerInputClaimTarget struct {
	MessageID          string
	Revision           int64
	FrameIncarnationID string
	StreamEpoch        int64
}

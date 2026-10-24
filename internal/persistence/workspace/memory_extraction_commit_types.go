package workspace

import (
	"fmt"

	transcriptstore "x-science/internal/persistence/transcript"
)

// CompletedExtractionSource is server-captured provenance, never model output.
// Exactly one history authority is present. Legacy commits additionally hold
// the journal's strict snapshot guard until the SQLite transaction completes.
type CompletedExtractionSource struct {
	Schema      int
	OwnerUserID string
	ProjectID   string
	FrameID     string
	Transcript  *transcriptstore.CompletedMemorySource
	Legacy      *LegacyExtractionSource
}

type LegacyExtractionSource struct {
	SessionID          string
	FrameIncarnationID string
	FinishEventID      int64
	PrefixSHA256       string
}

type ExtractionOperationFailure struct {
	Kind  string
	ID    string
	Error string
}

func (failure ExtractionOperationFailure) String() string {
	if failure.ID == "" {
		return fmt.Sprintf("%s: %s", failure.Kind, failure.Error)
	}
	return fmt.Sprintf("%s %s: %s", failure.Kind, failure.ID, failure.Error)
}

type ExtractionApplyResult struct {
	Appended      []string
	Replaced      []string
	Removed       []string
	PIUnavailable bool
	PIRejected    int
	Failures      []ExtractionOperationFailure
}

type ExtractionReplacement struct {
	Expected Memory
	NewID    string
	Body     string
	Evidence string
}

// ExtractionRemoval fences the exact fact inspected during preparation. A
// later in-place edit must not be removed using a stale classification.
type ExtractionRemoval struct {
	Expected Memory
}

// ExtractionMutationPlan contains only fully prepared operations. No classifier,
// repairer, model callback or mutable request is retained across commit.
type ExtractionMutationPlan struct {
	Append   []AgentMemoryAppendMutation
	Replace  []ExtractionReplacement
	Remove   []ExtractionRemoval
	Prepared ExtractionApplyResult
}

// LegacyExtractionOrderCheck reads only the already-held strict journal
// snapshot. It must not acquire another journal lock or read workspace DB.
// It is host-only and does not participate in any persisted source/digest.
type LegacyExtractionOrderCheck func(CompletedExtractionSource) (bool, error)

type ExtractionCommitInput struct {
	LegacyOrderCheck LegacyExtractionOrderCheck `json:"-"`
	DeliveryClaim    *MemoryExtractionDeliveryClaim
	Source           CompletedExtractionSource
	InputSHA256      string
	CursorBefore     int
	CursorNext       int
	AdvanceCursor    bool
	Overshoot        bool
	DeltaCount       int
	Gate             string
	Plan             ExtractionMutationPlan
}

// ExtractionCommitResult is the original durable outcome. Replayed describes
// the current read/commit attempt and is not part of the saved outcome.
type ExtractionCommitResult struct {
	DeliverySettled bool `json:"-"`
	Gate            string
	InputSHA256     string
	CursorBefore    int
	CursorAfter     int
	CursorAdvanced  bool
	Overshoot       bool
	DeltaCount      int
	Applied         ExtractionApplyResult
	Replayed        bool
}

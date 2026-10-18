package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const ManualKernelExecutionEventType = "kernel_manual_execution"

var (
	ErrManualKernelExecutionInvalid  = errors.New("invalid manual kernel execution")
	ErrManualKernelExecutionConflict = errors.New("manual kernel execution identity conflicts")
	ErrManualKernelExecutionStale    = errors.New("manual kernel execution state changed")
)

type ManualKernelRequest struct {
	RequestID                string `json:"request_id"`
	Source                   string `json:"source"`
	Language                 string `json:"language"`
	Environment              string `json:"environment"`
	KernelID                 string `json:"kernel_id,omitempty"`
	ExpectedKernelGeneration uint64 `json:"expected_kernel_generation,omitempty"`
	TimeoutMillis            int64  `json:"timeout_ms"`
}

// Binding is supplied by the registered Manager handle, never by the guest.
type ManualKernelBinding struct {
	ExecutionID        string `json:"execution_id"`
	OwnerID            string `json:"owner_id"`
	ProjectID          string `json:"project_id"`
	RootFrameID        string `json:"root_frame_id"`
	RootIncarnationID  string `json:"root_incarnation_id"`
	FrameID            string `json:"frame_id"`
	FrameIncarnationID string `json:"frame_incarnation_id"`
	KernelID           string `json:"kernel_id"`
	KernelGeneration   uint64 `json:"kernel_generation"`
	KernelKind         string `json:"kernel_kind"`
	ServerBootID       string `json:"server_boot_id"`
}

type ManualKernelExecution struct {
	Version int    `json:"version"`
	Origin  string `json:"origin"`
	ManualKernelBinding
	Request           ManualKernelRequest    `json:"request"`
	RequestSHA256     string                 `json:"request_sha256"`
	State             string                 `json:"state"`
	StateVersion      int64                  `json:"state_version"`
	AcceptedAt        time.Time              `json:"accepted_at"`
	StartedAt         *time.Time             `json:"started_at,omitempty"`
	CancelRequestedAt *time.Time             `json:"cancel_requested_at,omitempty"`
	FinishedAt        *time.Time             `json:"finished_at,omitempty"`
	Disposition       string                 `json:"disposition,omitempty"`
	ResultSHA256      string                 `json:"result_sha256,omitempty"`
	ExecutionLogID    string                 `json:"execution_log_id,omitempty"`
	Rejection         *ManualKernelRejection `json:"rejection,omitempty"`
}

// A worker may reject syntax/import preflight without starting a cell. Keep
// that real diagnostic in the lifecycle receipt, not an invented execution log.
type ManualKernelRejection struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

type AdmitManualKernelExecutionInput struct {
	Binding ManualKernelBinding
	Request ManualKernelRequest
}

type ManualKernelCompletionInput struct {
	Expected    ManualKernelExecution
	Disposition string
	FinishedAt  time.Time
	Log         *ExecutionLogRecord
	Rejection   *ManualKernelRejection
}

func ManualKernelExecutionID(owner, incarnation, requestID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("x-science:manual-kernel:"+owner+"\x00"+incarnation+"\x00"+requestID)).String()
}

func manualKernelEventID(executionID string) string { return "kernel-manual:" + executionID }

func manualKernelRequestDigest(request ManualKernelRequest) (string, error) {
	requestID, parseErr := uuid.Parse(request.RequestID)
	if parseErr != nil || requestID.String() != request.RequestID || !utf8.ValidString(request.Source) ||
		strings.TrimSpace(request.Source) == "" || len(request.Source) > 100000 || request.TimeoutMillis <= 0 || request.TimeoutMillis > math.MaxInt64/int64(time.Millisecond) ||
		(request.Language != "python" && request.Language != "r") || request.Environment == "" || len(request.Environment) > 100 ||
		strings.ContainsAny(request.Environment, "/\\\x00\r\n\t ") || (request.KernelID == "") != (request.ExpectedKernelGeneration == 0) {
		return "", ErrManualKernelExecutionInvalid
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]), nil
}

func validateManualKernelExecution(record ManualKernelExecution) error {
	digest, err := manualKernelRequestDigest(record.Request)
	if err != nil || digest != record.RequestSHA256 || record.Version != 1 || record.Origin != "user" || record.StateVersion <= 0 || record.StateVersion >= 9007199254740991 || record.AcceptedAt.IsZero() {
		return ErrManualKernelExecutionInvalid
	}
	for _, value := range []string{record.ExecutionID, record.OwnerID, record.ProjectID, record.RootFrameID, record.RootIncarnationID, record.FrameID, record.FrameIncarnationID, record.KernelID, record.KernelKind, record.ServerBootID} {
		if !validDetachedIdentity(value) {
			return ErrManualKernelExecutionInvalid
		}
	}
	if record.KernelGeneration == 0 || record.KernelGeneration > 9007199254740991 || record.ExecutionID != ManualKernelExecutionID(record.OwnerID, record.FrameIncarnationID, record.Request.RequestID) ||
		record.Request.KernelID != "" && (record.Request.KernelID != record.KernelID || record.Request.ExpectedKernelGeneration != record.KernelGeneration) {
		return ErrManualKernelExecutionConflict
	}
	if record.State != "terminal" && (record.ResultSHA256 != "" || record.ExecutionLogID != "") {
		return ErrManualKernelExecutionInvalid
	}
	if record.Rejection != nil && (record.State != "terminal" || record.Disposition != "rejected_before_start" || !utf8.ValidString(record.Rejection.Stdout) || !utf8.ValidString(record.Rejection.Stderr)) {
		return ErrManualKernelExecutionInvalid
	}
	if record.State != "terminal" && record.State != "outcome_unknown" && record.Disposition != "" {
		return ErrManualKernelExecutionInvalid
	}
	if len(record.Disposition) > 256 || !utf8.ValidString(record.Disposition) {
		return ErrManualKernelExecutionInvalid
	}
	if record.ResultSHA256 != "" {
		digest, err := hex.DecodeString(record.ResultSHA256)
		if err != nil || len(digest) != 32 {
			return ErrManualKernelExecutionInvalid
		}
	}
	switch record.State {
	case "queued":
		if record.StartedAt != nil || record.FinishedAt != nil || record.CancelRequestedAt != nil {
			return ErrManualKernelExecutionInvalid
		}
	case "started":
		if record.StartedAt == nil || record.FinishedAt != nil || record.CancelRequestedAt != nil {
			return ErrManualKernelExecutionInvalid
		}
	case "cancel_requested":
		if record.CancelRequestedAt == nil || record.FinishedAt != nil {
			return ErrManualKernelExecutionInvalid
		}
	case "outcome_unknown":
		if record.FinishedAt != nil || record.ExecutionLogID != "" || record.Disposition == "" {
			return ErrManualKernelExecutionInvalid
		}
	case "terminal":
		if record.FinishedAt == nil || record.ResultSHA256 == "" {
			return ErrManualKernelExecutionInvalid
		}
		switch record.Disposition {
		case "cancelled_before_start", "rejected_before_start":
			if record.StartedAt != nil || record.ExecutionLogID != "" {
				return ErrManualKernelExecutionInvalid
			}
		case "completed", "failed", "cancelled":
			if record.StartedAt == nil || record.ExecutionLogID != record.ExecutionID {
				return ErrManualKernelExecutionInvalid
			}
		default:
			return ErrManualKernelExecutionInvalid
		}
	default:
		return ErrManualKernelExecutionInvalid
	}
	return nil
}

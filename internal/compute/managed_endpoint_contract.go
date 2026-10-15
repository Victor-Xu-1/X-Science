package compute

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const ManagedEndpointOwnerProtocol = 1

var (
	ErrManagedEndpointOwnershipUnknown = errors.New("managed endpoint ownership is unverified; explicit owner adoption is required")
	ErrManagedEndpointStaleAction      = errors.New("managed endpoint action no longer owns its claim")
	ErrManagedEndpointOutcomeUnknown   = errors.New("managed endpoint action outcome is unknown; start was not replayed")
	ErrManagedEndpointCorruptOwner     = errors.New("managed endpoint owner record is corrupt")
)

// ManagedEndpointOwnerSpec is part of the approved registration. A nil spec
// preserves the historical registration bytes and is not evidence of ownership.
type ManagedEndpointOwnerSpec struct {
	Version        int                             `json:"version"`
	Kind           string                          `json:"kind"`
	Output         ManagedEndpointOutputPolicy     `json:"output"`
	Docker         *ManagedEndpointDockerSpec      `json:"docker,omitempty"`
	Adopt          *ManagedEndpointRuntimeIdentity `json:"adopt,omitempty"`
	LegacyApproval *ManagedEndpointLegacyApproval  `json:"legacyApproval,omitempty"`
}

type ManagedEndpointLegacyApproval struct {
	Registration ManagedEndpointRegistration `json:"registration"`
	Hash         string                      `json:"hash"`
}

type ManagedEndpointOutputPolicy struct {
	SegmentBytes int64 `json:"segmentBytes"`
	Segments     int   `json:"segments"`
}

type ManagedEndpointDockerSpec struct {
	Context   string `json:"context"`
	Container string `json:"container"`
}

// Runtime identity is a positive observation, not a locator. Stop action
// generations never replace the service instance which this identity denotes.
type ManagedEndpointRuntimeIdentity struct {
	Kind         string `json:"kind"`
	BootID       string `json:"bootId,omitempty"`
	Unit         string `json:"unit,omitempty"`
	InvocationID string `json:"invocationId,omitempty"`
	ControlGroup string `json:"controlGroup,omitempty"`
	PID          int64  `json:"pid,omitempty"`
	StartTicks   uint64 `json:"startTicks,omitempty"`
	DaemonID     string `json:"daemonId,omitempty"`
	ContainerID  string `json:"containerId,omitempty"`
}

func ManagedEndpointUnitName(instanceID string) string {
	return "x-science-endpoint-" + instanceID + ".service"
}

func validateManagedEndpointPhysicalIdentity(identity ManagedEndpointRuntimeIdentity) error {
	switch identity.Kind {
	case "native-unit":
		if len(identity.Unit) < len("a.service") || len(identity.Unit) > 255 || strings.HasPrefix(identity.Unit, "-") || !strings.HasSuffix(identity.Unit, ".service") || !validManagedOwnerComponent(identity.Unit) || identity.BootID == "" || len(identity.BootID) > 64 || len(identity.InvocationID) != 32 || identity.ControlGroup == "/" || !strings.HasPrefix(identity.ControlGroup, "/") || filepath.Clean(identity.ControlGroup) != identity.ControlGroup || strings.ContainsRune(identity.ControlGroup, '\x00') || identity.PID < 1 || identity.StartTicks < 1 || identity.ContainerID != "" || identity.DaemonID != "" {
			return errors.New("native endpoint owner identity is incomplete or foreign")
		}
		for _, c := range identity.InvocationID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return errors.New("native endpoint invocation identity is invalid")
			}
		}
	case "docker":
		if identity.DaemonID == "" || len(identity.DaemonID) > 256 || len(identity.ContainerID) != 64 || identity.BootID != "" || identity.Unit != "" || identity.InvocationID != "" || identity.ControlGroup != "" || identity.PID != 0 || identity.StartTicks != 0 {
			return errors.New("Docker endpoint owner identity is incomplete or mixed")
		}
		for _, c := range identity.ContainerID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return errors.New("Docker endpoint container identity is invalid")
			}
		}
	default:
		return errors.New("unknown managed endpoint physical owner kind")
	}
	return nil
}

func (identity ManagedEndpointRuntimeIdentity) ValidateFor(instance ManagedEndpointOwnerInstance) error {
	if instance.Registration.Owner == nil || identity.Kind != instance.Registration.Owner.Kind {
		return errors.New("managed endpoint physical owner kind does not match")
	}
	if err := validateManagedEndpointPhysicalIdentity(identity); err != nil {
		return err
	}
	switch identity.Kind {
	case "native-unit":
		expectedUnit := ManagedEndpointUnitName(instance.ID)
		if adopt := instance.Registration.Owner.Adopt; adopt != nil {
			expectedUnit = adopt.Unit
		}
		if identity.Unit != expectedUnit {
			return errors.New("native endpoint unit does not match its instance")
		}
	case "docker":
		if identity.DaemonID == "" || len(identity.ContainerID) != 64 || identity.BootID != "" || identity.Unit != "" || identity.InvocationID != "" || identity.ControlGroup != "" || identity.PID != 0 || identity.StartTicks != 0 {
			return errors.New("Docker endpoint owner identity is incomplete or mixed")
		}
		for _, c := range identity.ContainerID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return errors.New("Docker endpoint container identity is invalid")
			}
		}
	default:
		return errors.New("unknown managed endpoint physical owner kind")
	}
	if instance.Identity != (ManagedEndpointRuntimeIdentity{}) && instance.Identity != identity {
		return errors.New("managed endpoint physical owner incarnation changed")
	}
	return nil
}

// Positive stopped evidence is distinct from command success. A failed cleanup
// may retire an instance only if this exact physical owner was actually absent.
type ManagedEndpointStoppedProof struct {
	Identity         ManagedEndpointRuntimeIdentity `json:"identity"`
	ObservedAt       time.Time                      `json:"observedAt"`
	State            string                         `json:"state"`
	ControlReachable bool                           `json:"controlReachable"`
	CgroupEmpty      bool                           `json:"cgroupEmpty"`
}

func (proof *ManagedEndpointStoppedProof) ValidateFor(instance ManagedEndpointOwnerInstance) error {
	if proof == nil || !proof.ControlReachable || proof.ObservedAt.IsZero() {
		return errors.New("managed endpoint stopped evidence is missing")
	}
	if err := proof.Identity.ValidateFor(instance); err != nil {
		return err
	}
	switch proof.Identity.Kind {
	case "native-unit":
		if !proof.CgroupEmpty || (proof.State != "inactive" && proof.State != "failed" && proof.State != "absent") {
			return errors.New("native endpoint cgroup is not confirmed empty")
		}
	case "docker":
		if proof.State != "exited" && proof.State != "dead" && proof.State != "absent" {
			return errors.New("Docker endpoint is not confirmed stopped")
		}
	default:
		return errors.New("unknown stopped endpoint owner")
	}
	return nil
}

func (spec *ManagedEndpointOwnerSpec) Validate() error {
	if spec == nil {
		return ErrManagedEndpointOwnershipUnknown
	}
	if spec.Version != ManagedEndpointOwnerProtocol || spec.Output.SegmentBytes < 4096 || spec.Output.SegmentBytes > 8<<20 || spec.Output.Segments < 1 || spec.Output.Segments > 8 {
		return errors.New("managed endpoint owner version or bounded output policy is invalid")
	}
	switch spec.Kind {
	case "native-unit":
		if spec.Docker != nil {
			return errors.New("native endpoint cannot declare a Docker owner")
		}
	case "docker":
		if spec.Docker == nil || !validManagedOwnerComponent(spec.Docker.Context) || !validManagedOwnerComponent(spec.Docker.Container) {
			return errors.New("Docker endpoint requires an explicit context and container locator")
		}
	default:
		return fmt.Errorf("unsupported managed endpoint owner kind %q", spec.Kind)
	}
	if spec.Adopt != nil {
		if spec.Adopt.Kind != spec.Kind {
			return errors.New("managed endpoint adoption kind does not match its owner")
		}
		if err := validateManagedEndpointPhysicalIdentity(*spec.Adopt); err != nil {
			return err
		}
	}
	if spec.LegacyApproval != nil {
		legacy := spec.LegacyApproval
		if spec.Adopt == nil || legacy.Registration.Owner != nil || ApprovedManagedEndpointHash(legacy.Registration) != strings.TrimPrefix(legacy.Hash, "sha256:") {
			return errors.New("legacy endpoint approval does not match its original bytes")
		}
	}
	return nil
}

func validManagedOwnerComponent(value string) bool {
	if value == "" || len(value) > 255 || value == "." || value == ".." || strings.HasPrefix(value, ".") {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

type ManagedEndpointOwnerInstance struct {
	ID              string                         `json:"id"`
	UserID          string                         `json:"userId"`
	Endpoint        string                         `json:"endpoint"`
	Registration    ManagedEndpointRegistration    `json:"registration"`
	ApprovedHash    string                         `json:"approvedHash"`
	StartGeneration int64                          `json:"startGeneration"`
	Identity        ManagedEndpointRuntimeIdentity `json:"identity"`
	RuntimeRoot     string                         `json:"runtimeRoot"`
	ServiceDir      string                         `json:"serviceDir"`
	WorkspacePath   string                         `json:"workspacePath"`
	InitialAction   ManagedEndpointActionRef       `json:"initialAction"`
	WorkerIdentity  ManagedEndpointRuntimeIdentity `json:"workerIdentity"`
	CleanupRef      *ManagedEndpointActionRef      `json:"cleanupRef,omitempty"`
}

func (instance ManagedEndpointOwnerInstance) Validate() error {
	if !validManagedOwnerComponent(instance.ID) || instance.UserID == "" || instance.Endpoint != instance.Registration.Name || instance.StartGeneration < 1 || !filepath.IsAbs(instance.RuntimeRoot) || !filepath.IsAbs(instance.ServiceDir) || !filepath.IsAbs(instance.WorkspacePath) {
		return errors.New("managed endpoint instance authority is invalid")
	}
	if err := instance.Registration.Owner.Validate(); err != nil {
		return err
	}
	registration := instance.Registration
	parsed, err := url.Parse(registration.URL)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Hostname() != "127.0.0.1" || parsed.Port() != strconv.Itoa(registration.Port) || registration.Port < 1 || registration.Port > 65535 || !strings.HasPrefix(registration.LivePath, "/") || strings.TrimSpace(registration.StartScript) == "" || strings.TrimSpace(registration.StopScript) == "" || strings.ContainsRune(registration.StartScript+registration.StopScript+registration.LivePath, '\x00') {
		return errors.New("managed endpoint approved lifecycle contract is invalid")
	}
	if legacy := registration.Owner.LegacyApproval; legacy != nil {
		copy := registration
		copy.Owner = nil
		if ApprovedManagedEndpointHash(copy) != strings.TrimPrefix(legacy.Hash, "sha256:") {
			return errors.New("legacy endpoint adoption changed its original approved lifecycle bytes")
		}
	}
	if ApprovedManagedEndpointHash(instance.Registration) != strings.TrimPrefix(instance.ApprovedHash, "sha256:") {
		return errors.New("managed endpoint owner registration hash drifted")
	}
	if instance.InitialAction.InstanceID != instance.ID || instance.InitialAction.Generation != instance.StartGeneration {
		return errors.New("managed endpoint initial action does not match its instance")
	}
	if err := instance.InitialAction.Validate(); err != nil {
		return err
	}
	if instance.WorkerIdentity != (ManagedEndpointRuntimeIdentity{}) {
		if err := validateManagedEndpointPhysicalIdentity(instance.WorkerIdentity); err != nil {
			return err
		}
		if instance.WorkerIdentity.Kind != "native-unit" || instance.WorkerIdentity.Unit != ManagedEndpointUnitName(instance.ID) {
			return errors.New("managed endpoint worker identity does not match its instance")
		}
	}
	if instance.CleanupRef != nil {
		if err := instance.CleanupRef.Validate(); err != nil {
			return err
		}
		if instance.CleanupRef.InstanceID != instance.ID {
			return errors.New("managed endpoint cleanup fence belongs to another instance")
		}
	}
	if instance.Identity != (ManagedEndpointRuntimeIdentity{}) {
		return instance.Identity.ValidateFor(instance)
	}
	return nil
}

type ManagedEndpointActionRef struct {
	InstanceID  string `json:"instanceId"`
	Kind        string `json:"kind"`
	ClaimHandle string `json:"claimHandle"`
	Generation  int64  `json:"generation"`
}

func (ref ManagedEndpointActionRef) Validate() error {
	if !validManagedOwnerComponent(ref.InstanceID) || ref.ClaimHandle == "" || ref.Generation < 1 || (ref.Kind != "start" && ref.Kind != "stop") {
		return errors.New("managed endpoint action identity is invalid")
	}
	return nil
}

type ManagedEndpointAction struct {
	Ref                ManagedEndpointActionRef     `json:"ref"`
	Accepted           bool                         `json:"accepted"`
	CancelRequested    bool                         `json:"cancelRequested"`
	CleanupPending     bool                         `json:"cleanupPending,omitempty"`
	ObservationFailure string                       `json:"observationFailure,omitempty"`
	Receipt            *ManagedEndpointOwnerReceipt `json:"receipt,omitempty"`
}

type ManagedEndpointOwnerReceipt struct {
	Ref                  ManagedEndpointActionRef       `json:"ref"`
	Accepted             bool                           `json:"accepted"`
	ProcessExited        bool                           `json:"processExited"`
	NoScript             bool                           `json:"noScript,omitempty"`
	NotStarted           bool                           `json:"notStarted,omitempty"`
	NoOwnerCreated       bool                           `json:"noOwnerCreated,omitempty"`
	WorkerStoppedProof   *ManagedEndpointStoppedProof   `json:"workerStoppedProof,omitempty"`
	CleanupProcessExited bool                           `json:"cleanupProcessExited,omitempty"`
	CleanupExitCode      *int                           `json:"cleanupExitCode,omitempty"`
	CleanupFailure       string                         `json:"cleanupFailure,omitempty"`
	ExitCode             *int                           `json:"exitCode,omitempty"`
	OutputClosed         bool                           `json:"outputClosed"`
	OutputSequence       uint64                         `json:"outputSequence"`
	OutputDroppedBytes   uint64                         `json:"outputDroppedBytes"`
	OutputWarning        string                         `json:"outputWarning,omitempty"`
	Transcript           string                         `json:"transcript"`
	Identity             ManagedEndpointRuntimeIdentity `json:"identity"`
	OwnerVerified        bool                           `json:"ownerVerified"`
	Readiness            bool                           `json:"readiness"`
	Stopped              bool                           `json:"stopped"`
	StoppedProof         *ManagedEndpointStoppedProof   `json:"stoppedProof,omitempty"`
	Failure              string                         `json:"failure,omitempty"`
	Duration             time.Duration                  `json:"duration"`
}

func (receipt ManagedEndpointOwnerReceipt) Validate(ref ManagedEndpointActionRef) error {
	if receipt.Ref != ref || len(receipt.Transcript) > maxTranscriptBytes {
		return errors.New("managed endpoint terminal receipt is incomplete or foreign")
	}
	if receipt.Failure != "" {
		return errors.New(receipt.Failure)
	}
	if !receipt.Accepted && !receipt.NoScript {
		return errors.New("managed endpoint execution acceptance is missing")
	}
	if receipt.NoScript {
		if receipt.ProcessExited || receipt.ExitCode != nil {
			return errors.New("observed-only endpoint action cannot claim a script exit")
		}
	} else if !receipt.ProcessExited || receipt.ExitCode == nil {
		return errors.New("managed endpoint action exit is missing")
	}
	if receipt.ExitCode != nil && *receipt.ExitCode != 0 {
		return fmt.Errorf("managed endpoint action exited %d", *receipt.ExitCode)
	}
	if !receipt.OwnerVerified {
		return ErrManagedEndpointOwnershipUnknown
	}
	if ref.Kind == "start" && !receipt.Readiness {
		return errors.New("managed endpoint readiness is unconfirmed")
	}
	if ref.Kind == "stop" && !receipt.Stopped {
		return errors.New("managed endpoint stop is unconfirmed")
	}
	return nil
}

func (receipt ManagedEndpointOwnerReceipt) ValidateFor(instance ManagedEndpointOwnerInstance, ref ManagedEndpointActionRef) error {
	if err := receipt.Validate(ref); err != nil {
		return err
	}
	if receipt.NoScript && ref.Kind == "start" && instance.Registration.Owner.Adopt == nil {
		return errors.New("a new endpoint start requires its approved script execution")
	}
	if err := receipt.Identity.ValidateFor(instance); err != nil {
		return err
	}
	if ref.Kind == "stop" {
		return receipt.StoppedProof.ValidateFor(instance)
	}
	return nil
}

func (receipt ManagedEndpointOwnerReceipt) HasPositiveStoppedProof(instance ManagedEndpointOwnerInstance) bool {
	return receipt.Stopped && receipt.OwnerVerified && receipt.StoppedProof != nil && receipt.Identity == receipt.StoppedProof.Identity && receipt.StoppedProof.ValidateFor(instance) == nil
}

type ManagedEndpointClaim struct {
	UserID       string
	Name         string
	Handle       string
	Generation   int64
	Registration ManagedEndpointRegistration
	ApprovedHash string
	ServiceDir   string
}

// Store owns intentions and the existing endpoint state. An adapter cannot
// commit live/stopped, and this contract does not add a second scheduler.
type ManagedEndpointOwnerStore interface {
	PrepareManagedEndpointAction(context.Context, ManagedEndpointClaim, string, string) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error)
	AcceptManagedEndpointAction(context.Context, ManagedEndpointActionRef, ManagedEndpointRuntimeIdentity) (bool, error)
	ReadManagedEndpointAction(context.Context, ManagedEndpointActionRef) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error)
	CurrentManagedEndpointAction(context.Context, string) (ManagedEndpointOwnerInstance, ManagedEndpointAction, error)
	RecordManagedEndpointObservationFailure(context.Context, ManagedEndpointActionRef, error) error
	CancelManagedEndpointAction(context.Context, ManagedEndpointActionRef) (bool, error)
	AcquireManagedEndpointCleanup(context.Context, ManagedEndpointActionRef, *ManagedEndpointOwnerReceipt) (bool, error)
	ReserveManagedEndpointNoOwnerCleanup(context.Context, ManagedEndpointActionRef) (bool, error)
	SettleSupersededManagedEndpointAction(context.Context, ManagedEndpointActionRef, *ManagedEndpointOwnerReceipt) (bool, error)
	FinishManagedEndpointAction(context.Context, ManagedEndpointActionRef, ManagedEndpointOwnerReceipt) (string, error)
	PendingManagedEndpointActions(context.Context, *ManagedEndpointActionRef, int) ([]ManagedEndpointActionRef, error)
	ReadManagedEndpointLiveOwner(context.Context, string, string) (ManagedEndpointOwnerInstance, error)
}

type ManagedEndpointOwnerRuntime interface {
	Start(context.Context, ManagedEndpointOwnerInstance, ManagedEndpointAction, map[string]string) (ManagedEndpointOwnerReceipt, error)
	Stop(context.Context, ManagedEndpointOwnerInstance, ManagedEndpointAction, map[string]string) (ManagedEndpointOwnerReceipt, error)
	Observe(context.Context, ManagedEndpointOwnerInstance, ManagedEndpointAction) (ManagedEndpointOwnerReceipt, error)
	Cancel(context.Context, ManagedEndpointOwnerInstance, ManagedEndpointAction, map[string]string) (ManagedEndpointOwnerReceipt, error)
	Check(context.Context, ManagedEndpointOwnerInstance) error
}

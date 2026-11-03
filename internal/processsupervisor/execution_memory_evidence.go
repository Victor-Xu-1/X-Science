package processsupervisor

import (
	"errors"
	"math"
	"time"
)

// MemoryEvidenceValue distinguishes measured zero, unlimited and unavailable.
type MemoryEvidenceValue struct {
	Kind   string  `json:"kind"`
	Bytes  *uint64 `json:"bytes,omitempty"`
	Reason string  `json:"reason,omitempty"`
}

type MemoryEvidenceSample struct {
	At      time.Time           `json:"at"`
	Current MemoryEvidenceValue `json:"current"`
	Max     MemoryEvidenceValue `json:"max"`
	High    MemoryEvidenceValue `json:"high"`
	SwapMax MemoryEvidenceValue `json:"swap_max"`
}

type MemoryEvidenceCounter struct {
	Begin      *uint64 `json:"begin,omitempty"`
	End        *uint64 `json:"end,omitempty"`
	Delta      *uint64 `json:"delta,omitempty"`
	Continuity string  `json:"continuity"`
}

type MemoryEvidenceEvents struct {
	OOM     MemoryEvidenceCounter `json:"oom"`
	OOMKill MemoryEvidenceCounter `json:"oom_kill"`
	Max     MemoryEvidenceCounter `json:"max"`
	High    MemoryEvidenceCounter `json:"high"`
}

// ExecutionMemoryEvidenceV1 is host-only result data, never decoded from the
// arbitrary guest Response.Trace. Domain/window counters are not child causes.
type ExecutionMemoryEvidenceV1 struct {
	Version         int                   `json:"version"`
	Status          string                `json:"status"`
	Reason          string                `json:"reason,omitempty"`
	Scope           string                `json:"scope,omitempty"`
	Window          string                `json:"window,omitempty"`
	Begin           *MemoryEvidenceSample `json:"begin,omitempty"`
	End             *MemoryEvidenceSample `json:"end,omitempty"`
	Events          *MemoryEvidenceEvents `json:"events,omitempty"`
	PressureFailure *MemoryPressure       `json:"pressure_failure,omitempty"`
}

func UnavailableExecutionMemoryEvidence(reason string) ExecutionMemoryEvidenceV1 {
	return ExecutionMemoryEvidenceV1{Version: 1, Status: "unavailable", Reason: reason}
}

func (e ExecutionMemoryEvidenceV1) Validate() error {
	if e.Version != 1 || (e.PressureFailure != nil && !ValidHostMemoryPressure(*e.PressureFailure)) {
		return errors.New("host memory evidence version or pressure receipt is invalid")
	}
	switch e.Status {
	case "unavailable":
		if !validMemoryEvidenceReason(e.Reason) || e.Scope != "" || e.Window != "" || e.Begin != nil || e.End != nil || e.Events != nil {
			return errors.New("unavailable host memory evidence is invalid")
		}
	case "observed":
		if e.Reason != "" || e.Scope != "workload_domain" || e.Window != "dispatch_to_settlement" ||
			e.Begin == nil || e.End == nil || e.Events == nil || !e.Begin.valid() || !e.End.valid() || e.End.At.Before(e.Begin.At) ||
			!e.Events.OOM.valid() || !e.Events.OOMKill.valid() || !e.Events.Max.valid() || !e.Events.High.valid() {
			return errors.New("observed host memory evidence is invalid")
		}
	default:
		return errors.New("host memory evidence status is unsupported")
	}
	return nil
}

func ValidHostMemoryPressure(p MemoryPressure) bool {
	return p.Status == "pressured" && p.LimitBytes > 0 && !math.IsNaN(p.FullStallPercent) &&
		!math.IsInf(p.FullStallPercent, 0) && p.FullStallPercent >= 0 && p.FullStallPercent <= 100
}

func (v MemoryEvidenceValue) valid(allowUnlimited bool) bool {
	switch v.Kind {
	case "bytes":
		return v.Bytes != nil && v.Reason == ""
	case "unlimited":
		return allowUnlimited && v.Bytes == nil && v.Reason == ""
	case "unavailable":
		return v.Bytes == nil && validMemoryEvidenceReason(v.Reason)
	}
	return false
}

func (s MemoryEvidenceSample) valid() bool {
	return !s.At.IsZero() && s.Current.valid(false) && s.Max.valid(true) && s.High.valid(true) && s.SwapMax.valid(true)
}

func (c MemoryEvidenceCounter) valid() bool {
	switch c.Continuity {
	case "continuous":
		return c.Begin != nil && c.End != nil && c.Delta != nil && *c.End >= *c.Begin && *c.Delta == *c.End-*c.Begin
	case "reset":
		return c.Begin != nil && c.End != nil && c.Delta == nil && *c.End < *c.Begin
	case "unavailable":
		return c.Delta == nil && (c.Begin == nil || c.End == nil)
	}
	return false
}

func validMemoryEvidenceReason(reason string) bool {
	switch reason {
	case "legacy_v1", "no_sampler", "no_owned_domain", "unsupported_platform", "unproven_container_domain", "read_failed":
		return true
	}
	return false
}

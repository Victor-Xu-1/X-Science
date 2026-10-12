// Package namespace binds historical-format reads to frozen, owner-scoped
// compute receipts. It does not start jobs, mutate remote files or own storage.
package namespace

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
)

var ErrRecoveryUnconfirmed = errors.New("historical compute recovery requires verified immutable job and source evidence")

type JobBinding struct {
	JobID, OwnerUserID, ProjectID, FrameID, RootFrameID string
	Environment, Provider, ExternalID                   string
	InputSHA256, SourceSHA256                           string
}

// Lookup must obtain a frozen receipt from the canonical owner-scoped store,
// not deserialize a caller's claim that a job predates the namespace cutoff.
type Lookup func(context.Context, string, string) (JobBinding, bool, error)

type Proof struct {
	binding  JobBinding
	verified bool
}

type SourceBinding struct {
	JobID, Environment, Provider, ExternalID, ManifestSHA256 string
}

// SourceProof pairs the job's cutoff evidence with one original observed or
// durably recorded source digest. It cannot be replayed across same-input jobs.
type SourceProof struct {
	job    Proof
	source SourceBinding
}

func (p Proof) BindSource(source SourceBinding) (SourceProof, error) {
	if !p.verified || !validBinding(p.binding) || source.JobID != p.binding.JobID || source.Environment != p.binding.Environment ||
		source.Provider != p.binding.Provider || source.ExternalID != p.binding.ExternalID || !validSHA(source.ManifestSHA256) {
		return SourceProof{}, ErrRecoveryUnconfirmed
	}
	return SourceProof{job: p, source: source}, nil
}

func (p SourceProof) VerifyRead(inputSHA256, manifestSHA256 string) error {
	if p.job.VerifyInput(inputSHA256) != nil || !validSHA(manifestSHA256) || p.source.ManifestSHA256 != manifestSHA256 {
		return ErrRecoveryUnconfirmed
	}
	if _, err := p.job.BindSource(p.source); err != nil {
		return err
	}
	return nil
}

func Observe(ctx context.Context, current JobBinding, lookup Lookup) (Proof, error) {
	if ctx == nil || lookup == nil || !validBinding(current) {
		return Proof{}, ErrRecoveryUnconfirmed
	}
	frozen, found, err := lookup(ctx, current.OwnerUserID, current.JobID)
	if err != nil || !found || frozen != current {
		return Proof{}, errors.Join(ErrRecoveryUnconfirmed, err)
	}
	return Proof{binding: current, verified: true}, nil
}

func (p Proof) VerifyInput(inputSHA256 string) error {
	if !p.verified || !validBinding(p.binding) || !validSHA(inputSHA256) || inputSHA256 != p.binding.InputSHA256 {
		return ErrRecoveryUnconfirmed
	}
	return nil
}

func validBinding(binding JobBinding) bool {
	for _, value := range []string{binding.JobID, binding.OwnerUserID, binding.ProjectID, binding.Environment, binding.Provider, binding.ExternalID} {
		if value == "" || len(value) > 4096 || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\x00\r\n") {
			return false
		}
	}
	for _, value := range []string{binding.FrameID, binding.RootFrameID} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return false
		}
	}
	return validSHA(binding.InputSHA256) && validSHA(binding.SourceSHA256)
}

func validSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && len(value) == 64 && value == strings.ToLower(value)
}

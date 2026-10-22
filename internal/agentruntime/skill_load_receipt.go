package agentruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

const MaxSkillLoadContracts = 32

// SkillContractIdentity describes the complete host-selected instruction and
// file package. It is load evidence, never a workflow-success assertion.
type SkillContractIdentity struct {
	Name           string `json:"name"`
	Origin         string `json:"origin"`
	ContractSHA256 string `json:"contractSha256"`
	BundleKind     string `json:"bundleKind"`
	BundleSHA256   string `json:"bundleSha256"`
}

type SkillLoadBinding struct {
	StreamUID     string `json:"streamUid"`
	OwnerID       string `json:"ownerId"`
	InputRevision int64  `json:"inputRevision"`
	RunnerAttempt int64  `json:"runnerAttempt"`
	ToolCallID    string `json:"toolCallId"`
}

// SkillLoadReceipt is constructed by the verified host loader and transported
// beside model content. Only a successful durable lifecycle checkpoint makes
// it reusable. Argument digests keep this bounded without copying user text.
type SkillLoadReceipt struct {
	Version         int                     `json:"version"`
	SkillName       string                  `json:"skillName"`
	ArgumentsSHA256 string                  `json:"argumentsSha256"`
	FilterSHA256    string                  `json:"filterSha256"`
	Contracts       []SkillContractIdentity `json:"contracts"`
	ClosureSHA256   string                  `json:"closureSha256"`
	InvocationKey   string                  `json:"invocationKey"`
	Binding         *SkillLoadBinding       `json:"binding,omitempty"`
}

func NewSkillLoadReceipt(name, arguments, filter string, contracts []SkillContractIdentity) (*SkillLoadReceipt, error) {
	receipt := &SkillLoadReceipt{
		Version: 1, SkillName: strings.TrimSpace(name),
		ArgumentsSHA256: skillLoadSHA256([]byte(arguments)),
		FilterSHA256:    skillLoadSHA256([]byte(strings.TrimSpace(filter))),
		Contracts:       append([]SkillContractIdentity(nil), contracts...),
	}
	receipt.ClosureSHA256 = skillLoadJSONSHA256(receipt.Contracts)
	receipt.InvocationKey = receipt.expectedInvocationKey()
	if !receipt.Valid() {
		return nil, errors.New("Skill load receipt has an invalid contract identity")
	}
	return receipt, nil
}

func (receipt *SkillLoadReceipt) Clone() *SkillLoadReceipt {
	if receipt == nil {
		return nil
	}
	clone := *receipt
	clone.Contracts = append([]SkillContractIdentity(nil), receipt.Contracts...)
	if receipt.Binding != nil {
		binding := *receipt.Binding
		clone.Binding = &binding
	}
	return &clone
}

// Valid checks shape and content-addressed internal consistency. It does not
// authenticate arbitrary JSON; the host checkpoint and its scoped provenance
// remain the authority for durable receipt acceptance.
func (receipt *SkillLoadReceipt) Valid() bool {
	if receipt == nil || receipt.Version != 1 || !skillLoadName(receipt.SkillName) ||
		len(receipt.Contracts) == 0 || len(receipt.Contracts) > MaxSkillLoadContracts ||
		!skillLoadDigest(receipt.ArgumentsSHA256) || !skillLoadDigest(receipt.FilterSHA256) {
		return false
	}
	seen := make(map[string]bool, len(receipt.Contracts))
	for _, contract := range receipt.Contracts {
		name := strings.ToLower(contract.Name)
		if !skillLoadName(contract.Name) || strings.TrimSpace(contract.Origin) != contract.Origin ||
			contract.Origin == "" || len(contract.Origin) > 64 || seen[name] ||
			!skillLoadDigest(contract.ContractSHA256) ||
			!(contract.BundleKind == "files" && skillLoadDigest(contract.BundleSHA256) ||
				contract.BundleKind == "virtual" && contract.BundleSHA256 == "") {
			return false
		}
		seen[name] = true
	}
	if !strings.EqualFold(receipt.SkillName, receipt.Contracts[len(receipt.Contracts)-1].Name) {
		return false
	}
	return skillLoadDigest(receipt.ClosureSHA256) && skillLoadDigest(receipt.InvocationKey) &&
		receipt.ClosureSHA256 == skillLoadJSONSHA256(receipt.Contracts) &&
		receipt.InvocationKey == receipt.expectedInvocationKey()
}

func (receipt *SkillLoadReceipt) BoundToCall(callID string) bool {
	return receipt.Valid() && receipt.Binding != nil &&
		skillLoadName(receipt.Binding.StreamUID) && skillLoadName(receipt.Binding.OwnerID) &&
		receipt.Binding.InputRevision > 0 && receipt.Binding.RunnerAttempt > 0 &&
		skillLoadName(receipt.Binding.ToolCallID) && receipt.Binding.ToolCallID == callID
}

func (receipt *SkillLoadReceipt) MatchesArguments(name, arguments, filter string) bool {
	return receipt.Valid() && strings.EqualFold(strings.TrimPrefix(strings.TrimSpace(name), "/"), receipt.SkillName) &&
		receipt.ArgumentsSHA256 == skillLoadSHA256([]byte(arguments)) &&
		receipt.FilterSHA256 == skillLoadSHA256([]byte(strings.TrimSpace(filter)))
}

func (receipt *SkillLoadReceipt) expectedInvocationKey() string {
	return skillLoadJSONSHA256(struct {
		Version                          int
		Name, Arguments, Filter, Closure string
	}{receipt.Version, strings.ToLower(receipt.SkillName), receipt.ArgumentsSHA256,
		receipt.FilterSHA256, receipt.ClosureSHA256})
}

func skillLoadName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && len(name) <= 512 && !strings.ContainsRune(name, 0)
}

func skillLoadDigest(digest string) bool {
	if len(digest) != sha256.Size*2 || digest != strings.ToLower(digest) {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func skillLoadJSONSHA256(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return skillLoadSHA256(raw)
}

func skillLoadSHA256(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

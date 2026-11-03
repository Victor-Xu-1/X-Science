package workspace

import (
	"encoding/hex"
	"encoding/json"
	"errors"
)

// These document versions are independent of the unchanged socket protocol.
// V1 codecs remain closed and retain their original canonical bytes.
type KernelExecutionResultContract struct {
	ResultVersion               int `json:"result_version"`
	HostResourceEvidenceVersion int `json:"host_resource_evidence_version"`
}

func (c KernelExecutionResultContract) Valid() bool {
	return c.ResultVersion == 2 && c.HostResourceEvidenceVersion == 1
}

type KernelExecutionSessionSpecV2 struct {
	Version        int                           `json:"version"`
	Session        KernelExecutionSessionSpecV1  `json:"session"`
	ResultContract KernelExecutionResultContract `json:"result_contract"`
}

type KernelDetachedExecutionBindingV2 struct {
	BackendID          string `json:"backend_id"`
	BackendGeneration  int64  `json:"backend_generation"`
	ExecutorInstanceID string `json:"executor_instance_id"`
	SessionSpecSHA256  string `json:"session_spec_sha256"`
}

func (b KernelDetachedExecutionBindingV2) Valid() bool {
	return validDetachedIdentity(b.BackendID) && b.BackendGeneration > 0 &&
		validDetachedIdentity(b.ExecutorInstanceID) && validDetachedDocumentSHA(b.SessionSpecSHA256)
}

type KernelDetachedExecutionRequestV2 struct {
	Version        int                              `json:"version"`
	Request        KernelDetachedExecutionRequestV1 `json:"request"`
	Binding        KernelDetachedExecutionBindingV2 `json:"binding"`
	ResultContract KernelExecutionResultContract    `json:"result_contract"`
}

// Normalized documents expose the original payload without granting a V1
// request a capability that it never admitted. They are not wire encoders.
type KernelExecutionSessionDocument struct {
	KernelExecutionSessionSpecV1
	DocumentVersion int
	ResultContract  KernelExecutionResultContract
}

type KernelDetachedExecutionDocument struct {
	KernelDetachedExecutionRequestV1
	DocumentVersion int
	Binding         KernelDetachedExecutionBindingV2
	ResultContract  KernelExecutionResultContract
}

func DecodeKernelExecutionSessionDocument(raw string) (KernelExecutionSessionDocument, error) {
	version, err := detachedDocumentVersion(raw)
	if err != nil {
		return KernelExecutionSessionDocument{}, err
	}
	if version == 1 {
		v1, err := DecodeKernelExecutionSessionSpecV1(raw)
		return KernelExecutionSessionDocument{KernelExecutionSessionSpecV1: v1, DocumentVersion: 1}, err
	}
	var v2 KernelExecutionSessionSpecV2
	if version != 2 || len(raw) > 1<<20 || decodeClosedDetachedJSONObject(raw, &v2) != nil ||
		!v2.ResultContract.Valid() || !canonicalDetachedDocument(raw, v2) {
		return KernelExecutionSessionDocument{}, errors.New("kernel session document contract is unsupported or invalid")
	}
	base, err := json.Marshal(v2.Session)
	if err != nil {
		return KernelExecutionSessionDocument{}, err
	}
	v1, err := DecodeKernelExecutionSessionSpecV1(string(base))
	if err != nil {
		return KernelExecutionSessionDocument{}, err
	}
	return KernelExecutionSessionDocument{KernelExecutionSessionSpecV1: v1, DocumentVersion: 2, ResultContract: v2.ResultContract}, nil
}

func DecodeKernelDetachedExecutionDocument(raw string) (KernelDetachedExecutionDocument, error) {
	version, err := detachedDocumentVersion(raw)
	if err != nil {
		return KernelDetachedExecutionDocument{}, err
	}
	if version == 1 {
		v1, err := DecodeKernelDetachedExecutionRequestV1(raw)
		return KernelDetachedExecutionDocument{KernelDetachedExecutionRequestV1: v1, DocumentVersion: 1}, err
	}
	var v2 KernelDetachedExecutionRequestV2
	if version != 2 || len(raw) > 1<<20 || decodeClosedDetachedJSONObject(raw, &v2) != nil ||
		!v2.ResultContract.Valid() || !v2.Binding.Valid() || !canonicalDetachedDocument(raw, v2) {
		return KernelDetachedExecutionDocument{}, errors.New("kernel execution document contract is unsupported or invalid")
	}
	base, err := json.Marshal(v2.Request)
	if err != nil {
		return KernelDetachedExecutionDocument{}, err
	}
	v1, err := DecodeKernelDetachedExecutionRequestV1(string(base))
	if err != nil {
		return KernelDetachedExecutionDocument{}, err
	}
	return KernelDetachedExecutionDocument{KernelDetachedExecutionRequestV1: v1, DocumentVersion: 2, Binding: v2.Binding, ResultContract: v2.ResultContract}, nil
}

// ReadDetachedExecutionDocument derives the expected contract from the original
// immutable request. A replacement backend or a guest result cannot supply it.
func ReadDetachedExecutionDocument(execution DetachedKernelExecution) (KernelDetachedExecutionDocument, error) {
	doc, err := DecodeKernelDetachedExecutionDocument(execution.RequestJSON)
	if err != nil || execution.RequestSHA256 != sha256HexString(execution.RequestJSON) ||
		doc.ExecutionID != execution.ExecutionID || doc.OperationID != execution.OperationID ||
		!validDetachedIdentity(execution.BackendID) || execution.BackendGeneration <= 0 ||
		(doc.DocumentVersion == 2 && (doc.Binding.BackendID != execution.BackendID || doc.Binding.BackendGeneration != execution.BackendGeneration)) {
		return KernelDetachedExecutionDocument{}, ErrDetachedKernelExecutionConflict
	}
	return doc, nil
}

func detachedDocumentVersion(raw string) (int, error) {
	var header struct {
		Version int `json:"version"`
	}
	if json.Unmarshal([]byte(raw), &header) != nil || (header.Version != 1 && header.Version != 2) {
		return 0, errors.New("kernel document version is unsupported")
	}
	// Header dispatch never accepts a document: the selected closed decoder does.
	return header.Version, nil
}

func canonicalDetachedDocument(raw string, value any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && string(encoded) == raw
}

func validDetachedDocumentSHA(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

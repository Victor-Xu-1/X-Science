//go:build linux

package detached

import workspace "x-science/internal/persistence/workspace"

// Normal admission still creates V1. Recovery can continue an already admitted
// V2 request only on the same authoritative owner; it never upgrades or rebinds.
func (e *Executor) acceptsExecutionDocument(doc workspace.KernelDetachedExecutionDocument) bool {
	if doc.DocumentVersion == 1 {
		return e.sessionDocument.DocumentVersion != 2
	}
	return doc.DocumentVersion == 2 && doc.Binding.Valid() && doc.ResultContract.Valid() && e.sessionDocument.DocumentVersion == 2 &&
		doc.ResultContract == e.sessionDocument.ResultContract &&
		doc.Binding.BackendID == e.BackendID && doc.Binding.BackendGeneration == e.BackendGeneration &&
		doc.Binding.ExecutorInstanceID == e.ExecutorInstanceID && doc.Binding.SessionSpecSHA256 == e.sessionSpecSHA256
}

package server

import (
	"strings"
	"time"
)

func (s *Server) retirePendingKernelHostApproval(approvalID, reason string) error {
	if s == nil || s.runtimeStore == nil {
		return nil
	}
	// User decisions use the same lock. An accepted or denied decision must
	// never be replaced by a cancellation that observed an older snapshot.
	s.agentToolApprovalMu.Lock()
	defer s.agentToolApprovalMu.Unlock()
	entry, found, err := s.runtimeStore.Get(agentRuntimeApprovalNamespace, approvalID)
	if err != nil || !found {
		return err
	}
	value := mapValue(entry.Value)
	if strings.TrimSpace(stringValue(value["status"])) != "pending" ||
		!isKernelHostWaitOnlyApprovalSource(stringValue(value["approvalSource"])) {
		return nil
	}
	value["status"] = "failed"
	value["error"] = reason
	value["failureCode"] = "approval_wait_ended"
	value["resolvedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.runtimeStore.Set(agentRuntimeApprovalNamespace, approvalID, value)
	return err
}

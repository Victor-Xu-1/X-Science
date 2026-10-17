package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	kernelruntime "x-science/internal/kernel"
)

func TestManagedCleanupFailurePreservesUnknownThroughCancellation(t *testing.T) {
	for _, state := range []string{"cleanup_unknown", "cleanup_incomplete"} {
		t.Run(state, func(t *testing.T) {
			cleanup := &kernelruntime.WorkerProcessCleanupError{State: state, Cause: errors.New("private fixture diagnostic")}
			authority := &recordingManagedEnvironmentAuthority{mutateErr: fmt.Errorf("operation interrupted: %w", errors.Join(context.Canceled, cleanup))}
			request := managedOperationRequest{Kind: "create",
				Create:   &kernelruntime.CreateManagedEnvironmentInput{Name: "analysis", OperationID: "cleanup-observation"},
				Metadata: map[string]any{"operation_id": "cleanup-observation"}}
			payload, err := (&Server{}).runManagedOperation(context.Background(), "fixture-owner", manageEnvironmentsToolName, request, authority)
			if err != nil || payload["ok"] != false || payload["status"] != "failed" {
				t.Fatalf("cleanup uncertainty was reduced to cancellation or success: payload=%v error=%v", payload, err)
			}
			failure := mapValue(payload["failure"])
			details := mapValue(failure["details"])
			if failure["category"] != "process_cleanup_unknown" || details["cleanup_state"] != state ||
				details["physical_settlement_confirmed"] != false || details["automatic_replay_safe"] != false {
				t.Fatalf("cleanup contract was lost: %v", failure)
			}
			if !strings.Contains(stringValue(failure["recovery"]), "do not start a competing installer") ||
				strings.Contains(stringValue(payload["error"]), "private fixture diagnostic") {
				t.Fatalf("unsafe replay advice or private cause in the public error: %v", failure)
			}
		})
	}
}

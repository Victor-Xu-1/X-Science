package agentruntime

import (
	"strings"

	"synon-go/internal/failurecontract"
)

// ExecutionFailureLedger owns executed-call retry decisions for both live
// execution and durable replay. Its caller serializes observations; no result
// envelope is modified here. A new user turn starts a new ledger, not a new lease.
type ExecutionFailureLedger struct {
	targets    map[string]*executionTargetFailures
	onlyTarget string
}

// ExecutionFailuresForCall limits a history projection to its requested target.
// Unrelated workspace repairs are still observed, without retaining every
// other execution's failures while scanning a long conversation.
func ExecutionFailuresForCall(call ToolCall) ExecutionFailureLedger {
	return ExecutionFailureLedger{onlyTarget: executionFailureTarget(call)}
}

// Reset starts the scope of an explicit new user turn, not a recovered lease.
func (ledger *ExecutionFailureLedger) Reset() {
	ledger.targets = nil
}

type executionTargetFailures struct {
	exact    map[string]struct{}
	external bool
}

func executionFailureTarget(call ToolCall) string {
	return strings.ToLower(strings.TrimSpace(call.Name)) + "\x00" + semanticToolFailureTarget(call.Name, call.Arguments)
}

func executionFailureTool(name string, capabilities []string) bool {
	if len(capabilities) != 0 {
		return toolCapabilitySetContainsAny(capabilities, "runtime-execution", "artifact-write", "software-provisioning")
	}
	return registeredExecutionTool(strings.TrimSpace(name))
}

func executionFailureKind(value any) failurecontract.Kind {
	object := toolResultEnvelopeMap(value)
	kind := failurecontract.Kind(strings.TrimSpace(stringValueAt(object, "failure_kind")))
	if !failurecontract.Valid(kind) {
		kind = failurecontract.KindForDetailCode(toolResultCode(object))
		// Keep the historical untyped draining receipt interpretable through
		// the existing compatibility classifier, identically in both consumers.
		if kind == failurecontract.ResultRejected && isTransientRuntimeFailure(value) {
			kind = failurecontract.Transient
		}
	}
	return kind
}

func executionFailureNeedsExternalState(value any) bool {
	return failurecontract.NextAction(executionFailureKind(value)) == "wait_for_external_state_then_start_new_execution"
}

// Observe consumes only terminal execution receipts. A rejected admission is
// not an execution failure or proof of repair. Workspace repair retires exact
// input failures, never an external runtime condition.
func (ledger *ExecutionFailureLedger) Observe(call ToolCall, capabilities []string, value any) {
	if ToolResultDidNotExecute(value) {
		return
	}
	outcome := ClassifyToolResult(value)
	if WorkspaceMutationCommitted(call.Name, capabilities, outcome, value) {
		for key, state := range ledger.targets {
			if state.external {
				state.exact = nil
			} else {
				delete(ledger.targets, key)
			}
		}
	}
	if !executionFailureTool(call.Name, capabilities) {
		return
	}
	key := executionFailureTarget(call)
	if ledger.onlyTarget != "" && ledger.onlyTarget != key {
		return
	}
	identity := failedToolCallFingerprint(call)
	failed := outcome.HardFailed() || (outcome == ToolResultUnavailable && !isTransientRuntimeFailure(value))
	if !failed {
		if outcome == ToolResultSucceeded {
			if state := ledger.targets[key]; state != nil {
				delete(state.exact, identity)
				if !state.external && len(state.exact) == 0 {
					delete(ledger.targets, key)
				}
			}
		}
		return
	}
	if ledger.targets == nil {
		ledger.targets = make(map[string]*executionTargetFailures)
	}
	state := ledger.targets[key]
	if state == nil {
		state = &executionTargetFailures{}
		ledger.targets[key] = state
	}
	if state.exact == nil {
		state.exact = make(map[string]struct{})
	}
	state.exact[identity] = struct{}{}
	state.external = state.external || executionFailureNeedsExternalState(value)
}

// Boundary is side-effect-free: it cannot execute, invent an external repair,
// or infer success from diagnostic prose. Corrected inputs reopen local failures.
func (ledger *ExecutionFailureLedger) Boundary(call ToolCall) map[string]any {
	state := ledger.targets[executionFailureTarget(call)]
	if state == nil {
		return nil
	}
	if state.external {
		return semanticExternalStateRequiredBoundary(call.Name)
	}
	if _, failed := state.exact[failedToolCallFingerprint(call)]; !failed {
		return nil
	}
	return map[string]any{
		"ok": false, "executed": false, "preflight": true, "status": "repeated_failed_tool_call",
		"code": "repeated_failed_tool_call", "tool": strings.TrimSpace(call.Name), "retryable": false,
		"message":     "the same registered execution already failed and cannot be repeated unchanged",
		"next_action": "change_the_execution_inputs_then_retry",
	}
}

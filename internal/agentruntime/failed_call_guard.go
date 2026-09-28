package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"strings"
	"sync"

	"synon-go/internal/failurecontract"
)

type failedToolCallGuard struct {
	next ToolGateway

	mu                        sync.Mutex
	failedCallMutationEpoch   map[string]uint64
	nonProgressingCalls       map[string]uint64
	nonRetryablePartialCalls  map[string]int
	nonRetryablePartialEpoch  map[string]uint64
	blockedSourceDestinations map[string]string
	executionFailures         ExecutionFailureLedger
	transientSourceAttempts   map[string]int
	transientSourceEpoch      map[string]uint64
	toolCapabilities          map[string][]string
	mutationEpoch             uint64
}

const maxNonRetryablePartialAttempts = 2

// A source connector may legitimately be unavailable for a short interval,
// but changing the query does not repair an unavailable upstream. Bound the
// connector-level retry budget so a scientific task records the evidence gap
// and continues with the successful subset instead of looping forever.
const maxTransientSourceUnavailableAttempts = 3

// ToolCallAdmissionChecker lets a gateway prove that corrected arguments now
// satisfy its admitted schema. This prevents two parallel invalid calls from
// permanently poisoning a tool after the model has produced a valid repair.
type ToolCallAdmissionChecker interface {
	AdmitsToolCall(ToolCall) bool
}

// ToolCallAdmissionDiagnostics optionally exposes bounded, non-sensitive
// schema diagnostics for a rejected call. It is intentionally separate from
// ToolCallAdmissionChecker so existing gateways keep the boolean contract.
type ToolCallAdmissionDiagnostics interface {
	ToolCallAdmissionDiagnostic(ToolCall) string
}

// ToolCallPreflightDiagnostics lets a gateway check a proposed batch once
// before any model response, tool-start event, approval, or side effect becomes
// durable. Diagnostics are keyed by the exact input index and must be safe to
// return to the model. Authority/read failures and cancellation return an error,
// not model-repair feedback. Implementations must honor the request context.
type ToolCallPreflightDiagnostics interface {
	ToolCallPreflightDiagnostics(context.Context, []ToolCall) (map[int]string, error)
}

// ModelToolChoicePolicy may require one exact tool on a later model round
// after durable tool results materially change the decision state. The policy
// sees only the current native model transcript and advertised tool snapshot;
// it cannot execute a tool or manufacture arguments. A nil choice preserves
// normal provider-auto selection.
type ModelToolChoicePolicy interface {
	RequiredToolChoice(messages []Message, tools []ToolSchema) any
}

// RequiredToolCallRecovery may reconstruct one exact named call after bounded
// provider protocol repair has failed. Implementations must derive the complete
// call from durable machine state. A previously materialized source-owned
// continuation is recoverable; inventing new scientific semantics, external
// actions, or user-owned decisions is not. Generic "required" choices remain
// unrecoverable because the runtime cannot choose their semantics.
type RequiredToolCallRecovery interface {
	RecoverRequiredToolCall(requiredTool string, messages []Message, tools []ToolSchema) (ToolCall, bool)
}

// ModelToolSchemaExpansion lets a gateway expose additional exact tool
// schemas after a successful in-run discovery action such as loading a Skill.
// It may only add schemas from the authority snapshot captured before model
// execution; the engine remains the single owner of the advertised set.
type ModelToolSchemaExpansion interface {
	AdditionalModelToolSchemas(current []ToolSchema) []ToolSchema
}

// ToolCallExecutionIdentityResolver exposes the deterministic, side-effect-free
// identity a gateway will execute after host normalization. Retry guards use
// this identity without replacing the original model call in the transcript.
type ToolCallExecutionIdentityResolver interface {
	ToolCallExecutionIdentity(ToolCall) (ToolCall, bool)
}

func newFailedToolCallGuard(next ToolGateway, schemas ...ToolSchema) ToolGateway {
	if next == nil {
		return nil
	}
	guard := &failedToolCallGuard{
		next:                      next,
		failedCallMutationEpoch:   make(map[string]uint64),
		nonProgressingCalls:       make(map[string]uint64),
		nonRetryablePartialCalls:  make(map[string]int),
		nonRetryablePartialEpoch:  make(map[string]uint64),
		blockedSourceDestinations: make(map[string]string),
		transientSourceAttempts:   make(map[string]int),
		transientSourceEpoch:      make(map[string]uint64),
		toolCapabilities:          make(map[string][]string),
	}
	guard.updateToolSchemas(schemas)
	return guard
}

func (g *failedToolCallGuard) updateToolSchemas(schemas []ToolSchema) {
	if g == nil || len(schemas) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, schema := range schemas {
		name := strings.ToLower(strings.TrimSpace(schema.Name))
		if name == "" || len(schema.Capabilities) == 0 {
			continue
		}
		g.toolCapabilities[name] = append([]string(nil), schema.Capabilities...)
	}
}

func (g *failedToolCallGuard) toolCapabilitiesFor(name string) []string {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.toolCapabilities[strings.ToLower(strings.TrimSpace(name))]...)
}

func failedToolCallGuardBoundary(value map[string]any) ToolResult {
	if value == nil {
		value = map[string]any{"ok": false, "error": "tool execution was rejected before it started"}
	}
	value["executed"] = false
	value["preflight"] = true
	if message, _ := value["message"].(string); strings.TrimSpace(message) == "" {
		if detail, _ := value["error"].(string); strings.TrimSpace(detail) != "" {
			value["message"] = detail
		} else {
			value["message"] = "tool execution was rejected before it started"
		}
	}
	return ToolResult{Value: value}
}

func (g *failedToolCallGuard) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	executionCall := call
	if resolver, ok := g.next.(ToolCallExecutionIdentityResolver); ok {
		if resolved, valid := resolver.ToolCallExecutionIdentity(call); valid {
			executionCall = resolved
		}
	}
	toolName := strings.TrimSpace(executionCall.Name)
	toolCapabilities := g.toolCapabilitiesFor(toolName)
	executionTool := executionFailureTool(toolName, toolCapabilities)
	fingerprint := failedToolCallFingerprint(executionCall)
	sourceDestination := failedToolCallSourceDestination(executionCall, toolCapabilities)
	g.mu.Lock()
	failedEpoch, repeated := g.failedCallMutationEpoch[fingerprint]
	repeatedWithoutMutation := repeated && failedEpoch == g.mutationEpoch
	nonProgressEpoch, nonProgressTracked := g.nonProgressingCalls[fingerprint]
	repeatedNonProgress := nonProgressTracked && nonProgressEpoch == g.mutationEpoch
	_, blockedSourceDestination := g.blockedSourceDestinations[sourceDestination]
	partialEpoch, partialTracked := g.nonRetryablePartialEpoch[fingerprint]
	nonRetryablePartialAttempts := 0
	if partialTracked && partialEpoch == g.mutationEpoch {
		nonRetryablePartialAttempts = g.nonRetryablePartialCalls[fingerprint]
	}
	transientSourceBucket := transientSourceFailureBucket(toolName, toolCapabilities)
	transientSourceAttempts := 0
	if transientSourceBucket != "" && g.transientSourceEpoch[transientSourceBucket] == g.mutationEpoch {
		transientSourceAttempts = g.transientSourceAttempts[transientSourceBucket]
	}
	executionBoundary := g.executionFailures.Boundary(executionCall)
	g.mu.Unlock()
	if executionBoundary != nil {
		return failedToolCallGuardBoundary(executionBoundary), nil
	}
	if repeatedNonProgress {
		return failedToolCallGuardBoundary(map[string]any{
			"ok": false, "code": "repeated_non_progressing_tool_call", "tool": toolName,
			"error":     "the same mutating tool call already completed without changing authoritative state",
			"retryable": false,
			"recovery":  "change_the_mutation_arguments_or_choose_a_materially_different_capability",
		}), nil
	}
	if sourceDestination != "" && blockedSourceDestination {
		return failedToolCallGuardBoundary(map[string]any{
			// This is a recoverable source outcome, not a failed execution: no
			// network call starts and the model can choose another authority or
			// continue with verified evidence. Keeping it unavailable rather than
			// hard-failed prevents repeated route hygiene from inflating task error
			// rates while preserving the durable diagnostic.
			"ok": true, "sourceUnavailable": true, "code": "source_destination_rejected", "tool": toolName,
			"status":    "source_destination_rejected",
			"error":     "the exact source destination was already rejected and cannot be retried with presentation-only changes",
			"retryable": false,
			"recovery":  "choose_a_materially_different_authoritative_source_or_continue_with_verified_evidence",
		}), nil
	}
	if repeatedWithoutMutation {
		return failedToolCallGuardBoundary(map[string]any{
			"ok": false, "code": "repeated_failed_tool_call", "tool": toolName,
			"error":     "the same tool call already failed and cannot be repeated unchanged",
			"retryable": false, "recovery": "change_arguments_and_retry_the_same_tool_with_a_corrected_call",
		}), nil
	}
	if nonRetryablePartialAttempts >= maxNonRetryablePartialAttempts {
		return failedToolCallGuardBoundary(map[string]any{
			"ok": false, "code": "repeated_non_retryable_tool_call", "tool": toolName,
			"error":     "the same tool call returned a non-retryable partial failure twice and cannot be repeated unchanged",
			"retryable": false, "attempts": nonRetryablePartialAttempts,
			"recovery": "continue_with_the_successful_subset_or_change_the_failed_input_before_retrying",
		}), nil
	}
	if transientSourceAttempts >= maxTransientSourceUnavailableAttempts {
		return failedToolCallGuardBoundary(map[string]any{
			"ok": false, "code": "source_unavailable_retry_exhausted", "tool": toolName,
			"error":     "the same source connector remained unavailable after its bounded retry budget",
			"retryable": false, "attempts": transientSourceAttempts,
			"max_attempts": maxTransientSourceUnavailableAttempts,
			"recovery":     "record_the_source_gap_and_continue_with_successful_evidence_or_a_materially_different_authoritative_source",
		}), nil
	}
	result, err := g.next.Execute(ctx, call)
	if err != nil {
		return result, err
	}
	if len(result.ExecutedArguments) > 0 && json.Valid(result.ExecutedArguments) {
		executionCall.Arguments = append(json.RawMessage(nil), result.ExecutedArguments...)
		fingerprint = failedToolCallFingerprint(executionCall)
		sourceDestination = failedToolCallSourceDestination(executionCall, toolCapabilities)
	}
	// A trusted Materialized result is an immutable durable protocol value.
	// The guard may update its private retry budget from that value, but it must
	// not append attempts/recovery fields to result.Value after the host has
	// committed the canonical bytes and digest. Doing so makes Engine reject a
	// normal model-correctable kernel failure as an infrastructure conflict.
	mutableEnvelope := result.Materialized == nil
	outcome := ClassifyToolResult(result.Value)
	nonExecutingPreflight := IsNonExecutingPreflight(result.Value)
	if executionTool && ToolResultDidNotExecute(result.Value) && isTransientRuntimeFailure(result.Value) {
		// No execution exists to reconcile. A temporary admission outage must
		// not become a permanent failed-job lock: retry the same operation once
		// its owner recovers. Preserve the owner's retry advice and immutable
		// receipt; the engine's existing no-progress window bounds blind calls.
		return result, nil
	}
	if outcome.HardFailed() && boundedExactToolRetryAllowed(result.Value) && !executionTool {
		// Some stateful tool protocols deliberately permit one or more exact
		// resubmissions and expose the remaining budget in their trusted result
		// envelope. Do not let the generic duplicate-failure guard consume that
		// protocol-owned budget before the tool itself can count the next try.
		return result, nil
	}
	if executionTool && outcome.HardFailed() && executionFailureNeedsExternalState(result.Value) {
		if value, ok := result.Value.(map[string]any); ok && mutableEnvelope {
			kind := executionFailureKind(result.Value)
			code := toolResultCode(value)
			if code == "" {
				code = string(kind)
				value["code"] = code
			}
			value["failure_kind"] = string(kind)
			value["terminal"] = true
			value["retryable"] = false
			value["next_action"] = failurecontract.NextAction(kind)
			value["execution_unit_state"] = "failed"
		}
	} else if !executionTool && isTransientRuntimeFailure(result.Value) {
		// The runtime was draining (backend restart in progress) or the
		// upstream reported itself unavailable. These are infrastructure
		// conditions, not argument bugs: the identical call is the correct
		// retry once the runtime settles, so it must not poison the
		// exact-call fingerprint.
		if value, ok := result.Value.(map[string]any); ok && mutableEnvelope {
			value["retryable"] = true
			value["recovery"] = "retry_the_same_call_after_the_runtime_or_upstream_recovers"
		}
		if transientSourceBucket != "" && isRetryableSourceUnavailable(result.Value) {
			g.mu.Lock()
			if g.transientSourceEpoch[transientSourceBucket] != g.mutationEpoch {
				g.transientSourceAttempts[transientSourceBucket] = 0
				g.transientSourceEpoch[transientSourceBucket] = g.mutationEpoch
			}
			g.transientSourceAttempts[transientSourceBucket]++
			attempts := g.transientSourceAttempts[transientSourceBucket]
			g.mu.Unlock()
			if attempts >= maxTransientSourceUnavailableAttempts {
				if value, ok := result.Value.(map[string]any); ok && mutableEnvelope {
					value["retryable"] = false
					value["code"] = "source_unavailable_retry_exhausted"
					value["attempts"] = attempts
					value["max_attempts"] = maxTransientSourceUnavailableAttempts
					value["recovery"] = "record_the_source_gap_and_continue_with_successful_evidence_or_a_materially_different_authoritative_source"
				}
			}
		}
		return result, nil
	}
	nonRetryablePartial := outcome == ToolResultPartial && toolResultHasNonRetryableError(result.Value)
	definitiveUnavailable := outcome == ToolResultUnavailable && !isTransientRuntimeFailure(result.Value)
	nonProgressingMutation := !nonExecutingPreflight &&
		(outcome == ToolResultSucceeded || outcome == ToolResultPartial) &&
		workspaceMutationTool(toolName, toolCapabilities) && toolResultExplicitlyNoMutation(result.Value)
	mutationCommitted := WorkspaceMutationCommitted(toolName, toolCapabilities, outcome, result.Value)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.executionFailures.Observe(executionCall, toolCapabilities, result.Value)
	if sourceDestination != "" && (nonRetryableSourceBoundary(result.Value) || definitiveUnavailable) {
		g.blockedSourceDestinations[sourceDestination] = toolName
	}
	if mutationCommitted {
		g.mutationEpoch++
	}
	// Definitive failures poison the exact-call fingerprint. A partial result
	// is normally recoverable, but a non-retryable per-item failure gets its
	// own bounded counter so a model cannot loop forever on the same request
	// while still retaining the successful subset.
	if nonProgressingMutation {
		g.nonProgressingCalls[fingerprint] = g.mutationEpoch
	} else if nonRetryablePartial {
		if previousEpoch, tracked := g.nonRetryablePartialEpoch[fingerprint]; tracked && previousEpoch != g.mutationEpoch {
			// A successful workspace correction changes the bytes that the
			// pending save/registration call will observe. Its old partial
			// failure budget must not survive that correction, otherwise a
			// valid retry is mistaken for a blind duplicate.
			g.nonRetryablePartialCalls[fingerprint] = 0
		}
		g.nonRetryablePartialCalls[fingerprint]++
		g.nonRetryablePartialEpoch[fingerprint] = g.mutationEpoch
	} else if !executionTool && (outcome.HardFailed() || definitiveUnavailable) && !nonExecutingPreflight {
		g.failedCallMutationEpoch[fingerprint] = g.mutationEpoch
	} else if !nonExecutingPreflight {
		// A read-only inspection through the same executor does not repair a
		// different failed call. Committed mutations already advance the epoch;
		// success retires only this exact execution's stale failure receipt.
		// A successful execution of this exact call proves that the caller
		// has made progress; do not carry its stale partial-failure guard.
		delete(g.nonRetryablePartialCalls, fingerprint)
		delete(g.nonRetryablePartialEpoch, fingerprint)
		delete(g.failedCallMutationEpoch, fingerprint)
		delete(g.nonProgressingCalls, fingerprint)
		if transientSourceBucket != "" {
			delete(g.transientSourceAttempts, transientSourceBucket)
			delete(g.transientSourceEpoch, transientSourceBucket)
		}
	}
	return result, nil
}

func registeredExecutionTool(toolName string) bool {
	switch strings.ToLower(strings.TrimSpace(toolName)) {
	case "software_runtime", "python", "r", "bash", "repl", "operon", "edit_file":
		return true
	default:
		return false
	}
}

func semanticToolFailureTarget(toolName string, arguments json.RawMessage) string {
	input := map[string]any{}
	_ = json.Unmarshal(arguments, &input)
	normalizedTool := strings.ToLower(strings.TrimSpace(toolName))
	selected := map[string]any{"tool": normalizedTool}
	keys := []string{"capability", "execution_pack_id", "provider", "environment", "executable", "working_dir"}
	if normalizedTool == "edit_file" || normalizedTool == "read_file" {
		selected["tool"] = "workspace_file"
		selected["file_path"] = firstNonEmpty(stringValueAt(input, "file_path"), stringValueAt(input, "path"))
		keys = nil
	}
	for _, key := range keys {
		if value, found := input[key]; found {
			selected[key] = value
		}
	}
	encoded, _ := json.Marshal(selected)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:8])
}

func semanticExternalStateRequiredBoundary(toolName string) map[string]any {
	return map[string]any{
		"ok": false, "status": "external_state_required", "executed": false, "preflight": true,
		"code": "external_state_required", "tool": strings.TrimSpace(toolName),
		"message":     "the previous registered execution ended on an external runtime condition; wait for a new user or external-state signal before starting another execution",
		"next_action": "wait_for_external_state_then_start_new_execution",
	}
}

func boundedExactToolRetryAllowed(value any) bool {
	result, ok := value.(map[string]any)
	if !ok || result["retryable"] != true {
		return false
	}
	attempts, attemptsOK := boundedRetryCount(result["attempts"])
	maximum, maximumOK := boundedRetryCount(result["max_attempts"])
	return attemptsOK && maximumOK && attempts > 0 && maximum > attempts
}

func boundedRetryCount(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		return int64(number), number >= 0
	case int32:
		return int64(number), number >= 0
	case int64:
		return number, number >= 0
	case float64:
		integer := int64(number)
		return integer, number >= 0 && float64(integer) == number
	case json.Number:
		integer, err := number.Int64()
		return integer, err == nil && integer >= 0
	default:
		return 0, false
	}
}

// WorkspaceMutationCommitted advances retry state only after a tool that can
// change the task workspace reports a committed result. This is intentionally
// narrower than "any successful tool": a read/search result must not reopen a
// previously failed exact call. A hard-failed Python/R call may still have
// written durable files, so files_written/artifacts are treated as committed
// evidence and also advance the epoch.
// Both live execution and durable replay use this predicate; a restarted
// runner must neither forget unchanged failures nor reject committed repairs.
func WorkspaceMutationCommitted(toolName string, capabilities []string, outcome ToolResultOutcome, value any) bool {
	if ToolResultDidNotExecute(value) {
		return false
	}
	name := strings.TrimSpace(toolName)
	mutationTool := workspaceMutationTool(name, capabilities)
	if !mutationTool {
		return false
	}
	if toolResultExplicitlyNoMutation(value) {
		return false
	}
	if reported, committed := toolResultWorkspaceMutationReport(value); reported {
		return committed
	}
	if outcome == ToolResultSucceeded || outcome == ToolResultPartial {
		return true
	}
	return false
}

func isWorkspaceMutationTool(toolName string) bool {
	switch toolName {
	case "edit_file", "file_write", "Write", "Edit", "Patch", "file_patch", "file_replace",
		"json_patch", "NotebookEdit", "file_delete", "file_move", "file_copy", "file_mkdir",
		"python", "r", "bash", "repl", "software_runtime", "Python", "R", "Bash", "Operon", "shell_exec", "Shell", "powershell":
		return true
	default:
		return false
	}
}

func toolResultHasNonRetryableError(value any) bool {
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if toolResultHasNonRetryableError(item) {
				return true
			}
		}
	case map[string]any:
		if retryable, ok := typed["retryable"].(bool); ok && !retryable {
			if code, _ := typed["code"].(string); strings.TrimSpace(code) != "" {
				return true
			}
			if message, _ := typed["error"].(string); strings.TrimSpace(message) != "" {
				return true
			}
		}
		for _, key := range []string{"errors", "failures"} {
			if toolResultHasNonRetryableError(typed[key]) {
				return true
			}
		}
	}
	return false
}

func toolResultCode(value any) string {
	object, _ := value.(map[string]any)
	code, _ := object["code"].(string)
	return strings.TrimSpace(code)
}

func nonRetryableSourceBoundary(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	retryable, hasRetryable := object["retryable"].(bool)
	codeValue, _ := object["code"].(string)
	code := strings.ToLower(strings.TrimSpace(codeValue))
	return hasRetryable && !retryable && strings.HasPrefix(code, "secure_fetch_")
}

// isTransientRuntimeFailure reports infrastructure-level failures that should
// not drive the failed-call guard: the session runner was draining
// (ErrRuntimeDraining surfaces as "runtime draining"), or the tool explicitly
// reported the upstream source unavailable.
func isTransientRuntimeFailure(value any) bool {
	object := toolResultEnvelopeMap(value)
	if object == nil {
		return false
	}
	if object["sourceUnavailable"] == true || isRetryableSourceUnavailable(value) {
		return isRetryableSourceUnavailable(value)
	}
	code, _ := object["code"].(string)
	if strings.TrimSpace(code) == "runtime_draining" || strings.TrimSpace(code) == "software_runtime_unavailable" {
		return true
	}
	message, _ := object["error"].(string)
	return strings.Contains(strings.ToLower(message), "runtime draining")
}

func isRetryableSourceUnavailable(value any) bool {
	// Read the same closed, single-wrapper envelope as outcome classification.
	// An outer retry prohibition remains authoritative over a nested source.
	object := toolResultEnvelopeMap(value)
	for depth := 0; depth <= 1 && object != nil; depth++ {
		if object["retryable"] == false {
			return false
		}
		message, _ := object["error"].(string)
		message = strings.ToLower(strings.TrimSpace(message))
		if object["sourceUnavailable"] == true || strings.Contains(message, "authority is unavailable") ||
			strings.Contains(message, "upstream unavailable") {
			return true
		}
		object = toolResultEnvelopeMap(object["result"])
	}
	return false
}

func transientSourceFailureBucket(toolName string, capabilities []string) string {
	normalized := strings.ToLower(strings.TrimSpace(toolName))
	if strings.HasPrefix(normalized, "mcp__") {
		parts := strings.Split(normalized, "__")
		if len(parts) >= 3 && strings.TrimSpace(parts[1]) != "" {
			return "mcp:" + strings.TrimSpace(parts[1])
		}
	}
	if toolCapabilitySetContainsAny(capabilities, "source-evidence", "evidence-read", "source-download", "source-discovery", "source-investigation", "research") {
		return "source:" + normalized
	}
	if len(capabilities) > 0 {
		return ""
	}
	// Compatibility for callers and checkpoints created before capability
	// metadata was part of the immutable Tool schema.
	switch normalized {
	case "websearch", "web_search", "webfetch", "web_fetch", "webresearch", "web_research", "fetch_article_fulltext",
		"download_rcsb_file", "download_public_scientific_file", "search_rcsb_structures":
		return "source:" + normalized
	default:
		return ""
	}
}

func failedToolCallFingerprint(call ToolCall) string {
	canonicalArguments := []byte("{}")
	if len(call.Arguments) > 0 {
		var decoded any
		if json.Unmarshal(call.Arguments, &decoded) == nil {
			if object, ok := decoded.(map[string]any); ok {
				// Presentation copy is not execution identity. Changing only the
				// human label must not bypass an exact failed-call guard.
				delete(object, "human_description")
				decoded = object
			}
			if encoded, err := json.Marshal(decoded); err == nil {
				canonicalArguments = encoded
			}
		} else {
			canonicalArguments = append([]byte(nil), call.Arguments...)
		}
	}
	digest := sha256.Sum256(append([]byte(strings.TrimSpace(call.Name)+"\x00"), canonicalArguments...))
	return hex.EncodeToString(digest[:])
}

// ExecutionCallFingerprint is the durable execution identity shared by
// in-memory and cross-lease retry control. Presentation-only labels are
// excluded, so only a material argument change reopens a failed call.
func ExecutionCallFingerprint(toolName string, arguments json.RawMessage) string {
	return failedToolCallFingerprint(ToolCall{Name: toolName, Arguments: arguments})
}

func failedToolCallSourceDestination(call ToolCall, capabilities []string) string {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	sourceDestinationTool := toolCapabilitySetContainsAny(capabilities, "source-locator-read", "source-download")
	if len(capabilities) == 0 {
		sourceDestinationTool = name == "web_fetch" || name == "webfetch" || name == "download_public_scientific_file"
	}
	if !sourceDestinationTool {
		return ""
	}
	var input map[string]any
	if json.Unmarshal(call.Arguments, &input) != nil {
		return ""
	}
	rawURL, ok := input["url"].(string)
	if !ok || strings.TrimSpace(rawURL) == "" {
		return ""
	}
	identity := strings.TrimSpace(rawURL)
	if parsed, parseErr := url.Parse(identity); parseErr == nil {
		parsed.Fragment = ""
		identity = parsed.String()
	}
	return name + "\x00" + identity
}

func toolCapabilitySetContainsAny(capabilities []string, wanted ...string) bool {
	for _, capability := range capabilities {
		for _, candidate := range wanted {
			if strings.EqualFold(strings.TrimSpace(capability), strings.TrimSpace(candidate)) {
				return true
			}
		}
	}
	return false
}

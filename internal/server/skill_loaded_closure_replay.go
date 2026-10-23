package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"x-science/internal/agentruntime"
	eventjournal "x-science/internal/persistence/journal"
	transcriptstore "x-science/internal/persistence/transcript"
)

const runtimeSkillLoadReceiptField = "skillLoadReceipt"

func runtimeSkillLoadReceiptFromCommittedEvent(event transcriptstore.Event) *agentruntime.SkillLoadReceipt {
	if event.EventID <= 0 || event.Type != "runner_checkpoint" || event.RunnerAttempt == nil {
		return nil
	}
	var message eventjournal.Message
	if json.Unmarshal(event.PayloadJSON, &message) != nil {
		return nil
	}
	message["type"], message["runnerAttempt"] = event.Type, *event.RunnerAttempt
	return runtimeSkillLoadReceiptFromCheckpoint(eventjournal.Entry{Message: message})
}

func runtimeSkillLoadReceiptForScope(receipt *agentruntime.SkillLoadReceipt, authority *transcriptRunnerAuthority, callID string) bool {
	return authority != nil && receipt.BoundToCall(callID) &&
		receipt.Binding.StreamUID == authority.Stream.UID && receipt.Binding.OwnerID == authority.Stream.OwnerID
}

func runtimeSkillLoadReceiptFromCheckpoint(entry eventjournal.Entry) *agentruntime.SkillLoadReceipt {
	message := entry.Message
	if stringValue(message["type"]) != "runner_checkpoint" ||
		stringValue(message["status"]) != "completed" || stringValue(message["toolPhase"]) != "completed" ||
		!isCompletedSkillToolName(stringValue(message["toolName"])) || message["rejectedBeforeExecution"] == true ||
		agentruntime.ClassifyToolResult(message["toolResult"]) != agentruntime.ToolResultSucceeded {
		return nil
	}
	encoded, err := json.Marshal(message[runtimeSkillLoadReceiptField])
	if err != nil {
		return nil
	}
	var receipt agentruntime.SkillLoadReceipt
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&receipt) != nil || !receipt.BoundToCall(stringValue(message["toolCallId"])) {
		return nil
	}
	attempt, ok := exactPositiveInt(message["runnerAttempt"])
	if !ok || int64(attempt) != receipt.Binding.RunnerAttempt {
		return nil
	}
	// The host receipt supplies identity; executed arguments may only confirm
	// its selected root, never manufacture an identity for a legacy checkpoint.
	input := completedSkillInput(message)
	if !runtimeSkillLoadReceiptMatchesInput(&receipt, input) {
		return nil
	}
	return receipt.Clone()
}

func runtimeSkillLoadReceiptMatchesInput(receipt *agentruntime.SkillLoadReceipt, input map[string]any) bool {
	return input != nil && receipt.MatchesArguments(stringValue(input["skill"]), stringValue(input["args"]), stringValue(input["filter"]))
}

func completedSkillLoadReceiptsFromRunnerEntries(entries []eventjournal.Entry, authority *transcriptRunnerAuthority) []*agentruntime.SkillLoadReceipt {
	if authority == nil {
		return nil
	}
	var receipts []*agentruntime.SkillLoadReceipt
	for _, entry := range entries {
		receipt := runtimeSkillLoadReceiptFromCheckpoint(entry)
		if runtimeSkillLoadReceiptForScope(receipt, authority, stringValue(entry.Message["toolCallId"])) {
			receipts = append(receipts, receipt)
		}
	}
	if records, _, err := latestPersistedSessionRunnerToolContinuity(entries); err == nil {
		for _, record := range records {
			if record.Successful && record.Phase == "completed" && isCompletedSkillToolName(record.ToolName) &&
				runtimeSkillLoadReceiptForScope(record.SkillLoadReceipt, authority, record.ToolCallID) &&
				strings.EqualFold(record.SkillName, record.SkillLoadReceipt.SkillName) {
				receipts = append(receipts, record.SkillLoadReceipt.Clone())
			}
		}
	}
	return receipts
}

// The caller has already selected the current logical-input branch. Bind each
// original attempt's input revision to its durable runner state as well, so a
// self-consistent but transplanted JSON binding cannot invent source scope.
func (s *Server) restoreSkillLoadReceipts(ctx context.Context, entries []eventjournal.Entry, run *sessionRunnerChatRun) ([]*agentruntime.SkillLoadReceipt, error) {
	if run == nil || run.Transcript == nil {
		return nil, nil
	}
	candidates := completedSkillLoadReceiptsFromRunnerEntries(entries, run.Transcript)
	if s == nil || s.transcriptStore == nil {
		if len(candidates) == 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("Transcript authority is unavailable while restoring Skill load receipts")
	}
	referenced, err := s.restoreReferencedSkillLoadReceipts(ctx, entries, run)
	if err != nil {
		return nil, err
	}
	candidates = append(candidates, referenced...)
	revisions := make(map[int64]int64)
	var accepted []*agentruntime.SkillLoadReceipt
	for _, receipt := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		binding := receipt.Binding
		if binding.RunnerAttempt > run.Transcript.Claim.Attempt || binding.InputRevision != run.Transcript.Claim.ClaimedInputRevision {
			continue
		}
		revision, found := revisions[binding.RunnerAttempt]
		if !found {
			state, err := s.transcriptStore.GetRunnerRuntimeState(ctx, binding.StreamUID, binding.OwnerID, binding.RunnerAttempt)
			if err != nil {
				return nil, fmt.Errorf("restore Skill load receipt attempt %d: %w", binding.RunnerAttempt, err)
			}
			revision = state.ClaimedInputRevision
			revisions[binding.RunnerAttempt] = revision
		}
		if binding.InputRevision == revision {
			accepted = append(accepted, receipt)
		}
	}
	return accepted, nil
}

// A compacted identity-only model receipt does not authorize replacing its
// instruction package with today's same-name package. Match the captured bytes
// first, then materialize that same snapshot. Independently matched closures or
// explicit roots retain their own dependencies, even when an older closure that
// shares one member no longer matches.
func (snapshot runtimeSkillClosureSnapshot) forReplay(
	receipts []*agentruntime.SkillLoadReceipt,
	explicitRoots []string,
	provenContexts ...map[string]runtimeSkillReplayContextRoot,
) (runtimeSkillClosureSnapshot, []string, []string) {
	current := make(map[string]runtimeSkillContractSnapshot, len(snapshot.contracts))
	for _, contract := range snapshot.contracts {
		current[normalizedRuntimeSkillReceiptName(contract.skill.Name)] = contract
	}
	knownMembers, knownRoots, matchingRoots := map[string]bool{}, map[string]bool{}, map[string]bool{}
	parameterizedRoots := map[string]bool{}
	for _, receipt := range receipts {
		if receipt.Valid() && !receipt.MatchesArguments(receipt.SkillName, "", "") {
			parameterizedRoots[normalizedRuntimeSkillReceiptName(receipt.SkillName)] = true
		}
	}
	dependencies := map[string]bool{}
	for _, contract := range snapshot.contracts {
		for _, dependency := range contract.skill.RequiredSkills {
			dependencies[normalizedRuntimeSkillReceiptName(dependency)] = true
		}
	}
	var keys []string
	for _, receipt := range receipts {
		if !receipt.Valid() {
			continue
		}
		root := normalizedRuntimeSkillReceiptName(receipt.SkillName)
		knownRoots[root] = true
		for _, identity := range receipt.Contracts {
			knownMembers[normalizedRuntimeSkillReceiptName(identity.Name)] = true
		}
		closure, complete := runtimeSkillSnapshotClosure(current, root)
		// A receipt alone cannot reconstruct invocation arguments or a filter.
		// The runtime may additionally prove the original canonical full source,
		// latest acknowledgement and matching current rendered closure below.
		matches := complete && len(closure) == len(receipt.Contracts)
		if len(provenContexts) == 0 {
			matches = matches && !parameterizedRoots[root]
		} else {
			proof, proven := provenContexts[0][root]
			matches = matches && proven && proof.receipt.Valid() && proof.receipt.InvocationKey == receipt.InvocationKey
		}
		for index := 0; matches && index < len(closure); index++ {
			matches = closure[index].identity() == receipt.Contracts[index]
		}
		if matches {
			keys = append(keys, receipt.InvocationKey)
			matchingRoots[root] = true
		}
	}
	keep := map[string]bool{}
	keepRoot := func(root string) {
		closure, complete := runtimeSkillSnapshotClosure(current, root)
		if complete {
			for _, contract := range closure {
				keep[normalizedRuntimeSkillReceiptName(contract.skill.Name)] = true
			}
		}
	}
	for name := range current {
		if matchingRoots[name] || !knownMembers[name] && !dependencies[name] {
			keepRoot(name)
		}
	}
	for _, explicit := range explicitRoots {
		name := normalizedRuntimeSkillReceiptName(explicit)
		if !knownRoots[name] {
			keepRoot(name)
		}
	}
	filtered := runtimeSkillClosureSnapshot{}
	views := map[string]*runtimeSkillReplayContextMember{}
	if len(provenContexts) > 0 {
		for root, proof := range provenContexts[0] {
			if matchingRoots[root] {
				for _, item := range proof.members {
					views[normalizedRuntimeSkillReceiptName(item.Name)] = &runtimeSkillReplayContextMember{skill: item, sessionID: proof.sessionID}
				}
			}
		}
	}
	for _, contract := range snapshot.contracts {
		contract.contextView = nil
		if keep[normalizedRuntimeSkillReceiptName(contract.skill.Name)] {
			if view, found := views[normalizedRuntimeSkillReceiptName(contract.skill.Name)]; found {
				contract.contextView = view
			}
			filtered.contracts = append(filtered.contracts, contract)
		}
	}
	var changedRoots []string
	for root := range knownRoots {
		if !matchingRoots[root] {
			changedRoots = append(changedRoots, root)
		}
	}
	return filtered, uniqueSortedFolded(keys), uniqueSortedFolded(changedRoots)
}

// RequiredSkills order is part of the effective contract. Expand the captured
// graph dependency-first exactly as the loader does; missing edges or cycles
// cannot turn a partial current package into a match for historical evidence.
func runtimeSkillSnapshotClosure(current map[string]runtimeSkillContractSnapshot, root string) ([]runtimeSkillContractSnapshot, bool) {
	visited, visiting := map[string]bool{}, map[string]bool{}
	var closure []runtimeSkillContractSnapshot
	var visit func(string) bool
	visit = func(name string) bool {
		name = normalizedRuntimeSkillReceiptName(name)
		if visited[name] {
			return true
		}
		contract, found := current[name]
		if !found || visiting[name] || len(closure) >= sessionRunnerSkillDependencyClosureLimit {
			return false
		}
		visiting[name] = true
		for _, dependency := range contract.skill.RequiredSkills {
			if !visit(dependency) {
				return false
			}
		}
		delete(visiting, name)
		visited[name] = true
		closure = append(closure, contract)
		return true
	}
	complete := visit(root)
	return closure, complete
}

func runtimeSkillReplayContext(changed []string) string {
	if len(changed) == 0 {
		return ""
	}
	return "Earlier Skill contracts cannot be automatically restored for: " + strings.Join(changed, ", ") +
		". Their package or invocation scope is not reconstructed by the current system context. Earlier load receipts remain historical evidence, not a restored active contract. Use the normal Skill loader with the required arguments or filter if needed."
}

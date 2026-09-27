package server

import (
	"context"
	"log"
	"sort"
	"strings"

	workspace "synon-go/internal/persistence/workspace"
	"synon-go/internal/sciencecapability"
)

// bindManagedExecutionInputs runs at the final kernel boundary, including
// approved resumes. It consumes the existing output authority, not a marker,
// model claim, transcript summary or a second evidence store. Execution-only
// argv is pinned to immutable bytes; approval retains the original call.
func (s *Server) bindManagedExecutionInputs(
	ctx context.Context, access workspace.KernelFrameAccess, root, workingDir, name string, input map[string]any,
) (map[string]any, map[string]any, error) {
	engine, found := s.canonicalManagedExecutionPack(name, input)
	if !found {
		return input, nil, nil
	}
	pack := engine.ExecutionPack
	input, blocked, err := s.bindDocumentedExecutionInputs(ctx, root, workingDir, pack, input)
	if err != nil || blocked != nil {
		return input, blocked, err
	}
	guarded := false
	for _, parameter := range pack.Parameters {
		guarded = guarded || parameter.InputEvidence != nil
	}
	if !guarded {
		return input, nil, nil
	}
	if err := ctx.Err(); err != nil {
		return input, nil, err
	}
	args, values, err := executionEvidenceArguments(pack, stringValue(input["command"]))
	if err != nil {
		return input, executionInputAuthorityBoundary(pack, "", "invalid_arguments"), nil
	}
	groups := map[string][]sciencecapability.ExecutionParameter{}
	for _, parameter := range pack.Parameters {
		if parameter.InputEvidence != nil {
			if _, present := values[parameter.Argument]; present {
				group := parameter.InputEvidence.EvidenceGroup
				groups[group] = append(groups[group], parameter)
			}
		}
	}
	if len(groups) == 0 {
		return input, nil, nil
	}
	if s.workspaceStore == nil {
		return input, executionInputAuthorityBoundary(pack, "", "receipts_unavailable"), nil
	}
	records, err := s.workspaceStore.ListExecutionLog(access.Frame.ID, "")
	if err != nil {
		return input, s.executionInputAuthorityError(ctx, access, pack, err), ctx.Err()
	}
	bindings, err := s.workspaceStore.KernelLocalExecutionBindings(ctx, access)
	if err != nil {
		return input, s.executionInputAuthorityError(ctx, access, pack, err), ctx.Err()
	}
	// Unrelated historical packs are not dependencies of this handoff. Their
	// stale output must not poison a valid input chain during recovery.
	allowedByGroup := map[string]map[string]sciencecapability.ExecutionPack{}
	needed := map[string]bool{}
	for group := range groups {
		allowedByGroup[group] = s.executionInputResolverPacks(ctx, pack, group)
		for id := range allowedByGroup[group] {
			needed[id] = true
		}
	}
	excluded := []string{pack.ID}
	for _, capability := range s.scienceCapabilities.Capabilities {
		for _, engine := range capability.AcceptedEngines {
			if !needed[engine.ExecutionPack.ID] {
				excluded = append(excluded, engine.ExecutionPack.ID)
			}
		}
	}
	authorities, err := s.managedExecutionOutputAuthorities(ctx, access, root, records, bindings, excluded...)
	if err != nil {
		return input, s.executionInputAuthorityError(ctx, access, pack, err), ctx.Err()
	}
	ordered := make([]string, 0, len(groups))
	for group := range groups {
		ordered = append(ordered, group)
	}
	sort.Strings(ordered)
	replacements := map[string]string{}
	for _, group := range ordered {
		parameters := groups[group]
		for _, parameter := range pack.Parameters {
			if parameter.InputEvidence != nil && parameter.InputEvidence.EvidenceGroup == group && values[parameter.Argument] == "" {
				return input, executionInputAuthorityBoundary(pack, group, "incomplete_group"), nil
			}
		}
		// Copied or renamed files are eligible only by exact bytes, followed by
		// rebinding to the receipt-owned snapshot. Names never confer authority.
		digests := map[string]string{}
		for _, parameter := range parameters {
			_, digest, err := executionEvidenceFile(ctx, root, workingDir, values[parameter.Argument])
			if err != nil {
				return input, executionInputAuthorityBoundary(pack, group, "input_unavailable"), ctx.Err()
			}
			digests[parameter.Argument] = digest
		}
		allowed := allowedByGroup[group]
		matched := false
		var lineageFailure error
		for _, authority := range authorities {
			producer, allowed := allowed[authority.PackID]
			if !allowed || authority.Unavailable {
				continue
			}
			candidate := map[string]string{}
			for _, parameter := range parameters {
				path, digest := executionEvidenceOutput(root, authority, producer, parameter.InputEvidence.OutputKind)
				if path == "" || digest != digests[parameter.Argument] {
					break
				}
				candidate[parameter.Argument] = path
			}
			if len(candidate) != len(parameters) {
				continue
			}
			lineage, err := s.bindExecutionInputLineage(ctx, root, workingDir, pack, group, values, authority, producer)
			if err != nil {
				if ctx.Err() != nil {
					return input, nil, ctx.Err()
				}
				lineageFailure = err
				continue
			}
			for argument, path := range lineage {
				candidate[argument] = path
			}
			for argument, path := range candidate {
				if prior, found := replacements[argument]; found && prior != path {
					return input, executionInputAuthorityBoundary(pack, group, "conflicting_lineage"), nil
				}
				replacements[argument] = path
			}
			matched = true
			break
		}
		if !matched {
			if lineageFailure != nil {
				log.Printf("execution_input_lineage_unavailable frame=%q pack=%q group=%q error=%q", access.Frame.ID, pack.ID, group,
					truncateFeedbackRunes(redactFeedbackString(lineageFailure.Error()), 1000))
				return input, executionInputAuthorityBoundary(pack, group, executionInputLineageFailureReason(lineageFailure)), nil
			}
			return input, executionInputAuthorityBoundary(pack, group, "no_matching_execution"), nil
		}
	}
	if err := ctx.Err(); err != nil {
		return input, nil, err
	}
	normalized := copyMapAny(input)
	for i := 2; i < len(args)-1; i++ {
		if path, found := replacements[args[i]]; found {
			args[i+1] = path
			i++
		}
	}
	for i := range args {
		args[i] = shellSingleQuote(args[i])
	}
	normalized["command"] = strings.Join(args, " ")
	return normalized, nil, nil
}

func (s *Server) executionInputResolverPacks(ctx context.Context, pack sciencecapability.ExecutionPack, group string) map[string]sciencecapability.ExecutionPack {
	allowed := map[string]sciencecapability.ExecutionPack{}
	var selected []sciencecapability.ExecutionEvidenceResolver
	if run, _ := ctx.Value(transcriptRunnerChatRunContextKey{}).(*sessionRunnerChatRun); run != nil {
		selected = run.selectedEvidenceResolversSnapshot()
	}
	for _, resolver := range pack.EvidenceResolvers {
		if resolver.EvidenceGroup != group {
			continue
		}
		if choice, found := selectedEvidenceResolverFromSelection(selected, group); found &&
			(!strings.EqualFold(choice.Skill, resolver.Skill) || !strings.EqualFold(choice.Implementation, resolver.Implementation)) {
			continue
		}
		for _, producer := range s.scienceCapabilities.EvidenceResolverPacks(resolver) {
			allowed[producer.ID] = producer
		}
	}
	return allowed
}

func executionInputAuthorityBoundary(pack sciencecapability.ExecutionPack, group, reason string) map[string]any {
	result := map[string]any{
		"ok": false, "status": "execution_input_authority_required", "executed": false, "preflight": true,
		"execution_pack_id": pack.ID, "evidence_group": group, "reason": reason,
		"message":  "A derived file input lacks a matching successful upstream execution receipt, immutable output, or input lineage in this task.",
		"recovery": "Use the declared resolver's successful output for the same input. Preserve failed or unavailable evidence; do not manufacture validation files or relabel an unsuccessful result as successful.",
	}
	if reason == "materialization_unavailable" || reason == "receipts_unavailable" {
		result["retryable"] = true
		result["recovery"] = "Restore the task's receipt or immutable-file access, then retry this handoff. Preserve the successful upstream execution; an infrastructure failure does not require recomputing it."
	} else if alternative := executionDocumentedRecoveryOption(pack, group); alternative != nil {
		result["documented_input_alternative"] = alternative
		result["recovery"] = "Use successful resolver output for this input, or the registered documented alternative with its own input digest, sources, method, limitations and matching parameter values. The task may derive and validate those values itself; a new user transcription is not required. Replace automatic-output arguments when choosing the documented route. Preserve failed evidence and do not label a documented assumption as successful automatic output."
	}
	return result
}

func (s *Server) executionInputAuthorityError(ctx context.Context, access workspace.KernelFrameAccess, pack sciencecapability.ExecutionPack, err error) map[string]any {
	if ctx.Err() != nil {
		return nil
	}
	log.Printf("execution_input_authority_unavailable frame=%q pack=%q error=%q", access.Frame.ID, pack.ID,
		truncateFeedbackRunes(redactFeedbackString(err.Error()), 1000))
	return executionInputAuthorityBoundary(pack, "", "receipts_unavailable")
}

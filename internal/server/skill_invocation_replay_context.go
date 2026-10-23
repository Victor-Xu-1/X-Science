package server

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

// These views exist only during one preparation. They are not stored receipts,
// model-supplied grants, catalog mutations, or reusable cross-session caches.
type runtimeSkillReplayContextMember struct {
	skill     skills.Skill
	sessionID string
}

type runtimeSkillReplayContextRoot struct {
	receipt     *agentruntime.SkillLoadReceipt
	publication int64
	members     []skills.Skill
	sessionID   string
}

func (s *Server) prepareRuntimeSkillReplayContext(
	ctx context.Context,
	snapshot runtimeSkillClosureSnapshot,
	receipts []*agentruntime.SkillLoadReceipt,
	explicitRoots []string,
	run *sessionRunnerChatRun,
	sessionID string,
) (runtimeSkillClosureSnapshot, []skills.Skill, []string, []string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(receipts) == 0 {
		filtered, keys, changed := snapshot.forReplay(receipts, explicitRoots)
		prepared, err := filtered.materializeForContext(ctx, s, sessionID)
		return filtered, prepared, keys, changed, err
	}
	current := make(map[string]runtimeSkillContractSnapshot, len(snapshot.contracts))
	for _, item := range snapshot.contracts {
		current[normalizedRuntimeSkillReceiptName(item.skill.Name)] = item
	}
	var names []string
	for _, receipt := range receipts {
		if receipt.Valid() {
			if _, found := current[normalizedRuntimeSkillReceiptName(receipt.SkillName)]; found {
				names = append(names, receipt.SkillName)
			}
		}
	}
	sources, err := s.restoreRuntimeSkillReplaySources(ctx, run, uniqueSortedFolded(names))
	if err != nil {
		return runtimeSkillClosureSnapshot{}, nil, nil, nil, err
	}
	closures := make(map[string][]runtimeSkillContractSnapshot)
	needed := make(map[string]bool)
	for name, source := range sources {
		closure, complete := runtimeSkillSnapshotClosure(current, name)
		if !complete || !source.receipt.Valid() || len(closure) != len(source.receipt.Contracts) {
			continue
		}
		matches := true
		for index, item := range closure {
			matches = matches && item.identity() == source.receipt.Contracts[index]
		}
		if !matches {
			continue
		}
		closures[name] = closure
		for _, item := range closure {
			needed[normalizedRuntimeSkillReceiptName(item.skill.Name)] = true
		}
	}
	// Materialize each captured member once. Path/resource ownership remains
	// the existing workspace owner's responsibility, not an old receipt's grant.
	materialized := make(map[string]skills.Skill, len(needed))
	for _, item := range snapshot.contracts {
		name := normalizedRuntimeSkillReceiptName(item.skill.Name)
		if !needed[name] {
			continue
		}
		prepared, err := s.materializeAgentSkillRuntimeSnapshot(ctx, sessionID, item.skill, item.bundle, item.hasResources)
		if err != nil {
			return runtimeSkillClosureSnapshot{}, nil, nil, nil, fmt.Errorf("prepare original Skill context %q: %w", item.skill.Name, err)
		}
		materialized[name] = prepared
	}
	proofs := make(map[string]runtimeSkillReplayContextRoot)
	for name, closure := range closures {
		source := sources[name]
		items := make([]skills.Skill, 0, len(closure))
		for _, item := range closure {
			items = append(items, materialized[normalizedRuntimeSkillReceiptName(item.skill.Name)])
		}
		effective, prompt, err := renderRuntimeSkillInvocation(items, source.receipt.SkillName, source.arguments, source.filter)
		if err != nil || prompt != source.prompt {
			// Changed connector projection or relocated resource path cannot be
			// repaired by silently substituting today's text into the old load.
			continue
		}
		proofs[name] = runtimeSkillReplayContextRoot{
			receipt: source.receipt.Clone(), publication: source.acknowledgement.PublicationSeq,
			members:   runtimeSkillInvocationContext(effective, source.receipt.SkillName, source.arguments),
			sessionID: sessionID,
		}
	}
	proofs = compatibleRuntimeSkillReplayContexts(proofs)
	all := append([]*agentruntime.SkillLoadReceipt(nil), receipts...)
	for _, proof := range proofs {
		all = append(all, proof.receipt)
	}
	filtered, keys, changed := snapshot.forReplay(all, explicitRoots, proofs)
	prepared, err := filtered.materializeForContext(ctx, s, sessionID)
	if err != nil {
		return runtimeSkillClosureSnapshot{}, nil, nil, nil, err
	}
	_, contextErr := s.runtimeSkillContextFromSkills(prepared)
	if errors.Is(contextErr, errRuntimeSkillContextBudget) && len(keys) > 0 {
		// Individually valid original invocations may not fit together. This
		// optional reconstruction must not fail an otherwise usable task or
		// truncate a body while granting its key. Withdraw all restored proofs
		// and retain the normal explicit-reload path and immutable history.
		filtered, keys, changed = snapshot.forReplay(receipts, explicitRoots, map[string]runtimeSkillReplayContextRoot{})
		prepared, err = filtered.materializeForContext(ctx, s, sessionID)
		if err != nil {
			return runtimeSkillClosureSnapshot{}, nil, nil, nil, err
		}
		_, contextErr = s.runtimeSkillContextFromSkills(prepared)
	}
	if contextErr != nil {
		// Invalid or over-budget baseline selections remain real errors.
		return runtimeSkillClosureSnapshot{}, nil, nil, nil, contextErr
	}
	valid, err := s.runtimeSkillReplayBoundaryCurrent(ctx, run)
	if err != nil {
		return runtimeSkillClosureSnapshot{}, nil, nil, nil, err
	}
	if !valid {
		// Source and workspace reads may finish after a branch/input switch.
		// Retain history, but do not inject or activate any old loaded context.
		filtered, keys, changed = snapshot.forReplay(receipts, explicitRoots, map[string]runtimeSkillReplayContextRoot{})
		prepared, err = filtered.materializeForContext(ctx, s, sessionID)
	}
	return filtered, prepared, keys, changed, err
}

// A shared dependency has one effective body, selected by canonical latest
// successful acknowledgement. An older root whose original full closure would
// conflict must reload; it cannot grant keys beside a competing body.
func compatibleRuntimeSkillReplayContexts(proofs map[string]runtimeSkillReplayContextRoot) map[string]runtimeSkillReplayContextRoot {
	names := make([]string, 0, len(proofs))
	for name := range proofs {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := proofs[names[i]].publication, proofs[names[j]].publication
		if left == right {
			return names[i] < names[j]
		}
		return left > right
	})
	// Different contracts cannot both originate at one canonical publication.
	// Refuse a contradictory tied coordinate rather than choosing map order.
	tied := map[int64]map[string]string{}
	ambiguous := map[int64]map[string]bool{}
	for _, proof := range proofs {
		if tied[proof.publication] == nil {
			tied[proof.publication], ambiguous[proof.publication] = map[string]string{}, map[string]bool{}
		}
		for _, item := range proof.members {
			name := normalizedRuntimeSkillReceiptName(item.Name)
			if body, found := tied[proof.publication][name]; found && body != item.Body {
				ambiguous[proof.publication][name] = true
			}
			tied[proof.publication][name] = item.Body
		}
	}
	bodies := map[string]string{}
	accepted := map[string]runtimeSkillReplayContextRoot{}
	for _, name := range names {
		proof := proofs[name]
		compatible := proof.publication > 0
		for _, item := range proof.members {
			member := normalizedRuntimeSkillReceiptName(item.Name)
			body, found := bodies[member]
			compatible = compatible && !ambiguous[proof.publication][member] && (!found || body == item.Body)
		}
		if compatible {
			accepted[name] = proof
			for _, item := range proof.members {
				bodies[normalizedRuntimeSkillReceiptName(item.Name)] = item.Body
			}
		}
	}
	return accepted
}

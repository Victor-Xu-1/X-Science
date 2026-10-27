package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"x-science/internal/agentruntime"
	"x-science/internal/skills"
)

type runtimeSkillContractSnapshot struct {
	skill          skills.Skill
	bundle         agentSkillRuntimeBundle
	hasResources   bool
	bundleKind     string
	origin         string
	contractSHA256 string
	contextView    *runtimeSkillReplayContextMember
}

type runtimeSkillClosureSnapshot struct {
	contracts []runtimeSkillContractSnapshot
}

// Capture instruction metadata and every package resource once. The publisher
// mutex covers only source inspection; rendering, workspace writes and durable
// execution stay outside it. Neither an optional cached BodyHash nor a tool
// argument is a package identity.
func (s *Server) captureRuntimeSkillClosureSnapshot(items []skills.Skill) (runtimeSkillClosureSnapshot, error) {
	var snapshot runtimeSkillClosureSnapshot
	if len(items) == 0 || len(items) > sessionRunnerSkillDependencyClosureLimit {
		return snapshot, fmt.Errorf("Skill closure has an invalid size")
	}
	err := s.withPublishedRuntimeSkillClosureSnapshot(items, func() error {
		snapshot.contracts = make([]runtimeSkillContractSnapshot, 0, len(items))
		for _, selected := range items {
			// Skill contains slices and maps. Freeze their values with the same
			// complete typed contract that is hashed and subsequently rendered.
			raw, err := json.Marshal(selected)
			if err != nil {
				return err
			}
			var frozen skills.Skill
			if err := json.Unmarshal(raw, &frozen); err != nil {
				return err
			}
			contract := frozen
			contract.BodyHash, contract.Path = "", ""
			identity, err := json.Marshal(contract)
			if err != nil {
				return err
			}
			entry := runtimeSkillContractSnapshot{
				skill: frozen, origin: s.compatibilitySkillSource(frozen),
				contractSHA256: runtimeSkillClosureDigest(identity),
			}
			switch {
			case strings.HasPrefix(frozen.Path, "builtin:"):
				entry.bundleKind = "virtual"
			case strings.TrimSpace(frozen.Path) == "":
				if strings.Contains(frozen.Body, "${X_SCIENCE_SKILL_DIR}") {
					return fmt.Errorf("Skill %q has no runtime package", frozen.Name)
				}
			default:
				_, statErr := os.Stat(frozen.Path)
				if errors.Is(statErr, os.ErrNotExist) && !s.managedRuntimeSkillPath(frozen) &&
					!strings.Contains(frozen.Body, "${X_SCIENCE_SKILL_DIR}") {
					// Old declarative catalog entries can still supply instructions,
					// but a nonexistent package is not a verified virtual bundle.
					break
				}
				if statErr != nil {
					return fmt.Errorf("inspect Skill package %q: %w", frozen.Name, statErr)
				}
				entry.bundle, entry.hasResources, err = loadAgentSkillRuntimeBundle(frozen)
				if err != nil {
					return fmt.Errorf("snapshot Skill package %q: %w", frozen.Name, err)
				}
				entry.bundleKind = "files"
			}
			snapshot.contracts = append(snapshot.contracts, entry)
		}
		return nil
	})
	if err != nil {
		return runtimeSkillClosureSnapshot{}, err
	}
	return snapshot, nil
}

func (snapshot runtimeSkillClosureSnapshot) materialize(
	ctx context.Context,
	s *Server,
	sessionID string,
) ([]skills.Skill, error) {
	items := make([]skills.Skill, len(snapshot.contracts))
	for index, contract := range snapshot.contracts {
		item, err := s.materializeAgentSkillRuntimeSnapshot(
			ctx, sessionID, contract.skill, contract.bundle, contract.hasResources,
		)
		if err != nil {
			return nil, fmt.Errorf("materialize Skill runtime %q: %w", contract.skill.Name, err)
		}
		items[index] = item
	}
	return items, nil
}

func (snapshot runtimeSkillClosureSnapshot) skills() []skills.Skill {
	items := make([]skills.Skill, len(snapshot.contracts))
	for index, contract := range snapshot.contracts {
		items[index] = contract.skill
	}
	return items
}

// Context preparation derives a rendered copy from the same captured bytes.
// Rendering never changes the contract identity or re-reads a published path.
func (snapshot runtimeSkillClosureSnapshot) materializeForContext(ctx context.Context, s *Server, sessionID string) ([]skills.Skill, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	prepared := make([]skills.Skill, len(snapshot.contracts))
	for index, contract := range snapshot.contracts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if contract.contextView != nil {
			if contract.contextView.sessionID != sessionID {
				return nil, errors.New("replayed Skill context belongs to another session")
			}
			prepared[index] = contract.contextView.skill
			continue
		}
		item, err := s.materializeAgentSkillRuntimeSnapshot(ctx, sessionID, contract.skill, contract.bundle, contract.hasResources)
		if err != nil {
			return nil, fmt.Errorf("materialize Skill runtime %q: %w", contract.skill.Name, err)
		}
		prepared[index] = item
		if strings.Contains(prepared[index].Body, "${X_SCIENCE_SKILL_DIR}") {
			prepared[index].Body = renderSkillPrompt(prepared[index], "")
		}
	}
	return prepared, nil
}

func (snapshot runtimeSkillClosureSnapshot) receipt(name, arguments, filter string) (*agentruntime.SkillLoadReceipt, error) {
	identities := make([]agentruntime.SkillContractIdentity, len(snapshot.contracts))
	for index, contract := range snapshot.contracts {
		if contract.bundleKind == "" {
			return nil, nil // Unknown legacy package: preserve load, not exact reuse.
		}
		identities[index] = contract.identity()
	}
	return agentruntime.NewSkillLoadReceipt(name, arguments, filter, identities)
}

func (snapshot runtimeSkillContractSnapshot) identity() agentruntime.SkillContractIdentity {
	return agentruntime.SkillContractIdentity{
		Name: snapshot.skill.Name, Origin: snapshot.origin,
		ContractSHA256: snapshot.contractSHA256, BundleKind: snapshot.bundleKind, BundleSHA256: snapshot.bundle.digest,
	}
}

func bindRuntimeSkillLoadReceipt(raw any, run *sessionRunnerChatRun, callID string) *agentruntime.SkillLoadReceipt {
	receipt, ok := mapValue(raw)["load_receipt"].(*agentruntime.SkillLoadReceipt)
	if !ok || !receipt.Valid() || run == nil || run.Transcript == nil {
		return nil
	}
	bound := receipt.Clone()
	bound.Binding = &agentruntime.SkillLoadBinding{
		StreamUID: run.Transcript.Stream.UID, OwnerID: run.Transcript.Stream.OwnerID,
		InputRevision: run.Transcript.Claim.ClaimedInputRevision,
		RunnerAttempt: run.Transcript.Claim.Attempt, ToolCallID: callID,
	}
	if !bound.BoundToCall(callID) {
		return nil
	}
	return bound
}

func runtimeSkillClosureDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func normalizedRuntimeSkillReceiptName(name string) string {
	return strings.ToLower(strings.TrimPrefix(strings.TrimSpace(name), "/"))
}

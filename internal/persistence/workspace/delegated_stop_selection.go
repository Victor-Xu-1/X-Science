package workspace

import (
	"context"

	transcriptstore "x-science/internal/persistence/transcript"
)

// Descendants are selected only through their own verified branch provenance.
// Unknown legacy origins are not promoted into browser branch authority.
func (s *Store) cancelDelegatedSelectionTx(ctx context.Context, tx *transcriptstore.ImmediateTransaction, owner string, parentScope DelegatedStopScope, target DelegatedStopTarget, seen map[string]bool) ([]DelegatedStopOutcome, error) {
	if seen[target.FrameID] {
		return nil, nil
	}
	seen[target.FrameID] = true
	actual, status, err := delegatedStopTargetTx(ctx, tx, owner, parentScope, target.FrameID)
	if err != nil {
		return nil, err
	}
	if actual != target {
		return nil, ErrDelegatedStopTargetStale
	}
	var result []DelegatedStopOutcome
	if status != "terminal" {
		children, err := delegatedStopSnapshotTx(ctx, tx, owner, target.FrameID, "")
		if err != nil {
			return nil, err
		}
		if children.Scope.ParentFrameIncarnationID != target.FrameIncarnationID || children.Scope.RootFrameID != parentScope.RootFrameID || children.Scope.RootFrameIncarnationID != parentScope.RootFrameIncarnationID {
			return nil, ErrDelegatedStopScopeStale
		}
		for _, child := range children.Targets {
			if !child.InSelectedBranch || !child.CanStop || child.Target == nil {
				continue
			}
			nested, err := s.cancelDelegatedSelectionTx(ctx, tx, owner, children.Scope, *child.Target, seen)
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
		}
	}
	outcome, err := s.cancelDelegatedTargetTx(ctx, tx, owner, parentScope, target)
	if err != nil {
		return nil, err
	}
	return append(result, outcome), nil
}

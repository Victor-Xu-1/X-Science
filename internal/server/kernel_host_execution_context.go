package server

import (
	"context"

	kernelruntime "x-science/internal/kernel"
	workspace "x-science/internal/persistence/workspace"
)

// An authorized execution outlives a detached foreground wait. Keep that
// lifetime while retaining only the server-owned source-definition projection
// and reviewer restriction; the binding still supplies the current live claim.
func (s *Server) agentKernelExecutionHostPolicy(
	ctx context.Context,
	access workspace.KernelFrameAccess,
	workspaceDir string,
	allowedTools []string,
	fresh bool,
	binding kernelTranscriptExecutionBinding,
) *kernelruntime.HostCallPolicy {
	policyContext := context.Background()
	if ctx != nil {
		if source, ok := ctx.Value(specialistExecutionSourceContextKey{}).(specialistExecutionSource); ok {
			policyContext = context.WithValue(policyContext, specialistExecutionSourceContextKey{}, source)
		}
		if reviewerScope := sessionReviewerEvidenceScopeFromContext(ctx); reviewerScope != nil {
			policyContext = withSessionReviewerEvidenceScope(policyContext, reviewerScope)
		}
	}
	policy := s.agentKernelHostCallPolicy(policyContext, access, workspaceDir, allowedTools, fresh, binding)
	if ctx == nil {
		return policy
	}
	run, found := transcriptRunnerChatRunFromContext(ctx)
	if !found || policy.Handler == nil || run.Transcript.Stream != binding.stream ||
		run.Transcript.Claim != binding.claim || binding.operationID == "" ||
		run.KernelOperationIDs[binding.outerToolUseID] != binding.operationID {
		return policy
	}
	// Carry the same bound task identity into detached callbacks without
	// extending their cancellation, method, reviewer or live-claim authority.
	handler := policy.Handler
	policy.Handler = func(callCtx context.Context, call kernelruntime.HostCall) (any, error) {
		if callCtx != nil {
			callCtx = withTranscriptRunnerChatRun(callCtx, run)
		}
		return handler(callCtx, call)
	}
	return policy
}

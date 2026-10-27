package server

import (
	"context"
	"encoding/hex"

	"x-science/internal/agentruntime"
	"x-science/internal/memoryconfig"
	"x-science/internal/memorypolicy"
	"x-science/internal/memorytools"
	workspace "x-science/internal/persistence/workspace"
)

// Runs only while the original completion's live runner is being settled. It
// never reconstructs an oversize proof from a durable diagnostic or old payload.
func (runtime *memoryExtractionRuntime) prepareMemoryModeDecision(ctx context.Context, authority *transcriptRunnerAuthority, scope memorytools.Scope, preference memoryAutomaticPreferences, request *agentruntime.ModelRequest, diagnostic memoryViewDiagnostic, value *workspace.MemoryExtractionCompletion) {
	if authority.Claim.Attempt <= 0 {
		return
	}
	value.ModeDecision = &workspace.MemoryExtractionModeDecision{
		RequestedMode: value.Mode, EffectiveMode: value.Mode, Reason: workspace.MemoryModeConfigured,
		RecoveryPolicy: preference.ForkedViewRecovery, RequestAttempt: authority.Claim.Attempt,
		ModelProfileSHA256: value.Model.ProfileSHA256,
	}
	if diagnostic != memoryViewOversize || value.Diagnostic != string(memoryViewOversize) ||
		value.Mode != "forked" || preference.ForkedViewRecovery != memoryconfig.ForkedViewRecoveryCompact || request == nil {
		return
	}
	// The captured model must still resolve to the same original provider/model
	// configuration. Constructing this client does not submit a model request.
	digest, err := hex.DecodeString(value.Model.ProfileSHA256)
	if err != nil || len(digest) != 32 || value.Model.ProviderID == "" || value.Model.Model == "" || runtime.server.transcriptStore == nil {
		return
	}
	stream, err := runtime.server.transcriptStore.ValidateLiveRunnerClaim(ctx, authority.Claim)
	if err != nil || stream.UID != authority.Stream.UID || stream.SessionID != authority.Stream.SessionID ||
		stream.OwnerID != scope.UserID || stream.ProjectID != scope.ProjectID || stream.FrameID != scope.SourceFrameID {
		return
	}
	pinned := context.WithValue(ctx, memoryExtractionModelContextKey{}, value.Model)
	if _, err := runtime.server.newMemoryRuntimeModelClient(pinned, scope, value.Model.Model, memorypolicy.ExtractionDeadline, 1); err != nil {
		return
	}
	value.Mode = "compact"
	value.Diagnostic = ""
	value.ModeDecision.EffectiveMode = "compact"
	value.ModeDecision.Reason = workspace.MemoryModeForkedOversize
}

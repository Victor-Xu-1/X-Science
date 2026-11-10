package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"x-science/internal/agentruntime"
	transcriptstore "x-science/internal/persistence/transcript"
)

const agentPublicScientificAcquisitionSchema = transcriptstore.NativeDownloadSourceAcquisitionSchema
const nativeScientificSourceLimit = 4096

type nativeScientificAcquisitionGatewayKey struct{}
type nativeScientificSourceInvocationKey struct{}

type nativeScientificSourceInvocation struct {
	CallID, InputSHA256 string
}

func withNativeScientificAcquisitionGateway(ctx context.Context, gateway serverAgentRuntimeToolGateway) context.Context {
	return context.WithValue(ctx, nativeScientificAcquisitionGatewayKey{}, gateway)
}

func (g serverAgentRuntimeToolGateway) nativeScientificAcquisitionAllowed() bool {
	if g.server == nil || g.taskRun == nil || g.taskRun.Transcript == nil || !g.hasToolSnapshot ||
		g.exactToolAuthority || g.directExecutor || g.suppressHooks || !g.toolAllowed("web_fetch") {
		return false
	}
	tool, found := g.server.registeredTool("web_fetch")
	return found && tool.Executable
}

// Check the private phase input again after normal pre-tool hooks, before any
// network execution. A hook may deny it, but cannot redirect an acquisition or
// turn this bounded prefix into an unbounded fetch.
func validateNativeScientificSourceInvocation(ctx context.Context, callID, name string, input map[string]any) error {
	witness, present := ctx.Value(nativeScientificSourceInvocationKey{}).(nativeScientificSourceInvocation)
	if !present {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil || name != "web_fetch" || callID != witness.CallID || scientificSourceDigest(raw) != witness.InputSHA256 {
		return errAgentPublicScientificFileSource
	}
	return nil
}

// acquireNativeScientificSource is a phase of the already-admitted parent,
// never a model proposal or a kernel operation. Direct callers without the
// actual inherited gateway retain the original strict source admission.
func (s *Server) acquireNativeScientificSource(ctx context.Context, run transcriptArtifactRun, parentID string, input map[string]any, request agentPublicScientificFileRequest) error {
	gateway, present := ctx.Value(nativeScientificAcquisitionGatewayKey{}).(serverAgentRuntimeToolGateway)
	if !present || gateway.server != s || !gateway.nativeScientificAcquisitionAllowed() ||
		gateway.taskRun.Transcript.Claim != run.Authority.Claim {
		return errAgentPublicScientificFileSource
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	phase, err := s.newNativeScientificAcquisition(ctx, gateway.taskRun, run, parentID, input, request)
	if err != nil {
		return err
	}
	// The caller just read the immutable projection through the source owner.
	// Recheck after parent admission so an already committed phase wins a retry.
	if _, err := s.validateAgentPublicScientificSourceURL(ctx, run.Authority.Stream.UID, run.Authority.Stream.OwnerID, request); err == nil {
		return nil
	} else if !errors.Is(err, errAgentPublicScientificSourceMissing) {
		return err
	}
	if err := s.checkpointNativeScientificAcquisition(ctx, run, phase, "source_acquisition_start"); err != nil {
		return err
	}
	child := agentruntime.ToolCall{ID: phase.ToolCallID, Name: "web_fetch", Arguments: append(json.RawMessage(nil), phase.ToolInput...)}
	childCtx := context.WithValue(ctx, nativeScientificSourceInvocationKey{}, nativeScientificSourceInvocation{
		CallID: child.ID, InputSHA256: phase.RequestSHA256,
	})
	// A parent's approval never approves a newly required source call. Keep
	// the full snapshot and let the existing child permission/hooks run afresh.
	gateway.resumeAfterApproval = false
	gateway.origin = "native-download-source"
	receipt := gateway.executeGateway(childCtx, child)
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.validateNativeScientificAcquisitionParent(ctx, phase, true); err != nil {
		return err
	}
	phase.ToolResult, err = json.Marshal(receipt.Value)
	if err != nil {
		return err
	}
	// Checkpoints use JSON objects, whose canonical field order differs from
	// typed executor structs. Hash the exact persisted representation so a real
	// result survives checkpoint serialization and restart without losing proof.
	phase.ToolResult, err = json.Marshal(normalizeNativeScientificSourceResult(phase.ToolResult))
	if err != nil {
		return err
	}
	phase.ResultSHA256 = scientificSourceDigest(phase.ToolResult)
	terminal := "source_acquisition_completed"
	if receipt.Err != nil || receipt.Status != "completed" && receipt.Status != "partial" ||
		!agentPublicScientificResultAuthorizesDownload(normalizeNativeScientificSourceResult(phase.ToolResult), request.SourceURL) {
		terminal = "source_acquisition_failed"
	}
	if err := s.checkpointNativeScientificAcquisition(ctx, run, phase, terminal); err != nil {
		return err
	}
	if terminal != "source_acquisition_completed" {
		return errors.Join(errAgentPublicScientificFileSource, receipt.Err)
	}
	return nil
}

func normalizeNativeScientificSourceResult(raw json.RawMessage) any {
	var result any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	_ = decoder.Decode(&result)
	return result
}

func (s *Server) checkpointNativeScientificAcquisition(ctx context.Context, run transcriptArtifactRun, phase nativeScientificAcquisition, state string) error {
	if _, err := s.validateNativeScientificAcquisitionParent(ctx, phase, true); err != nil {
		return err
	}
	phase.ToolPhase = state
	return s.appendNativeScientificSourceFact(ctx, run, phase.nativeScientificSourceParent, phase.ToolCallID+"-"+state, phase, true)
}

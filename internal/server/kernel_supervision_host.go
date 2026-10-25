package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
	kernelruntime "x-science/internal/kernel"
	transcriptstore "x-science/internal/persistence/transcript"
	workspace "x-science/internal/persistence/workspace"
)

const (
	maxKernelDelegateBatch       = 48
	maxKernelCollectBatch        = 1000
	maxKernelStopBatch           = 1000
	maxKernelCollectTimeout      = 1800 * time.Second
	defaultKernelCollectTimeout  = 30 * time.Second
	kernelDelegateSubmitToolName = "submit_output"
)

var kernelChildRuns sync.Map

type kernelMessageBudgets struct {
	mu           sync.Mutex
	peerPosts    int
	asidePosts   int
	peerCallIDs  map[string]struct{}
	asideCallIDs map[string]struct{}
}

func (s *Server) reserveKernelPeerPending(notificationID, targetFrameID string) (int, bool, bool) {
	if s == nil {
		return 0, false, false
	}
	notificationID = strings.TrimSpace(notificationID)
	targetFrameID = strings.TrimSpace(targetFrameID)
	s.kernelPeerPendingMu.Lock()
	defer s.kernelPeerPendingMu.Unlock()
	if s.kernelPeerPending == nil {
		s.kernelPeerPending = map[string]int{}
	}
	if s.kernelPeerReservations == nil {
		s.kernelPeerReservations = map[string]string{}
	}
	pending := s.kernelPeerPending[targetFrameID]
	if existing, found := s.kernelPeerReservations[notificationID]; found {
		return pending, false, existing == targetFrameID
	}
	if pending >= 32 {
		return pending, false, false
	}
	s.kernelPeerPending[targetFrameID] = pending + 1
	s.kernelPeerReservations[notificationID] = targetFrameID
	return pending + 1, true, true
}

func (s *Server) releaseKernelPeerReservations(notificationIDs ...string) {
	if s == nil || len(notificationIDs) == 0 {
		return
	}
	s.kernelPeerPendingMu.Lock()
	defer s.kernelPeerPendingMu.Unlock()
	for _, notificationID := range notificationIDs {
		notificationID = strings.TrimSpace(notificationID)
		targetFrameID, found := s.kernelPeerReservations[notificationID]
		if !found {
			continue
		}
		delete(s.kernelPeerReservations, notificationID)
		remaining := s.kernelPeerPending[targetFrameID] - 1
		if remaining > 0 {
			s.kernelPeerPending[targetFrameID] = remaining
		} else {
			delete(s.kernelPeerPending, targetFrameID)
		}
	}
}

type kernelChildRunKey struct {
	server  *Server
	frameID string
}

// Private lifetime identity, never a frame-ID lookup or a persisted authority.
type kernelChildOwnerContextKey struct{}

type kernelChildRun struct {
	stopCompletion  delegatedStopRegistrationCompletion
	targetMu        sync.Mutex
	target          *workspace.DelegatedStopTarget
	canonicalStop   *workspace.DelegatedStopTarget
	completionClaim transcriptstore.RunnerClaim
	stop            context.CancelFunc
	cancel          context.CancelFunc
	done            chan struct{}
	wake            chan struct{}
}

func (r *kernelChildRun) signal() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func isKernelSupervisionHostMethod(method string) bool {
	switch method {
	case "host.delegate", "host.collect", "host.children", "host.delegation_stats", "host.stop_child", "host.send_message":
		return true
	default:
		return false
	}
}

func (s *Server) handleKernelSupervisionHostCall(ctx context.Context, bound kernelHostExecutionIdentity, access workspace.KernelFrameAccess, method string, args []any, kwargs map[string]any, callID string, messageBudgets *kernelMessageBudgets) (any, error) {
	if s == nil || s.workspaceStore == nil || s.sessionStore == nil || s.eventJournal == nil {
		return nil, kernelruntime.NewHostCallError("unavailable", "child supervision runtime is not configured")
	}
	switch method {
	case "host.delegate":
		requests, options, _, err := parseKernelDelegateCall(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		if len(requests) == 0 {
			return []any{}, nil
		}
		spawnCap := kernelDelegationSpawnCap(access)
		stats, err := s.workspaceStore.KernelDelegationStats(ctx, access.Frame.ID, access.UserID, spawnCap)
		if err != nil {
			return nil, classifyKernelHostError(err)
		}
		if stats.SpawnedThisTask+len(requests) > stats.Cap {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", fmt.Sprintf("delegation spawn cap exceeded: spawned=%d requested=%d cap=%d", stats.SpawnedThisTask, len(requests), stats.Cap))
		}
		validRequests, validIndexes, results := partitionKernelDelegateOutputSchemas(requests)
		validRequests, validIndexes = s.partitionKernelDelegateAuthorities(access, validRequests, validIndexes, results)
		if len(validRequests) == 0 {
			return results, nil
		}
		var admission *workspace.KernelDelegationAdmission
		if !bound.fresh && bound.transcriptExecution.stream.UID != "" {
			identity, err := s.validateKernelTranscriptExecution(ctx, bound, access)
			if err != nil {
				return nil, err
			}
			admission, err = s.workspaceStore.CaptureKernelDelegationAdmission(ctx, access.Frame.ID, access.UserID, callID, identity.OuterToolUseID, identity.RunnerClaim, validRequests)
			if err != nil {
				return nil, classifyKernelHostError(err)
			}
		}
		children, err := s.workspaceStore.CreateKernelSupervisedChildren(ctx, workspace.CreateKernelDelegatesInput{
			Admission:     admission,
			ParentFrameID: access.Frame.ID, OwnerUserID: access.UserID, ToolUseID: callID,
			Requests: validRequests, SpawnCap: spawnCap,
			MaxDepth: s.kernelDelegationMaxDepth(),
		})
		if err != nil {
			return nil, classifyKernelHostError(err)
		}
		concurrency := len(children)
		gate := make(chan struct{}, concurrency)
		validResults := make([]any, len(children))
		for index, child := range children {
			if err := s.ensureKernelChildSession(child); err != nil {
				s.failKernelChildInitialization(child, err)
				validResults[index] = map[string]any{
					"frame_id": child.FrameID, "status": "failed", "dispatched": false,
					"error": "delegated child session initialization failed: " + err.Error(),
				}
				continue
			}
			s.startKernelChildRun(child, gate)
			validResults[index] = kernelChildRunningDescriptor(child, true)
		}
		if !options.Wait {
			return mergeKernelDelegateResults(results, validIndexes, validResults), nil
		}
		validResults = s.waitKernelChildren(ctx, access, children, options.Timeout, true)
		return mergeKernelDelegateResults(results, validIndexes, validResults), nil
	case "host.collect":
		frameIDs, timeout, err := parseKernelCollectCall(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		children := make([]workspace.KernelSupervisedChild, len(frameIDs))
		valid := make([]bool, len(frameIDs))
		for index, frameID := range frameIDs {
			child, found, err := s.workspaceStore.GetKernelSupervisedChild(ctx, access.Frame.ID, frameID, access.UserID)
			if err != nil {
				return nil, classifyKernelHostError(err)
			}
			if found {
				children[index], valid[index] = child, true
				if !child.Dispatched {
					if err := s.ensureKernelChildSession(child); err == nil {
						s.startKernelChildRun(child)
					}
				}
			}
		}
		return s.collectKernelChildren(ctx, access, frameIDs, children, valid, timeout), nil
	case "host.children":
		if len(args) != 0 || len(kwargs) != 0 {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", "host.children takes no arguments")
		}
		children, err := s.workspaceStore.ListKernelActiveChildren(ctx, access.Frame.ID, access.UserID)
		if err != nil {
			return nil, classifyKernelHostError(err)
		}
		items := make([]any, len(children))
		for index, child := range children {
			items[index] = map[string]any{
				"frame_id": child.FrameID, "agent_name": child.AgentName, "name": child.Name,
				"status": child.Status, "task": child.Task,
				"started_at": child.StartedAt.UTC().Format(time.RFC3339Nano),
			}
		}
		return map[string]any{"running_children": items, "count": len(items)}, nil
	case "host.delegation_stats":
		if len(args) != 0 || len(kwargs) != 0 {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", "host.delegation_stats takes no arguments")
		}
		stats, err := s.workspaceStore.KernelDelegationStats(ctx, access.Frame.ID, access.UserID, kernelDelegationSpawnCap(access))
		if err != nil {
			return nil, classifyKernelHostError(err)
		}
		return stats, nil
	case "host.stop_child":
		frameIDs, single, reason, err := parseKernelStopChildCall(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		results := make([]any, len(frameIDs))
		for index, frameID := range frameIDs {
			results[index] = s.stopKernelChild(ctx, access, frameID, reason, callID)
		}
		if single {
			return results[0], nil
		}
		return results, nil
	case "host.send_message":
		target, message, kind, err := parseKernelSendMessageCall(args, kwargs)
		if err != nil {
			return nil, kernelruntime.NewHostCallError("invalid_arguments", err.Error())
		}
		return s.sendKernelChildMessage(ctx, access, target, message, kind, callID, messageBudgets)
	default:
		return nil, kernelruntime.NewHostCallError("method_not_allowed", "host method is not allowed")
	}
}

func (s *Server) failKernelChildInitialization(child workspace.KernelSupervisedChild, failure error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// An accepted delegation replay can fail Session I/O after another input
	// has taken ownership. Only its immutable initial input may be settled;
	// never upgrade this cleanup to the latest dispatch or an unfenced write.
	initial, err := s.workspaceStore.KernelChildInitialTarget(ctx, child)
	if err == nil && initial == nil {
		err = workspace.ErrDelegatedStopOriginUnavailable
	}
	if err == nil {
		_, err = s.workspaceStore.CompleteKernelSupervisedChild(ctx, child.FrameID, child.OwnerUserID, "failed", nil, failure.Error(),
			workspace.KernelChildCompletionFence{Target: *initial})
	}
	if err != nil {
		log.Printf("kernel child initialization failure not settled: frame=%s initialization=%v completion=%v", child.FrameID, failure, err)
	}
}

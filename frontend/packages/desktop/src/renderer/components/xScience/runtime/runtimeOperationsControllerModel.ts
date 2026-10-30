import type { TConversationRuntimeSummary } from '@/common/config/storage';
import type { XScienceRuntimeInvalidation } from '@/renderer/services/xScienceRuntimeOperations';
import type { XSciencePlanReference, XScienceRuntimeSnapshot } from './runtimeOperationsModel';

export const planReferenceIdentity = (reference: XSciencePlanReference | null | undefined): string | null =>
  reference ? JSON.stringify([reference.artifactId, reference.versionId]) : null;

export const TERMINAL_RUNTIME_STATUSES = new Set<NonNullable<XScienceRuntimeInvalidation['terminal_status']>>([
  'completed',
  'failed',
  'cancelled',
]);

export const OFFLINE_RECOVERY_INTERVAL_MS = 2500;
export const MAX_OFFLINE_RECOVERY_INTERVAL_MS = 30_000;
export const ACTIVE_RUNTIME_SNAPSHOT_INTERVAL_MS = 5000;
export const ACTIVE_RUNTIME_STATES = new Set([
  'running',
  'starting',
  'waiting_approval',
  'waiting_confirmation',
  'waiting_input',
  'cancelling',
]);

const PAUSE_TRANSITION_SETTLED_STATUSES = new Set([
  'paused',
  'cancelled',
  'canceled',
  'failed',
  'completed',
  'finished',
  'succeeded',
]);

/**
 * A successful stop request is only locally complete once the authoritative
 * task snapshot reaches an explicit settled state. Until then the previous
 * processing snapshot is stale and must not be projected as running again.
 */
export const isRuntimePauseTransitionSettled = (snapshot: XScienceRuntimeSnapshot | null): boolean =>
  Boolean(snapshot && PAUSE_TRANSITION_SETTLED_STATUSES.has(snapshot.status.trim().toLowerCase()));

export const projectAcceptedRuntimeStart = (
  snapshot: XScienceRuntimeSnapshot | null,
  acceptedStartPending: boolean,
  localSubmissionPending = false
): XScienceRuntimeSnapshot | null => {
  // A previous durable attempt cannot acknowledge the current submission.
  // Stop continues through its same ticket owner before or after dispatch.
  if (snapshot && localSubmissionPending) {
    return {
      ...snapshot,
      status: 'starting',
      statusDescription: null,
      error: null,
      failureReason: null,
      failureKind: null,
      completedAt: null,
      runtimeStage: null,
      runtimeActive: false,
      runtimeTaskActive: false,
      runtimeAttempt: null,
      runtimeInputRevision: null,
      runtimeStartedAt: null,
      runtimeFinishedAt: null,
      runtimeTaskStartedAt: null,
      runtimeTaskFinishedAt: null,
      runtimeElapsedMs: null,
      runtimeElapsedAvailable: false,
      runtimeTaskElapsedMs: null,
      runtimeTaskElapsedAvailable: false,
      taskMetrics: snapshot.taskMetrics
        ? {
            ...snapshot.taskMetrics,
            latest: {
              inputTokens: null,
              outputTokens: null,
              cacheReadTokens: null,
              cacheWriteTokens: null,
              totalTokens: null,
              modelCallCount: null,
              toolCallCount: null,
            },
          }
        : null,
      reviewStatus: null,
      reviewKind: null,
      reviewTrigger: null,
      reviewVerdict: null,
      reviewIssueCount: 0,
      reviewBlockingIssueCount: 0,
      canCancel: true,
      canResume: false,
      pendingInputRequiresContinue: false,
      pendingInputRequests: [],
      planApproval: null,
    };
  }
  if (
    !snapshot ||
    !acceptedStartPending ||
    (!snapshot.pendingInputRequiresContinue && (snapshot.status !== 'completed' || snapshot.runtimeAttempt !== null))
  ) {
    return snapshot;
  }
  return {
    ...snapshot,
    status: 'starting',
    statusDescription: null,
    error: null,
    failureKind: null,
    completedAt: null,
    runtimeActive: true,
    runtimeTaskFinishedAt: null,
    runtimeTaskActive: true,
    canCancel: false,
    canResume: false,
    pendingInputRequiresContinue: false,
  };
};

export const offlineRecoveryDelay = (consecutiveFailures: number): number =>
  Math.min(OFFLINE_RECOVERY_INTERVAL_MS * 2 ** consecutiveFailures, MAX_OFFLINE_RECOVERY_INTERVAL_MS);

export const runtimeSummaryChanged = (
  previous: TConversationRuntimeSummary | null,
  next: TConversationRuntimeSummary
): boolean => {
  if (!previous) return true;
  return (
    previous.state !== next.state ||
    previous.can_send_message !== next.can_send_message ||
    previous.has_task !== next.has_task ||
    previous.task_status !== next.task_status ||
    previous.is_processing !== next.is_processing ||
    previous.pending_confirmations !== next.pending_confirmations ||
    previous.turn_id !== next.turn_id
  );
};

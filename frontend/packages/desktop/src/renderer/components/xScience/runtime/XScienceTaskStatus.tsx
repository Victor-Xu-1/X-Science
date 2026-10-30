/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Spin, Tooltip, Trigger } from '@arco-design/web-react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  currentContextRequestActivity,
  contextRequestActivityLabels,
  type ContextRequestActivity,
} from './contextRequestActivity';
import { Attention, CheckOne, Close, CloseOne, PauseOne, Redo, Right } from '@icon-park/react';
import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import {
  isXScienceLongTaskActive,
  projectXScienceLongTaskStatus,
  type XScienceLongTaskPhase,
  type XScienceLongTaskStatus,
} from './longTaskStatusModel';
import type {
  XScienceReviewStopReason,
  XScienceRuntimeFailureKind,
  XScienceRuntimeSnapshot,
} from './runtimeOperationsModel';
import XScienceTaskCenterPanel, { type XScienceTaskCenterMetrics } from './XScienceTaskCenterPanel';
import { useTaskCapsulePlacement } from './useTaskCapsulePlacement';
import { runtimePausePresentation } from './runtimePausePresentation';
import './XScienceTaskStatus.css';

export type XScienceTaskStatusError = 'load' | 'refresh' | null;

type XScienceTaskStatusProps = {
  contextActivity?: ContextRequestActivity | null;
  contextFrameIncarnationId?: string;
  snapshot: XScienceRuntimeSnapshot | null;
  loading: boolean;
  snapshotError: XScienceTaskStatusError;
  runtimeState?: string;
  localPreparing?: boolean;
  terminalProjectionPending?: boolean;
  runtimeAuthorityUnavailable?: boolean;
  showLoadingStatus?: boolean;
  pausing?: boolean;
  resuming?: boolean;
  pendingInputCount?: number;
  taskCenterMetrics?: XScienceTaskCenterMetrics;
  planAvailable?: boolean;
  recoveryModelLabel?: string | null;
  onResume?: () => void;
  onChooseModel?: () => void;
  onStop?: () => void | Promise<void>;
  onRefresh: () => void;
  onOpenReviewFindings?: () => void;
  onOpenPlan?: () => void;
  onOpenPendingInput?: () => void;
};

const RFC3339_TIMESTAMP = /^(\d{4})-(\d{2})-(\d{2})[Tt](\d{2}):(\d{2}):(\d{2})(?:\.\d+)?([Zz]|([+-])(\d{2}):(\d{2}))$/;

const parseRfc3339Timestamp = (value: string | null): number | null => {
  if (!value) return null;
  const match = RFC3339_TIMESTAMP.exec(value);
  if (!match) return null;
  const [, yearText, monthText, dayText, hourText, minuteText, secondText, zone, , offsetHourText, offsetMinuteText] =
    match;
  const year = Number(yearText);
  const month = Number(monthText);
  const day = Number(dayText);
  const hour = Number(hourText);
  const minute = Number(minuteText);
  const second = Number(secondText);
  const leapYear = year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
  const daysInMonth = [31, leapYear ? 29 : 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  if (month < 1 || month > 12 || day < 1 || day > daysInMonth[month - 1] || hour > 23 || minute > 59 || second > 59) {
    return null;
  }
  if (zone.toUpperCase() !== 'Z') {
    const offsetHour = Number(offsetHourText);
    const offsetMinute = Number(offsetMinuteText);
    if (offsetHour > 14 || offsetMinute > 59 || (offsetHour === 14 && offsetMinute !== 0)) return null;
  }
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? timestamp : null;
};

const elapsedMilliseconds = (status: XScienceLongTaskStatus, now: number): number | null => {
  if (status.elapsedAvailable === false) return null;
  if (status.elapsedMs !== null) {
    if (!status.terminal && status.elapsedActive) {
      const observedAt = parseRfc3339Timestamp(status.elapsedObservedAt);
      return observedAt === null ? status.elapsedMs : status.elapsedMs + Math.max(0, now - observedAt);
    }
    return status.elapsedMs;
  }
  const start = parseRfc3339Timestamp(status.startedAt);
  const end = status.terminal ? parseRfc3339Timestamp(status.completedAt ?? status.updatedAt) : now;
  return start !== null && end !== null && end >= start ? end - start : null;
};

const taskElapsedMilliseconds = (status: XScienceLongTaskStatus, now: number): number | null => {
  if (status.taskElapsedAvailable === false) return null;
  if (status.taskElapsedMs !== null) {
    if (!status.taskCompleted && !status.terminal && status.taskElapsedActive) {
      const observedAt = parseRfc3339Timestamp(status.taskElapsedObservedAt);
      return observedAt === null ? status.taskElapsedMs : status.taskElapsedMs + Math.max(0, now - observedAt);
    }
    return status.taskElapsedMs;
  }
  const start = parseRfc3339Timestamp(status.taskStartedAt);
  const end =
    status.taskCompleted || status.terminal ? parseRfc3339Timestamp(status.taskCompletedAt ?? status.updatedAt) : now;
  return start !== null && end !== null && end >= start ? end - start : null;
};

const hasLiveElapsedClock = (status: XScienceLongTaskStatus): boolean => {
  if (status.taskElapsedAvailable === false) return false;
  if (status.taskCompleted || status.terminal) return false;
  if (status.taskElapsedMs !== null) {
    return status.taskElapsedActive === true && parseRfc3339Timestamp(status.taskElapsedObservedAt) !== null;
  }
  return parseRfc3339Timestamp(status.taskStartedAt) !== null;
};

const formatElapsed = (milliseconds: number): string => {
  const totalSeconds = Math.floor(milliseconds / 1_000);
  const hours = Math.floor(totalSeconds / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  const seconds = totalSeconds % 60;
  const minuteSecond = `${String(minutes).padStart(2, '0')}:${String(seconds).padStart(2, '0')}`;
  return hours > 0 ? `${String(hours).padStart(2, '0')}:${minuteSecond}` : minuteSecond;
};

const padTimestampPair = (part: number): string => String(part).padStart(2, '0');

const formatTaskTimestamp = (value: string | null): string | null => {
  const timestamp = parseRfc3339Timestamp(value);
  if (timestamp === null) return null;
  const date = new Date(timestamp);
  return `${padTimestampPair(date.getMonth() + 1)}/${padTimestampPair(date.getDate())} ${padTimestampPair(
    date.getHours()
  )}:${padTimestampPair(date.getMinutes())}:${padTimestampPair(date.getSeconds())}`;
};

const titleKeys: Record<XScienceLongTaskPhase, string> = {
  queued: 'conversation.xscienceRuntime.runtimeOperations.taskQueued',
  starting: 'conversation.xscienceRuntime.runtimeOperations.taskStarting',
  finalizing: 'conversation.xscienceRuntime.runtimeOperations.taskFinalizing',
  running: 'conversation.xscienceRuntime.runtimeOperations.taskRunning',
  reviewing: 'conversation.xscienceRuntime.runtimeOperations.taskReviewing',
  scientific_reviewing: 'conversation.xscienceRuntime.runtimeOperations.taskScientificReviewing',
  waiting_input: 'conversation.xscienceRuntime.runtimeOperations.taskNeedsAction',
  waiting_approval: 'conversation.xscienceRuntime.runtimeOperations.taskWaitingApproval',
  paused: 'conversation.xscienceRuntime.runtimeOperations.taskPaused',
  cancelling: 'conversation.xscienceRuntime.runtimeOperations.taskCancelling',
  completed: 'conversation.xscienceRuntime.runtimeOperations.taskCompleted',
  failed: 'conversation.xscienceRuntime.runtimeOperations.taskFailed',
  cancelled: 'conversation.xscienceRuntime.runtimeOperations.taskCancelled',
};

const failureReasonKeys: Record<string, string> = {
  response_language_mismatch: 'conversation.xscienceRuntime.runtimeOperations.failurePresentation',
  runner_stop_hook_failed: 'conversation.xscienceRuntime.runtimeOperations.failureFinalization',
  runner_cleanup_failed: 'conversation.xscienceRuntime.runtimeOperations.failureCleanup',
  runner_response_persistence_failed: 'conversation.xscienceRuntime.runtimeOperations.failurePersistence',
  runner_history_preparation_failed: 'conversation.xscienceRuntime.runtimeOperations.failureHistoryPreparation',
  runner_preparation_failed: 'conversation.xscienceRuntime.runtimeOperations.failurePreparation',
};

const reviewStopKeys: Record<XScienceReviewStopReason, string> = {
  no_progress: 'reviewStopNoProgress',
  source_changed: 'reviewStopSourceChanged',
  cancelled: 'reviewStopCancelled',
  model_protocol: 'reviewStopModelProtocol',
  provider_unavailable: 'reviewStopProviderUnavailable',
  unknown: 'reviewStopUnknown',
};

const failureKindKeys: Partial<Record<NonNullable<XScienceRuntimeFailureKind>, string>> = {
  safety_refusal: 'conversation.xscienceRuntime.runtimeOperations.safetyPaused',
  model_not_found: 'conversation.xscienceRuntime.runtimeOperations.modelUnavailable',
  model_overloaded: 'conversation.xscienceRuntime.runtimeOperations.modelBusy',
  service_interrupted: 'conversation.xscienceRuntime.runtimeOperations.runtimeInterrupted',
  not_found: 'conversation.xscienceRuntime.runtimeOperations.failureNotFound',
  unauthorized: 'conversation.xscienceRuntime.runtimeOperations.failureUnauthorized',
  rate_limited: 'conversation.xscienceRuntime.runtimeOperations.failureRateLimited',
  quota_exhausted: 'conversation.xscienceRuntime.runtimeOperations.failureQuotaExhausted',
  invalid_request: 'conversation.xscienceRuntime.runtimeOperations.failureInvalidRequest',
  transient: 'conversation.xscienceRuntime.runtimeOperations.failureTransient',
  image_build_failed: 'conversation.xscienceRuntime.runtimeOperations.failureImageBuildFailed',
  network_denied: 'conversation.xscienceRuntime.runtimeOperations.failureNetworkDenied',
  network_bridge_down: 'conversation.xscienceRuntime.runtimeOperations.failureNetworkBridgeDown',
  ownership_mismatch: 'conversation.xscienceRuntime.runtimeOperations.failureOwnershipMismatch',
  provider_degraded: 'conversation.xscienceRuntime.runtimeOperations.failureProviderDegraded',
  result_rejected: 'conversation.xscienceRuntime.runtimeOperations.failureResultRejected',
};

const ACTIVE_RUNTIME_STATES = new Set([
  'running',
  'starting',
  'waiting_approval',
  'waiting_confirmation',
  'waiting_input',
  'cancelling',
]);

const StatusIcon: React.FC<{
  phase: XScienceLongTaskPhase | null;
  busy: boolean;
  reviewNeedsAttention?: boolean;
}> = ({ phase, busy, reviewNeedsAttention = false }) => {
  if (busy) {
    return (
      <span className='x-science-task-center__icon' data-testid='x-science-task-status-icon' aria-hidden='true'>
        <span className='x-science-task-center__spinner' data-testid='x-science-task-status-spinner'>
          <Spin size={14} />
        </span>
      </span>
    );
  }
  return (
    <span className='x-science-task-center__icon' data-testid='x-science-task-status-icon' aria-hidden='true'>
      {phase === 'failed' || phase === null || reviewNeedsAttention ? <Attention theme='outline' size={16} /> : null}
      {phase === 'cancelled' ? <CloseOne theme='outline' size={16} /> : null}
      {phase === 'completed' && !reviewNeedsAttention ? <CheckOne theme='outline' size={16} /> : null}
      {phase !== 'failed' &&
      phase !== null &&
      phase !== 'cancelled' &&
      phase !== 'completed' &&
      !reviewNeedsAttention ? (
        <PauseOne theme='outline' size={16} />
      ) : null}
    </span>
  );
};

const XScienceTaskStatus: React.FC<XScienceTaskStatusProps> = ({
  contextActivity,
  contextFrameIncarnationId,
  snapshot,
  loading,
  snapshotError,
  runtimeState = 'idle',
  localPreparing = false,
  terminalProjectionPending = false,
  runtimeAuthorityUnavailable = false,
  showLoadingStatus = false,
  pausing = false,
  resuming = false,
  pendingInputCount = 0,
  taskCenterMetrics,
  planAvailable = false,
  recoveryModelLabel = null,
  onResume,
  onChooseModel,
  onStop,
  onOpenReviewFindings,
  onRefresh,
  onOpenPlan,
  onOpenPendingInput,
}) => {
  const { t } = useTranslation();
  const [now, setNow] = useState(() => Date.now());
  const [detailsOpen, setDetailsOpen] = useState(false);
  const closeDetailsForDrag = useCallback(() => setDetailsOpen(false), []);
  const { capsuleRef, capsuleStyle, minimized, setMinimized, dragging, detached, capsulePointerProps } =
    useTaskCapsulePlacement(snapshot?.rootFrameId ?? snapshot?.frameId, closeDetailsForDrag);
  const projected = useMemo(() => (snapshot ? projectXScienceLongTaskStatus(snapshot) : null), [snapshot]);
  const status = projected?.ok ? projected.value : null;
  const failureKindKey = snapshot?.failureKind ? failureKindKeys[snapshot.failureKind] : undefined;
  const failureKindLabel = failureKindKey ? t(failureKindKey) : null;
  const providerPaused = status?.phase === 'paused' && snapshot?.failureReason === 'model_provider_unavailable';
  const providerPause = providerPaused
    ? runtimePausePresentation(snapshot?.failureReason ?? null, snapshot?.providerFailureKind)
    : null;
  const failureReasonKey =
    snapshot?.status === 'failed' && snapshot.failureReason ? failureReasonKeys[snapshot.failureReason] : undefined;
  const publicFailureLabel =
    (failureReasonKey ? t(failureReasonKey) : null) ??
    failureKindLabel ??
    (snapshot?.status === 'failed' ? t('conversation.xscienceRuntime.runtimeOperations.taskFailed') : null);
  const guidedFailureDetail = providerPause
    ? t(`conversation.xscienceRuntime.runtimeOperations.${providerPause.description}`)
    : snapshot?.failureKind === 'model_not_found'
      ? recoveryModelLabel
        ? t('conversation.xscienceRuntime.runtimeOperations.modelUnavailableWithRecovery', {
            model: recoveryModelLabel,
          })
        : t('conversation.xscienceRuntime.runtimeOperations.modelUnavailableDescription')
      : snapshot?.failureKind === 'safety_refusal'
        ? recoveryModelLabel
          ? t('conversation.xscienceRuntime.runtimeOperations.safetyPausedWithRecovery', {
              model: recoveryModelLabel,
            })
          : t('conversation.xscienceRuntime.runtimeOperations.safetyPausedDescription')
        : snapshot?.failureKind === 'service_interrupted'
          ? t('conversation.xscienceRuntime.runtimeOperations.runtimeInterruptedDescription')
          : snapshot?.failureKind === 'model_overloaded'
            ? t('conversation.xscienceRuntime.runtimeOperations.modelBusyDescription')
            : null;
  // Runtime reason codes and backend diagnostics remain available to recovery
  // logic, but are not user-facing copy. The capsule presents the closed,
  // localized failure category from the authoritative runtime projection.
  const presentationFailure = snapshot?.failureReason === 'response_language_mismatch';
  const preparationDetailKey =
    snapshot?.status === 'failed'
      ? snapshot.failureReason === 'runner_history_preparation_failed'
        ? 'conversation.xscienceRuntime.runtimeOperations.failureHistoryPreparationDetail'
        : snapshot.failureReason === 'runner_preparation_failed'
          ? 'conversation.xscienceRuntime.runtimeOperations.failurePreparationDetail'
          : null
      : null;
  const failureDetailSource = presentationFailure
    ? t('conversation.xscienceRuntime.runtimeOperations.failurePresentationDetail')
    : preparationDetailKey
      ? t(preparationDetailKey)
      : (guidedFailureDetail ?? publicFailureLabel);
  const failureDetail = failureDetailSource ? redactErrorText(failureDetailSource).trim().slice(0, 500) || null : null;
  const unsupported = Boolean(snapshot && projected && !projected.ok);
  const busy = loading && !snapshot && !localPreparing;
  const phase = status?.phase ?? null;
  // Pending transcript hydration is not execution liveness. Only a successful
  // completed run can be waiting to display its final answer; failure, pause,
  // cancellation and newer active phases keep their authoritative presentation.
  const completedAnswerPending = terminalProjectionPending && phase === 'completed';
  const specializedFailure =
    snapshot?.failureKind === 'model_not_found' ||
    snapshot?.failureKind === 'safety_refusal' ||
    snapshot?.failureKind === 'model_overloaded' ||
    snapshot?.failureKind === 'service_interrupted';
  const recoveryResumeLabel =
    snapshot?.failureKind === 'model_overloaded'
      ? t('conversation.xscienceRuntime.runtimeOperations.retryLater')
      : (snapshot?.failureKind === 'model_not_found' || snapshot?.failureKind === 'safety_refusal') &&
          recoveryModelLabel
        ? t('conversation.xscienceRuntime.runtimeOperations.continueWithModel', {
            model: recoveryModelLabel,
          })
        : t('conversation.xscienceRuntime.runtimeOperations.continueRunning');
  const chooseModelAction =
    (providerPaused || snapshot?.failureKind === 'model_not_found' || snapshot?.failureKind === 'safety_refusal') &&
    onChooseModel
      ? onChooseModel
      : undefined;
  const taskActive = status ? isXScienceLongTaskActive(status) : false;
  // A cached active snapshot is not proof of a live task after the runtime
  // authority becomes unreachable. Keep the capsule in a neutral reconnecting
  // state until a fresh authoritative snapshot arrives instead of continuing
  // to present the stale phase as "running".
  const authorityPending =
    !busy &&
    snapshotError !== null &&
    (runtimeAuthorityUnavailable || ACTIVE_RUNTIME_STATES.has(runtimeState) || taskActive);
  const resumableRun = Boolean(status?.canResume && onResume);
  const waitingForModel = providerPaused && snapshot?.providerFailureKind === 'configuration_missing';
  const resumablePause =
    resumableRun && !waitingForModel && (status?.phase === 'cancelled' || status?.phase === 'paused');
  const reviewInterrupted = Boolean(
    status?.taskCompleted && ['failed', 'cancelled', 'canceled'].includes(status.reviewStatus ?? '')
  );
  const reviewStopKey = reviewStopKeys[status?.reviewStopReason ?? 'unknown'];
  const reviewStopLabel =
    reviewInterrupted && !authorityPending
      ? t(`conversation.xscienceRuntime.runtimeOperations.${reviewStopKey}`)
      : null;
  const reviewStopDetail =
    reviewInterrupted && !authorityPending
      ? `${t(`conversation.xscienceRuntime.runtimeOperations.${reviewStopKey}Detail`)} ${t('conversation.xscienceRuntime.runtimeOperations.reviewStopResultRetained')}`
      : null;
  const reviewHasFindings = Boolean(
    status?.taskCompleted && status.reviewStatus === 'completed' && status.reviewVerdict === 'revise'
  );
  const reviewHasWarnings = Boolean(
    status?.taskCompleted && status.reviewStatus === 'completed' && status.reviewVerdict === 'pass_with_warnings'
  );
  const reviewCompleted = Boolean(
    status?.taskCompleted &&
    status.reviewStatus === 'completed' &&
    status.reviewVerdict !== 'revise' &&
    status.reviewVerdict !== 'pass_with_warnings'
  );
  const reviewNeedsAttention = reviewInterrupted || reviewHasFindings || reviewHasWarnings;
  const reviewFindingsAvailable = Boolean(
    status && status.reviewIssueCount > 0 && (reviewHasFindings || reviewHasWarnings) && onOpenReviewFindings
  );
  const reviewAvailable = Boolean(
    status?.taskCompleted && status.reviewStatus === 'completed' && snapshot?.reviewFrameId && onOpenReviewFindings
  );
  const pausePending = pausing && taskActive;
  const inlinePause = Boolean((localPreparing || status?.canCancel) && status?.phase !== 'cancelling' && onStop && !pausing);

  useEffect(() => {
    setNow(Date.now());
    if (!status || !hasLiveElapsedClock(status)) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [
    status?.frameId,
    status?.startedAt,
    status?.terminal,
    status?.elapsedMs,
    status?.elapsedAvailable,
    status?.elapsedObservedAt,
    status?.elapsedActive,
    status?.taskStartedAt,
    status?.taskCompletedAt,
    status?.taskElapsedMs,
    status?.taskElapsedAvailable,
    status?.taskElapsedObservedAt,
    status?.taskElapsedActive,
  ]);

  const latestElapsed = status ? elapsedMilliseconds(status, now) : null;
  const taskElapsed = status ? taskElapsedMilliseconds(status, now) : null;
  let title = t('conversation.xscienceRuntime.sendBox.runtimeStatusUnavailable');
  let dataState = phase ?? 'unavailable';
  if (localPreparing) {
    title = t('conversation.xscienceRuntime.runtimeOperations.taskStarting');
    dataState = 'starting';
  } else if (busy || authorityPending) {
    title = t('conversation.xscienceRuntime.runtimeOperations.taskStatusLoading');
    dataState = 'loading';
  } else if (completedAnswerPending) {
    title = t('conversation.xscienceRuntime.runtimeOperations.taskFinalizing');
    dataState = 'finalizing';
  } else if (unsupported) {
    title = t('conversation.xscienceRuntime.runtimeOperations.runtimeStatusUnsupported');
  } else if (pausePending) {
    title = t('conversation.xscienceRuntime.runtimeOperations.taskCancelling');
    dataState = 'cancelling';
  } else if (status) {
    const reviewRunning = status.phase === 'reviewing' || status.phase === 'scientific_reviewing';
    if (reviewInterrupted) {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskCompletedReviewInterrupted');
      dataState = 'completed-review-interrupted';
    } else if (reviewHasFindings) {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskCompletedReviewFindings', {
        count: status.reviewIssueCount,
      });
      dataState = 'completed-review-findings';
    } else if (reviewHasWarnings) {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskCompletedReviewWarnings', {
        count: status.reviewIssueCount,
      });
      dataState = 'completed-review-warnings';
    } else if (reviewCompleted) {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskCompletedReviewCompleted');
      dataState = 'completed-reviewed';
    } else if (reviewRunning && status.reviewTrigger === 'manual') {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskManualReviewing');
    } else if (reviewRunning && status.reviewTrigger === 'auto') {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskAutoReviewing');
    } else if (phase === 'failed' && specializedFailure && failureKindLabel) {
      title = failureKindLabel;
    } else if (waitingForModel) {
      title = t('conversation.xscienceRuntime.runtimeOperations.taskWaitingModel');
      dataState = 'waiting_input';
    } else if (resumablePause || phase === 'paused') {
      const pause = runtimePausePresentation(
        phase === 'paused' ? (status?.failureReason ?? null) : null,
        snapshot?.providerFailureKind
      );
      title = t(`conversation.xscienceRuntime.runtimeOperations.${pause.title}`);
    } else if ((phase === 'running' || phase === 'starting' || phase === 'queued') && snapshot?.retryReason) {
      title = t(
        snapshot.retryReason === 'model_provider_temporarily_unavailable'
          ? 'conversation.xscienceRuntime.runtimeOperations.taskProviderRetrying'
          : 'conversation.xscienceRuntime.runtimeOperations.taskProviderTransportRetrying'
      );
      dataState = 'provider-retrying';
    } else {
      title = t(titleKeys[status.phase]);
    }
  }
  const failure = !localPreparing && (phase === 'failed' || unsupported || (!snapshot && !busy && !authorityPending));
  const elapsedValue = taskElapsed === null ? null : formatElapsed(taskElapsed);
  const taskElapsedValue = taskElapsed === null ? null : formatElapsed(taskElapsed);
  const latestElapsedValue = latestElapsed === null ? null : formatElapsed(latestElapsed);
  const startedAtValue = formatTaskTimestamp(status?.taskStartedAt ?? null);
  const finishedAtValue = formatTaskTimestamp(status?.taskCompletedAt ?? null);
  const elapsedLabel =
    status?.taskElapsedAvailable === false
      ? t('conversation.xscienceRuntime.runtimeOperations.taskElapsedUnavailable')
      : taskElapsed === null
        ? null
        : t('conversation.xscienceRuntime.runtimeOperations.taskTotalElapsed', {
            duration: elapsedValue,
          });
  const attentionCount = pendingInputCount + (snapshot?.planApproval ? 1 : 0);
  const requestActivity =
    !loading && !snapshotError && !runtimeAuthorityUnavailable && (phase === 'running' || phase === 'starting')
      ? currentContextRequestActivity(
          contextActivity,
          snapshot,
          getRendererAccountScopeToken(),
          contextFrameIncarnationId
        )
      : null;
  const requestActivityLabel = requestActivity ? t(contextRequestActivityLabels[requestActivity.phase]) : null;
  const secondaryLabelCandidate =
    attentionCount > 0
      ? t('conversation.xscienceRuntime.runtimeOperations.taskPendingCount', { count: attentionCount })
      : reviewStopLabel
        ? reviewStopLabel
        : phase === 'failed' && publicFailureLabel
          ? specializedFailure
            ? elapsedLabel
            : publicFailureLabel
          : (requestActivityLabel ?? elapsedLabel);
  const secondaryLabel = secondaryLabelCandidate === title ? null : secondaryLabelCandidate;
  const taskCenterAvailable = !busy && Boolean(status || unsupported || authorityPending || failure);

  useEffect(() => {
    setDetailsOpen(false);
  }, [snapshot?.frameId]);

  // Initial hydration has no authoritative task state yet. Do not paint a
  // synthetic status capsule that flashes during refresh; real runtime states
  // render as soon as the authority responds.
  if (busy && !showLoadingStatus) return null;

  const statusIcon = (
    <StatusIcon
      phase={phase}
      busy={localPreparing || busy || authorityPending || completedAnswerPending || resuming || taskActive || pausePending}
      reviewNeedsAttention={reviewNeedsAttention}
    />
  );

  return (
    <div
      className='x-science-task-center mb-8px'
      data-testid='x-science-runtime-status'
      data-state={dataState}
      data-detached={detached ? 'true' : 'false'}
      data-minimized={minimized ? 'true' : 'false'}
      data-failure-kind={snapshot?.failureKind ?? undefined}
      aria-label={t('conversation.xscienceRuntime.runtimeOperations.runtimeControls')}
    >
      <div
        ref={capsuleRef}
        className={`x-science-task-center__pill${minimized ? ' x-science-task-center__pill--minimized' : ''}`}
        data-testid='x-science-task-status-indicator'
        data-state={resumablePause ? 'paused' : dataState}
        data-open={detailsOpen ? 'true' : 'false'}
        data-dragging={dragging ? 'true' : 'false'}
        data-detached={detached ? 'true' : 'false'}
        data-minimized={minimized ? 'true' : 'false'}
        style={capsuleStyle}
        role={failure ? 'alert' : 'status'}
        aria-live='off'
        {...capsulePointerProps}
      >
        {minimized ? (
          <Tooltip content={title} position='top' mini disabled={dragging}>
            <button
              type='button'
              className='x-science-task-center__orb'
              data-testid='x-science-task-status-restore'
              aria-label={t('conversation.xscienceRuntime.runtimeOperations.restoreTaskStatus', {
                status: title,
              })}
              onClick={() => setMinimized(false)}
            >
              {statusIcon}
            </button>
          </Tooltip>
        ) : taskCenterAvailable ? (
          <Trigger
            className='x-science-task-center-popover'
            trigger='click'
            position='top'
            popupVisible={detailsOpen}
            onVisibleChange={setDetailsOpen}
            clickToClose
            clickOutsideToClose
            escToClose
            containerScrollToClose
            updateOnScroll
            autoFixPosition
            popupAlign={{ top: 8 }}
            duration={120}
            unmountOnExit
            popup={() => (
              <XScienceTaskCenterPanel
                requestActivityLabel={requestActivityLabel}
                phase={phase}
                statusTitle={title}
                startedAtValue={startedAtValue}
                finishedAtValue={finishedAtValue}
                taskElapsedValue={taskElapsedValue}
                latestElapsedValue={latestElapsedValue}
                metrics={taskCenterMetrics}
                planAvailable={planAvailable && Boolean(onOpenPlan)}
                planNeedsApproval={Boolean(snapshot?.planApproval && onOpenPlan)}
                reviewAvailable={reviewAvailable}
                reviewFindingsAvailable={reviewFindingsAvailable}
                reviewVerdict={status?.reviewVerdict ?? null}
                reviewIssueCount={status?.reviewIssueCount ?? 0}
                pendingInputCount={pendingInputCount}
                recoveryAvailable={failure || authorityPending || unsupported || providerPaused}
                recoveryResumeLabel={resumableRun ? recoveryResumeLabel : null}
                failureDetail={failureDetail}
                reviewStopDetail={reviewStopDetail}
                onClose={() => setDetailsOpen(false)}
                onRefresh={onRefresh}
                onResume={resumableRun ? onResume : undefined}
                onChooseModel={chooseModelAction}
                onOpenReviewFindings={onOpenReviewFindings}
                onOpenPlan={onOpenPlan}
                onOpenPendingInput={onOpenPendingInput}
              />
            )}
          >
            <button
              type='button'
              className='x-science-task-center__summary-trigger'
              data-testid='x-science-task-details-trigger'
              aria-haspopup='dialog'
              aria-expanded={detailsOpen}
              aria-label={t('conversation.xscienceRuntime.runtimeOperations.openTaskCenter', {
                status: title,
                detail: secondaryLabel ?? '',
              }).trim()}
              title={t('conversation.xscienceRuntime.runtimeOperations.taskCenterTitle')}
            >
              {statusIcon}
              <span
                className='x-science-task-center__title'
                title={title}
                aria-live={failure ? 'assertive' : 'polite'}
                aria-atomic='true'
              >
                {title}
              </span>
              {secondaryLabel ? (
                <span
                  className='x-science-task-center__metric'
                  title={secondaryLabel}
                  aria-live={attentionCount > 0 ? 'polite' : 'off'}
                  data-testid={
                    phase === 'failed' && status?.failureReason
                      ? 'x-science-task-failure-reason'
                      : secondaryLabel === elapsedLabel
                        ? 'x-science-task-elapsed'
                        : undefined
                  }
                >
                  {secondaryLabel}
                </span>
              ) : null}
              <span
                className='x-science-task-center__chevron'
                data-open={detailsOpen ? 'true' : 'false'}
                aria-hidden='true'
              >
                <Right theme='outline' size={12} />
              </span>
            </button>
          </Trigger>
        ) : (
          <div className='x-science-task-center__summary-trigger x-science-task-center__summary-trigger--static'>
            {statusIcon}
            <span
              className='x-science-task-center__title'
              title={title}
              aria-live={failure ? 'assertive' : 'polite'}
              aria-atomic='true'
            >
              {title}
            </span>
            {secondaryLabel ? (
              <span className='x-science-task-center__metric' title={secondaryLabel}>
                {secondaryLabel}
              </span>
            ) : null}
          </div>
        )}
        {!minimized && resumableRun ? (
          <button
            type='button'
            className='x-science-task-center__control'
            data-testid='x-science-task-status-action'
            data-task-capsule-action='true'
            aria-label={recoveryResumeLabel}
            title={recoveryResumeLabel}
            disabled={resuming}
            onClick={() => void onResume?.()}
          >
            <Redo theme='outline' size={14} aria-hidden='true' />
          </button>
        ) : !minimized && inlinePause ? (
          <button
            type='button'
            className='x-science-task-center__control'
            data-testid='x-science-task-pause'
            data-task-capsule-action='true'
            aria-label={t('conversation.xscienceRuntime.runtimeOperations.pauseTask')}
            title={t('conversation.xscienceRuntime.runtimeOperations.pauseTask')}
            onClick={() => void onStop?.()}
          >
            <Close theme='outline' size={14} aria-hidden='true' />
          </button>
        ) : null}
      </div>
    </div>
  );
};

export default XScienceTaskStatus;

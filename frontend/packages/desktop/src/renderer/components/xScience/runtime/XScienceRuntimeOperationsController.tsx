import { PRODUCT_NAME } from '@/common/config/productIdentity';
import type { ContextRequestActivity } from './contextRequestActivity';
import { XScienceContinueIntent, type XScienceContinueInput } from './resumeCommandIntent';
import type { TConversationRuntimeSummary } from '@/common/config/storage';
import { useLayoutContext } from '@/renderer/hooks/context/LayoutContext';
import {
  approveXSciencePlan,
  discardXSciencePlan,
  loadXScienceRuntimeSnapshot,
  resumeXScienceFrame,
  resolveXScienceAskUserRequest,
  resolveXScienceInputRequest,
  subscribeXScienceRuntimeInvalidation,
  type XScienceRuntimeInvalidation,
} from '@/renderer/services/xScienceRuntimeOperations';
import { isXScienceHttpError } from '@/renderer/services/xScienceHttp';
import { invalidateXScienceFrameReads } from '@/renderer/services/xScienceFrameReads';
import { loadXScienceFrameVerification, type XScienceVerificationCheck } from '@/renderer/services/xScienceAnnotations';
import type {
  XScienceApprovalScope,
  XSciencePendingInputRequest,
  XSciencePlanReference,
  XScienceRuntimeSnapshot,
  XScienceRuntimeModelOption,
} from './runtimeOperationsModel';
import {
  chooseXScienceRecoveryModel,
  doesXScienceRuntimeSnapshotNeedActiveRefresh,
  toXScienceRuntimeSummary,
} from './runtimeOperationsModel';
import type { XScienceAskUserResponse } from '@/renderer/services/xScienceRuntimeOperations';
import { Message } from '@arco-design/web-react';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAddEventListener } from '@/renderer/utils/emitter';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import type { ConversationRuntimeView } from '@/renderer/pages/conversation/runtime/conversationRuntimeViewStore';
import { useRealtime } from '@/renderer/hooks/context/RealtimeContext';
import type { XScienceTaskStatusError } from './XScienceTaskStatus';
import type { XScienceTaskCenterMetrics } from './XScienceTaskCenterPanel';
import XScienceRuntimeDrawers from './XScienceRuntimeDrawers';
import XScienceRuntimeStatusSurface from './XScienceRuntimeStatusSurface';
import { useXSciencePlanPreview } from './useXSciencePlanPreview';
import {
  ACTIVE_RUNTIME_SNAPSHOT_INTERVAL_MS,
  ACTIVE_RUNTIME_STATES,
  TERMINAL_RUNTIME_STATUSES,
  isRuntimePauseTransitionSettled,
  offlineRecoveryDelay,
  projectAcceptedRuntimeStart,
  runtimeSummaryChanged,
} from './runtimeOperationsControllerModel';

type XScienceRuntimeOperationsProps = {
  contextActivity?: ContextRequestActivity | null;
  contextFrameIncarnationId?: string;
  conversationId: string;
  runtimeState?: string;
  terminalProjectionPending?: boolean;
  runtimeView?: Pick<ConversationRuntimeView, 'state' | 'terminalProjectionPending' | 'localPreparing'>;
  onResumeStarted?: () => void;
  onResumeFailed?: (reason: string) => void;
  onResumed: (turnId: string, runtime: TConversationRuntimeSummary) => void;
  onRuntimeUpdated?: (turnId: string, runtime: TConversationRuntimeSummary, source: 'initial' | 'refresh') => void;
  onPlanReferenceChange?: (owner: string, reference: XSciencePlanReference | null) => void;
  runtimeAuthorityUnavailable?: boolean;
  onRetryRuntimeAuthority?: () => void;
  onStop?: () => void | Promise<void>;
  onReviewActivityChange?: (active: boolean) => void;
  onRepairAndRegenerate?: (checks: XScienceVerificationCheck[]) => void | Promise<void>;
  taskCenterMetrics?: XScienceTaskCenterMetrics;
  modelInfo?: {
    currentModelId: string | null;
    availableModels: XScienceRuntimeModelOption[];
  } | null;
  onChooseModel?: () => void;
};

const XScienceRuntimeOperationsController: React.FC<XScienceRuntimeOperationsProps> = ({
  contextActivity,
  contextFrameIncarnationId,
  conversationId,
  runtimeState: suppliedRuntimeState = 'idle',
  terminalProjectionPending: suppliedTerminalProjectionPending = false,
  runtimeView,
  onResumeStarted,
  onResumeFailed,
  onResumed,
  onRuntimeUpdated,
  onPlanReferenceChange,
  runtimeAuthorityUnavailable = false,
  onRetryRuntimeAuthority,
  onStop,
  onReviewActivityChange,
  onRepairAndRegenerate,
  taskCenterMetrics,
  modelInfo,
  onChooseModel,
}) => {
  const runtimeState = runtimeView?.state ?? suppliedRuntimeState;
  const terminalProjectionPending = runtimeView?.terminalProjectionPending ?? suppliedTerminalProjectionPending;
  const localPreparing = runtimeView?.localPreparing === true;
  const layout = useLayoutContext();
  const { snapshot: realtimeSnapshot } = useRealtime();
  const { t } = useTranslation();
  const [snapshot, setSnapshot] = useState<XScienceRuntimeSnapshot | null>(null);
  const snapshotRef = useRef<XScienceRuntimeSnapshot | null>(null);
  const publishedRuntimeRef = useRef<TConversationRuntimeSummary | null>(null);
  const acceptedStartPendingRef = useRef(runtimeState === 'starting' || runtimeState === 'running');
  const previousRuntimeAuthorityRef = useRef({ conversationId, runtimeState });
  const onRuntimeUpdatedRef = useRef(onRuntimeUpdated);
  onRuntimeUpdatedRef.current = onRuntimeUpdated;
  const onPlanReferenceChangeRef = useRef(onPlanReferenceChange);
  onPlanReferenceChangeRef.current = onPlanReferenceChange;
  const onReviewActivityChangeRef = useRef(onReviewActivityChange);
  onReviewActivityChangeRef.current = onReviewActivityChange;
  const conversationAuthorityRef = useRef(conversationId);
  conversationAuthorityRef.current = conversationId;
  const pendingInputRef = useRef<HTMLDivElement>(null);
  const loadedConversationRef = useRef<string | null>(null);
  const snapshotRequestRef = useRef<{
    owner: string;
    revision: number;
    controller: AbortController;
    promise: Promise<XScienceRuntimeSnapshot>;
  } | null>(null);
  const openPlanReviewRef = useRef<() => void>(() => {});
  const [loading, setLoading] = useState(true);
  const [ownerTransitionLoading, setOwnerTransitionLoading] = useState(false);
  const [snapshotError, setSnapshotError] = useState<XScienceTaskStatusError>(null);
  const [snapshotRevision, setSnapshotRevision] = useState(0);
  const realtimeRefreshTimerRef = useRef<number | null>(null);
  const [resolvingRequestId, setResolvingRequestId] = useState<string | null>(null);
  const [reviewDrawerVisible, setReviewDrawerVisible] = useState(false);
  const reviewOwnerRef = useRef<string | null>(null);
  const reviewRequestRef = useRef(0);
  const reviewControllerRef = useRef<AbortController | null>(null);
  const [reviewChecks, setReviewChecks] = useState<XScienceVerificationCheck[]>([]);
  const [reviewLoading, setReviewLoading] = useState(false);
  const [reviewError, setReviewError] = useState<string | null>(null);
  const [reviewTrigger, setReviewTrigger] = useState<'auto' | 'manual' | null>(null);
  const [reviewRepairing, setReviewRepairing] = useState(false);
  const inputRequestRef = useRef(0);
  const inputControllerRef = useRef<AbortController | null>(null);
  const resumeRequestRef = useRef(0);
  const resumeControllerRef = useRef<AbortController | null>(null);
  const resumeIntentRef = useRef(new XScienceContinueIntent());
  const retryResumeRef = useRef<() => void>(() => {});
  const [resumeInFlight, setResumeInFlight] = useState(false);
  const [pauseInFlight, setPauseInFlight] = useState(false);
  const planActionRequestRef = useRef(0);
  const planActionControllerRef = useRef<AbortController | null>(null);
  const planPreview = useXSciencePlanPreview(
    conversationId,
    loadedConversationRef.current === conversationId ? (snapshot?.taskPlan ?? null) : null
  );
  const [planAction, setPlanAction] = useState<'approve' | 'discard' | null>(null);
  const [messageApi, messageContextHolder] = Message.useMessage();
  const snapshotReportsActiveRuntime = doesXScienceRuntimeSnapshotNeedActiveRefresh(snapshot);
  const shouldRefreshActiveRuntime = ACTIVE_RUNTIME_STATES.has(runtimeState) || snapshotReportsActiveRuntime;
  const applySnapshot = useCallback((nextSnapshot: XScienceRuntimeSnapshot | null) => {
    snapshotRef.current = nextSnapshot;
    setSnapshot(nextSnapshot);
    onPlanReferenceChangeRef.current?.(conversationAuthorityRef.current, nextSnapshot?.taskPlan ?? null);
    onReviewActivityChangeRef.current?.(
      nextSnapshot?.runtimeStage === 'reviewing' || nextSnapshot?.runtimeStage === 'scientific_reviewing'
    );
  }, []);

  useEffect(() => {
    if (!snapshot || !resolvingRequestId) return;
    // A resumed runner may publish the next AskUser request before the
    // resolution request's finally block runs. Once the authoritative
    // snapshot no longer contains the request that was being submitted, that
    // request is durably settled and must not disable the new card.
    if (!snapshot.pendingInputRequests.some((request) => request.requestId === resolvingRequestId)) {
      setResolvingRequestId(null);
    }
  }, [resolvingRequestId, snapshot]);

  useEffect(() => {
    if (pauseInFlight && isRuntimePauseTransitionSettled(snapshot)) {
      setPauseInFlight(false);
    }
  }, [pauseInFlight, snapshot]);

  const requestSnapshot = useCallback((owner: string, revision: number): Promise<XScienceRuntimeSnapshot> => {
    const current = snapshotRequestRef.current;
    if (current?.owner === owner && current.revision === revision) return current.promise;
    current?.controller.abort();
    const controller = new AbortController();
    const promise = Promise.resolve()
      .then(() => loadXScienceRuntimeSnapshot(owner, { signal: controller.signal }))
      .finally(() => {
        if (snapshotRequestRef.current?.promise === promise) snapshotRequestRef.current = null;
      });
    snapshotRequestRef.current = { owner, revision, controller, promise };
    return promise;
  }, []);

  const applyAuthoritativeSnapshot = useCallback(
    (nextSnapshot: XScienceRuntimeSnapshot, publishRuntime: boolean, source: 'initial' | 'refresh' = 'refresh') => {
      applySnapshot(nextSnapshot);
      let runtime: TConversationRuntimeSummary;
      try {
        runtime = toXScienceRuntimeSummary(nextSnapshot);
      } catch (reason) {
        console.error(
          `[${PRODUCT_NAME} RuntimeOperations] Failed to derive runtime summary`,
          redactErrorText(reason instanceof Error ? reason.message : String(reason))
        );
        return;
      }
      const previous = publishedRuntimeRef.current;
      publishedRuntimeRef.current = runtime;
      if (publishRuntime && runtimeSummaryChanged(previous, runtime)) {
        onRuntimeUpdatedRef.current?.(runtime.turn_id ?? nextSnapshot.rootFrameId, runtime, source);
      }
    },
    [applySnapshot]
  );
  useEffect(() => {
    let active = true;
    const release = subscribeXScienceRuntimeInvalidation(
      conversationId,
      (event?: XScienceRuntimeInvalidation) => {
        if (!active) return;
        if (event?.terminal_status && TERMINAL_RUNTIME_STATUSES.has(event.terminal_status)) {
          if (realtimeRefreshTimerRef.current !== null) {
            window.clearTimeout(realtimeRefreshTimerRef.current);
            realtimeRefreshTimerRef.current = null;
          }
          setSnapshotRevision((value) => value + 1);
          return;
        }
        if (realtimeRefreshTimerRef.current !== null) return;
        realtimeRefreshTimerRef.current = window.setTimeout(() => {
          realtimeRefreshTimerRef.current = null;
          if (active) setSnapshotRevision((value) => value + 1);
        }, 80);
      },
      { getRetrySnapshot: () => snapshotRef.current }
    );
    return () => {
      active = false;
      release();
      if (realtimeRefreshTimerRef.current !== null) {
        window.clearTimeout(realtimeRefreshTimerRef.current);
        realtimeRefreshTimerRef.current = null;
      }
    };
  }, [conversationId]);

  useEffect(() => {
    let active = true;
    const initialLoad = loadedConversationRef.current !== conversationId;
    if (initialLoad) {
      setOwnerTransitionLoading(loadedConversationRef.current !== null);
      loadedConversationRef.current = conversationId;
      publishedRuntimeRef.current = null;
      applySnapshot(null);
      setLoading(true);
      inputRequestRef.current += 1;
      inputControllerRef.current?.abort();
      inputControllerRef.current = null;
      planActionRequestRef.current += 1;
      planActionControllerRef.current?.abort();
      planActionControllerRef.current = null;
      setResolvingRequestId(null);
      setPlanAction(null);
      resumeRequestRef.current += 1;
      resumeControllerRef.current?.abort();
      resumeControllerRef.current = null;
      setResumeInFlight(false);
      setPauseInFlight(false);
      setSnapshotError(null);
    }
    void requestSnapshot(conversationId, snapshotRevision)
      .then((nextSnapshot) => {
        if (active) {
          setSnapshotError(null);
          // The task snapshot is the transcript-aware authority. Publish the
          // first load too: the collection row can still carry a processing
          // Frame after the runner has entered an explicit paused boundary.
          applyAuthoritativeSnapshot(nextSnapshot, true, initialLoad ? 'initial' : 'refresh');
        }
      })
      .catch((reason: unknown) => {
        if (active) {
          console.error(
            `[${PRODUCT_NAME} RuntimeOperations] Failed to load runtime snapshot`,
            redactErrorText(reason instanceof Error ? reason.message : String(reason))
          );
          setSnapshotError(snapshotRef.current ? 'refresh' : 'load');
          if (snapshotRef.current === null) applySnapshot(null);
        }
      })
      .finally(() => {
        if (
          active &&
          conversationAuthorityRef.current === conversationId &&
          loadedConversationRef.current === conversationId
        ) {
          setLoading(false);
          setOwnerTransitionLoading(false);
        }
      });
    return () => {
      active = false;
    };
  }, [applyAuthoritativeSnapshot, applySnapshot, conversationId, requestSnapshot, snapshotRevision]);

  useEffect(() => {
    if (!['offline', 'reconnecting', 'protocol-error'].includes(realtimeSnapshot.status)) return;
    if (!shouldRefreshActiveRuntime) return;
    let active = true;
    let timer: number | null = null;
    let consecutiveFailures = 0;
    const schedule = () => {
      timer = window.setTimeout(() => {
        timer = null;
        void refresh();
      }, offlineRecoveryDelay(consecutiveFailures));
    };
    const refresh = async () => {
      invalidateXScienceFrameReads(conversationId);
      void requestSnapshot(conversationId, snapshotRevision)
        .then((nextSnapshot) => {
          if (active) {
            consecutiveFailures = 0;
            setSnapshotError(null);
            applyAuthoritativeSnapshot(nextSnapshot, true);
          }
        })
        .catch((reason: unknown) => {
          if (!active) return;
          consecutiveFailures = Math.min(consecutiveFailures + 1, 8);
          console.error(
            `[${PRODUCT_NAME} RuntimeOperations] Failed to refresh runtime snapshot`,
            redactErrorText(reason instanceof Error ? reason.message : String(reason))
          );
          setSnapshotError('refresh');
        })
        .finally(() => {
          if (active) schedule();
        });
    };
    schedule();
    return () => {
      active = false;
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [
    applyAuthoritativeSnapshot,
    conversationId,
    realtimeSnapshot.status,
    requestSnapshot,
    shouldRefreshActiveRuntime,
    snapshotRevision,
  ]);

  useEffect(() => {
    // The parent conversation gate can observe a terminal realtime event
    // before this component receives the corresponding frame snapshot. Keep
    // reconciling while either authority still reports an active task so the
    // capsule cannot remain stuck on "running" until a full page reload.
    if (!shouldRefreshActiveRuntime) return;
    if (['offline', 'reconnecting', 'protocol-error'].includes(realtimeSnapshot.status)) return;
    let active = true;
    let timer: number | null = null;
    const refresh = () => {
      invalidateXScienceFrameReads(conversationId);
      void requestSnapshot(conversationId, snapshotRevision)
        .then((nextSnapshot) => {
          if (active) {
            setSnapshotError(null);
            applyAuthoritativeSnapshot(nextSnapshot, true);
          }
        })
        .catch((reason: unknown) => {
          if (!active) return;
          console.error(
            `[${PRODUCT_NAME} RuntimeOperations] Active runtime heartbeat failed`,
            redactErrorText(reason instanceof Error ? reason.message : String(reason))
          );
          setSnapshotError('refresh');
        })
        .finally(() => {
          if (active) {
            timer = window.setTimeout(refresh, ACTIVE_RUNTIME_SNAPSHOT_INTERVAL_MS);
          }
        });
    };
    timer = window.setTimeout(refresh, ACTIVE_RUNTIME_SNAPSHOT_INTERVAL_MS);
    return () => {
      active = false;
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [
    applyAuthoritativeSnapshot,
    conversationId,
    realtimeSnapshot.status,
    requestSnapshot,
    shouldRefreshActiveRuntime,
    snapshotRevision,
  ]);

  useEffect(
    () => () => {
      snapshotRequestRef.current?.controller.abort();
      snapshotRequestRef.current = null;
      reviewControllerRef.current?.abort();
      reviewControllerRef.current = null;
      inputControllerRef.current?.abort();
      inputControllerRef.current = null;
      resumeControllerRef.current?.abort();
      resumeControllerRef.current = null;
      planActionControllerRef.current?.abort();
      planActionControllerRef.current = null;
    },
    []
  );

  const openReviewFindings = () => {
    const currentSnapshot = snapshotRef.current;
    const rootFrameId = currentSnapshot?.rootFrameId;
    if (!rootFrameId) {
      messageApi.info(t('conversation.xscienceRuntime.runtimeOperations.noReviewResult'));
      return;
    }
    const owner = conversationId;
    const request = ++reviewRequestRef.current;
    const reviewerFrameId = currentSnapshot.reviewFrameId;
    reviewControllerRef.current?.abort();
    const controller = new AbortController();
    reviewControllerRef.current = controller;
    reviewOwnerRef.current = owner;
    setReviewDrawerVisible(true);
    setReviewTrigger(currentSnapshot.reviewTrigger);
    setReviewChecks([]);
    setReviewLoading(true);
    setReviewError(null);
    const fetchWithSignal: typeof fetch = (input, init) => fetch(input, { ...init, signal: controller.signal });
    void loadXScienceFrameVerification(rootFrameId, undefined, fetchWithSignal)
      .then((result) => {
        if (conversationAuthorityRef.current !== owner || reviewRequestRef.current !== request) return;
        const checks = reviewerFrameId
          ? result.checks.filter((check) => check.reviewerFrameId === reviewerFrameId)
          : result.checks;
        setReviewChecks(checks);
      })
      .catch((reason: unknown) => {
        if (conversationAuthorityRef.current !== owner || reviewRequestRef.current !== request) return;
        console.error(
          `[${PRODUCT_NAME} RuntimeOperations] Failed to load review findings`,
          redactErrorText(reason instanceof Error ? reason.message : String(reason))
        );
        setReviewError(t('conversation.xscienceRuntime.runtimeOperations.reviewResultLoadFailed'));
      })
      .finally(() => {
        if (conversationAuthorityRef.current === owner && reviewRequestRef.current === request) {
          if (reviewControllerRef.current === controller) reviewControllerRef.current = null;
          setReviewLoading(false);
        }
      });
  };

  const closeReviewFindings = () => {
    reviewRequestRef.current += 1;
    reviewOwnerRef.current = null;
    reviewControllerRef.current?.abort();
    reviewControllerRef.current = null;
    setReviewDrawerVisible(false);
    setReviewChecks([]);
    setReviewLoading(false);
    setReviewError(null);
    setReviewTrigger(null);
    setReviewRepairing(false);
  };

  const repairAndRegenerate = async () => {
    if (!onRepairAndRegenerate || reviewRepairing || reviewChecks.length === 0) return;
    setReviewRepairing(true);
    try {
      await onRepairAndRegenerate(reviewChecks);
      closeReviewFindings();
    } catch (reason) {
      console.error(
        `[${PRODUCT_NAME} RuntimeOperations] Failed to repair and regenerate from review findings`,
        redactErrorText(reason instanceof Error ? reason.message : String(reason))
      );
      messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.repairAndRegenerateFailed'));
    } finally {
      setReviewRepairing(false);
    }
  };

  const refreshRuntime = () => {
    invalidateXScienceFrameReads(conversationId);
    setSnapshotError(null);
    if (snapshotRef.current === null) setLoading(true);
    onRetryRuntimeAuthority?.();
    setSnapshotRevision((value) => value + 1);
  };

  const pauseTask = async () => {
    if (!onStop || pauseInFlight) return;
    const owner = conversationId;
    setPauseInFlight(true);
    try {
      await onStop();
      if (conversationAuthorityRef.current !== owner) return;
      invalidateXScienceFrameReads(owner);
      setSnapshotRevision((value) => value + 1);
    } catch {
      // The owner handles the user-facing failure message. Keep this wrapper
      // settled so the icon-only control cannot create an unhandled rejection.
      if (conversationAuthorityRef.current === owner) {
        setPauseInFlight(false);
      }
    }
  };

  const resumeTask = async (modelOverride?: string) => {
    if (!snapshotRef.current?.canResume || resumeInFlight || resumeControllerRef.current) return;
    const owner = conversationId;
    let input: XScienceContinueInput;
    try {
      input = resumeIntentRef.current.prepare(owner, snapshotRef.current.resumeTarget, modelOverride);
    } catch (reason) {
      if (reason instanceof Error && reason.message === 'resume_pending_controls_changed') {
        messageApi.info(t('conversation.xscienceRuntime.runtimeOperations.resumeRetryOriginal'));
        return;
      }
      refreshRuntime();
      messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.resumeRefreshNeeded'));
      return;
    }
    const operation = ++resumeRequestRef.current;
    resumeControllerRef.current?.abort();
    const controller = new AbortController();
    resumeControllerRef.current = controller;
    setResumeInFlight(true);
    onResumeStarted?.();
    try {
      // Ordinary continuation resolves the currently selected model at call
      // time. A typed unavailable/safety recovery may explicitly choose the
      // verified alternative shown to the user; both paths still use the same
      // backend resume authority.
      const result = await resumeXScienceFrame(owner, input, {
        signal: controller.signal,
      });
      if (conversationAuthorityRef.current !== owner || resumeRequestRef.current !== operation) return;
      resumeIntentRef.current.accepted(owner, input);
      setSnapshotError(null);
      applyAuthoritativeSnapshot(result.snapshot, false);
      onResumed(result.snapshot.rootFrameId, result.runtime);
      messageApi.success(t('conversation.xscienceRuntime.runtimeOperations.resumeSucceeded'));
    } catch (reason) {
      if (conversationAuthorityRef.current !== owner || resumeRequestRef.current !== operation) return;
      if (isXScienceHttpError(reason) && reason.code.startsWith('RESUME_')) {
        onResumeFailed?.(reason.code);
        if (reason.code === 'RESUME_COMMAND_BUSY') {
          resumeIntentRef.current.busy(owner);
          messageApi.info(t('conversation.xscienceRuntime.runtimeOperations.resumeBusy'));
        } else {
          resumeIntentRef.current.reject(owner);
          messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.resumeStateChanged'));
        }
        refreshRuntime();
        return;
      }
      console.error(
        `[${PRODUCT_NAME} RuntimeOperations] Failed to resume task`,
        redactErrorText(reason instanceof Error ? reason.message : String(reason))
      );
      onResumeFailed?.(reason instanceof Error ? reason.message : String(reason));
      messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.resumeFailed'));
    } finally {
      if (conversationAuthorityRef.current === owner && resumeRequestRef.current === operation) {
        if (resumeControllerRef.current === controller) resumeControllerRef.current = null;
        setResumeInFlight(false);
      }
    }
  };
  retryResumeRef.current = () => {
    void resumeTask();
  };
  useEffect(() => {
    if (!resumeInFlight && resumeIntentRef.current.retryAfterSnapshot(conversationId, snapshot?.resumeTarget)) {
      retryResumeRef.current();
    }
  }, [conversationId, snapshot, resumeInFlight]);

  const reconcileInputConflict = async (
    reason: unknown,
    owner: string,
    operation: number,
    controller: AbortController
  ) => {
    if (!isXScienceHttpError(reason) || reason.status !== 409) return false;
    // Both questions and permission requests can expire while their card is
    // visible. The existing snapshot authority decides what remains actionable.
    invalidateXScienceFrameReads(owner);
    const refreshedSnapshot = await loadXScienceRuntimeSnapshot(owner, { signal: controller.signal });
    if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return true;
    setSnapshotError(null);
    applyAuthoritativeSnapshot(refreshedSnapshot, false);
    onResumed(refreshedSnapshot.rootFrameId, toXScienceRuntimeSummary(refreshedSnapshot));
    return true;
  };

  const resolveInput = async (
    request: XSciencePendingInputRequest,
    decision: 'allow' | 'deny',
    scope: XScienceApprovalScope = 'once'
  ) => {
    if (inputControllerRef.current && !inputControllerRef.current.signal.aborted) return;
    const owner = conversationId;
    const operation = ++inputRequestRef.current;
    inputControllerRef.current?.abort();
    const controller = new AbortController();
    inputControllerRef.current = controller;
    setResolvingRequestId(request.requestId);
    onResumeStarted?.();
    try {
      const result = await resolveXScienceInputRequest(conversationId, request, decision, scope, {
        signal: controller.signal,
      });
      if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return;
      applyAuthoritativeSnapshot(result.snapshot, false);
      onResumed(result.snapshot.rootFrameId, result.runtime);
      const allowMessages: Record<XScienceApprovalScope, string> = {
        once: t('conversation.xscienceRuntime.runtimeOperations.approvalAllowedOnce'),
        conversation: t('conversation.xscienceRuntime.runtimeOperations.approvalAllowedConversation'),
        project: t('conversation.xscienceRuntime.runtimeOperations.approvalAllowedProject'),
        always: t('conversation.xscienceRuntime.runtimeOperations.approvalAllowedAlways'),
      };
      messageApi.success(
        decision === 'allow' ? allowMessages[scope] : t('conversation.xscienceRuntime.runtimeOperations.approvalDenied')
      );
    } catch (reason) {
      if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return;
      let reportedReason = reason;
      try {
        if (await reconcileInputConflict(reason, owner, operation, controller)) return;
      } catch (refreshReason) {
        reportedReason = refreshReason;
      }
      if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return;
      console.error(
        `[${PRODUCT_NAME} RuntimeOperations] Failed to resolve approval request`,
        redactErrorText(reportedReason instanceof Error ? reportedReason.message : String(reportedReason))
      );
      onResumeFailed?.(reportedReason instanceof Error ? reportedReason.message : String(reportedReason));
      messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.approvalSubmitFailed'));
      throw reportedReason;
    } finally {
      if (conversationAuthorityRef.current === owner && inputRequestRef.current === operation) {
        if (inputControllerRef.current === controller) inputControllerRef.current = null;
        setResolvingRequestId(null);
      }
    }
  };

  const resolveAskUser = async (request: XSciencePendingInputRequest, response: XScienceAskUserResponse) => {
    if (inputControllerRef.current && !inputControllerRef.current.signal.aborted) return;
    const owner = conversationId;
    const operation = ++inputRequestRef.current;
    inputControllerRef.current?.abort();
    const controller = new AbortController();
    inputControllerRef.current = controller;
    setResolvingRequestId(request.requestId);
    onResumeStarted?.();
    try {
      const result = await resolveXScienceAskUserRequest(conversationId, request, response, {
        signal: controller.signal,
      });
      if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return;
      applyAuthoritativeSnapshot(result.snapshot, false);
      onResumed(result.snapshot.rootFrameId, result.runtime);
      messageApi.success(
        response.action === 'cancel'
          ? t('conversation.xscienceRuntime.runtimeOperations.answerSkipped')
          : response.action === 'discuss'
            ? t('conversation.xscienceRuntime.runtimeOperations.discussionRequested')
            : response.action === 'decide_for_me'
              ? t('conversation.xscienceRuntime.runtimeOperations.delegatedDecision')
              : t('conversation.xscienceRuntime.runtimeOperations.answerSubmitted')
      );
    } catch (reason) {
      if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return;
      let reportedReason = reason;
      try {
        if (await reconcileInputConflict(reason, owner, operation, controller)) return;
      } catch (refreshReason) {
        reportedReason = refreshReason;
      }
      if (conversationAuthorityRef.current !== owner || inputRequestRef.current !== operation) return;
      console.error(
        `[${PRODUCT_NAME} RuntimeOperations] Failed to resolve user question`,
        redactErrorText(reportedReason instanceof Error ? reportedReason.message : String(reportedReason))
      );
      onResumeFailed?.(reportedReason instanceof Error ? reportedReason.message : String(reportedReason));
      messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.answerSubmitFailed'));
      throw reportedReason;
    } finally {
      if (conversationAuthorityRef.current === owner && inputRequestRef.current === operation) {
        if (inputControllerRef.current === controller) inputControllerRef.current = null;
        setResolvingRequestId(null);
      }
    }
  };

  const openPlanReview = () => {
    if (!planPreview.open()) {
      messageApi.info(t('conversation.xscienceRuntime.runtimeOperations.noPlanAvailable'));
    }
  };
  openPlanReviewRef.current = openPlanReview;
  useAddEventListener(
    'xscience.runtime.plan.open',
    (frameId) => {
      if (frameId === conversationAuthorityRef.current) openPlanReviewRef.current();
    },
    []
  );

  const completePlanReview = async (action: 'approve' | 'discard') => {
    if (planActionControllerRef.current && !planActionControllerRef.current.signal.aborted) return;
    const reviewedReference = planPreview.captureDecision(
      snapshotRef.current?.taskPlan,
      snapshotRef.current?.planApproval
    );
    if (!reviewedReference) {
      planPreview.setError(t('conversation.xscienceRuntime.runtimeOperations.planChanged'));
      return;
    }
    const owner = conversationId;
    const request = ++planActionRequestRef.current;
    planActionControllerRef.current?.abort();
    const controller = new AbortController();
    planActionControllerRef.current = controller;
    setPlanAction(action);
    onResumeStarted?.();
    try {
      const result =
        action === 'approve'
          ? await approveXSciencePlan(conversationId, { signal: controller.signal }, reviewedReference)
          : await discardXSciencePlan(conversationId, { signal: controller.signal }, reviewedReference);
      if (conversationAuthorityRef.current !== owner || planActionRequestRef.current !== request) return;
      applyAuthoritativeSnapshot(result.snapshot, false);
      if (planPreview.ownsPreview(reviewedReference)) planPreview.close();
      onResumed(result.snapshot.rootFrameId, result.runtime);
      messageApi.success(
        action === 'approve'
          ? t('conversation.xscienceRuntime.runtimeOperations.planApproved')
          : t('conversation.xscienceRuntime.runtimeOperations.planDiscarded')
      );
    } catch (reason) {
      if (conversationAuthorityRef.current !== owner || planActionRequestRef.current !== request) return;
      console.error(
        `[${PRODUCT_NAME} RuntimeOperations] Failed to complete plan review`,
        redactErrorText(reason instanceof Error ? reason.message : String(reason))
      );
      onResumeFailed?.(reason instanceof Error ? reason.message : String(reason));
      messageApi.error(t('conversation.xscienceRuntime.runtimeOperations.planActionFailed'));
      planPreview.setError(
        t(
          isXScienceHttpError(reason) && reason.status === 409
            ? 'conversation.xscienceRuntime.runtimeOperations.planChanged'
            : 'conversation.xscienceRuntime.runtimeOperations.planActionFailed'
        ),
        reviewedReference
      );
      if (isXScienceHttpError(reason) && reason.status === 409) refreshRuntime();
    } finally {
      if (conversationAuthorityRef.current === owner && planActionRequestRef.current === request) {
        if (planActionControllerRef.current === controller) planActionControllerRef.current = null;
        setPlanAction(null);
      }
    }
  };

  const ownsConversation = loadedConversationRef.current === conversationId;
  const visibleSnapshot = ownsConversation ? snapshot : null;
  const previousRuntimeAuthority = previousRuntimeAuthorityRef.current;
  if (previousRuntimeAuthority.conversationId !== conversationId) {
    acceptedStartPendingRef.current = runtimeState === 'starting' || runtimeState === 'running';
  } else if (
    (runtimeState === 'starting' || runtimeState === 'running') &&
    previousRuntimeAuthority.runtimeState !== 'starting' &&
    previousRuntimeAuthority.runtimeState !== 'running'
  ) {
    acceptedStartPendingRef.current = true;
  }
  if (
    visibleSnapshot &&
    visibleSnapshot.status !== 'completed' &&
    visibleSnapshot.status !== 'failed' &&
    visibleSnapshot.status !== 'cancelled'
  ) {
    acceptedStartPendingRef.current = false;
  } else if (visibleSnapshot && visibleSnapshot.runtimeAttempt !== null) {
    acceptedStartPendingRef.current = false;
  }
  previousRuntimeAuthorityRef.current = { conversationId, runtimeState };
  const statusSnapshot = projectAcceptedRuntimeStart(visibleSnapshot, acceptedStartPendingRef.current, localPreparing);
  const visibleTaskCenterMetrics: XScienceTaskCenterMetrics = {
    total: {
      tokenUsage: statusSnapshot?.taskMetrics?.total?.totalTokens ?? taskCenterMetrics?.total.tokenUsage ?? null,
      modelCallCount:
        statusSnapshot?.taskMetrics?.total?.modelCallCount ?? taskCenterMetrics?.total.modelCallCount ?? null,
      toolCallCount:
        statusSnapshot?.taskMetrics?.total?.toolCallCount ?? taskCenterMetrics?.total.toolCallCount ?? null,
    },
    latest: {
      tokenUsage: statusSnapshot?.taskMetrics?.latest?.totalTokens ?? taskCenterMetrics?.latest.tokenUsage ?? null,
      modelCallCount:
        statusSnapshot?.taskMetrics?.latest?.modelCallCount ?? taskCenterMetrics?.latest.modelCallCount ?? null,
      toolCallCount:
        statusSnapshot?.taskMetrics?.latest?.toolCallCount ?? taskCenterMetrics?.latest.toolCallCount ?? null,
    },
  };
  const planAvailable = Boolean(visibleSnapshot?.taskPlan);
  const recoveryModel = visibleSnapshot
    ? chooseXScienceRecoveryModel(
        visibleSnapshot.modelId,
        modelInfo?.currentModelId ?? null,
        modelInfo?.availableModels ?? []
      )
    : null;
  const requiresAlternativeModel =
    visibleSnapshot?.failureKind === 'model_not_found' || visibleSnapshot?.failureKind === 'safety_refusal';
  const recoveryResumeModelId = requiresAlternativeModel ? recoveryModel?.id : undefined;
  const recoveryCanResume = !requiresAlternativeModel || Boolean(recoveryResumeModelId);
  const visibleLoading = !ownsConversation || loading;
  const pendingRequest = visibleSnapshot?.pendingInputRequests[0];
  const askUserQuestion = pendingRequest?.questions[0] ?? null;
  const openPendingInput = () => {
    const target = pendingInputRef.current;
    if (!target) return;
    target.scrollIntoView?.({ behavior: 'smooth', block: 'nearest' });
    target.focus({ preventScroll: true });
  };

  return (
    <>
      {messageContextHolder}
      <XScienceRuntimeStatusSurface
        contextActivity={contextActivity}
        contextFrameIncarnationId={contextFrameIncarnationId}
        snapshot={statusSnapshot}
        loading={visibleLoading}
        snapshotError={ownsConversation ? snapshotError : null}
        runtimeState={runtimeState}
        localPreparing={localPreparing}
        terminalProjectionPending={terminalProjectionPending}
        runtimeAuthorityUnavailable={runtimeAuthorityUnavailable}
        showLoadingStatus={ownerTransitionLoading}
        pausing={pauseInFlight}
        resuming={resumeInFlight}
        pendingInputCount={visibleSnapshot?.pendingInputRequests.length ?? 0}
        taskCenterMetrics={visibleTaskCenterMetrics}
        planAvailable={planAvailable}
        recoveryModelLabel={recoveryModel?.label ?? null}
        onResume={recoveryCanResume ? () => void resumeTask(recoveryResumeModelId) : undefined}
        onChooseModel={onChooseModel}
        onStop={onStop ? (localPreparing ? () => void onStop() : () => void pauseTask()) : undefined}
        onRefresh={refreshRuntime}
        onOpenReviewFindings={openReviewFindings}
        onOpenPlan={planAvailable ? openPlanReview : undefined}
        onOpenPendingInput={pendingRequest ? openPendingInput : undefined}
        pendingRequest={pendingRequest}
        pendingInputRef={pendingInputRef}
        resolvingRequestId={resolvingRequestId}
        pendingAriaLabel={
          askUserQuestion
            ? t('conversation.xscienceRuntime.runtimeOperations.waitingForAnswer')
            : t('conversation.xscienceRuntime.runtimeOperations.taskWaitingApproval')
        }
        onResolveAskUser={resolveAskUser}
        onResolveInput={resolveInput}
      />
      <XScienceRuntimeDrawers
        mobile={Boolean(layout?.isMobile)}
        reviewTitle={
          reviewTrigger === 'manual'
            ? t('conversation.xscienceRuntime.runtimeOperations.manualReviewResult')
            : t('conversation.xscienceRuntime.runtimeOperations.reviewResult')
        }
        reviewVisible={reviewDrawerVisible && reviewOwnerRef.current === conversationId}
        reviewChecks={reviewChecks}
        reviewLoading={reviewLoading}
        reviewError={reviewError}
        reviewRepairing={reviewRepairing}
        onCloseReview={closeReviewFindings}
        onRetryReview={openReviewFindings}
        onRepairAndRegenerate={onRepairAndRegenerate ? repairAndRegenerate : undefined}
        planTitle={t('conversation.xscienceRuntime.runtimeOperations.taskPlan')}
        planVisible={planPreview.visible}
        planDocument={planPreview.document}
        planReference={planPreview.reference}
        planLoading={planPreview.loading}
        planError={planPreview.error}
        planReplaced={planPreview.replaced}
        planCanRetry={planPreview.canRetry}
        planVersions={planPreview.versions}
        planVersionsLoading={planPreview.versionsLoading}
        planVersionsError={planPreview.versionsError}
        planCanLoadVersions={planPreview.canLoadVersions}
        onLoadPlanVersions={planPreview.loadVersions}
        onSelectPlanVersion={planPreview.selectVersion}
        onOpenCurrentPlan={planPreview.canOpenCurrent ? openPlanReview : undefined}
        planApprovalAvailable={
          planPreview.isSelected(snapshot?.planApproval) && planPreview.isSelected(snapshot?.taskPlan)
        }
        planAction={planAction}
        returnPlanLabel={t('conversation.xscienceRuntime.runtimeOperations.returnPlan')}
        approvePlanLabel={t('conversation.xscienceRuntime.runtimeOperations.approveAndExecute')}
        onClosePlan={planPreview.close}
        onRetryPlan={planPreview.retry}
        onCompletePlan={(action) => void completePlanReview(action)}
      />
    </>
  );
};

export default XScienceRuntimeOperationsController;

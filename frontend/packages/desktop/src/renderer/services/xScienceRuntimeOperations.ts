import {
  normalizeXScienceExecutionLog,
  normalizeXSciencePlanDocument,
  normalizeXScienceRuntimeSnapshot,
  toXScienceRuntimeSummary,
  type XScienceExecutionRecord,
  type XScienceApprovalScope,
  type XSciencePendingInputRequest,
  type XSciencePlanDocument,
  type XSciencePlanReference,
  type XScienceRuntimeSnapshot,
} from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import type { TConversationRuntimeSummary } from '@/common/config/storage';
import { ipcBridge } from '@/common';
import type { IRuntimeStatusEvent } from '@/common/adapter/ipcBridge';
import {
  requestXScienceJson as requestJson,
  type XScienceGatewayOptions as XScienceRuntimeGatewayOptions,
} from './xScienceHttp';
import { normalizeXScienceConversationBranches } from './xScienceConversationBranches';
import { invalidateXScienceFrameReads, loadCachedXScienceFrame } from './xScienceFrameReads';

export type { XScienceGatewayOptions as XScienceRuntimeGatewayOptions } from './xScienceHttp';

export type XScienceAskUserResponse = {
  action: 'answer' | 'decide_for_me' | 'discuss' | 'cancel';
  answers?: Record<string, string>;
  message?: string;
};

export type XScienceAskUserForkResult = {
  rootFrameId: string;
  branchId: string;
  generation: number;
};

export type ForkXScienceAtAskUserAnswerInput = {
  rootFrameId: string;
  sourceFrameId: string;
  sourceBranchId: string;
  toolUseId: string;
  response: XScienceAskUserResponse;
  clientMutationId: string;
};

export type XScienceResumeInput = {
  model?: string;
};

export type XScienceExecutionLogPage = {
  records: XScienceExecutionRecord[];
  nextBefore: string | null;
  total: number;
};

export const X_SCIENCE_EXECUTION_LOG_PAGE_SIZE = 200;

export type XScienceRuntimeInvalidation = Pick<IRuntimeStatusEvent, 'terminal_status'>;

type XScienceRuntimeInvalidationListener = (event?: XScienceRuntimeInvalidation) => void;

const localRuntimeInvalidationListeners = new Map<string, Set<XScienceRuntimeInvalidationListener>>();

export function notifyXScienceRuntimeInvalidation(frameId: string, event?: XScienceRuntimeInvalidation): void {
  const normalizedFrameId = frameId.trim();
  if (!normalizedFrameId) return;
  invalidateXScienceFrameReads(normalizedFrameId);
  for (const listener of localRuntimeInvalidationListeners.get(normalizedFrameId) ?? []) listener(event);
}

export function subscribeXScienceRuntimeInvalidation(
  frameId: string,
  onInvalidate: (event?: XScienceRuntimeInvalidation) => void
): () => void {
  const normalizedFrameId = frameId.trim();
  const invalidate = (event?: XScienceRuntimeInvalidation) => {
    invalidateXScienceFrameReads(normalizedFrameId);
    onInvalidate(event);
  };
  let localListeners = localRuntimeInvalidationListeners.get(normalizedFrameId);
  if (!localListeners) {
    localListeners = new Set();
    localRuntimeInvalidationListeners.set(normalizedFrameId, localListeners);
  }
  localListeners.add(onInvalidate);
  const releaseStatus = ipcBridge.runtime.statusChanged.on((event) => {
    if (event.scope.kind === 'conversation' && event.scope.id === normalizedFrameId) invalidate(event);
  });
  const invalidateConfirmation = (event: { conversation_id: string }) => {
    if (event.conversation_id === normalizedFrameId) invalidate();
  };
  const releaseConfirmationAdd = ipcBridge.conversation.confirmation.add.on(invalidateConfirmation);
  const releaseConfirmationUpdate = ipcBridge.conversation.confirmation.update.on(invalidateConfirmation);
  const releaseConfirmationRemove = ipcBridge.conversation.confirmation.remove.on(invalidateConfirmation);
  const releaseReconnect = ipcBridge.realtime.reconnected.on(() => invalidate());
  let active = true;
  return () => {
    if (!active) return;
    active = false;
    releaseReconnect();
    releaseConfirmationRemove();
    releaseConfirmationUpdate();
    releaseConfirmationAdd();
    releaseStatus();
    localListeners?.delete(onInvalidate);
    if (localListeners?.size === 0) localRuntimeInvalidationListeners.delete(normalizedFrameId);
  };
}

export async function loadXScienceRuntimeSnapshot(
  frameId: string,
  options: XScienceRuntimeGatewayOptions = {}
): Promise<XScienceRuntimeSnapshot> {
  const payload = await loadCachedXScienceFrame(frameId, options);
  const snapshot = normalizeXScienceRuntimeSnapshot(payload);
  if (snapshot.frameId !== frameId) throw new Error('runtime_snapshot_owner_mismatch');
  return snapshot;
}

export async function loadXScienceExecutionLogPage(
  frameId: string,
  before: string | null = null,
  options: XScienceRuntimeGatewayOptions = {}
): Promise<XScienceExecutionLogPage> {
  const query = new URLSearchParams({ limit: String(X_SCIENCE_EXECUTION_LOG_PAGE_SIZE) });
  if (before) query.set('before', before);
  const payload = await requestJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/execution-log?${query.toString()}`,
    {},
    options
  );
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new Error('X-Science execution-log page response is invalid');
  }
  const record = payload as Record<string, unknown>;
  if (!Array.isArray(record.records) || !Number.isSafeInteger(record.total) || Number(record.total) < 0) {
    throw new Error('X-Science execution-log page response is invalid');
  }
  const nextBefore = record.next_before;
  if (nextBefore !== undefined && typeof nextBefore !== 'string') {
    throw new Error('X-Science execution-log page cursor is invalid');
  }
  return {
    records: normalizeXScienceExecutionLog(record.records),
    nextBefore: typeof nextBefore === 'string' && nextBefore ? nextBefore : null,
    total: Number(record.total),
  };
}

export async function loadXSciencePlanDocument(
  artifactId: string,
  options: XScienceRuntimeGatewayOptions = {}
): Promise<XSciencePlanDocument> {
  const payload = await requestJson<unknown>(`/api/artifacts/${encodeURIComponent(artifactId)}`, {}, options);
  return normalizeXSciencePlanDocument(payload);
}

export async function resumeXScienceFrame(
  frameId: string,
  input: XScienceResumeInput = {},
  options: XScienceRuntimeGatewayOptions = {}
): Promise<{ snapshot: XScienceRuntimeSnapshot; runtime: TConversationRuntimeSummary }> {
  await requestJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/resume`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(input),
    },
    options
  );
  invalidateXScienceFrameReads(frameId);
  const snapshot = await loadXScienceRuntimeSnapshot(frameId, options);
  return { snapshot, runtime: toXScienceRuntimeSummary(snapshot) };
}

export async function resolveXScienceInputRequest(
  frameId: string,
  request: XSciencePendingInputRequest,
  decision: 'allow' | 'deny',
  scope: XScienceApprovalScope = 'once',
  options: XScienceRuntimeGatewayOptions = {}
): Promise<{ snapshot: XScienceRuntimeSnapshot; runtime: TConversationRuntimeSummary }> {
  const approved = decision === 'allow';
  await requestJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/resolve-input`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        responses: [
          {
            requestId: request.requestId,
            ...(request.toolId ? { tool_id: request.toolId } : {}),
            approved,
            action: decision,
            ...(approved && scope !== 'once' ? { scope } : {}),
            ...(approved && (request.mode === 'ro' || request.mode === 'rw') ? { mode: request.mode } : {}),
          },
        ],
      }),
    },
    options
  );
  invalidateXScienceFrameReads(frameId);
  const snapshot = await loadXScienceRuntimeSnapshot(frameId, options);
  return { snapshot, runtime: toXScienceRuntimeSummary(snapshot) };
}

export async function resolveXScienceAskUserRequest(
  frameId: string,
  request: XSciencePendingInputRequest,
  response: XScienceAskUserResponse,
  options: XScienceRuntimeGatewayOptions = {}
): Promise<{ snapshot: XScienceRuntimeSnapshot; runtime: TConversationRuntimeSummary }> {
  await requestJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/resolve-input`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        responses: [
          {
            requestId: request.requestId,
            ...(request.toolId ? { tool_id: request.toolId } : {}),
            approved: response.action !== 'cancel',
            action: response.action,
            ...(response.answers ? { answers: response.answers } : {}),
            ...(response.message ? { message: response.message } : {}),
          },
        ],
      }),
    },
    options
  );
  invalidateXScienceFrameReads(frameId);
  const snapshot = await loadXScienceRuntimeSnapshot(frameId, options);
  return { snapshot, runtime: toXScienceRuntimeSummary(snapshot) };
}

export async function forkXScienceAtAskUserAnswer(
  input: ForkXScienceAtAskUserAnswerInput,
  options: XScienceRuntimeGatewayOptions = {}
): Promise<XScienceAskUserForkResult> {
  const { rootFrameId, sourceFrameId, sourceBranchId, toolUseId, response } = input;
  if (sourceFrameId !== rootFrameId) throw new Error('ask_user_branch_source_must_use_root_conversation');
  if (!/^br_[0-9a-f]{8}$/.test(sourceBranchId)) throw new Error('AskUser source branch is unavailable');
  const clientMutationId = input.clientMutationId.trim();
  if (!clientMutationId) throw new Error('AskUser branch mutation identity is required');
  const branchState = normalizeXScienceConversationBranches(
    rootFrameId,
    await requestJson<unknown>(`/api/frames/${encodeURIComponent(rootFrameId)}/branches`, {}, options)
  );
  if (!branchState.branches.some((branch) => branch.id === sourceBranchId)) {
    throw new Error('AskUser source branch is unavailable');
  }
  const payload = await requestJson<unknown>(
    `/api/frames/${encodeURIComponent(rootFrameId)}/fork-at-answer`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json', 'Idempotency-Key': clientMutationId },
      body: JSON.stringify({
        tool_use_id: toolUseId,
        response,
        source_branch_id: sourceBranchId,
        expected_branch_id: branchState.activeBranchId,
        expected_generation: branchState.generation,
        client_mutation_id: clientMutationId,
      }),
    },
    options
  );
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) {
    throw new Error('X-Science fork-at-answer response is invalid');
  }
  const record = payload as Record<string, unknown>;
  const responseRootFrameId = typeof record.root_frame_id === 'string' ? record.root_frame_id.trim() : '';
  const branchId = typeof record.branch_id === 'string' ? record.branch_id.trim() : '';
  const generation =
    typeof record.generation === 'number' && Number.isSafeInteger(record.generation) ? record.generation : 0;
  if (responseRootFrameId !== rootFrameId || !/^br_[0-9a-f]{8}$/.test(branchId) || generation <= 0) {
    throw new Error('X-Science fork-at-answer response is invalid');
  }
  invalidateXScienceFrameReads(rootFrameId);
  return { rootFrameId: responseRootFrameId, branchId, generation };
}

export async function approveXSciencePlan(
  frameId: string,
  options: XScienceRuntimeGatewayOptions = {},
  expectedPlan?: XSciencePlanReference
): Promise<{ snapshot: XScienceRuntimeSnapshot; runtime: TConversationRuntimeSummary }> {
  return completePlanReview(frameId, 'approve-plan', options, expectedPlan);
}

export async function discardXSciencePlan(
  frameId: string,
  options: XScienceRuntimeGatewayOptions = {},
  expectedPlan?: XSciencePlanReference
): Promise<{ snapshot: XScienceRuntimeSnapshot; runtime: TConversationRuntimeSummary }> {
  return completePlanReview(frameId, 'discard-plan', options, expectedPlan);
}

async function completePlanReview(
  frameId: string,
  operation: 'approve-plan' | 'discard-plan',
  options: XScienceRuntimeGatewayOptions,
  expectedPlan?: XSciencePlanReference
): Promise<{ snapshot: XScienceRuntimeSnapshot; runtime: TConversationRuntimeSummary }> {
  await requestJson<unknown>(
    `/api/frames/${encodeURIComponent(frameId)}/${operation}`,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(
        expectedPlan
          ? {
              expected_plan_artifact_id: expectedPlan.artifactId,
              expected_plan_version_id: expectedPlan.versionId ?? '',
            }
          : {}
      ),
    },
    options
  );
  invalidateXScienceFrameReads(frameId);
  const snapshot = await loadXScienceRuntimeSnapshot(frameId, options);
  return { snapshot, runtime: toXScienceRuntimeSummary(snapshot) };
}

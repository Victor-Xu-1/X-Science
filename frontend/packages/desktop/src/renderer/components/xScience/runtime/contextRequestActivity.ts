import type { ContextUsageState } from '@/renderer/hooks/xScience/useContextUsage';
import type { XScienceRuntimeSnapshot } from './runtimeOperationsModel';

export type ContextRequestActivity = {
  accountScope: string;
  conversationId: string;
  frameIncarnationId: string;
  attempt: number;
  claimedInputRevision: number;
  requestId: string;
  phase: 'requesting' | 'thinking' | 'generating';
  observedAt: string;
};

/** Read-only projection of the existing context owner, never a second request or execution authority. */
export function contextRequestActivity(state: ContextUsageState, accountScope: string): ContextRequestActivity | null {
  if (state.status !== 'available' || state.refreshState) return null;
  const value = state.snapshot;
  const association = value.association;
  if (
    value.state !== 'request' ||
    value.observationKind !== 'model_call' ||
    !association ||
    association.runnerAttempt !== value.attempt ||
    association.scope.conversationId !== state.conversationId
  )
    return null;
  const phase = value.progress?.phase;
  if (phase === 'compacting') return null;
  return {
    accountScope,
    conversationId: state.conversationId,
    frameIncarnationId: association.scope.frameIncarnationId,
    attempt: association.runnerAttempt,
    claimedInputRevision: association.claimedInputRevision,
    requestId: value.requestId,
    phase: phase ?? 'requesting',
    observedAt: value.progress?.observedAt ?? value.observedAt,
  };
}

export function currentContextRequestActivity(
  activity: ContextRequestActivity | null | undefined,
  snapshot: XScienceRuntimeSnapshot | null,
  accountScope: string,
  frameIncarnationId: string | undefined
): ContextRequestActivity | null {
  if (
    !activity ||
    !snapshot ||
    !frameIncarnationId ||
    activity.accountScope !== accountScope ||
    activity.conversationId !== snapshot.frameId ||
    activity.frameIncarnationId !== frameIncarnationId ||
    activity.attempt !== snapshot.runtimeAttempt ||
    activity.claimedInputRevision !== snapshot.runtimeInputRevision
  )
    return null;
  return activity;
}

export const contextRequestActivityLabels = {
  requesting: 'conversation.xscienceRuntime.runtimeOperations.modelActivityRequesting',
  thinking: 'conversation.xscienceRuntime.runtimeOperations.modelActivityThinking',
  generating: 'conversation.xscienceRuntime.runtimeOperations.modelActivityGenerating',
} as const;

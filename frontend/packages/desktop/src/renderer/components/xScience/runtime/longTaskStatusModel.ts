/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  projectXScienceTaskAuthority,
  type XScienceTaskAuthorityPhase,
  type XScienceRuntimeSnapshot,
} from './runtimeOperationsModel';

export type XScienceLongTaskPhase = XScienceTaskAuthorityPhase;

export type XScienceLongTaskStatus = {
  frameId: string;
  rootFrameId: string;
  phase: XScienceLongTaskPhase;
  terminal: boolean;
  taskCompleted: boolean;
  description: string | null;
  error: string | null;
  failureReason: string | null;
  startedAt: string | null;
  completedAt: string | null;
  updatedAt: string | null;
  elapsedMs: number | null;
  elapsedObservedAt: string | null;
  elapsedActive: boolean | null;
  taskStartedAt: string | null;
  taskCompletedAt: string | null;
  taskElapsedMs: number | null;
  taskElapsedObservedAt: string | null;
  taskElapsedActive: boolean | null;
  canCancel: boolean;
  canResume: boolean;
  reviewStatus: string | null;
  reviewDescription: string | null;
  reviewTrigger: 'auto' | 'manual' | null;
  reviewVerdict: XScienceRuntimeSnapshot['reviewVerdict'];
  reviewIssueCount: number;
  reviewBlockingIssueCount: number;
};

export type XScienceLongTaskStatusResult =
  | { ok: true; value: XScienceLongTaskStatus }
  | { ok: false; code: 'long_task_status_unsupported' };

const terminalPhases = new Set<XScienceLongTaskPhase>(['completed', 'failed', 'cancelled']);

export function projectXScienceLongTaskStatus(
  snapshot: XScienceRuntimeSnapshot
): XScienceLongTaskStatusResult {
  const authority = projectXScienceTaskAuthority(snapshot);
  if (!authority) return { ok: false, code: 'long_task_status_unsupported' };
  const { phase, taskCompleted } = authority;

  return {
    ok: true,
    value: {
      frameId: snapshot.frameId,
      rootFrameId: snapshot.rootFrameId,
      phase,
      terminal: terminalPhases.has(phase),
      taskCompleted,
      description: snapshot.statusDescription,
      error: snapshot.error,
      failureReason: snapshot.failureReason,
      startedAt: snapshot.runtimeStartedAt ?? snapshot.createdAt,
      completedAt: snapshot.runtimeFinishedAt ?? snapshot.completedAt,
      updatedAt: snapshot.updatedAt,
      elapsedMs: snapshot.runtimeElapsedMs,
      elapsedObservedAt: snapshot.runtimeObservedAt,
      elapsedActive: snapshot.runtimeActive,
      taskStartedAt: snapshot.runtimeTaskStartedAt ?? snapshot.runtimeStartedAt ?? snapshot.createdAt,
      taskCompletedAt: snapshot.runtimeTaskFinishedAt ?? snapshot.runtimeFinishedAt ?? snapshot.completedAt,
      taskElapsedMs: snapshot.runtimeTaskElapsedMs ?? snapshot.runtimeElapsedMs,
      taskElapsedObservedAt: snapshot.runtimeTaskObservedAt ?? snapshot.runtimeObservedAt,
      taskElapsedActive: snapshot.runtimeTaskActive ?? snapshot.runtimeActive,
      canCancel: snapshot.canCancel,
      canResume: snapshot.canResume,
      reviewStatus: snapshot.reviewStatus,
      reviewDescription: snapshot.reviewDescription,
      reviewTrigger: snapshot.reviewTrigger,
      reviewVerdict: snapshot.reviewVerdict,
      reviewIssueCount: snapshot.reviewIssueCount,
      reviewBlockingIssueCount: snapshot.reviewBlockingIssueCount,
    },
  };
}

export const isXScienceLongTaskActive = (status: XScienceLongTaskStatus): boolean =>
  status.phase === 'queued' ||
  status.phase === 'starting' ||
  status.phase === 'finalizing' ||
  status.phase === 'running' ||
  status.phase === 'reviewing' ||
  status.phase === 'scientific_reviewing' ||
  status.phase === 'cancelling';

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  projectXScienceLongTaskStatus,
  type XScienceLongTaskStatus,
} from '@/renderer/components/xScience/runtime/longTaskStatusModel';
import type { XScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { describe, expect, it } from 'vitest';

const snapshot = (status: string, overrides: Partial<XScienceRuntimeSnapshot> = {}): XScienceRuntimeSnapshot => ({
  frameId: 'frame-1',
  rootFrameId: 'root-1',
  status,
  statusDescription: null,
  error: null,
  failureKind: null,
  failureReason: null,
  errorStatus: null,
  modelId: null,
  taskMetrics: null,
  createdAt: '2026-07-22T07:58:00Z',
  completedAt: null,
  updatedAt: '2026-07-22T08:00:00Z',
  runtimeElapsedMs: null,
  runtimeObservedAt: null,
  runtimeStartedAt: null,
  runtimeFinishedAt: null,
  runtimeActive: null,
  runtimeTaskElapsedMs: null,
  runtimeTaskObservedAt: null,
  runtimeTaskStartedAt: null,
  runtimeTaskFinishedAt: null,
  runtimeTaskActive: null,
  runtimeAttempt: null,
  runtimeInputRevision: null,
  runtimeStage: null,
  reviewStatus: null,
  reviewKind: null,
  reviewProfile: null,
  reviewFrameId: null,
  reviewDescription: null,
  reviewTrigger: null,
  reviewVerdict: null,
  reviewIssueCount: 0,
  reviewBlockingIssueCount: 0,
  canCancel: false,
  canResume: false,
  pendingInputRequests: [],
  planApproval: null,
  ...overrides,
});

const expectStatus = (status: string, phase: XScienceLongTaskStatus['phase']) => {
  const result = projectXScienceLongTaskStatus(snapshot(status));
  expect(result).toMatchObject({ ok: true, value: { phase } });
};

describe('longTaskStatusModel', () => {
  it('maps only the current authoritative frame statuses into typed phases', () => {
    expectStatus('queued', 'queued');
    expectStatus('pending', 'queued');
    expectStatus('starting', 'starting');
    expectStatus('processing', 'running');
    expectStatus('running', 'running');
    expectStatus('cancelling', 'cancelling');
    expectStatus('completed', 'completed');
    expectStatus('finished', 'completed');
    expectStatus('failed', 'failed');
    expectStatus('cancelled', 'cancelled');
    expectStatus('canceled', 'cancelled');
  });

  it('labels a pending accepted input as starting while the runner claim is pending', () => {
    const result = projectXScienceLongTaskStatus(
      snapshot('processing', {
        runtimeStage: 'starting',
        runtimeElapsedMs: 2_500,
        runtimeObservedAt: '2026-08-10T08:00:00Z',
        runtimeStartedAt: '2026-08-10T07:59:57.500Z',
        runtimeActive: true,
      })
    );

    expect(result).toMatchObject({
      ok: true,
      value: { phase: 'starting', terminal: false, elapsedMs: 2_500, elapsedActive: true },
    });
  });
  it('distinguishes user input and plan approval without inventing progress', () => {
    expect(
      projectXScienceLongTaskStatus(
        snapshot('awaiting_user_response', {
          pendingInputRequests: [
            {
              requestId: 'request-1',
              toolId: null,
              kind: 'ask_user',
              tool: null,
              code: null,
              description: null,
              environment: null,
              mode: null,
              questions: [],
            },
          ],
        })
      )
    ).toMatchObject({ ok: true, value: { phase: 'waiting_input' } });
    expect(
      projectXScienceLongTaskStatus(
        snapshot('awaiting_plan_approval', {
          planApproval: { artifactId: 'artifact-plan', versionId: 'version-plan' },
        })
      )
    ).toMatchObject({ ok: true, value: { phase: 'waiting_approval' } });
  });

  it('lets an authoritative pending action override a lagging processing label', () => {
    expect(
      projectXScienceLongTaskStatus(
        snapshot('processing', {
          pendingInputRequests: [
            {
              requestId: 'request-1',
              toolId: 'tool-1',
              kind: 'local_exec',
              tool: 'python',
              code: 'print(1)',
              description: null,
              environment: 'python',
              mode: 'live',
              questions: [],
            },
          ],
        })
      )
    ).toMatchObject({ ok: true, value: { phase: 'waiting_input' } });
    expect(
      projectXScienceLongTaskStatus(
        snapshot('processing', {
          planApproval: { artifactId: 'artifact-plan', versionId: 'version-plan' },
        })
      )
    ).toMatchObject({ ok: true, value: { phase: 'waiting_approval' } });
  });

  it('preserves failed and cancelled as distinct terminal states', () => {
    const failed = projectXScienceLongTaskStatus(
      snapshot('failed', { error: 'provider unavailable', failureReason: 'provider_unavailable', canResume: true })
    );
    const cancelled = projectXScienceLongTaskStatus(snapshot('cancelled', { canResume: true }));

    expect(failed).toMatchObject({
      ok: true,
      value: {
        phase: 'failed',
        terminal: true,
        error: 'provider unavailable',
        failureReason: 'provider_unavailable',
        canResume: true,
      },
    });
    expect(cancelled).toMatchObject({
      ok: true,
      value: { phase: 'cancelled', terminal: true, error: null, canResume: true },
    });
  });

  it('projects active and failed reviews without replacing a completed task', () => {
    expect(
      projectXScienceLongTaskStatus(
        snapshot('completed', {
          runtimeStage: 'reviewing',
          reviewStatus: 'processing',
          reviewTrigger: 'manual',
          runtimeActive: false,
        })
      )
    ).toMatchObject({
      ok: true,
      value: {
        phase: 'reviewing',
        terminal: false,
        taskCompleted: true,
        reviewStatus: 'processing',
        reviewTrigger: 'manual',
      },
    });
    expect(
      projectXScienceLongTaskStatus(
        snapshot('completed', {
          runtimeStage: 'review_failed',
          reviewStatus: 'failed',
          reviewDescription: 'Reviewer model returned invalid structured tool data',
        })
      )
    ).toMatchObject({
      ok: true,
      value: { phase: 'completed', terminal: true, taskCompleted: true, reviewStatus: 'failed' },
    });
  });

  it('recognizes a non-terminal paused run without fabricating user-action boundaries', () => {
    expect(projectXScienceLongTaskStatus(snapshot('paused', { canResume: true }))).toMatchObject({
      ok: true,
      value: { phase: 'paused', terminal: false, canResume: true },
    });
    expect(projectXScienceLongTaskStatus(snapshot('awaiting_user_response'))).toMatchObject({
      ok: true,
      value: { phase: 'running', terminal: false },
    });
    expect(projectXScienceLongTaskStatus(snapshot('awaiting_plan_approval'))).toMatchObject({
      ok: true,
      value: { phase: 'running', terminal: false },
    });
  });

  it('exposes no fabricated checkpoint, cost, percentage, or ETA fields', () => {
    const result = projectXScienceLongTaskStatus(
      snapshot('processing', { statusDescription: 'Querying public databases', canCancel: true })
    );
    expect(result.ok).toBe(true);
    if (!result.ok) return;

    expect(result.value).toMatchObject({
      frameId: 'frame-1',
      rootFrameId: 'root-1',
      phase: 'running',
      description: 'Querying public databases',
      startedAt: '2026-07-22T07:58:00Z',
      completedAt: null,
      canCancel: true,
      canResume: false,
    });
    expect(result.value).not.toHaveProperty('checkpoint');
    expect(result.value).not.toHaveProperty('cost');
    expect(result.value).not.toHaveProperty('progress');
    expect(result.value).not.toHaveProperty('eta');
  });

  it('prefers transcript-owned timing fields while preserving legacy timestamps as fallback', () => {
    const result = projectXScienceLongTaskStatus(
      snapshot('processing', {
        runtimeElapsedMs: 120_000,
        runtimeObservedAt: '2026-08-10T08:00:00Z',
        runtimeStartedAt: '2026-08-10T07:57:00Z',
        runtimeFinishedAt: null,
        runtimeActive: true,
        runtimeTaskElapsedMs: 600_000,
        runtimeTaskObservedAt: '2026-08-10T08:00:00Z',
        runtimeTaskStartedAt: '2026-08-09T22:00:00Z',
        runtimeTaskFinishedAt: null,
        runtimeTaskActive: true,
      })
    );
    expect(result).toMatchObject({
      ok: true,
      value: {
        startedAt: '2026-08-10T07:57:00Z',
        completedAt: null,
        elapsedMs: 120_000,
        elapsedObservedAt: '2026-08-10T08:00:00Z',
        elapsedActive: true,
        taskStartedAt: '2026-08-09T22:00:00Z',
        taskCompletedAt: null,
        taskElapsedMs: 600_000,
        taskElapsedObservedAt: '2026-08-10T08:00:00Z',
        taskElapsedActive: true,
      },
    });
  });
});

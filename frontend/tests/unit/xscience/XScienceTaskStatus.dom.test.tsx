/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import XScienceTaskStatus from '@/renderer/components/xScience/runtime/XScienceTaskStatus';
import {
  normalizeXScienceRuntimeSnapshot,
  type XScienceRuntimeSnapshot,
} from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const snapshot = (status: string, overrides: Partial<XScienceRuntimeSnapshot> = {}): XScienceRuntimeSnapshot => ({
  frameId: 'frame-status-center',
  rootFrameId: 'frame-status-center',
  projectId: 'project-1',
  agentName: 'OPERON',
  isHidden: false,
  status,
  statusDescription: 'Querying public biomedical databases',
  error: null,
  failureKind: null,
  failureReason: null,
  errorStatus: null,
  modelId: 'ark-code-latest',
  taskMetrics: null,
  createdAt: '2026-08-10T07:59:00Z',
  completedAt: null,
  updatedAt: '2026-08-10T08:00:00Z',
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
  canCancel: status === 'processing',
  canResume: status === 'failed' || status === 'cancelled' || status === 'paused',
  pendingInputRequests: [],
  taskPlan: null,
  planApproval: null,
  ...overrides,
});

const defaultProps = {
  loading: false,
  snapshotError: null,
  onRefresh: vi.fn(),
};

const expectDetailsSurfaceClosed = () => {
  expect(screen.queryByTestId('x-science-task-status-panel')).not.toBeInTheDocument();
  expect(screen.queryByRole('region', { name: /task details|任务状态详情/i })).not.toBeInTheDocument();
};

describe('XScienceTaskStatus', () => {
  it.each(['en-US', 'zh-CN'] as const)(
    '443 explains stopped manual review safely without failing or retrying the completed task in %s',
    async (language) => {
      const onResume = vi.fn();
      const onRefresh = vi.fn();
      const value = normalizeXScienceRuntimeSnapshot({
        id: 'review-root',
        status: 'completed',
        runtime_active: false,
        runtime_review_frame_id: 'completion-review-current',
        runtime_review_status: 'failed',
        runtime_review_trigger: 'manual',
        runtime_stage: 'review_failed',
        runtime_review_stop_reason: 'no_progress',
        runtime_review_description: 'PRIVATE_REVIEW_DIAGNOSTIC',
      });
      await renderWithI18n(
        <XScienceTaskStatus {...defaultProps} snapshot={value} onResume={onResume} onRefresh={onRefresh} />,
        language
      );
      const capsule = screen.getByTestId('x-science-task-status-indicator');
      expect(capsule).toHaveAttribute('role', 'status');
      expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute(
        'data-state',
        'completed-review-interrupted'
      );
      fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
      const detail = screen.getByTestId('x-science-task-center-review-stop-detail');
      expect(detail).toHaveTextContent(
        language === 'en-US' ? /repeated work made no progress/i : /重复操作未产生新进展/
      );
      expect(detail).toHaveTextContent(language === 'en-US' ? /completed task.*unchanged/i : /已完成任务.*不变/);
      expect(detail).toHaveTextContent(language === 'en-US' ? /Manual review/ : /手动审阅/);
      expect(document.body).not.toHaveTextContent('PRIVATE_REVIEW_DIAGNOSTIC');
      expect(screen.queryByTestId('x-science-task-center-failure-detail')).not.toBeInTheDocument();
      expect(screen.queryByTestId('x-science-task-status-spinner')).not.toBeInTheDocument();
      expect(onResume).not.toHaveBeenCalled();
      expect(onRefresh).not.toHaveBeenCalled();
    }
  );

  it('443 keeps missing or malformed historical reasons unknown and clears the old explanation on owner replacement', async () => {
    const old = normalizeXScienceRuntimeSnapshot({
      id: 'old-root',
      status: 'completed',
      runtime_review_status: 'failed',
      runtime_review_stop_reason: { private: 'no_progress' },
      runtime_review_description: 'PRIVATE_NO_PROGRESS',
    });
    const view = await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={old} />, 'en-US');
    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveTextContent('Review stop reason unavailable');
    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.getByTestId('x-science-task-center-review-stop-detail')).toHaveTextContent('no reliable stop reason');
    expect(document.body).not.toHaveTextContent('PRIVATE_NO_PROGRESS');
    const next = normalizeXScienceRuntimeSnapshot({
      id: 'new-owner-root',
      status: 'completed',
      runtime_stage: 'reviewing',
      runtime_review_status: 'processing',
      runtime_review_trigger: 'manual',
    });
    view.rerender(<XScienceTaskStatus {...defaultProps} snapshot={next} />);
    await waitFor(expectDetailsSurfaceClosed);
    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveTextContent('Manual review in progress');
    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.queryByTestId('x-science-task-center-review-stop-detail')).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent('no reliable stop reason');
  });

  it('C419 return focus hands Plan the task center persistent opener before disposing its temporary action', async () => {
    let handedFocus: Element | null = null;
    const onOpenPlan = vi.fn(() => {
      handedFocus = document.activeElement;
    });
    await renderWithI18n(
      <XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} planAvailable onOpenPlan={onOpenPlan} />,
      'en-US'
    );
    const trigger = screen.getByTestId('x-science-task-details-trigger');
    trigger.focus();
    fireEvent.click(trigger);
    const action = screen.getByTestId('x-science-task-open-plan');
    action.focus();
    fireEvent.click(action);
    expect(onOpenPlan).toHaveBeenCalledTimes(1);
    await waitFor(expectDetailsSurfaceClosed);
    expect(handedFocus).toBe(trigger);
  });

  it.each(['en-US', 'zh-CN'] as const)(
    'does not call a configured subscription refusal a missing-model wait in %s',
    async (language) => {
      const onResume = vi.fn();
      const paused = {
        ...snapshot('paused', {
          failureReason: 'model_provider_unavailable',
          statusDescription: 'PRIVATE_PROVIDER_BODY_SENTINEL',
          modelId: 'saved-model',
        }),
        providerFailureKind: 'subscription' as const,
      };
      await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={paused} onResume={onResume} />, language);
      const indicator = screen.getByTestId('x-science-task-status-indicator');
      expect(indicator).toHaveAttribute('data-state', 'paused');
      expect(indicator).toHaveTextContent(language === 'en-US' ? 'Model subscription unavailable' : '模型订阅不可用');
      expect(indicator).not.toHaveTextContent(/waiting for model selection|等待选择模型/i);
      expect(screen.queryByTestId('x-science-task-status-spinner')).not.toBeInTheDocument();
      expect(onResume).not.toHaveBeenCalled();
      fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
      expect(screen.getByTestId('x-science-task-center-failure-detail')).toHaveTextContent(
        language === 'en-US' ? 'subscription or billing' : '订阅或计费'
      );
      expect(document.body).not.toHaveTextContent('PRIVATE_PROVIDER_BODY_SENTINEL');
      fireEvent.click(screen.getByTestId('x-science-task-status-action'));
      expect(onResume).toHaveBeenCalledOnce();
    }
  );

  it.each([
    ['paused', 'en-US'],
    ['completed', 'en-US'],
    ['processing', 'en-US'],
    ['paused', 'zh-CN'],
    ['completed', 'zh-CN'],
    ['processing', 'zh-CN'],
  ] as const)(
    'does not invent a clock from timestamps or cached numeric data when %s timing is unavailable in %s',
    async (phase, language) => {
      await renderWithI18n(
        <XScienceTaskStatus
          {...defaultProps}
          snapshot={snapshot(phase, {
            runtimeElapsedMs: 30_000,
            runtimeElapsedAvailable: true,
            runtimeTaskElapsedMs: 0,
            runtimeTaskElapsedAvailable: false,
            runtimeTaskStartedAt: '2026-08-01T00:00:00Z',
            runtimeTaskObservedAt: '2026-08-10T08:00:00Z',
            runtimeTaskActive: true,
          })}
        />,
        language
      );
      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveTextContent(
        language === 'en-US' ? 'Elapsed time unavailable' : '累计耗时不可用'
      );
      expect(screen.queryByText(/Total 00:00|Total 00:30/)).not.toBeInTheDocument();
    }
  );
  it.each(['en-US', 'zh-CN'] as const)(
    'labels the no-progress pause in %s even without a resume action',
    async (language) => {
      await renderWithI18n(
        <XScienceTaskStatus
          {...defaultProps}
          snapshot={snapshot('paused', { failureReason: 'provider_stream_no_progress' })}
        />,
        language
      );
      expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'paused');
      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveTextContent(
        language === 'en-US' ? 'Model response paused' : '模型响应已暂停'
      );
      expect(screen.queryByTestId('x-science-task-status-spinner')).not.toBeInTheDocument();
    }
  );
  it.each(['en-US', 'zh-CN'] as const)(
    'shows the retry cause in %s and clears it on recovered progress',
    async (language) => {
      const { rerender } = await renderWithI18n(
        <XScienceTaskStatus
          {...defaultProps}
          snapshot={snapshot('processing', { retryReason: 'model_provider_temporarily_unavailable' })}
          onStop={vi.fn()}
        />,
        language
      );
      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveTextContent(
        language === 'en-US' ? 'Model service unavailable · retrying' : '模型服务暂时不可用，正在重试'
      );
      expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'provider-retrying');
      expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
      rerender(<XScienceTaskStatus {...defaultProps} snapshot={snapshot('processing')} />);
      expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'running');
      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveTextContent(
        language === 'en-US' ? 'Task running' : '任务正在运行'
      );
      expect(screen.queryByText(/service unavailable|模型服务暂时不可用/)).not.toBeInTheDocument();
    }
  );
  it('announces the phase independently of clocks and focuses/returns from the nonmodal details', async () => {
    await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} />);
    const indicator = screen.getByTestId('x-science-task-status-indicator');
    expect(indicator).toHaveAttribute('aria-live', 'off');
    expect(indicator.querySelector('.x-science-task-center__title')).toHaveAttribute('aria-live', 'polite');
    const trigger = screen.getByTestId('x-science-task-details-trigger');
    trigger.focus();
    fireEvent.click(trigger);
    const panel = await screen.findByRole('dialog', { name: '任务状态详情' });
    expect(panel).toHaveAttribute('aria-modal', 'false');
    expect(panel).toContainElement(document.activeElement as HTMLElement);
    fireEvent.keyDown(panel, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
  });
  it.each(['response_language_mismatch'])('shows a precise safe presentation failure for %s', async (failureReason) => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('failed', {
          failureKind: 'result_rejected',
          failureReason,
          error: 'private provider payload sk-secretValue123456',
        })}
      />
    );
    expect(screen.getByTestId('x-science-task-failure-reason')).toHaveTextContent(
      /回复呈现需要修正|Response presentation needs correction/i
    );
    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.getByTestId('x-science-task-center-failure-detail')).toHaveTextContent(
      /已有文件和执行结果已保留|Existing files and execution results are preserved/i
    );
    expect(screen.queryByText(/sk-secretValue123456/)).not.toBeInTheDocument();
  });
  afterEach(() => {
    cleanup();
    vi.clearAllMocks();
    vi.useRealTimers();
  });

  it('does not render a false task state while authority is loading', async () => {
    await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={null} loading />);

    expect(screen.queryByTestId('x-science-runtime-status')).not.toBeInTheDocument();
    expect(screen.queryByText(/confirming task status|正在确认任务状态/i)).not.toBeInTheDocument();
    expectDetailsSurfaceClosed();
  });

  it('keeps a terminal capsule in finalizing state until the final answer is rendered', async () => {
    await renderWithI18n(
      <XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} terminalProjectionPending />
    );

    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-state', 'finalizing');
    expect(screen.getByText(/preparing final result|正在整理最终结果/i)).toBeInTheDocument();
    expect(screen.queryByText(/^task completed$|^任务已完成$/i)).not.toBeInTheDocument();
  });

  it.each([
    { status: 'failed', phase: 'failed', active: false },
    { status: 'cancelled', phase: 'cancelled', active: false },
    { status: 'paused', phase: 'paused', active: false },
    { status: 'awaiting_user_response', phase: 'waiting_input', active: false },
    { status: 'processing', phase: 'running', active: true },
  ])(
    'does not replace $status with result preparation while messages synchronize',
    async ({ status, phase, active }) => {
      const onResume = vi.fn();
      await renderWithI18n(
        <XScienceTaskStatus
          {...defaultProps}
          snapshot={snapshot(
            status,
            status === 'awaiting_user_response'
              ? {
                  pendingInputRequests: [
                    {
                      requestId: 'pending-input',
                      toolId: 'ask-input',
                      kind: 'ask_user',
                      tool: 'ask_user',
                      code: null,
                      description: null,
                      environment: null,
                      mode: null,
                      questions: [],
                    },
                  ],
                }
              : {}
          )}
          terminalProjectionPending
          onResume={status === 'failed' ? onResume : undefined}
        />
      );

      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-state', phase);
      expect(screen.queryByText(/preparing final result|正在整理最终结果/i)).not.toBeInTheDocument();
      expect(Boolean(screen.queryByTestId('x-science-task-status-spinner'))).toBe(active);
      if (status === 'failed') {
        fireEvent.click(screen.getByTestId('x-science-task-status-action'));
        expect(onResume).toHaveBeenCalledOnce();
      }
    }
  );

  it('keeps the running capsule and its pause control as the only active controls', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-10T08:00:05Z'));
    const onStop = vi.fn();
    const onOpenPlan = vi.fn();
    const onRefresh = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('processing', {
          createdAt: '2026-08-10T04:00:00Z',
          runtimeElapsedMs: 120_000,
          runtimeObservedAt: '2026-08-10T08:00:05Z',
          runtimeStartedAt: '2026-08-10T07:56:00Z',
          runtimeActive: true,
          runtimeTaskElapsedMs: 600_000,
          runtimeTaskObservedAt: '2026-08-10T08:00:05Z',
          runtimeTaskStartedAt: '2026-08-10T06:00:00Z',
          runtimeTaskActive: true,
          runtimeAttempt: 7,
          runtimeInputRevision: 3,
        })}
        onStop={onStop}
        onOpenPlan={onOpenPlan}
        onRefresh={onRefresh}
        taskCenterMetrics={{
          total: { tokenUsage: 1_234_567, modelCallCount: 8, toolCallCount: 42 },
          latest: { tokenUsage: 123_456, modelCallCount: 3, toolCallCount: 19 },
        }}
      />
    );

    expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'running');
    expect(screen.getByTestId('x-science-task-status-indicator').tagName).toBe('DIV');
    expect(screen.getByTestId('x-science-task-status-icon')).toHaveClass('x-science-task-center__icon');
    expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('10:00');
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveAttribute('title', expect.stringContaining('10:00'));
    expect(screen.getByText(/任务正在运行|Task running/i)).toHaveAttribute('title');
    await act(() => vi.advanceTimersByTimeAsync(1_000));
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('10:01');

    const detailsTrigger = screen.getByTestId('x-science-task-details-trigger');
    expect(screen.getByTestId('x-science-task-pause')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-pause').querySelector('svg')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-pause')).toHaveClass('x-science-task-center__control');
    expectDetailsSurfaceClosed();
    fireEvent.click(screen.getByTestId('x-science-task-pause'));
    expect(onStop).toHaveBeenCalledOnce();
    fireEvent.click(detailsTrigger);
    expect(screen.getByTestId('x-science-task-status-panel')).toBeInTheDocument();
    const summaryIcon = document.querySelector('.x-science-task-center-panel__summary-icon');
    expect(summaryIcon?.querySelector('svg')).toBeInTheDocument();
    expect(summaryIcon?.querySelector('img')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-metric-elapsed')).toHaveTextContent('10:01');
    expect(screen.getByTestId('x-science-task-metric-elapsed')).toHaveTextContent(/最新一轮 02:01|Latest 02:01/i);
    expect(screen.getByTestId('x-science-task-metric-tokens')).toHaveTextContent('1.2M');
    expect(screen.getByTestId('x-science-task-metric-tokens')).toHaveTextContent(/最新一轮 123.5K|Latest 123.5K/i);
    expect(screen.getByTestId('x-science-task-metric-model-calls')).toHaveTextContent('8');
    expect(screen.getByTestId('x-science-task-metric-model-calls')).toHaveTextContent(/最新一轮 3|Latest 3/i);
    expect(screen.getByTestId('x-science-task-metric-tools')).toHaveTextContent('42');
    expect(screen.getByTestId('x-science-task-metric-tools')).toHaveTextContent(/最新一轮 19|Latest 19/i);
    expect(screen.queryByText('Querying public biomedical databases')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /查看执行记录|View execution log/i })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /重新加载运行状态|Reload runtime/i })).not.toBeInTheDocument();
    expect(onOpenPlan).not.toHaveBeenCalled();
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it('integrates a failed resumable run into the same continue-running capsule', async () => {
    const onResume = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('failed', {
          error: 'provider rejected sk-secretValue123456 for alice@example.com',
          failureReason: 'model_provider_unavailable',
        })}
        onResume={onResume}
      />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    const indicator = screen.getByTestId('x-science-task-status-indicator');
    const resumeAction = screen.getByTestId('x-science-task-status-action');
    expect(center).toHaveAttribute('data-state', 'failed');
    expect(indicator.tagName).toBe('DIV');
    expect(indicator).toHaveAttribute('data-state', 'failed');
    expect(resumeAction).toHaveAccessibleName(/继续运行|Continue running/i);
    expect(screen.getByTestId('x-science-task-status-icon')).toHaveClass('x-science-task-center__icon');
    expect(screen.getByTestId('x-science-task-status-action')).toHaveClass('x-science-task-center__control');
    expect(screen.queryByTestId('x-science-task-pause')).not.toBeInTheDocument();
    expectDetailsSurfaceClosed();
    expect(screen.queryByText('secretValue123456')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-details-trigger')).toHaveTextContent(/任务运行失败|Task failed/i);
    expect(screen.getByTestId('x-science-task-details-trigger')).not.toHaveTextContent('model_provider_unavailable');
    expect(screen.queryByTestId('x-science-task-failure-reason')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.getByRole('heading', { name: /失败原因|Failure reason/i })).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-center-failure-detail')).toHaveTextContent(/任务运行失败|Task failed/i);
    expect(screen.getByTestId('x-science-task-center-failure-detail')).not.toHaveTextContent('provider rejected');
    expect(screen.queryByText('secretValue123456')).not.toBeInTheDocument();

    fireEvent.click(resumeAction);
    expect(onResume).toHaveBeenCalledOnce();
  });

  it('renders a closed failure kind as a localized actionable capsule label', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('failed', {
          failureKind: 'network_bridge_down',
          failureReason: 'tool_failed',
          error: 'The governed network bridge is unavailable.',
        })}
      />
    );

    expect(screen.getByTestId('x-science-task-failure-reason')).toHaveTextContent(
      /网络桥接服务不可用|Network bridge unavailable/i
    );
    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.getByTestId('x-science-task-center-failure-detail')).toHaveTextContent(
      /网络桥接服务不可用|Network bridge unavailable/i
    );
    expect(screen.getByTestId('x-science-task-center-failure-detail')).not.toHaveTextContent(
      'The governed network bridge is unavailable.'
    );
  });

  it('keeps model recovery in the unified task center with a concrete next action', async () => {
    const onResume = vi.fn();
    const onChooseModel = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('failed', {
          failureKind: 'model_not_found',
          error: 'provider returned model_not_found',
          modelId: 'model-a',
        })}
        recoveryModelLabel='Model B'
        onResume={onResume}
        onChooseModel={onChooseModel}
      />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    expect(center).toHaveAttribute('data-failure-kind', 'model_not_found');
    expect(center).toHaveTextContent(/模型不可用|Model unavailable/i);
    const resume = screen.getByTestId('x-science-task-status-action');
    expect(resume).toHaveAccessibleName(/改用 Model B 继续|Continue with Model B/i);

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.getByTestId('x-science-task-center-failure-detail')).toHaveTextContent(/Model B/);
    fireEvent.click(screen.getByTestId('x-science-task-choose-model'));
    expect(onChooseModel).toHaveBeenCalledOnce();
    fireEvent.click(resume);
    expect(onResume).toHaveBeenCalledOnce();
  });

  it('exposes an available historical plan from the compact task center', async () => {
    const onOpenPlan = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} planAvailable onOpenPlan={onOpenPlan} />
    );

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));

    const planAction = screen.getByTestId('x-science-task-open-plan');
    expect(planAction).toHaveAccessibleName(/任务计划.*查看计划|Task plan.*View plan/i);
    fireEvent.click(planAction);
    expect(onOpenPlan).toHaveBeenCalledOnce();
    await waitFor(() => expect(screen.queryByTestId('x-science-task-status-panel')).not.toBeInTheDocument());
  });

  it('keeps a user-paused run as one cohesive continue-running capsule', async () => {
    const onResume = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('cancelled', {
          statusDescription: 'user_cancelled',
          failureReason: 'user_cancelled',
        })}
        onResume={onResume}
      />
    );

    const indicator = screen.getByTestId('x-science-task-status-indicator');
    const resumeAction = screen.getByTestId('x-science-task-status-action');
    expect(indicator.tagName).toBe('DIV');
    expect(indicator).toHaveAttribute('data-state', 'paused');
    expect(resumeAction).toHaveAccessibleName(/继续运行|Continue running/i);
    expect(screen.queryByTestId('x-science-task-pause')).not.toBeInTheDocument();
    expect(screen.queryByTestId('x-science-task-failure-reason')).not.toBeInTheDocument();
    expect(indicator).not.toHaveTextContent('user_cancelled');
    expectDetailsSurfaceClosed();

    fireEvent.click(resumeAction);
    expect(onResume).toHaveBeenCalledOnce();
  });

  it('renders a provider-paused run as a frozen non-error resume capsule', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-10T08:02:05Z'));
    const onResume = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('paused', {
          statusDescription: 'provider quota is exhausted; choose another model and continue',
          runtimeElapsedMs: 120_000,
          runtimeObservedAt: '2026-08-10T08:02:00Z',
          runtimeStartedAt: '2026-08-10T08:00:00Z',
          runtimeActive: false,
        })}
        onResume={onResume}
      />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    const indicator = screen.getByTestId('x-science-task-status-indicator');
    expect(center).toHaveAttribute('data-state', 'paused');
    expect(indicator).toHaveAttribute('data-state', 'paused');
    expect(indicator.tagName).toBe('DIV');
    expect(screen.getByTestId('x-science-task-status-icon').querySelector('svg')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-status-icon').querySelector('img')).not.toBeInTheDocument();
    expect(screen.queryByTestId('x-science-task-status-spinner')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('02:00');
    await act(() => vi.advanceTimersByTimeAsync(5_000));
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('02:00');
    fireEvent.click(screen.getByTestId('x-science-task-status-action'));
    expect(onResume).toHaveBeenCalledOnce();
    expectDetailsSurfaceClosed();
  });

  it('labels a missing model as waiting for model selection instead of a generic pause', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('paused', {
          statusDescription: 'no active saved model provider is configured',
          failureReason: 'model_provider_unavailable',
          providerFailureKind: 'configuration_missing',
        })}
        onResume={vi.fn()}
      />
    );

    expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'waiting_input');
    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-state', 'waiting_input');
    expect(screen.getByText(/任务等待选择模型|Task waiting for model selection/i)).toBeInTheDocument();
    expect(screen.queryByText(/任务已暂停|Task paused/i)).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-status-action')).toHaveAccessibleName(/继续运行|Continue running/i);
  });

  it('turns the inline pause control into a busy state while cancellation is in flight', async () => {
    await renderWithI18n(
      <XScienceTaskStatus {...defaultProps} snapshot={snapshot('processing')} pausing onStop={vi.fn()} />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    expect(center).toHaveAttribute('data-state', 'cancelling');
    expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
    expect(screen.queryByTestId('x-science-task-pause')).not.toBeInTheDocument();
    expectDetailsSurfaceClosed();
  });

  it('keeps the legacy frame timestamp fallback and freezes completed duration', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-08-10T08:00:05Z'));
    await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={snapshot('processing')} />);
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('01:05');

    cleanup();
    vi.setSystemTime(new Date('2026-08-10T12:00:00Z'));
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('completed', {
          completedAt: null,
          updatedAt: '2026-08-10T08:02:03Z',
        })}
      />
    );
    expect(screen.getByTestId('x-science-runtime-status')).toHaveAttribute('data-state', 'completed');
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('03:03');
    expect(vi.getTimerCount()).toBe(0);
    expectDetailsSurfaceClosed();
  });

  it('shows manual review and review interruption inside the same neutral capsule', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeElapsedMs: 123_000,
          runtimeActive: false,
          runtimeStage: 'scientific_reviewing',
          reviewStatus: 'processing',
          reviewTrigger: 'manual',
        })}
      />
    );
    const reviewing = screen.getByTestId('x-science-task-status-indicator');
    expect(reviewing.tagName).toBe('DIV');
    expect(reviewing).toHaveTextContent(/手动审阅中|Manual review in progress/i);
    expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('02:03');
    expect(screen.getByTestId('x-science-runtime-status')).not.toHaveAttribute('role', 'alert');

    cleanup();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeElapsedMs: 123_000,
          runtimeActive: false,
          runtimeStage: 'review_failed',
          reviewStatus: 'failed',
          reviewDescription: 'Reviewer model returned invalid structured tool data',
        })}
      />
    );
    const interrupted = screen.getByTestId('x-science-task-status-indicator');
    expect(interrupted.tagName).toBe('DIV');
    expect(interrupted).toHaveTextContent(/任务已完成 · 审阅中断|Task completed · Review interrupted/i);
    expect(screen.queryByTestId('x-science-task-status-spinner')).not.toBeInTheDocument();
    expect(interrupted).toHaveAttribute('role', 'status');
  });

  it('labels an explicitly enabled auto review inside the same capsule', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeElapsedMs: 123_000,
          runtimeActive: false,
          runtimeStage: 'scientific_reviewing',
          reviewStatus: 'processing',
          reviewTrigger: 'auto',
        })}
      />
    );
    const reviewing = screen.getByTestId('x-science-task-status-indicator');
    expect(reviewing.tagName).toBe('DIV');
    expect(reviewing).toHaveTextContent(/自动审阅中|Auto review in progress/i);
    expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-runtime-status')).not.toHaveAttribute('role', 'alert');
  });

  it('shows review findings on the completed capsule without reviving the spinner or failing the task', async () => {
    const onOpenReviewFindings = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        onOpenReviewFindings={onOpenReviewFindings}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeElapsedMs: 123_000,
          runtimeActive: false,
          runtimeStage: 'review_completed',
          reviewStatus: 'completed',
          reviewTrigger: 'manual',
          reviewVerdict: 'revise',
          reviewIssueCount: 3,
          reviewBlockingIssueCount: 2,
        })}
      />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    const indicator = screen.getByTestId('x-science-task-status-indicator');
    expect(center).toHaveAttribute('data-state', 'completed-review-findings');
    expect(indicator).toHaveTextContent(/3/);
    expect(indicator).toHaveAttribute('role', 'status');
    expect(screen.queryByTestId('x-science-task-status-spinner')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('02:03');
    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    const reviewButton = screen.getByTestId('x-science-task-open-review');
    expect(reviewButton).toHaveTextContent(/查看问题|View issues/);
    fireEvent.click(reviewButton);
    expect(onOpenReviewFindings).toHaveBeenCalledOnce();
  });

  it('keeps a passing review available from the completed task overview', async () => {
    const onOpenReviewFindings = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        onOpenReviewFindings={onOpenReviewFindings}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeActive: false,
          runtimeStage: 'review_completed',
          reviewStatus: 'completed',
          reviewTrigger: 'manual',
          reviewVerdict: 'pass',
          reviewFrameId: 'completion-review-1',
        })}
      />
    );

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    const reviewButton = screen.getByTestId('x-science-task-open-review');
    expect(reviewButton).toHaveAccessibleName(/查看审阅结果.*审阅通过|View review result.*Review passed/i);
    fireEvent.click(reviewButton);
    expect(onOpenReviewFindings).toHaveBeenCalledOnce();
  });

  it('does not mislabel a completed review with no aggregate verdict as requiring changes', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        onOpenReviewFindings={vi.fn()}
        snapshot={snapshot('completed', {
          runtimeStage: 'review_completed',
          reviewStatus: 'completed',
          reviewFrameId: 'completion-review-without-verdict',
          reviewVerdict: null,
        })}
      />
    );

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));

    expect(screen.getByTestId('x-science-task-open-review')).toHaveAccessibleName(
      /查看审阅结果.*审阅已完成|View review result.*Review completed/i
    );
    expect(screen.queryByText(/审阅要求修改|Review requires changes/i)).not.toBeInTheDocument();
  });

  it('states when a completed task has no durable plan instead of silently hiding plan history', async () => {
    await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} />);

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));

    expect(screen.getByTestId('x-science-task-plan-unavailable')).toHaveTextContent(
      /本次任务未记录计划|No plan was recorded for this task/i
    );
    expect(screen.queryByTestId('x-science-task-open-plan')).not.toBeInTheDocument();
  });

  it('does not render an empty attention group when no review destination is available', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeActive: false,
          runtimeStage: 'review_completed',
          reviewStatus: 'completed',
          reviewVerdict: 'revise',
          reviewIssueCount: 2,
        })}
      />
    );

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.queryByTestId('x-science-task-open-review')).not.toBeInTheDocument();
    expect(screen.queryByText(/待处理|Needs attention/i)).not.toBeInTheDocument();
  });

  it('keeps the capsule compact while limiting the hub to metrics and user-required actions', async () => {
    const onOpenPendingInput = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('awaiting_input', {
          pendingInputRequests: [
            {
              requestId: 'request-1',
              toolId: 'tool-1',
              kind: 'ask_user',
              tool: 'ask_user',
              code: null,
              description: null,
              environment: null,
              mode: null,
              questions: [],
            },
            {
              requestId: 'request-2',
              toolId: 'tool-2',
              kind: 'approval',
              tool: 'shell',
              code: null,
              description: null,
              environment: null,
              mode: null,
              questions: [],
            },
          ],
        })}
        pendingInputCount={2}
        taskCenterMetrics={{
          total: { tokenUsage: 16_384, modelCallCount: 6, toolCallCount: 11 },
          latest: { tokenUsage: 8_192, modelCallCount: 2, toolCallCount: 7 },
        }}
        onOpenPendingInput={onOpenPendingInput}
      />
    );

    const indicator = screen.getByTestId('x-science-task-status-indicator');
    expect(indicator).toHaveClass('x-science-task-center__pill');
    expect(indicator).toHaveTextContent(/2 项待处理|2 pending/i);
    expectDetailsSurfaceClosed();

    fireEvent.click(screen.getByTestId('x-science-task-details-trigger'));
    expect(screen.getByTestId('x-science-task-open-pending-input')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-metric-tokens')).toHaveTextContent('16.4K');
    expect(screen.getByTestId('x-science-task-metric-tokens')).toHaveTextContent(/最新一轮 8.2K|Latest 8.2K/i);
    expect(screen.getByTestId('x-science-task-metric-tools')).toHaveTextContent('11');
    expect(screen.getByTestId('x-science-task-metric-tools')).toHaveTextContent(/最新一轮 7|Latest 7/i);
    expect(screen.queryByText(/会话选项|Conversation options/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/编排与队列|Orchestration and queue/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/任务上下文|Task context/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/打开工作区|Open workspace/i)).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('x-science-task-open-pending-input'));
    expect(onOpenPendingInput).toHaveBeenCalledOnce();
    await waitFor(expectDetailsSurfaceClosed);
  });

  it('opens fixed-size task details above the movable capsule instead of a draggable panel or page drawer', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('completed', {
          completedAt: '2026-08-10T08:02:03Z',
          runtimeElapsedMs: 183_456,
          runtimeStartedAt: '2026-08-10T07:59:00.123Z',
          runtimeFinishedAt: '2026-08-10T08:02:03.579Z',
          runtimeActive: false,
          runtimeTaskElapsedMs: 14_347_507,
          runtimeTaskObservedAt: '2026-08-10T08:02:03.579Z',
          runtimeTaskStartedAt: '2026-08-09T10:45:14.577Z',
          runtimeTaskFinishedAt: '2026-08-10T08:02:03.579Z',
          runtimeTaskActive: false,
        })}
        taskCenterMetrics={{
          total: { tokenUsage: 27_231_839, modelCallCount: 514, toolCallCount: 254 },
          latest: { tokenUsage: 4_096, modelCallCount: 1, toolCallCount: 3 },
        }}
      />
    );

    const trigger = screen.getByTestId('x-science-task-details-trigger');
    const indicator = screen.getByTestId('x-science-task-status-indicator');
    expect(trigger).toHaveAttribute('aria-expanded', 'false');
    expect(indicator).toHaveAttribute('data-open', 'false');
    fireEvent.pointerDown(trigger, {
      button: 0,
      isPrimary: true,
      pointerId: 6,
      clientX: 240,
      clientY: 214,
    });
    fireEvent.pointerUp(trigger, { isPrimary: true, pointerId: 6, clientX: 240, clientY: 214 });
    fireEvent.click(trigger);

    const taskDetails = await screen.findByRole('dialog', { name: /task details|任务状态详情/i });
    expect(taskDetails).toHaveAttribute('data-phase', 'completed');
    expect(screen.getByTestId('x-science-task-status-snapshot')).toBeInTheDocument();
    expect(trigger).toHaveAttribute('aria-expanded', 'true');
    expect(indicator).toHaveAttribute('data-open', 'true');
    expect(document.querySelector('.x-science-task-center-popover')).toBeInTheDocument();
    expect(document.querySelector('.arco-drawer')).not.toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-elapsed')).toHaveTextContent('03:59:07');
    expect(screen.getByTestId('x-science-task-metric-elapsed')).toHaveTextContent('03:59:07');
    expect(screen.getByTestId('x-science-task-metric-elapsed')).toHaveTextContent(/最新一轮 03:03|Latest 03:03/i);
    expect(screen.getByTestId('x-science-task-metric-tokens')).toHaveTextContent('27.2M');
    expect(screen.getByTestId('x-science-task-metric-tokens')).toHaveTextContent(/最新一轮 4.1K|Latest 4.1K/i);
    expect(screen.getByTestId('x-science-task-metric-tools')).toHaveTextContent('254');
    expect(screen.getByTestId('x-science-task-metric-tools')).toHaveTextContent(/最新一轮 3|Latest 3/i);
    expect(screen.getByTestId('x-science-task-metric-started')).not.toHaveTextContent('—');
    expect(screen.getByTestId('x-science-task-metric-finished')).not.toHaveTextContent('—');
    expect(screen.getByTestId('x-science-task-metric-started')).toHaveTextContent(/^\d{2}\/\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(screen.getByTestId('x-science-task-metric-finished')).toHaveTextContent(/^\d{2}\/\d{2} \d{2}:\d{2}:\d{2}$/);
    expect(screen.queryByTestId('x-science-task-panel-drag-handle')).not.toBeInTheDocument();
    expect(screen.queryByTestId('x-science-task-panel-resize-handle')).not.toBeInTheDocument();

    fireEvent.click(trigger);
    await waitFor(expectDetailsSurfaceClosed);

    Object.defineProperty(indicator, 'getBoundingClientRect', {
      configurable: true,
      value: () => ({
        left: 200,
        top: 200,
        right: 491,
        bottom: 232,
        width: 291,
        height: 32,
        x: 200,
        y: 200,
        toJSON: () => ({}),
      }),
    });
    fireEvent.pointerDown(indicator, {
      button: 0,
      isPrimary: true,
      pointerId: 7,
      clientX: 240,
      clientY: 214,
    });
    expect(indicator).toHaveAttribute('data-dragging', 'false');
    fireEvent.pointerMove(indicator, { isPrimary: true, pointerId: 7, clientX: 300, clientY: 254 });
    expect(indicator).toHaveAttribute('data-dragging', 'true');
    expect(indicator).toHaveAttribute('data-detached', 'true');
    expect(indicator).toHaveAttribute('data-minimized', 'true');
    expect(indicator).toHaveStyle({ position: 'fixed', left: '285px', top: '239px' });
    fireEvent.pointerMove(document.body, {
      isPrimary: true,
      pointerId: 7,
      clientX: 440,
      clientY: 354,
    });
    expect(indicator).toHaveStyle({ position: 'fixed', left: '425px', top: '339px' });
    fireEvent.pointerUp(document.body, {
      isPrimary: true,
      pointerId: 7,
      clientX: 440,
      clientY: 354,
    });
    expect(indicator).toHaveAttribute('data-dragging', 'false');
    expect(window.localStorage.getItem('x-science.task-status.presentation.v5.frame-status-center')).toContain(
      '"x":425'
    );
    expect(screen.queryByTestId('x-science-task-details-trigger')).not.toBeInTheDocument();
  });

  it('automatically becomes a breathing status orb when dragged, restores on click, and preserves its presentation', async () => {
    await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} />);

    const indicator = screen.getByTestId('x-science-task-status-indicator');
    expect(screen.queryByTestId('x-science-task-status-minimize')).not.toBeInTheDocument();
    Object.defineProperty(indicator, 'getBoundingClientRect', {
      configurable: true,
      value: () => ({
        left: 200,
        top: 200,
        right: 491,
        bottom: 232,
        width: 291,
        height: 32,
        x: 200,
        y: 200,
        toJSON: () => ({}),
      }),
    });
    fireEvent.pointerDown(indicator, {
      button: 0,
      isPrimary: true,
      pointerId: 9,
      clientX: 240,
      clientY: 214,
    });
    fireEvent.pointerMove(indicator, { isPrimary: true, pointerId: 9, clientX: 280, clientY: 234 });
    fireEvent.pointerUp(document.body, {
      isPrimary: true,
      pointerId: 9,
      clientX: 280,
      clientY: 234,
    });
    expect(indicator).toHaveAttribute('data-minimized', 'true');
    expect(indicator).toHaveClass('x-science-task-center__pill--minimized');
    expect(screen.queryByTestId('x-science-task-details-trigger')).not.toBeInTheDocument();
    const restoreButton = screen.getByTestId('x-science-task-status-restore');
    expect(restoreButton).toHaveAccessibleName(/恢复任务状态.*任务已完成|Restore task status.*Task completed/i);
    fireEvent.mouseEnter(restoreButton);
    expect(await screen.findByRole('tooltip')).toHaveTextContent(/任务已完成|Task completed/i);
    expect(window.localStorage.getItem('x-science.task-status.presentation.v5.frame-status-center')).toContain(
      '"minimized":true'
    );

    cleanup();
    await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={snapshot('completed')} />);
    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-minimized', 'true');

    fireEvent.click(screen.getByTestId('x-science-task-status-restore'));
    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-minimized', 'false');
    expect(screen.getByTestId('x-science-task-details-trigger')).toBeInTheDocument();
  });

  it('keeps position and minimized state isolated per conversation across A to B to A navigation', async () => {
    const conversationA = snapshot('completed', {
      frameId: 'conversation-a',
      rootFrameId: 'conversation-a',
    });
    const conversationB = snapshot('completed', {
      frameId: 'conversation-b',
      rootFrameId: 'conversation-b',
    });
    const view = await renderWithI18n(<XScienceTaskStatus {...defaultProps} snapshot={conversationA} />);

    const indicatorA = screen.getByTestId('x-science-task-status-indicator');
    Object.defineProperty(indicatorA, 'getBoundingClientRect', {
      configurable: true,
      value: () => ({
        left: 200,
        top: 200,
        right: 491,
        bottom: 232,
        width: 291,
        height: 32,
        x: 200,
        y: 200,
        toJSON: () => ({}),
      }),
    });
    fireEvent.pointerDown(indicatorA, {
      button: 0,
      isPrimary: true,
      pointerId: 11,
      clientX: 240,
      clientY: 214,
    });
    fireEvent.pointerMove(document.body, {
      isPrimary: true,
      pointerId: 11,
      clientX: 520,
      clientY: 374,
    });
    fireEvent.pointerUp(document.body, {
      isPrimary: true,
      pointerId: 11,
      clientX: 520,
      clientY: 374,
    });

    expect(indicatorA).toHaveAttribute('data-minimized', 'true');
    expect(indicatorA).toHaveStyle({ position: 'fixed', left: '505px', top: '359px' });
    expect(window.localStorage.getItem('x-science.task-status.presentation.v5.conversation-a')).toContain(
      '"minimized":true'
    );
    expect(window.localStorage.getItem('x-science.task-status.presentation.v5.conversation-b')).toBeNull();

    view.rerender(<XScienceTaskStatus {...defaultProps} snapshot={conversationB} />);
    await waitFor(() =>
      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-minimized', 'false')
    );
    const indicatorB = screen.getByTestId('x-science-task-status-indicator');
    expect(indicatorB).toHaveAttribute('data-detached', 'false');
    expect(indicatorB.style.position).toBe('');
    expect(indicatorB.style.left).toBe('');
    expect(indicatorB.style.top).toBe('');
    expect(screen.getByTestId('x-science-task-details-trigger')).toBeInTheDocument();

    view.rerender(<XScienceTaskStatus {...defaultProps} snapshot={conversationA} />);
    await waitFor(() =>
      expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('data-minimized', 'true')
    );
    const restoredA = screen.getByTestId('x-science-task-status-indicator');
    expect(restoredA).toHaveAttribute('data-detached', 'true');
    expect(restoredA).toHaveStyle({ position: 'fixed', left: '505px', top: '359px' });
    expect(screen.queryByTestId('x-science-task-details-trigger')).not.toBeInTheDocument();
  });

  it('fails closed for an unknown backend state without adding a recovery panel', async () => {
    const onRefresh = vi.fn();
    await renderWithI18n(
      <XScienceTaskStatus {...defaultProps} snapshot={snapshot('future_runtime_state')} onRefresh={onRefresh} />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    expect(center).toHaveAttribute('data-state', 'unavailable');
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expectDetailsSurfaceClosed();
    fireEvent.click(screen.getByTestId('x-science-task-status-indicator'));
    expect(onRefresh).not.toHaveBeenCalled();
  });

  it('keeps an active task in neutral confirmation when its first runtime snapshot is transiently unavailable', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={null}
        loading={false}
        snapshotError='load'
        runtimeState='running'
      />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    expect(center).toHaveAttribute('data-state', 'loading');
    expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
    expect(screen.getByTestId('x-science-task-status-indicator')).toHaveAttribute('role', 'status');
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not present a cached active snapshot as live while runtime authority is unavailable', async () => {
    await renderWithI18n(
      <XScienceTaskStatus
        {...defaultProps}
        snapshot={snapshot('running')}
        snapshotError='refresh'
        runtimeState='running'
        runtimeAuthorityUnavailable
      />
    );

    const center = screen.getByTestId('x-science-runtime-status');
    expect(center).toHaveAttribute('data-state', 'loading');
    expect(screen.getByTestId('x-science-task-status-spinner')).toBeInTheDocument();
    expect(center).not.toHaveTextContent(/任务正在运行|Task running/i);
  });
});

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Attention, CheckOne, Close, CloseOne, PauseOne, Right } from '@icon-park/react';
import React, { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceLongTaskPhase } from './longTaskStatusModel';
import type { XScienceReviewVerdict } from './runtimeOperationsModel';
import { restoreScopedFocus } from '@/renderer/utils/focusScope';

export type XScienceTaskCenterMetricScope = {
  tokenUsage: number | null;
  modelCallCount: number | null;
  toolCallCount: number | null;
};

export type XScienceTaskCenterMetrics = {
  total: XScienceTaskCenterMetricScope;
  latest: XScienceTaskCenterMetricScope;
};

type XScienceTaskCenterPanelProps = {
  phase: XScienceLongTaskPhase | null;
  statusTitle: string;
  startedAtValue: string | null;
  finishedAtValue: string | null;
  taskElapsedValue: string | null;
  latestElapsedValue: string | null;
  metrics?: XScienceTaskCenterMetrics;
  planAvailable: boolean;
  planNeedsApproval: boolean;
  reviewAvailable: boolean;
  reviewFindingsAvailable: boolean;
  reviewVerdict?: XScienceReviewVerdict;
  reviewIssueCount?: number;
  pendingInputCount?: number;
  recoveryAvailable?: boolean;
  recoveryResumeLabel?: string | null;
  failureDetail?: string | null;
  reviewStopDetail?: string | null;
  onClose: () => void;
  onRefresh?: () => void;
  onResume?: () => void;
  onChooseModel?: () => void;
  onOpenReviewFindings?: () => void;
  onOpenPlan?: () => void;
  onOpenPendingInput?: () => void;
};

type TaskCenterActionRowProps = {
  label: string;
  meta?: string | null;
  count?: number;
  attention?: boolean;
  testId?: string;
  onClick: () => void;
};

type TaskCenterMetricProps = {
  label: string;
  value: string;
  detail: string;
  title?: string;
  testId: string;
};

type TaskCenterRecordRowProps = {
  label: string;
  meta: string;
  testId: string;
};

const StatusGlyph: React.FC<{ phase: XScienceLongTaskPhase | null }> = ({ phase }) => {
  if (phase === 'completed') return <CheckOne theme='outline' size={16} aria-hidden='true' />;
  if (phase === 'failed') return <Attention theme='outline' size={16} aria-hidden='true' />;
  if (phase === 'cancelled') return <CloseOne theme='outline' size={16} aria-hidden='true' />;
  return <PauseOne theme='outline' size={16} aria-hidden='true' />;
};

const TaskCenterMetric: React.FC<TaskCenterMetricProps> = ({ label, value, detail, title, testId }) => (
  <div className='x-science-task-center-panel__metric-card' data-testid={testId} title={title}>
    <dt>{label}</dt>
    <dd>{value}</dd>
    <span>{detail}</span>
  </div>
);

const formatCompactTokenUsage = (value: number | null, locale: string): string | null => {
  if (value === null || !Number.isFinite(value) || value < 0) return null;
  const formatter = new Intl.NumberFormat(locale, { maximumFractionDigits: 1 });
  if (value >= 1_000_000) return `${formatter.format(value / 1_000_000)}M`;
  if (value >= 1_000) return `${formatter.format(value / 1_000)}K`;
  return formatter.format(value);
};

const TaskCenterActionRow: React.FC<TaskCenterActionRowProps> = ({
  label,
  meta,
  count,
  attention = false,
  testId,
  onClick,
}) => (
  <button
    type='button'
    className={`x-science-task-center-panel__action${attention ? ' x-science-task-center-panel__action--attention' : ''}`}
    data-testid={testId}
    onClick={onClick}
  >
    <span className='x-science-task-center-panel__action-copy'>
      <span className='x-science-task-center-panel__action-label'>{label}</span>
      {meta ? <span className='x-science-task-center-panel__action-meta'>{meta}</span> : null}
    </span>
    {typeof count === 'number' && count > 0 ? (
      <span className='x-science-task-center-panel__count' aria-label={String(count)}>
        {count}
      </span>
    ) : null}
    <Right theme='outline' size={14} aria-hidden='true' />
  </button>
);

const TaskCenterRecordRow: React.FC<TaskCenterRecordRowProps> = ({ label, meta, testId }) => (
  <div className='x-science-task-center-panel__action x-science-task-center-panel__action--static' data-testid={testId}>
    <span className='x-science-task-center-panel__action-copy'>
      <span className='x-science-task-center-panel__action-label'>{label}</span>
      <span className='x-science-task-center-panel__action-meta'>{meta}</span>
    </span>
  </div>
);

const XScienceTaskCenterPanel: React.FC<XScienceTaskCenterPanelProps> = ({
  phase,
  statusTitle,
  startedAtValue,
  finishedAtValue,
  taskElapsedValue,
  latestElapsedValue,
  metrics,
  planAvailable,
  planNeedsApproval,
  reviewAvailable,
  reviewFindingsAvailable,
  reviewVerdict = null,
  reviewIssueCount = 0,
  pendingInputCount = 0,
  recoveryAvailable = false,
  recoveryResumeLabel = null,
  failureDetail = null,
  reviewStopDetail = null,
  onClose,
  onRefresh,
  onResume,
  onChooseModel,
  onOpenReviewFindings,
  onOpenPlan,
  onOpenPendingInput,
}) => {
  const { t, i18n } = useTranslation();
  const panelRef = useRef<HTMLDivElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const openerRef = useRef<HTMLElement | null>(null);
  useEffect(() => {
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    openerRef.current = opener;
    const panel = panelRef.current;
    headingRef.current?.focus({ preventScroll: true });
    return () => {
      // An outside pointer action may already have focused another control.
      // Restore only when focus belonged to this popover or was removed with it.
      const focus = document.activeElement;
      if (opener?.isConnected && (focus === document.body || panel?.contains(focus)))
        opener.focus({ preventScroll: true });
    };
  }, []);
  const numberFormatter = new Intl.NumberFormat(i18n.language);
  const taskTokenUsage = metrics?.total.tokenUsage ?? null;
  const latestTokenUsage = metrics?.latest.tokenUsage ?? null;
  const taskModelCallCount = metrics?.total.modelCallCount ?? null;
  const latestModelCallCount = metrics?.latest.modelCallCount ?? null;
  const taskToolCallCount = metrics?.total.toolCallCount ?? null;
  const latestToolCallCount = metrics?.latest.toolCallCount ?? null;
  const compactTaskTokenUsage = formatCompactTokenUsage(taskTokenUsage, i18n.language);
  const compactLatestTokenUsage = formatCompactTokenUsage(latestTokenUsage, i18n.language) ?? '—';
  const exactTaskTokenUsage = taskTokenUsage === null ? null : numberFormatter.format(taskTokenUsage);
  const exactTaskModelCalls = taskModelCallCount === null ? null : numberFormatter.format(taskModelCallCount);
  const exactLatestModelCalls = latestModelCallCount === null ? '—' : numberFormatter.format(latestModelCallCount);
  const exactTaskToolCalls = taskToolCallCount === null ? null : numberFormatter.format(taskToolCallCount);
  const exactLatestToolCalls = latestToolCallCount === null ? '—' : numberFormatter.format(latestToolCallCount);
  const latestDetail = (value: string | null): string =>
    t('conversation.xscienceRuntime.runtimeOperations.taskMetricLatestRound', { value: value ?? '—' });
  const attentionActionCount =
    (pendingInputCount > 0 && onOpenPendingInput ? pendingInputCount : 0) +
    (planNeedsApproval && onOpenPlan ? 1 : 0) +
    (reviewFindingsAvailable && onOpenReviewFindings ? 1 : 0);
  const reviewResultMeta =
    reviewVerdict === 'pass'
      ? t('conversation.xscienceRuntime.runtimeOperations.reviewPassed')
      : reviewVerdict === 'pass_with_warnings'
        ? t('conversation.xscienceRuntime.runtimeOperations.reviewPassedWithWarnings')
        : reviewVerdict === 'revise'
          ? t('conversation.xscienceRuntime.runtimeOperations.reviewNeedsChanges')
          : t('conversation.xscienceRuntime.runtimeOperations.reviewCompleted');
  const showTaskOverview =
    (planAvailable && !planNeedsApproval && Boolean(onOpenPlan)) ||
    (reviewAvailable && !reviewFindingsAvailable && Boolean(onOpenReviewFindings)) ||
    (phase === 'completed' && !planAvailable);

  const runAction = (action?: () => void) => {
    onClose();
    action?.();
  };
  const openPlan = () =>
    runAction(() => {
      restoreScopedFocus(openerRef.current, panelRef.current);
      onOpenPlan?.();
    });

  return (
    <div
      ref={panelRef}
      className='x-science-task-center-panel'
      data-testid='x-science-task-status-panel'
      data-phase={phase ?? 'unavailable'}
      role='dialog'
      aria-modal='false'
      aria-label={t('conversation.xscienceRuntime.runtimeOperations.taskDetails')}
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.preventDefault();
          event.stopPropagation();
          onClose();
        }
      }}
    >
      <header className='x-science-task-center-panel__summary'>
        <div className='x-science-task-center-panel__summary-icon' aria-hidden='true'>
          <StatusGlyph phase={phase} />
        </div>
        <h2 ref={headingRef} tabIndex={-1} className='x-science-task-center-panel__title'>
          {statusTitle}
        </h2>
        <button
          type='button'
          className='x-science-task-center-panel__close'
          aria-label={t('common.close')}
          onClick={onClose}
        >
          <Close size={16} aria-hidden='true' />
        </button>
      </header>

      <div className='x-science-task-center-panel__body'>
        <div className='x-science-task-center-panel__snapshot' data-testid='x-science-task-status-snapshot'>
          <dl className='x-science-task-center-panel__timeline'>
            <div>
              <dt>{t('conversation.xscienceRuntime.runtimeOperations.taskMetricStartedAt')}</dt>
              <dd data-testid='x-science-task-metric-started' title={startedAtValue ?? undefined}>
                {startedAtValue ?? '—'}
              </dd>
            </div>
            <div>
              <dt>{t('conversation.xscienceRuntime.runtimeOperations.taskMetricFinishedAt')}</dt>
              <dd data-testid='x-science-task-metric-finished' title={finishedAtValue ?? undefined}>
                {finishedAtValue ?? '—'}
              </dd>
            </div>
          </dl>

          <dl
            className='x-science-task-center-panel__metrics'
            aria-label={t('conversation.xscienceRuntime.runtimeOperations.taskMetrics')}
          >
            <TaskCenterMetric
              label={t('conversation.xscienceRuntime.runtimeOperations.taskMetricElapsed')}
              value={taskElapsedValue ?? '—'}
              detail={latestDetail(latestElapsedValue)}
              testId='x-science-task-metric-elapsed'
            />
            <TaskCenterMetric
              label={t('conversation.xscienceRuntime.runtimeOperations.taskMetricTokenUsage')}
              value={compactTaskTokenUsage ?? '—'}
              detail={latestDetail(compactLatestTokenUsage)}
              title={exactTaskTokenUsage ?? undefined}
              testId='x-science-task-metric-tokens'
            />
            <TaskCenterMetric
              label={t('conversation.xscienceRuntime.runtimeOperations.taskMetricModelCalls')}
              value={exactTaskModelCalls ?? '—'}
              detail={latestDetail(exactLatestModelCalls)}
              testId='x-science-task-metric-model-calls'
            />
            <TaskCenterMetric
              label={t('conversation.xscienceRuntime.runtimeOperations.taskMetricToolCalls')}
              value={exactTaskToolCalls ?? '—'}
              detail={latestDetail(exactLatestToolCalls)}
              testId='x-science-task-metric-tools'
            />
          </dl>
        </div>

        {phase === 'completed' && reviewStopDetail ? (
          <section
            className='x-science-task-center-panel__section x-science-task-center-panel__section--attention'
            aria-labelledby='x-science-task-center-review-stop'
          >
            <h3 id='x-science-task-center-review-stop'>
              {t('conversation.xscienceRuntime.runtimeOperations.taskCompletedReviewInterrupted')}
            </h3>
            <p
              className='m-0 break-words text-12px leading-19px text-t-secondary'
              data-testid='x-science-task-center-review-stop-detail'
            >
              {reviewStopDetail}
            </p>
          </section>
        ) : null}

        {(phase === 'failed' || phase === 'paused') && failureDetail ? (
          <section
            className='x-science-task-center-panel__section x-science-task-center-panel__section--attention'
            aria-labelledby='x-science-task-center-failure-reason'
          >
            <h3 id='x-science-task-center-failure-reason'>
              {t('conversation.xscienceRuntime.runtimeOperations.taskFailureReason')}
            </h3>
            <p
              className='m-0 whitespace-pre-wrap break-words text-12px leading-19px text-t-secondary'
              data-testid='x-science-task-center-failure-detail'
              role='alert'
            >
              {failureDetail}
            </p>
          </section>
        ) : null}

        {attentionActionCount > 0 ? (
          <section
            className='x-science-task-center-panel__section x-science-task-center-panel__section--attention'
            aria-labelledby='x-science-task-center-attention'
          >
            <div className='x-science-task-center-panel__section-heading'>
              <h3 id='x-science-task-center-attention'>
                {t('conversation.xscienceRuntime.runtimeOperations.taskAttention')}
              </h3>
              <span className='x-science-task-center-panel__count'>{attentionActionCount}</span>
            </div>
            <div className='x-science-task-center-panel__actions'>
              {pendingInputCount > 0 && onOpenPendingInput ? (
                <TaskCenterActionRow
                  label={
                    phase === 'waiting_input'
                      ? t('conversation.xscienceRuntime.runtimeOperations.waitingForAnswer')
                      : t('conversation.xscienceRuntime.runtimeOperations.taskWaitingApproval')
                  }
                  meta={t('conversation.xscienceRuntime.runtimeOperations.taskPendingCount', {
                    count: pendingInputCount,
                  })}
                  count={pendingInputCount}
                  attention
                  testId='x-science-task-open-pending-input'
                  onClick={() => runAction(onOpenPendingInput)}
                />
              ) : null}
              {planNeedsApproval && onOpenPlan ? (
                <TaskCenterActionRow
                  label={t('conversation.xscienceRuntime.runtimeOperations.taskPlan')}
                  meta={t('conversation.xscienceRuntime.runtimeOperations.taskWaitingApproval')}
                  attention
                  testId='x-science-task-open-plan'
                  onClick={openPlan}
                />
              ) : null}
              {reviewFindingsAvailable && onOpenReviewFindings ? (
                <TaskCenterActionRow
                  label={t('conversation.xscienceRuntime.runtimeOperations.viewReviewFindings')}
                  meta={t('conversation.xscienceRuntime.runtimeOperations.taskIssueCount', {
                    count: reviewIssueCount,
                  })}
                  count={reviewIssueCount}
                  attention
                  testId='x-science-task-open-review'
                  onClick={() => runAction(onOpenReviewFindings)}
                />
              ) : null}
            </div>
          </section>
        ) : null}

        {showTaskOverview ? (
          <section className='x-science-task-center-panel__section' aria-labelledby='x-science-task-center-overview'>
            <h3 id='x-science-task-center-overview'>
              {t('conversation.xscienceRuntime.runtimeOperations.taskOverview')}
            </h3>
            <div className='x-science-task-center-panel__actions'>
              {planAvailable && !planNeedsApproval && onOpenPlan ? (
                <TaskCenterActionRow
                  label={t('conversation.xscienceRuntime.runtimeOperations.taskPlan')}
                  meta={t('conversation.xscienceRuntime.runtimeOperations.viewPlan')}
                  testId='x-science-task-open-plan'
                  onClick={openPlan}
                />
              ) : phase === 'completed' && !planAvailable ? (
                <TaskCenterRecordRow
                  label={t('conversation.xscienceRuntime.runtimeOperations.taskPlan')}
                  meta={t('conversation.xscienceRuntime.runtimeOperations.taskPlanUnavailable')}
                  testId='x-science-task-plan-unavailable'
                />
              ) : null}
              {reviewAvailable && !reviewFindingsAvailable && onOpenReviewFindings ? (
                <TaskCenterActionRow
                  label={t('conversation.xscienceRuntime.runtimeOperations.viewReviewResult')}
                  meta={reviewResultMeta}
                  testId='x-science-task-open-review'
                  onClick={() => runAction(onOpenReviewFindings)}
                />
              ) : null}
            </div>
          </section>
        ) : null}

        {recoveryAvailable && onRefresh ? (
          <section className='x-science-task-center-panel__section' aria-labelledby='x-science-task-center-recovery'>
            <h3 id='x-science-task-center-recovery'>
              {t('conversation.xscienceRuntime.runtimeOperations.taskRecovery')}
            </h3>
            <div className='x-science-task-center-panel__actions'>
              {onResume && recoveryResumeLabel ? (
                <TaskCenterActionRow
                  label={recoveryResumeLabel}
                  testId='x-science-task-resume'
                  onClick={() => runAction(onResume)}
                />
              ) : null}
              {onChooseModel ? (
                <TaskCenterActionRow
                  label={t('conversation.xscienceRuntime.runtimeOperations.chooseAnotherModel')}
                  testId='x-science-task-choose-model'
                  onClick={() => runAction(onChooseModel)}
                />
              ) : null}
              <TaskCenterActionRow
                label={t('conversation.xscienceRuntime.runtimeOperations.reloadRuntime')}
                testId='x-science-task-refresh'
                onClick={() => runAction(onRefresh)}
              />
            </div>
          </section>
        ) : null}
      </div>
    </div>
  );
};

export default XScienceTaskCenterPanel;

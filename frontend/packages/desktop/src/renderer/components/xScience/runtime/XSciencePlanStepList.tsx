import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XSciencePlanDocument } from './runtimeOperationsModel';
import { toolPublicDetailText } from '@/renderer/services/i18n/toolPublicDetailLocale';

const XSciencePlanStepList: React.FC<{
  steps: XSciencePlanDocument['phases'][number]['steps'];
}> = ({ steps }) => {
  const { t, i18n } = useTranslation();
  const chinese = i18n?.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  return steps.length > 0 ? (
    <ol className='x-science-plan-review__steps'>
      {steps.map((step) => (
        <li
          key={step.id}
          className='x-science-plan-review__step'
          data-plan-step-id={step.id}
          data-plan-step-status={step.status}
        >
          <div className='x-science-plan-review__step-row'>
            <span
              className={`x-science-plan-review__step-status x-science-plan-review__step-status--${
                step.status ?? 'pending'
              }`}
              aria-label={planStepStatusLabel(step.status, chinese)}
            >
              {planStepStatusSymbol(step.status)}
            </span>
            <div className='x-science-plan-review__step-content'>
              <div className='x-science-plan-review__step-title'>{step.title}</div>
              {step.description ? (
                <div className='x-science-plan-review__step-description'>{step.description}</div>
              ) : null}
            </div>
          </div>
        </li>
      ))}
    </ol>
  ) : (
    <div className='x-science-plan-review__empty-steps'>
      {t('conversation.xscienceRuntime.runtimeOperations.noExecutionSteps')}
    </div>
  );
};

function planStepStatusSymbol(status: XSciencePlanDocument['phases'][number]['steps'][number]['status']): string {
  if (status === 'completed') return '✓';
  if (status === 'failed' || status === 'cancelled') return '×';
  if (status === 'blocked') return '!';
  if (status === 'in_progress') return '●';
  if (status === 'unknown') return '?';
  return '○';
}

function planStepStatusLabel(
  status: XSciencePlanDocument['phases'][number]['steps'][number]['status'],
  chinese: boolean
): string {
  const labels = {
    pending: 'planPending',
    in_progress: 'planInProgress',
    completed: 'planCompleted',
    blocked: 'planBlocked',
    failed: 'planFailed',
    cancelled: 'planCancelled',
    unknown: 'planUnknown',
  } as const;
  return toolPublicDetailText(chinese, labels[status ?? 'pending']);
}

export default XSciencePlanStepList;

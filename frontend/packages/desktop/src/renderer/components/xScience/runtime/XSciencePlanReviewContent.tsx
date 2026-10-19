import { Button, Empty, Spin } from '@arco-design/web-react';
import { Redo } from '@icon-park/react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XSciencePlanDocument, XSciencePlanReference } from './runtimeOperationsModel';
import XSciencePlanTree from './XSciencePlanTree';
import './XSciencePlanReviewContent.css';

const XSciencePlanReviewContent: React.FC<{
  document: XSciencePlanDocument | null;
  reference: XSciencePlanReference | null;
  loading: boolean;
  error: string | null;
  onRetry: () => void;
  replaced?: boolean;
  canRetry?: boolean;
  onOpenCurrent?: () => void;
}> = ({ document, reference, loading, error, onRetry, replaced = false, canRetry = true, onOpenCurrent }) => {
  const { t, i18n } = useTranslation();
  const notice = replaced ? (
    <div className='px-12px py-8px text-12px text-t-secondary flex items-center justify-between gap-8px' role='status'>
      <span>{t('conversation.xscienceRuntime.runtimeOperations.planReplaced')}</span>
      {onOpenCurrent ? (
        <Button size='small' onClick={onOpenCurrent}>
          {t('conversation.xscienceRuntime.runtimeOperations.openCurrentPlan')}
        </Button>
      ) : null}
    </div>
  ) : null;
  if (loading) {
    return (
      <>
        {notice}
        <div className='h-240px flex-center'>
          <Spin tip={t('conversation.xscienceRuntime.runtimeOperations.loadingPlan')} />
        </div>
      </>
    );
  }
  if (error) {
    return (
      <>
        {notice}
        <div className='px-12px py-20px flex flex-col items-center gap-10px text-12px text-danger-6'>
          <span>{error}</span>
          {canRetry ? (
            <Button size='small' icon={<Redo theme='outline' size={14} />} onClick={onRetry}>
              {t('conversation.xscienceRuntime.runtimeOperations.reloadPlan')}
            </Button>
          ) : onOpenCurrent ? (
            <Button size='small' onClick={onOpenCurrent}>
              {t('conversation.xscienceRuntime.runtimeOperations.openCurrentPlan')}
            </Button>
          ) : null}
        </div>
      </>
    );
  }
  if (!document) return <Empty description={t('conversation.xscienceRuntime.runtimeOperations.noPlan')} />;
  const revisionNumber = reference?.revisionNumber ?? null;
  const revisionCount = reference?.revisionCount ?? null;
  const generatedAt = reference?.generatedAt
    ? new Intl.DateTimeFormat(i18n.resolvedLanguage || i18n.language, {
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
      }).format(new Date(reference.generatedAt))
    : null;
  const confidence = document.feasibility?.confidence?.toLowerCase() ?? null;
  const confidenceLabel =
    confidence === 'high'
      ? t('conversation.xscienceRuntime.runtimeOperations.planAssessment.high')
      : confidence === 'medium'
        ? t('conversation.xscienceRuntime.runtimeOperations.planAssessment.medium')
        : confidence === 'low'
          ? t('conversation.xscienceRuntime.runtimeOperations.planAssessment.low')
          : confidence;
  return (
    <div className='x-science-plan-review' data-testid='x-science-plan-review'>
      {notice}
      <div className='x-science-plan-review__meta'>
        <span className='font-[500] text-t-secondary'>
          {t(
            replaced
              ? 'conversation.xscienceRuntime.runtimeOperations.historicalExecutionPlan'
              : 'conversation.xscienceRuntime.runtimeOperations.currentExecutionPlan'
          )}
        </span>
        {revisionNumber && revisionCount ? (
          <span>
            {t('conversation.xscienceRuntime.runtimeOperations.planRevision', {
              revision: revisionNumber,
              count: revisionCount,
            })}
          </span>
        ) : null}
        {generatedAt ? (
          <span>
            {t('conversation.xscienceRuntime.runtimeOperations.planGeneratedAt', {
              time: generatedAt,
            })}
          </span>
        ) : null}
        {confidenceLabel ? (
          <span>
            {t('conversation.xscienceRuntime.runtimeOperations.planAssessment.label', {
              confidence: confidenceLabel,
            })}
          </span>
        ) : null}
      </div>
      {reference && !reference.versionId ? (
        <p className='text-12px text-t-secondary'>
          {t('conversation.xscienceRuntime.runtimeOperations.legacyPlanNotice')}
        </p>
      ) : null}
      <h3 className='x-science-plan-review__summary'>{document.taskSummary}</h3>
      {document.feasibility?.rationale ? (
        <p className='x-science-plan-review__rationale'>{document.feasibility.rationale}</p>
      ) : null}
      <XSciencePlanTree document={document} />
    </div>
  );
};

export default XSciencePlanReviewContent;

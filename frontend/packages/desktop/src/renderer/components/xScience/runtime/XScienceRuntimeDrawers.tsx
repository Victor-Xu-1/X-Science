import { Button, Drawer } from '@arco-design/web-react';
import { CheckOne, CloseOne } from '@icon-park/react';
import React from 'react';
import type { XScienceVerificationCheck } from '@/renderer/services/xScienceAnnotations';
import type { XSciencePlanDocument, XSciencePlanReference } from './runtimeOperationsModel';
import XSciencePlanReviewContent from './XSciencePlanReviewContent';
import XScienceReviewFindingsContent from './XScienceReviewFindingsContent';

type Props = {
  mobile: boolean;
  reviewTitle: string;
  reviewVisible: boolean;
  reviewChecks: XScienceVerificationCheck[];
  reviewLoading: boolean;
  reviewError: string | null;
  reviewRepairing: boolean;
  onCloseReview: () => void;
  onRetryReview: () => void;
  onRepairAndRegenerate?: () => void;
  planTitle: string;
  planVisible: boolean;
  planDocument: XSciencePlanDocument | null;
  planReference: XSciencePlanReference | null;
  planLoading: boolean;
  planError: string | null;
  planReplaced?: boolean;
  planCanRetry?: boolean;
  onOpenCurrentPlan?: () => void;
  planApprovalAvailable: boolean;
  planAction: 'approve' | 'discard' | null;
  returnPlanLabel: string;
  approvePlanLabel: string;
  onClosePlan: () => void;
  onRetryPlan: () => void;
  onCompletePlan: (action: 'approve' | 'discard') => void;
};

const XScienceRuntimeDrawers = ({
  mobile,
  reviewTitle,
  reviewVisible,
  reviewChecks,
  reviewLoading,
  reviewError,
  reviewRepairing,
  onCloseReview,
  onRetryReview,
  onRepairAndRegenerate,
  planTitle,
  planVisible,
  planDocument,
  planReference,
  planLoading,
  planError,
  planReplaced,
  planCanRetry,
  onOpenCurrentPlan,
  planApprovalAvailable,
  planAction,
  returnPlanLabel,
  approvePlanLabel,
  onClosePlan,
  onRetryPlan,
  onCompletePlan,
}: Props) => (
  <>
    <Drawer
      title={reviewTitle}
      visible={reviewVisible}
      wrapClassName='x-science-task-center-drawer'
      width={mobile ? '100%' : 680}
      footer={null}
      unmountOnExit
      onCancel={onCloseReview}
    >
      <XScienceReviewFindingsContent
        checks={reviewChecks}
        loading={reviewLoading}
        error={reviewError}
        onRetry={onRetryReview}
        onRepairAndRegenerate={onRepairAndRegenerate}
        repairing={reviewRepairing}
      />
    </Drawer>
    <Drawer
      title={planTitle}
      visible={planVisible}
      wrapClassName='x-science-task-center-drawer'
      width={mobile ? '100%' : 720}
      footer={null}
      unmountOnExit
      onCancel={onClosePlan}
    >
      <XSciencePlanReviewContent
        document={planDocument}
        reference={planReference}
        loading={planLoading}
        error={planError}
        replaced={planReplaced}
        canRetry={planCanRetry}
        onOpenCurrent={onOpenCurrentPlan}
        onRetry={onRetryPlan}
      />
      {planDocument && !planError && !planLoading && planApprovalAvailable ? (
        <div className='sticky bottom-0 mt-16px px-12px py-12px flex justify-end gap-8px border-t border-solid border-[var(--color-border-2)] bg-base'>
          <Button
            aria-label={returnPlanLabel}
            disabled={Boolean(planAction)}
            loading={planAction === 'discard'}
            icon={<CloseOne theme='outline' size={15} />}
            onClick={() => onCompletePlan('discard')}
          >
            {returnPlanLabel}
          </Button>
          <Button
            type='primary'
            aria-label={approvePlanLabel}
            disabled={Boolean(planAction)}
            loading={planAction === 'approve'}
            icon={<CheckOne theme='outline' size={15} />}
            onClick={() => onCompletePlan('approve')}
          >
            {approvePlanLabel}
          </Button>
        </div>
      ) : null}
    </Drawer>
  </>
);

export default XScienceRuntimeDrawers;

import type { XScienceAskUserResponse } from '@/renderer/services/xScienceRuntimeOperations';
import type { RefObject } from 'react';
import React from 'react';
import AskUserCard from './AskUserCard';
import XScienceApprovalCard from './XScienceApprovalCard';
import XScienceTaskAttentionNotice from './XScienceTaskAttentionNotice';
import type {
  XScienceApprovalScope,
  XSciencePendingInputRequest,
  XScienceRuntimeSnapshot,
} from './runtimeOperationsModel';
import type { XScienceTaskCenterMetrics } from './XScienceTaskCenterPanel';
import XScienceTaskStatus, { type XScienceTaskStatusError } from './XScienceTaskStatus';

type Props = {
  snapshot: XScienceRuntimeSnapshot | null;
  loading: boolean;
  snapshotError: XScienceTaskStatusError;
  runtimeState: string;
  terminalProjectionPending: boolean;
  runtimeAuthorityUnavailable: boolean;
  showLoadingStatus: boolean;
  pausing: boolean;
  resuming: boolean;
  pendingInputCount: number;
  taskCenterMetrics: XScienceTaskCenterMetrics;
  planAvailable: boolean;
  recoveryModelLabel: string | null;
  onResume?: () => void;
  onChooseModel?: () => void;
  onStop?: () => void;
  onRefresh: () => void;
  onOpenReviewFindings: () => void;
  onOpenPlan?: () => void;
  onOpenPendingInput?: () => void;
  pendingRequest?: XSciencePendingInputRequest;
  pendingInputRef: RefObject<HTMLDivElement | null>;
  resolvingRequestId: string | null;
  pendingAriaLabel: string;
  onResolveAskUser: (
    request: XSciencePendingInputRequest,
    response: XScienceAskUserResponse
  ) => void | Promise<void>;
  onResolveInput: (
    request: XSciencePendingInputRequest,
    decision: 'allow' | 'deny',
    scope?: XScienceApprovalScope
  ) => void | Promise<void>;
};

const XScienceRuntimeStatusSurface = ({
  snapshot,
  loading,
  snapshotError,
  runtimeState,
  terminalProjectionPending,
  runtimeAuthorityUnavailable,
  showLoadingStatus,
  pausing,
  resuming,
  pendingInputCount,
  taskCenterMetrics,
  planAvailable,
  recoveryModelLabel,
  onResume,
  onChooseModel,
  onStop,
  onRefresh,
  onOpenReviewFindings,
  onOpenPlan,
  onOpenPendingInput,
  pendingRequest,
  pendingInputRef,
  resolvingRequestId,
  pendingAriaLabel,
  onResolveAskUser,
  onResolveInput,
}: Props) => {
  const question = pendingRequest?.questions[0] ?? null;
  return (
    <>
      <XScienceTaskStatus
        snapshot={snapshot}
        loading={loading}
        snapshotError={snapshotError}
        runtimeState={runtimeState}
        terminalProjectionPending={terminalProjectionPending}
        runtimeAuthorityUnavailable={runtimeAuthorityUnavailable}
        showLoadingStatus={showLoadingStatus}
        pausing={pausing}
        resuming={resuming}
        pendingInputCount={pendingInputCount}
        taskCenterMetrics={taskCenterMetrics}
        planAvailable={planAvailable}
        recoveryModelLabel={recoveryModelLabel}
        onResume={onResume}
        onChooseModel={onChooseModel}
        onStop={onStop}
        onRefresh={onRefresh}
        onOpenReviewFindings={onOpenReviewFindings}
        onOpenPlan={onOpenPlan}
        onOpenPendingInput={onOpenPendingInput}
      />
      {snapshot?.planApproval ? (
        <XScienceTaskAttentionNotice kind='plan' reason={null} onAction={onOpenPlan} />
      ) : snapshot?.status === 'paused' && !pendingInputCount ? (
        <XScienceTaskAttentionNotice
          kind='paused'
          reason={snapshot.failureReason}
          busy={resuming}
          onAction={snapshot.canResume ? onResume : undefined}
        />
      ) : null}
      {pendingRequest ? (
        <div
          key={`${pendingRequest.requestId}:${pendingRequest.kind}`}
          ref={pendingInputRef}
          className='mb-8px'
          data-testid='x-science-pending-input-anchor'
          tabIndex={-1}
          aria-label={pendingAriaLabel}
        >
          {question ? (
            <AskUserCard
              question={question}
              questions={pendingRequest.questions}
              busy={resolvingRequestId === pendingRequest.requestId}
              disabled={Boolean(resolvingRequestId) && resolvingRequestId !== pendingRequest.requestId}
              onResolve={(response) => onResolveAskUser(pendingRequest, response)}
            />
          ) : (
            <XScienceApprovalCard
              request={pendingRequest}
              queueIndex={1}
              queueTotal={pendingInputCount || 1}
              busy={resolvingRequestId === pendingRequest.requestId}
              onAllow={(scope) => onResolveInput(pendingRequest, 'allow', scope)}
              onDeny={() => onResolveInput(pendingRequest, 'deny')}
            />
          )}
        </div>
      ) : null}
    </>
  );
};

export default XScienceRuntimeStatusSurface;

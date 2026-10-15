import { Button } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { runtimePausePresentation } from './runtimePausePresentation';
import type { XScienceProviderFailureKind } from './runtimeOperationsModel';
import './XScienceTaskAttentionNotice.css';

type Props = {
  kind: 'plan' | 'paused' | 'pending_input';
  reason: string | null;
  providerFailureKind?: XScienceProviderFailureKind | null;
  busy?: boolean;
  onAction?: () => void;
  onChooseModel?: () => void;
};

/** An entry to existing controllers, never another permission or resume authority. */
export default function XScienceTaskAttentionNotice({
  kind,
  reason,
  providerFailureKind,
  busy = false,
  onAction,
  onChooseModel,
}: Props) {
  const { t } = useTranslation();
  const pause = runtimePausePresentation(reason, providerFailureKind);
  const providerPaused = kind === 'paused' && reason === 'model_provider_unavailable';
  const title = t(
    `conversation.xscienceRuntime.runtimeOperations.${kind === 'plan' ? 'planApprovalTitle' : kind === 'pending_input' ? 'pendingInputContinuationTitle' : providerPaused ? pause.title : 'recoveryTitle'}`
  );
  const description = t(
    `conversation.xscienceRuntime.runtimeOperations.${kind === 'plan' ? 'planApprovalDescription' : kind === 'pending_input' ? 'pendingInputContinuationDescription' : pause.description}`
  );
  const action = t(
    `conversation.xscienceRuntime.runtimeOperations.${kind === 'plan' ? 'openPlanApproval' : 'resumePausedTask'}`
  );
  return (
    <section className='x-science-task-attention mb-8px' aria-label={title} data-testid={`x-science-${kind}-attention`}>
      <div className='x-science-task-attention__copy'>
        <strong>{title}</strong>
        <p>{description}</p>
      </div>
      {providerPaused && onChooseModel ? (
        <Button disabled={busy} onClick={onChooseModel}>
          {t('conversation.selectModel')}
        </Button>
      ) : null}
      {onAction ? (
        <Button type='primary' disabled={busy} loading={busy} onClick={onAction}>
          {action}
        </Button>
      ) : null}
    </section>
  );
}

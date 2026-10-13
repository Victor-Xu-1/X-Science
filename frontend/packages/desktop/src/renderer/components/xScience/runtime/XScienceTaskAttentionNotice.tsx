import { Button } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { runtimePausePresentation } from './runtimePausePresentation';
import './XScienceTaskAttentionNotice.css';

type Props = {
  kind: 'plan' | 'paused';
  reason: string | null;
  busy?: boolean;
  onAction?: () => void;
};

/** An entry to existing controllers, never another permission or resume authority. */
export default function XScienceTaskAttentionNotice({ kind, reason, busy = false, onAction }: Props) {
  const { t } = useTranslation();
  const pause = runtimePausePresentation(reason);
  const title = t(
    `conversation.xscienceRuntime.runtimeOperations.${kind === 'plan' ? 'planApprovalTitle' : 'recoveryTitle'}`
  );
  const description = t(
    `conversation.xscienceRuntime.runtimeOperations.${kind === 'plan' ? 'planApprovalDescription' : pause.description}`
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
      {onAction ? (
        <Button type='primary' disabled={busy} loading={busy} onClick={onAction}>
          {action}
        </Button>
      ) : null}
    </section>
  );
}

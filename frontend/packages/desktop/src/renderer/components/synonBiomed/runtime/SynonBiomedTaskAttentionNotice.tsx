import { Button } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import './SynonBiomedTaskAttentionNotice.css';

type Props = {
  kind: 'plan' | 'paused';
  reason: string | null;
  busy?: boolean;
  onAction?: () => void;
};

/** An entry to existing controllers, never another permission or resume authority. */
export default function SynonBiomedTaskAttentionNotice({ kind, reason, busy = false, onAction }: Props) {
  const { t } = useTranslation();
  const title = t(
    `conversation.synonRuntime.runtimeOperations.${kind === 'plan' ? 'planApprovalTitle' : 'recoveryTitle'}`
  );
  const description = t(
    `conversation.synonRuntime.runtimeOperations.${
      kind === 'plan'
        ? 'planApprovalDescription'
        : reason === 'provider_output_token_limit'
          ? 'outputLimitPaused'
          : 'recoveryPaused'
    }`
  );
  const action = t(
    `conversation.synonRuntime.runtimeOperations.${kind === 'plan' ? 'openPlanApproval' : 'resumePausedTask'}`
  );
  return (
    <section className='synon-task-attention mb-8px' aria-label={title} data-testid={`synon-biomed-${kind}-attention`}>
      <div className='synon-task-attention__copy'>
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

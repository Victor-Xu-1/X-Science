import React from 'react';
import { useTranslation } from 'react-i18next';
import type { DelegatedStopPresentation } from './useDelegatedStop';
import type { DelegatedStopOperation } from '@/renderer/services/runtime/delegatedStopTypes';

type StopStateLabel =
  | 'submitting'
  | 'checking'
  | 'unconfirmed'
  | 'rejected'
  | 'observationUnavailable'
  | 'stopping'
  | 'alreadyTerminal'
  | 'settled';
function stateLabel(operation: DelegatedStopOperation): StopStateLabel {
  if (operation.phase === 'sending') return 'submitting';
  if (operation.phase === 'checking') return 'checking';
  if (operation.phase === 'uncertain') return 'unconfirmed';
  if (operation.phase === 'rejected') return 'rejected';
  const targets = operation.receipt?.targets ?? [];
  if (targets.some((row) => row.execution_observation === 'unavailable')) return 'observationUnavailable';
  if (targets.some((row) => row.execution_observation === 'running')) return 'stopping';
  if (targets.length && targets.every((row) => row.outcome === 'already_terminal')) return 'alreadyTerminal';
  if (operation.phase === 'settled' && targets.every((row) => row.execution_observation === 'stopped'))
    return 'settled';
  return 'stopping';
}
export default function DelegatedStopActions({
  stop,
  childFrameId,
  rootTurnBusy = false,
}: {
  stop: DelegatedStopPresentation;
  childFrameId?: string;
  rootTurnBusy?: boolean;
}) {
  const { t } = useTranslation();
  const { controller, view } = stop;
  const row = childFrameId ? view.snapshot?.targets.find((target) => target.frame_id === childFrameId) : undefined;
  const operation = view.operation;
  const belongs =
    !childFrameId || operation?.request.mode === 'branch' || operation?.request.target?.frame_id === childFrameId;
  const shown = belongs ? operation : null;
  const pending = Boolean(operation && operation.phase !== 'settled' && operation.phase !== 'rejected');
  const allowed = childFrameId ? row?.can_stop === true : view.snapshot?.can_stop_branch === true;
  const issue = shown?.issue ?? view.issue;
  const label = t(childFrameId ? 'conversation.delegatedStop.child' : 'conversation.delegatedStop.branch');
  if (stop.unavailable)
    return (
      <div className='delegate-dock__stop-state' role='alert'>
        {t('conversation.delegatedStop.errors.capacity')}
        <button type='button' onClick={stop.retry}>
          {t('common.retry')}
        </button>
      </div>
    );
  if (!controller) return null;
  const visible = Boolean(
    allowed || shown || issue || view.loading || (childFrameId && row && (!row.target || row.status === 'unavailable'))
  );
  if (!visible) return null;
  return (
    <div className='delegate-dock__stop-controls' aria-label={label}>
      {(!rootTurnBusy || childFrameId) && (allowed || pending) && (
        <button
          type='button'
          className='delegate-dock__stop-button'
          aria-label={label}
          title={label}
          disabled={!allowed || pending || view.loading || Boolean(view.issue)}
          onClick={() => controller.start(view.snapshot, childFrameId)}
        >
          <svg width='14' height='14' viewBox='0 0 16 16' aria-hidden='true'>
            <rect x='3' y='3' width='10' height='10' rx='1.5' fill='currentColor' />
          </svg>
        </button>
      )}
      {view.loading && !shown && <span role='status'>{t('conversation.delegatedStop.loading')}</span>}
      {shown && (
        <span role='status' title={shown.request.branch.branch_id}>
          {t(`conversation.delegatedStop.states.${stateLabel(shown)}`)}
        </span>
      )}
      {shown?.receipt && shown.receipt.targets.length > 0 && (
        <details className='delegate-dock__stop-results'>
          <summary>{t('conversation.delegatedStop.results', { count: shown.receipt.targets.length })}</summary>
          <ul>
            {shown.receipt.targets.map((result) => (
              <li key={result.target.frame_id}>
                <span>{result.target.frame_id}</span>
                {' · '}
                {t(`conversation.delegatedStop.outcomes.${result.outcome}`)}
                {' · '}
                {t(`conversation.delegatedStop.observations.${result.execution_observation}`)}
                {result.diagnostic && (
                  <span>
                    {' · '}
                    {result.diagnostic}
                  </span>
                )}
              </li>
            ))}
          </ul>
        </details>
      )}
      {issue && <span role='alert'>{t(`conversation.delegatedStop.errors.${issue}`)}</span>}
      {!shown && row && (!row.target || row.status === 'unavailable') && (
        <span>{t('conversation.delegatedStop.targetUnavailable')}</span>
      )}
      {view.recoveryLimited && <span role='status'>{t('conversation.delegatedStop.recoveryLimited')}</span>}
      {shown && shown.phase !== 'rejected' && (
        <button
          type='button'
          className='delegate-dock__stop-text'
          disabled={['sending', 'checking'].includes(shown.phase)}
          onClick={() => {
            void controller.check(shown.request.request_id);
          }}
        >
          {t('conversation.delegatedStop.check')}
        </button>
      )}
      {shown && ['uncertain', 'stopping'].includes(shown.phase) && (
        <button
          type='button'
          className='delegate-dock__stop-text'
          onClick={() => controller.retry(shown.request.request_id)}
        >
          {t('conversation.delegatedStop.retry')}
        </button>
      )}
      {(!shown || shown.phase === 'rejected') && issue && (
        <button
          type='button'
          className='delegate-dock__stop-text'
          onClick={() => {
            void controller.refresh();
          }}
        >
          {t('common.retry')}
        </button>
      )}
    </div>
  );
}

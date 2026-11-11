import React from 'react';
import { useTranslation } from 'react-i18next';
import {
  CALL_PHASE_OFFSETS,
  TRANSPORT_PHASE_OFFSETS,
  type ContextCallPhaseTiming as CallTiming,
} from '@/renderer/services/runtime/contextInspection';
import styles from './ContextWindowInspector.module.css';

const PREFIX = 'conversation.contextInspector.phaseTiming.';
const milliseconds = (offset: number) =>
  `${(offset / 1000).toLocaleString(undefined, { maximumFractionDigits: 3 })} ms`;

/** Completed-call evidence inside the existing detail; never a live status. */
export default function ContextCallPhaseTiming({ timing }: { timing?: CallTiming }) {
  const { t } = useTranslation();
  const unknown = t(PREFIX + 'notRecorded');
  return (
    <details>
      <summary>{t(PREFIX + 'title')}</summary>
      {!timing ? (
        <p className={styles.muted}>{unknown}</p>
      ) : (
        <>
          <p className={styles.muted}>{t(PREFIX + 'help')}</p>
          {(timing.truncated || timing.saturated) && <p className={styles.muted}>{t(PREFIX + 'partial')}</p>}
          <dl className={styles.tokenRows}>
            {timing.preparation?.map((span, index) => (
              <div key={`${span.stage}:${index}`}>
                <dt>{t(PREFIX + span.stage)}</dt>
                <dd>
                  {milliseconds(span.beginUs)} → {span.endUs === undefined ? unknown : milliseconds(span.endUs)}
                </dd>
              </div>
            ))}
            {CALL_PHASE_OFFSETS.map((key) => (
              <div key={key}>
                <dt>{t(PREFIX + key)}</dt>
                <dd>{timing.offsetsUs[key] === undefined ? unknown : milliseconds(timing.offsetsUs[key])}</dd>
              </div>
            ))}
          </dl>
          {timing.transportAttempts?.map((attempt, index) => (
            <React.Fragment key={index}>
              <p className={styles.muted}>
                {t(PREFIX + 'httpAttempt', { count: attempt.attempt })} · {attempt.protocol}
              </p>
              <dl className={styles.tokenRows}>
                {TRANSPORT_PHASE_OFFSETS.map((key) => (
                  <div key={key}>
                    <dt>{t(PREFIX + key)}</dt>
                    <dd>{attempt.offsetsUs[key] === undefined ? unknown : milliseconds(attempt.offsetsUs[key])}</dd>
                  </div>
                ))}
              </dl>
            </React.Fragment>
          ))}
        </>
      )}
    </details>
  );
}

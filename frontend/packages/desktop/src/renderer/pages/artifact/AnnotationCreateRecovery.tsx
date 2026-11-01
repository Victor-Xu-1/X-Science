import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { AnnotationCreateView } from '@/renderer/services/runtime/AnnotationCreateIntentOwner';
import { useAnnotationCreateIntent } from '@/renderer/hooks/useAnnotationCreateIntent';
import type { XScienceAnnotationTarget } from '@/renderer/services/xScienceAnnotations';
import type { XScienceArtifactAnnotation } from '@/renderer/services/xScienceAnnotations';

const messages = {
  restored: 'preview.annotationRecovery.restored',
  resolving: 'preview.annotationRecovery.resolving',
  sending: 'preview.annotationRecovery.sending',
  uncertain: 'preview.annotationRecovery.uncertain',
  sourceUnavailable: 'preview.annotationRecovery.sourceUnavailable',
  conflict: 'preview.annotationRecovery.conflict',
  missing: 'preview.annotationRecovery.missing',
  storage: 'preview.annotationRecovery.storage',
} as const;

/** Inline recovery of the frozen request; it never reads the currently edited draft. */
export function AnnotationCreateRecovery({
  view,
  retry,
  onRecovered,
  onMissing,
  observeSuccess = false,
}: {
  view: AnnotationCreateView;
  observeSuccess?: boolean;
  retry: () => Promise<XScienceArtifactAnnotation | null>;
  onRecovered?: (annotation: XScienceArtifactAnnotation) => void | Promise<void>;
  onMissing?: () => void;
}) {
  const { t } = useTranslation();
  const active = useRef(true);
  const delivered = useRef(onRecovered);
  const missingCallback = useRef(onMissing);
  useLayoutEffect(() => {
    delivered.current = onRecovered;
    missingCallback.current = onMissing;
  }, [onRecovered, onMissing]);
  const [displayRetry, setDisplayRetry] = useState<XScienceArtifactAnnotation | null>(null);
  const seenSuccess = useRef(view.lastSuccess);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  const deliver = useCallback(async (annotation: XScienceArtifactAnnotation) => {
    if (!active.current) return;
    try {
      await delivered.current?.(annotation);
      if (active.current) setDisplayRetry(null);
    } catch {
      if (active.current) setDisplayRetry(annotation);
    }
  }, []);
  const recover = async () => {
    try {
      const annotation = await retry();
      if (annotation && active.current && !observeSuccess) await deliver(annotation);
    } catch {
      /* The owner publishes the recoverable request state; do not clear its intent. */
    }
  };
  useEffect(() => {
    const success = view.lastSuccess;
    if (!success || seenSuccess.current === success) return;
    seenSuccess.current = success;
    if (observeSuccess && active.current) void deliver(success.annotation);
  }, [view.lastSuccess, observeSuccess, deliver]);
  const observedMissing = useRef(false);
  useEffect(() => {
    const missing = view.status === 'missing';
    if (!missing) {
      observedMissing.current = false;
      return;
    }
    if (observedMissing.current) return;
    observedMissing.current = true;
    if (active.current) missingCallback.current?.();
  }, [view.status]);
  if (!view.pending && !displayRetry && (view.status === 'idle' || view.status === 'saved')) return null;
  const status = view.status in messages ? (view.status as keyof typeof messages) : 'restored';
  return (
    <section
      className='my-8px rounded-8px border border-solid border-base bg-fill-1 p-10px text-12px leading-18px'
      role={view.busy ? 'status' : 'alert'}
      aria-busy={view.busy}
      data-testid='annotation-create-recovery'
    >
      <p className='m-0'>{t(displayRetry ? 'preview.annotationRecovery.displayFailed' : messages[status])}</p>
      {view.pending && (
        <blockquote className='mx-0 my-6px max-h-54px overflow-auto whitespace-pre-wrap text-t-secondary'>
          {view.pending.body.text}
        </blockquote>
      )}
      {displayRetry ? (
        <button type='button' onClick={() => void deliver(displayRetry)}>
          {t('preview.annotationRecovery.retryDisplay')}
        </button>
      ) : (
        view.pending && (
          <button type='button' disabled={view.busy} onClick={() => void recover()}>
            {t('preview.annotationRecovery.retry')}
          </button>
        )
      )}
    </section>
  );
}

/** The same owner remains reachable when its selection dialog is closed. */
export function AnnotationCreateRecoveryForTarget({
  target,
  onRecovered,
  onMissing,
}: {
  target: XScienceAnnotationTarget;
  onRecovered?: (annotation: XScienceArtifactAnnotation) => void | Promise<void>;
  onMissing?: () => void;
}) {
  const creator = useAnnotationCreateIntent(target);
  return (
    <AnnotationCreateRecovery
      key={creator.scopeKey}
      view={creator.view}
      retry={creator.retryFrozen}
      onRecovered={onRecovered}
      onMissing={onMissing}
      observeSuccess
    />
  );
}

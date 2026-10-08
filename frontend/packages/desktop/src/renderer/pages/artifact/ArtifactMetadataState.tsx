/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useLayoutEffect, useRef } from 'react';
import { Button, Spin } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import type { ArtifactReadState } from './useArtifactMetadataReads';

export function ArtifactMetadataState({
  state,
  label,
  owner,
  onRetry,
  hasCachedValue = false,
  className,
  compact = false,
  children,
}: {
  state: ArtifactReadState<unknown>;
  label: string;
  owner: string;
  onRetry: () => void;
  hasCachedValue?: boolean;
  className?: string;
  compact?: boolean;
  children?: React.ReactNode;
}) {
  const { t } = useTranslation();
  const region = useRef<HTMLDivElement>(null);
  const returnIntent = useRef(false);
  useLayoutEffect(() => {
    returnIntent.current = false;
  }, [owner]);
  useLayoutEffect(() => {
    if (state.status !== 'ready' || !returnIntent.current) return;
    returnIntent.current = false;
    const target = region.current;
    if (document.activeElement === document.body && !target?.closest('[hidden],[aria-hidden="true"]'))
      target?.focus({ preventScroll: true });
  }, [state.status]);
  return (
    <div
      ref={region}
      className={className}
      role='group'
      aria-label={label}
      tabIndex={-1}
      aria-busy={state.status === 'loading'}
    >
      {state.status !== 'ready' && (
        <div className={`${compact ? '' : 'px-16px py-12px'} flex flex-col gap-8px text-12px text-t-secondary`}>
          <div role={state.status === 'failed' ? 'alert' : 'status'} className='flex items-center gap-8px'>
            {state.status === 'loading' && <Spin size={14} />}
            {t(`preview.artifact.metadata.${state.status}`, { section: label })}
          </div>
          {state.status === 'failed' && (
            <Button
              size='small'
              className='self-start'
              aria-label={t('preview.artifact.metadata.retry', { section: label })}
              onClick={() => {
                returnIntent.current = true;
                onRetry();
              }}
            >
              {t('common.retry')}
            </Button>
          )}
          {hasCachedValue && <p className='m-0 text-11px text-t-tertiary'>{t('preview.artifact.metadata.cached')}</p>}
        </div>
      )}
      {(state.status === 'ready' || hasCachedValue) && children}
    </div>
  );
}

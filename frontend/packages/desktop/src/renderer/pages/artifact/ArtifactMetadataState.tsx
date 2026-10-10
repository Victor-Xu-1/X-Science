/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useRef } from 'react';
import { Button, Spin } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import type { ArtifactReadState } from './useArtifactMetadataReads';
import { useArtifactRecoveryFocus } from './useArtifactRecoveryFocus';

export function ArtifactMetadataState({
  state,
  label,
  owner,
  onRetry,
  hasCachedValue = false,
  showKnownContent = false,
  className,
  compact = false,
  regionRef,
  children,
}: {
  state: ArtifactReadState<unknown>;
  label: string;
  owner: string;
  onRetry: () => void;
  hasCachedValue?: boolean;
  showKnownContent?: boolean;
  className?: string;
  compact?: boolean;
  regionRef?: React.RefObject<HTMLDivElement | null>;
  children?: React.ReactNode;
}) {
  const { t } = useTranslation();
  const internalRegion = useRef<HTMLDivElement>(null);
  const region = regionRef ?? internalRegion;
  const intendRecoveryFocus = useArtifactRecoveryFocus(owner, state.status, region);
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
                intendRecoveryFocus();
                onRetry();
              }}
            >
              {t('common.retry')}
            </Button>
          )}
          {hasCachedValue && <p className='m-0 text-11px text-t-tertiary'>{t('preview.artifact.metadata.cached')}</p>}
        </div>
      )}
      {(state.status === 'ready' || hasCachedValue || showKnownContent) && children}
    </div>
  );
}

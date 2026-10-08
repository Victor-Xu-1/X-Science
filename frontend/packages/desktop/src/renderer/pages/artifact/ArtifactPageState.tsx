/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React, { useRef } from 'react';
import { Button, Empty, Spin } from '@arco-design/web-react';
import { FileText } from '@icon-park/react';
import { useTranslation } from 'react-i18next';

export function ArtifactPageState({
  state,
  onRetry,
}: {
  state: 'loading' | 'failed' | 'missing';
  onRetry: () => void;
}) {
  const { t } = useTranslation();
  const heading = useRef<HTMLHeadingElement>(null);
  return (
    <div className='artifact-page size-full overflow-hidden bg-1'>
      <div className='artifact-shell size-full max-w-1600px mx-auto flex flex-col'>
        <header className='artifact-header min-h-68px px-18px md:px-28px py-12px flex items-center gap-12px border-b border-solid border-[var(--color-border-2)]'>
          <FileText theme='outline' size={21} className='shrink-0 text-t-secondary' aria-hidden />
          <h1 ref={heading} tabIndex={-1} className='m-0 flex-1 text-17px leading-24px font-[600] text-t-primary'>
            {t('preview.artifact.filePreview')}
          </h1>
          <Button href='#/guid'>{t('preview.artifact.backToWorkspace')}</Button>
        </header>
        <div className='min-h-0 flex-1 overflow-auto flex-center flex-col gap-16px px-24px py-32px'>
          {state === 'loading' ? (
            <div role='status' className='flex flex-col items-center gap-12px text-12px text-t-secondary'>
              <Spin />
              {t('common.loading')}
            </div>
          ) : (
            <>
              <div role={state === 'failed' ? 'alert' : undefined}>
                <Empty
                  description={t(state === 'failed' ? 'preview.artifact.loadFailed' : 'preview.artifact.notFound')}
                />
              </div>
              {state === 'failed' && (
                <Button
                  type='primary'
                  onClick={() => {
                    heading.current?.focus({ preventScroll: true });
                    onRetry();
                  }}
                >
                  {t('common.retry')}
                </Button>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

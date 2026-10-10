/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { Button, Empty, Spin } from '@arco-design/web-react';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { scientificPreviewErrorKey, type ScientificPreviewError } from './scientificPreviewError';

type StructurePreviewStatusProps = {
  filename: string;
  loading: boolean;
  error: ScientificPreviewError | null;
  onRetry: () => void;
};

/** An explicit retry reuses the existing, owner-fenced engine lifecycle. */
export function useStructurePreviewRetry({
  content,
  contentUrl,
  filename,
  loading,
  error,
}: Omit<StructurePreviewStatusProps, 'onRetry'> & { content?: string; contentUrl?: string }) {
  const [loadRevision, setLoadRevision] = useState(0);
  const statusRootRef = useRef<HTMLElement>(null);
  const returnFocus = useRef(false);

  useEffect(() => {
    returnFocus.current = false;
  }, [content, contentUrl, filename]);

  useEffect(() => {
    if (loading || !returnFocus.current) return;
    returnFocus.current = false;
    const root = statusRootRef.current;
    const active = document.activeElement;
    if (!root || root.closest('[hidden], [aria-hidden="true"]')) return;
    if (active && active !== document.body && active.isConnected) return;
    if (error) root.querySelector<HTMLButtonElement>('[data-testid="structure-preview-retry"]')?.focus();
    else root.focus();
  }, [loading, error]);

  const retryStructure = () => {
    returnFocus.current = true;
    setLoadRevision((revision) => revision + 1);
  };
  return { loadRevision, statusRootRef, retryStructure };
}

export function StructurePreviewStatus({ filename, loading, error, onRetry }: StructurePreviewStatusProps) {
  const { t } = useTranslation();
  if (error) {
    return (
      <div className='synon-biomed-molstar__overlay synon-biomed-molstar__overlay--error' role='alert'>
        <Empty
          description={t(scientificPreviewErrorKey(error), {
            kind: t('preview.scientific.structure.kind'),
            ...error.details,
          })}
        />
        <Button type='secondary' data-testid='structure-preview-retry' onClick={onRetry}>
          {t('preview.scientific.retry')}
        </Button>
      </div>
    );
  }
  if (!loading) return null;
  return (
    <div
      className='synon-biomed-molstar__overlay'
      role='status'
      aria-label={t('preview.scientific.loadingNamed', { name: filename })}
    >
      <Spin />
    </div>
  );
}

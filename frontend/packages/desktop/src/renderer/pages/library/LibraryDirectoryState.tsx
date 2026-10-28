import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LibraryReadPhase } from './useLibraryWorkbench';

/** Presentation of the existing read owner, without requests or retained state. */
export function LibraryDirectoryState({
  view,
  errorText,
  retry,
  className,
}: {
  view: LibraryReadPhase;
  errorText: string;
  retry?: () => void;
  className?: string;
}) {
  const { t } = useTranslation();
  if (view.status === 'error' || view.refreshError)
    return (
      <div role='alert' className={className}>
        <span>{errorText}</span>
        {retry && (
          <button type='button' onClick={retry}>
            {t('common.retry')}
          </button>
        )}
      </div>
    );
  if (view.status === 'loading' || view.refreshing)
    return (
      <p role='status' className={className}>
        {t('common.loading')}
      </p>
    );
  return null;
}

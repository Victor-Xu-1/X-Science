import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

type StorageNamespaceUpgradeReport = {
  version: 1;
  migrated: number;
  failures: Array<{ area: string; code: string }>;
  cachePending: boolean;
};

declare global {
  interface Window {
    __xScienceStorageNamespaceUpgrade?: StorageNamespaceUpgradeReport;
  }
}

const STATUS_EVENT = 'x-science:storage-upgrade-status';
const failureCount = (): number => window.__xScienceStorageNamespaceUpgrade?.failures.length ?? 0;

/** The startup migrator owns data; this notice only exposes its recovery state. */
export default function StorageNamespaceUpgradeNotice() {
  const { t } = useTranslation();
  const [failures, setFailures] = useState(failureCount);
  const [dismissed, setDismissed] = useState(false);
  useEffect(() => {
    const update = () => {
      setFailures(failureCount());
      setDismissed(false);
    };
    window.addEventListener(STATUS_EVENT, update);
    update(); // Close the render-to-effect gap for asynchronous cache failures.
    return () => window.removeEventListener(STATUS_EVENT, update);
  }, []);
  if (!failures || dismissed) return null;
  return (
    <aside
      role='alert'
      data-testid='storage-namespace-upgrade-notice'
      className='fixed right-4 top-4 z-50 max-w-sm rounded-xl border border-line-base bg-1 p-4 text-t-1 shadow-md'
    >
      <p className='mb-3 text-sm'>{t('common.storageNamespaceUpgradeWarning')}</p>
      <div className='flex justify-end gap-3'>
        <button type='button' className='min-h-8 px-2 text-sm' onClick={() => setDismissed(true)}>
          {t('common.close')}
        </button>
        <button type='button' className='min-h-8 px-2 text-sm underline' onClick={() => window.location.reload()}>
          {t('common.reload')}
        </button>
      </div>
    </aside>
  );
}

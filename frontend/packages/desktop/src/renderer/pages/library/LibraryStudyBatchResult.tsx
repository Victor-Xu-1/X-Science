import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureStudyBatchRow } from './literatureStudyBatchContract';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { LibraryStudyContent } from './LibraryStudySnapshot';
import { readStudyBatchResult } from './literatureStudyBatchApi';

export function LibraryStudyBatchResult({
  account,
  batchId,
  index,
  row,
  onOpenDetails,
}: {
  account: string;
  batchId: string;
  index: number;
  row: LiteratureStudyBatchRow;
  onOpenDetails: (id: string) => void;
}) {
  const { t } = useTranslation();
  const [state, setState] = useState<{ value?: Awaited<ReturnType<typeof readStudyBatchResult>>; failed?: boolean }>(
    {}
  );
  useEffect(() => {
    const controller = new AbortController();
    setState({});
    void readStudyBatchResult(batchId, index, row, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted && account === getRendererAccountScopeToken()) setState({ value });
      })
      .catch(() => {
        if (!controller.signal.aborted && account === getRendererAccountScopeToken()) setState({ failed: true });
      });
    return () => controller.abort();
  }, [account, batchId, index, row.request.operation_id, row.result_revision]);
  if (account !== getRendererAccountScopeToken()) return null;
  if (state.failed) return <p role='alert'>{t('conversation.libraryStudyBatch.resultChanged')}</p>;
  if (!state.value?.view.snapshot) return <p role='status'>{t('conversation.libraryStudy.loading')}</p>;
  return (
    <div className='library-study library-study-batch__result'>
      <p className='library-study__coverage'>
        {t('conversation.libraryStudy.coverage.' + state.value.view.snapshot.coverage)}
      </p>
      <LibraryStudyContent snapshot={state.value.view.snapshot} />
      <button type='button' className='library-workbench__button' onClick={() => onOpenDetails(state.value!.item.id)}>
        {t('conversation.libraryStudyBatch.details')}
      </button>
    </div>
  );
}

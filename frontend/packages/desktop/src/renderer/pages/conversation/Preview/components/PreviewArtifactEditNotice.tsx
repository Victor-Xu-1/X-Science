import React from 'react';
import { useTranslation } from 'react-i18next';
import type { PreviewArtifactEditState } from '../context/usePreviewArtifactEdits';

export function PreviewArtifactEditNotice({
  state,
  onOpenLatest,
}: {
  state?: PreviewArtifactEditState;
  onOpenLatest: () => void;
}) {
  const { t } = useTranslation();
  if (!state || (state.status === 'editing' && !state.restoring) || state.status === 'saving') return null;
  const key =
    state.status === 'conflict'
      ? 'conflict'
      : state.status === 'error'
        ? 'failed'
        : state.status === 'loading'
          ? 'loading'
          : 'historical';
  return (
    <div
      role='status'
      className='flex shrink-0 items-center justify-between gap-10px border-b border-arco-1 bg-fill-1 px-12px py-8px text-12px text-t-secondary'
    >
      <span>{t(`preview.artifactEdit.${key}`)}</span>
      {state.status === 'conflict' && state.currentHeadVersionId ? (
        <button
          type='button'
          onClick={onOpenLatest}
          className='shrink-0 border-0 bg-transparent text-brand hover:underline'
        >
          {t('preview.artifactEdit.openLatest')}
        </button>
      ) : null}
    </div>
  );
}

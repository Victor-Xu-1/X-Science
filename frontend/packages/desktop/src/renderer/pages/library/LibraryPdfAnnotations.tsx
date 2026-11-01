import { ANNOTATION_NOTE_MAX_UTF16_UNITS, isAnnotationNoteSaveable } from '@/common/config/annotationLimits';
import { Input } from '@arco-design/web-react';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type XSciencePdfArtifactViewer from '../artifact/XSciencePdfArtifactViewer';
import { useAnnotationStore } from '../artifact/useArtifactAnnotationStore';
import {
  annotationInputFromCanvasSelection,
  artifactCanvasSelectionIdentity,
  type XScienceArtifactCanvasSelection,
} from '../artifact/artifactCanvasSelection';

/** The preview and its note list share the original canonical annotation owner. */
export function LibraryPdfAnnotations({
  Viewer,
  filename,
  contentUrl,
  versionId,
}: {
  Viewer: typeof XSciencePdfArtifactViewer;
  filename: string;
  contentUrl: string;
  versionId: string;
}) {
  const { t } = useTranslation();
  const store = useAnnotationStore({ kind: 'literature', versionId }, 0);
  const [selection, setSelection] = useState<XScienceArtifactCanvasSelection | null>(null);
  const [note, setNote] = useState('');
  const [failed, setFailed] = useState(false);
  const [reveal, setReveal] = useState<{ page: number; id: string }>();
  const save = async (event: React.FormEvent) => {
    event.preventDefault();
    if (!selection || !isAnnotationNoteSaveable(note) || store.busy || store.loading || store.failed || !store.checksum)
      return;
    setFailed(false);
    try {
      if (await store.create(annotationInputFromCanvasSelection(selection, note.trim()))) {
        setSelection(null);
        setNote('');
      }
    } catch {
      setFailed(true);
    }
  };
  return (
    <div className='flex h-full min-h-0 flex-col' data-testid='library-pdf-annotations'>
      <div className='min-h-240px flex-1'>
        <Viewer
          filename={filename}
          contentUrl={contentUrl}
          annotations={store.annotations}
          onSelectionChange={(value) => {
            if (!store.busy) {
              const previousAnchor = selection ? artifactCanvasSelectionIdentity(selection) : null;
              const nextAnchor = value ? artifactCanvasSelectionIdentity(value) : null;
              setSelection(value);
              if (nextAnchor !== previousAnchor) {
                setNote('');
                setFailed(false);
              }
            }
          }}
          onAnnotationClick={(value) => {
            if (value.pageNumber) setReveal({ page: value.pageNumber, id: `${value.id}:${Date.now()}` });
          }}
          targetPage={reveal?.page}
          revealId={reveal?.id}
        />
      </div>
      <section
        className='border-t border-solid border-base p-12px text-12px'
        aria-label={t('preview.artifactAnnotations.title')}
        aria-busy={store.loading || store.busy}
      >
        <div className='flex items-center justify-between gap-8px'>
          <strong>{t('preview.artifactAnnotations.currentVersionCount', { count: store.annotations.length })}</strong>
          <button type='button' disabled={store.busy} onClick={() => void store.reload()}>
            {t('preview.artifactAnnotations.refresh')}
          </button>
        </div>
        {store.failed && <p role='alert'>{t('preview.artifactAnnotations.loadFailed')}</p>}
        {selection && (
          <form onSubmit={(event) => void save(event)} className='mt-8px flex flex-col gap-6px'>
            <blockquote className='max-h-70px overflow-auto whitespace-pre-wrap text-t-secondary'>
              {selection.text}
            </blockquote>
            <label>
              {t('preview.selectionAnnotation.content')}
              <Input.TextArea
                aria-label={t('preview.selectionAnnotation.content')}
                value={note}
                maxLength={{ length: ANNOTATION_NOTE_MAX_UTF16_UNITS, errorOnly: true }}
                showWordLimit
                rows={2}
                disabled={store.busy}
                className='w-full text-12px'
                onChange={setNote}
              />
            </label>
            <div className='flex gap-8px'>
              <button
                type='submit'
                disabled={
                  store.loading || store.busy || store.failed || !store.checksum || !isAnnotationNoteSaveable(note)
                }
              >
                {t('preview.artifactAnnotations.add')}
              </button>
              <button
                type='button'
                disabled={store.busy}
                onClick={() => {
                  setSelection(null);
                  setNote('');
                  setFailed(false);
                }}
              >
                {t('common.cancel')}
              </button>
            </div>
            {failed && <p role='alert'>{t('preview.selectionAnnotation.addFailed')}</p>}
          </form>
        )}
        {!store.loading && (
          <ul className='mt-8px max-h-140px list-none space-y-6px overflow-auto p-0'>
            {store.annotations.map((value) => (
              <li key={value.id}>
                <button
                  type='button'
                  className='text-left'
                  onClick={() => {
                    if (value.pageNumber) setReveal({ page: value.pageNumber, id: `${value.id}:${Date.now()}` });
                  }}
                >
                  {value.label} · {value.text}
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

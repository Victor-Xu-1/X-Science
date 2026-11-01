import { ANNOTATION_NOTE_MAX_UTF16_UNITS, isAnnotationNoteSaveable } from '@/renderer/utils/annotationNote';
import type { XScienceArtifactAnnotation } from '@/renderer/services/xScienceAnnotations';
import type { XScienceArtifactCanvasSelection } from './artifactCanvasSelection';
import { Input } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { useArtifactSelectionEditor } from './useArtifactSelectionEditor';
import { AnnotationCreateRecovery } from './AnnotationCreateRecovery';
import type { AnnotationCreateSource } from '@/renderer/services/runtime/annotationCreateIntent';

export const ArtifactSelectionAnnotationModal: React.FC<{
  artifactId: string;
  versionId: string;
  selection: XScienceArtifactCanvasSelection;
  onCancel: () => void;
  onCreated: (annotation: XScienceArtifactAnnotation) => void | Promise<void>;
  sourceHint?: AnnotationCreateSource | null;
  onRecovered?: (annotation: XScienceArtifactAnnotation) => void | Promise<void>;
  onMissing?: () => void;
}> = ({ artifactId, versionId, selection, onCancel, onCreated, sourceHint, onRecovered, onMissing }) => {
  const { t } = useTranslation();
  const { note, setNote, saving, error, created, save, retryDisplay, close, creator, recovered, missing } =
    useArtifactSelectionEditor(artifactId, versionId, selection, onCreated, sourceHint, onRecovered, onMissing);
  const closeView = () => {
    close();
    onCancel();
  };

  return (
    <Modal
      title={
        selection.type === 'point'
          ? t('preview.selectionAnnotation.pointTitle')
          : selection.type === 'html_element'
            ? t('preview.selectionAnnotation.elementTitle')
            : t('preview.selectionAnnotation.selectionTitle')
      }
      visible
      onCancel={closeView}
      onOk={() => {
        if (created && error !== 'display') closeView();
        else void (created ? retryDisplay() : save());
      }}
      confirmLoading={saving}
      okButtonProps={{
        disabled: saving || (!created && (!isAnnotationNoteSaveable(note) || Boolean(creator.view.pending))),
      }}
      okText={created ? t(error === 'display' ? 'common.retry' : 'common.close') : t('preview.artifactAnnotations.add')}
      cancelText={t(created ? 'common.close' : 'common.cancel')}
      hideCancel={created !== null}
      unmountOnExit
      style={{ width: 560, maxWidth: 'calc(100vw - 32px)' }}
    >
      <div className='flex flex-col gap-12px' aria-busy={saving}>
        <div
          className='max-h-120px overflow-auto border-l-3 border-solid bg-fill-1 px-10px py-8px whitespace-pre-wrap break-words text-12px leading-19px text-t-primary'
          style={{ borderLeftColor: 'rgb(var(--primary-6))' }}
        >
          {selection.text}
        </div>
        <label className='flex flex-col gap-6px text-12px text-t-secondary'>
          {t('preview.artifactAnnotations.fields.content')}
          <Input.TextArea
            aria-label={t('preview.selectionAnnotation.content')}
            value={note}
            onChange={setNote}
            disabled={saving || created !== null}
            autoFocus
            autoSize={{ minRows: 3, maxRows: 8 }}
            maxLength={{ length: ANNOTATION_NOTE_MAX_UTF16_UNITS, errorOnly: true }}
            showWordLimit
          />
        </label>
        <AnnotationCreateRecovery
          key={creator.scopeKey}
          view={creator.view}
          retry={creator.retryFrozen}
          onRecovered={recovered}
          onMissing={missing}
        />
        {created && (
          <div role='status' className='text-12px text-success-6'>
            {t('preview.artifactAnnotations.added')}
          </div>
        )}
        {error && (
          <div
            role='alert'
            className={`text-12px leading-18px ${error === 'display' ? 'text-warning-6' : 'text-danger-6'}`}
          >
            {t(
              error === 'display'
                ? 'preview.selectionAnnotation.refreshFailed'
                : 'preview.selectionAnnotation.addFailed'
            )}
          </div>
        )}
      </div>
    </Modal>
  );
};

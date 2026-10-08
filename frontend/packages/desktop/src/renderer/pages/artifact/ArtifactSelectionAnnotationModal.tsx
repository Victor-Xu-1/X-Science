import type { SynonBiomedArtifactAnnotation } from '@/renderer/services/synonBiomedAnnotations';
import type { SynonBiomedArtifactCanvasSelection } from './artifactCanvasSelection';
import { Input } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { useArtifactSelectionEditor } from './useArtifactSelectionEditor';

export const ArtifactSelectionAnnotationModal: React.FC<{
  artifactId: string;
  versionId: string;
  selection: SynonBiomedArtifactCanvasSelection;
  onCancel: () => void;
  onCreated: (annotation: SynonBiomedArtifactAnnotation) => void | Promise<void>;
}> = ({ artifactId, versionId, selection, onCancel, onCreated }) => {
  const { t } = useTranslation();
  const { note, setNote, saving, error, created, save, retryDisplay, close } = useArtifactSelectionEditor(
    artifactId,
    versionId,
    selection,
    onCreated
  );
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
      okButtonProps={{ disabled: !note.trim() || saving }}
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
            maxLength={4000}
            showWordLimit
          />
        </label>
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

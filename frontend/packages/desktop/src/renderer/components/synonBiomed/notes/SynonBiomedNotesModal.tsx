import type { SynonBiomedNoteTarget } from '@/renderer/services/synonBiomedNotes';
import { Button, Empty, Input, Popconfirm, Spin } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { Delete, Edit, Plus, Refresh } from '@icon-park/react';
import React, { useRef } from 'react';
import { useTranslation } from 'react-i18next';
import MarkdownView from '@/renderer/components/Markdown';
import { useSynonBiomedNotesEditor } from './useSynonBiomedNotesEditor';

type Props = {
  visible: boolean;
  target: SynonBiomedNoteTarget;
  onClose: () => void;
};

const SynonBiomedNotesModal: React.FC<Props> = ({ visible, target, onClose }) => {
  const { t, i18n } = useTranslation();
  const {
    notes,
    draft,
    setDraft,
    editingId,
    loading,
    loadFailed,
    saving,
    deletingId,
    mutationFailed,
    load,
    save,
    remove,
    edit,
    cancelEditing,
  } = useSynonBiomedNotesEditor(visible, target);
  const draftInput = useRef<React.ComponentRef<typeof Input.TextArea>>(null);
  const writing = saving || deletingId !== null;

  return (
    <Modal
      title={t('conversation.notes.title')}
      visible={visible}
      onCancel={onClose}
      footer={null}
      unmountOnExit
      style={{ width: 620, maxWidth: 'calc(100vw - 24px)' }}
    >
      <div className='flex flex-col gap-12px' data-testid='synon-biomed-notes-modal' aria-busy={loading || writing}>
        <Input.TextArea
          ref={draftInput}
          value={draft}
          onChange={setDraft}
          disabled={writing}
          autoSize={{ minRows: 3, maxRows: 8 }}
          maxLength={20_000}
          showWordLimit
          placeholder={editingId ? t('conversation.notes.editPlaceholder') : t('conversation.notes.addPlaceholder')}
          aria-label={t('conversation.notes.content')}
        />
        <div className='flex justify-end gap-8px'>
          {editingId && (
            <Button
              disabled={writing}
              onClick={() => {
                cancelEditing();
                draftInput.current?.focus();
              }}
            >
              {t('conversation.notes.cancelEditing')}
            </Button>
          )}
          <Button
            type='primary'
            icon={<Plus />}
            loading={saving}
            disabled={!draft.trim() || loading || writing}
            onClick={() => void save()}
          >
            {editingId ? t('conversation.notes.saveChanges') : t('conversation.notes.add')}
          </Button>
        </div>
        {mutationFailed && (
          <div role='alert' className='text-12px text-danger-6'>
            {t(mutationFailed === 'save' ? 'conversation.notes.saveFailed' : 'conversation.notes.deleteFailed')}
          </div>
        )}
        {loadFailed && !loading && (
          <div
            className={`${notes.length === 0 ? 'min-h-120px ' : ''}flex flex-col items-center justify-center gap-12px text-t-secondary`}
          >
            <span role='alert'>{t('conversation.notes.loadFailed')}</span>
            <Button icon={<Refresh />} disabled={writing} onClick={() => void load()}>
              {t('common.retry')}
            </Button>
          </div>
        )}

        {loading ? (
          <div
            className='min-h-120px flex items-center justify-center'
            role='status'
            aria-label={t('conversation.notes.loading')}
          >
            <Spin />
          </div>
        ) : notes.length === 0 ? (
          !loadFailed && <Empty description={t('conversation.notes.empty')} />
        ) : (
          <div className='max-h-360px overflow-y-auto flex flex-col gap-8px'>
            {notes.map((note) => (
              <div key={note.id} className='border border-solid border-b-1 rounded-6px p-12px'>
                <div className='break-words text-14px'>
                  <MarkdownView>{note.content}</MarkdownView>
                </div>
                <div className='mt-8px flex items-center justify-between gap-8px text-12px text-t-secondary'>
                  <span>{new Date(note.updatedAt).toLocaleString(i18n.language)}</span>
                  <div className='flex gap-4px'>
                    <Button
                      type='text'
                      size='small'
                      icon={<Edit />}
                      disabled={writing}
                      aria-label={t('conversation.notes.edit')}
                      onClick={() => {
                        edit(note);
                        draftInput.current?.focus();
                      }}
                    />
                    <Popconfirm title={t('conversation.notes.deleteConfirm')} onOk={() => remove(note.id)}>
                      <Button
                        type='text'
                        status='danger'
                        size='small'
                        icon={<Delete />}
                        disabled={writing}
                        loading={deletingId === note.id}
                        aria-label={t('conversation.notes.delete')}
                      />
                    </Popconfirm>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
    </Modal>
  );
};

export default SynonBiomedNotesModal;

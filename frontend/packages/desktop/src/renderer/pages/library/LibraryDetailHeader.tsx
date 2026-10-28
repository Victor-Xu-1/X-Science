import React from 'react';
import { ArrowLeft, Pencil, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';

/** Mode chrome only; Details keeps the draft, dismissal and focus owners. */
export function LibraryDetailHeader({
  title,
  editing,
  busy,
  editorId,
  headerRef,
  editRef,
  edit,
  back,
  close,
}: {
  title: string;
  editing: boolean;
  busy: boolean;
  editorId: string;
  headerRef: React.RefObject<HTMLDivElement | null>;
  editRef: React.RefObject<HTMLButtonElement | null>;
  edit: () => void;
  back: () => void;
  close: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div
      ref={headerRef}
      tabIndex={-1}
      className='library-workbench__detail-main-header'
      role='group'
      aria-label={t('conversation.libraryWorkbench.details')}
    >
      {editing && (
        <button
          type='button'
          className='library-workbench__icon'
          disabled={busy}
          onClick={back}
          aria-label={t('conversation.libraryWorkbench.back')}
        >
          <ArrowLeft size={16} aria-hidden='true' />
        </button>
      )}
      <div className='library-workbench__detail-main-title'>
        <h2 title={title}>{editing ? t('conversation.libraryWorkbench.edit') : title}</h2>
        {editing && <p className='library-workbench__detail-subtitle'>{title}</p>}
      </div>
      <div className='library-workbench__detail-main-actions'>
        <button
          ref={editRef}
          type='button'
          className='library-workbench__button'
          hidden={editing}
          disabled={busy}
          aria-expanded={editing}
          aria-controls={editing ? editorId : undefined}
          onClick={edit}
        >
          <Pencil size={14} aria-hidden='true' />
          {t('conversation.libraryWorkbench.edit')}
        </button>
        <button type='button' className='library-workbench__icon' onClick={close} aria-label={t('common.close')}>
          <X size={16} aria-hidden='true' />
        </button>
      </div>
    </div>
  );
}

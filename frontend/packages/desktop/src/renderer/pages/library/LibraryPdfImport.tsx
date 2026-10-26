import React from 'react';
import { FilePlus2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LibraryPdfImportModel } from './useLibraryPdfImport';
import './libraryPdfImport.css';

/** The real browser chooser is shared by draft selection and exact-file reselection. */
export function LibraryPdfImport({ model }: { model: LibraryPdfImportModel }) {
  const { t } = useTranslation();
  return (
    <span className='library-pdf-import__entry'>
      <button
        ref={model.entryRef}
        type='button'
        className='library-workbench__button library-pdf-import__button'
        aria-expanded={model.open}
        disabled={model.blockedByEditor}
        onClick={() => {
          if (
            model.pending ||
            model.busy ||
            model.discarding ||
            model.open ||
            model.otherPending ||
            model.view.error === 'storage'
          )
            model.openPanel();
          else model.inputRef.current?.click();
        }}
      >
        <FilePlus2 size={14} aria-hidden='true' />
        {t(model.pending ? 'conversation.libraryPdfImport.resumeEntry' : 'conversation.libraryPdfImport.entry')}
      </button>
      <input
        ref={model.inputRef}
        className='library-pdf-import__file-input'
        type='file'
        accept='application/pdf,.pdf'
        aria-label={t('conversation.libraryPdfImport.fileInput')}
        onChange={(event) => {
          const selected = event.currentTarget.files?.[0];
          event.currentTarget.value = '';
          if (selected) model.chooseFile(selected);
        }}
      />
    </span>
  );
}

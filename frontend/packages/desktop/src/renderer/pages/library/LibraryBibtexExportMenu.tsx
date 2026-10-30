import React from 'react';
import { Dropdown, Menu } from '@arco-design/web-react';
import { Download, LoaderCircle } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { useLibraryActionMenu } from './useLibraryActionMenu';
import type { LibraryBibtexExportModel } from './useLibraryBibtexExport';

export function LibraryBibtexExportMenu({ model }: { model: LibraryBibtexExportModel }) {
  const { t } = useTranslation();
  const menu = useLibraryActionMenu(model.disabled);
  return (
    <span className='library-selection-toolbar__export'>
      <Dropdown
        trigger='click'
        position='br'
        popupVisible={menu.visible}
        onVisibleChange={menu.visibility}
        droplist={
          <Menu
            onKeyDownCapture={menu.escape}
            onClickMenuItem={(_key, event) => menu.select(event, () => void model.start())}
          >
            <Menu.Item key='bibtex'>BibTeX (.bib)</Menu.Item>
          </Menu>
        }
      >
        <button
          ref={menu.trigger}
          type='button'
          disabled={model.disabled}
          aria-label={t('conversation.libraryExport.entry')}
          aria-busy={model.phase === 'loading'}
          aria-haspopup='menu'
          aria-expanded={menu.visible}
          onKeyDownCapture={menu.escape}
        >
          {model.phase === 'loading' ? (
            <LoaderCircle size={14} aria-hidden='true' />
          ) : (
            <Download size={14} aria-hidden='true' />
          )}
          {t(model.phase === 'loading' ? 'conversation.libraryExport.loading' : 'conversation.libraryExport.entry')}
        </button>
      </Dropdown>
      {model.phase === 'error' && <span role='alert'>{t('conversation.libraryExport.error')}</span>}
      {model.phase === 'success' && <span role='status'>{t('conversation.libraryExport.success')}</span>}
    </span>
  );
}

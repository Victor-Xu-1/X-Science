import React, { useCallback } from 'react';
import { Dropdown, Menu } from '@arco-design/web-react';
import { Plus } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LibraryPdfImportModel } from './useLibraryPdfImport';
import { activateLibraryPdfImport, LibraryPdfChooser } from './LibraryPdfImport';
import { useLibraryActionMenu } from './useLibraryActionMenu';

/** Only connected methods are shown; the single PDF chooser remains mounted outside the popup. */
export function LibraryAddMenu({
  disabled,
  manualDisabled,
  add,
  pdf,
}: {
  disabled: boolean;
  manualDisabled: boolean;
  add: () => void;
  pdf: LibraryPdfImportModel;
}) {
  const { t } = useTranslation();
  const menu = useLibraryActionMenu(disabled);
  const attachTrigger = useCallback(
    (node: HTMLButtonElement | null) => {
      menu.trigger.current = node;
      pdf.entryRef.current = node;
    },
    [menu.trigger, pdf.entryRef]
  );
  return (
    <span className='library-pdf-import__entry'>
      <Dropdown
        trigger='click'
        position='br'
        popupVisible={menu.visible}
        onVisibleChange={menu.visibility}
        droplist={
          <Menu
            onKeyDownCapture={menu.escape}
            onClickMenuItem={(key, event) =>
              menu.select(event, () => {
                if (key === 'manual' && !manualDisabled) add();
                if (key === 'pdf') activateLibraryPdfImport(pdf);
              })
            }
          >
            <Menu.Item key='manual' disabled={manualDisabled} aria-disabled={manualDisabled}>
              {t('conversation.libraryManual.add')}
            </Menu.Item>
            <Menu.Item key='pdf' disabled={pdf.blockedByEditor} aria-disabled={pdf.blockedByEditor}>
              {t(pdf.pending ? 'conversation.libraryPdfImport.resumeEntry' : 'conversation.libraryPdfImport.entry')}
            </Menu.Item>
          </Menu>
        }
      >
        <button
          ref={attachTrigger}
          type='button'
          className='library-workbench__button'
          disabled={disabled}
          aria-haspopup='menu'
          aria-expanded={menu.visible}
          onKeyDownCapture={menu.escape}
        >
          <Plus size={14} aria-hidden='true' />
          {t('conversation.libraryManual.addMenu')}
        </button>
      </Dropdown>
      <LibraryPdfChooser model={pdf} />
    </span>
  );
}

import React from 'react';
import { Dropdown, Menu } from '@arco-design/web-react';
import { Check, ChevronRight, Download, FileText, LoaderCircle, MoreHorizontal, Pencil, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { isLiteratureExportFormat } from '@/common/adapter/literatureReferenceExport';
import { useLibraryExportMenu } from './useLibraryExportMenu';
import type { LibraryReferenceExportModel } from './useLibraryReferenceExport';
export function LibraryReferenceExportMenu({
  model,
  scope = false,
  collectionActions,
}: {
  model: LibraryReferenceExportModel;
  scope?: boolean;
  collectionActions?: { edit: () => void; remove: () => void; disabled: boolean };
}) {
  const { t } = useTranslation();
  const triggerDisabled = scope ? model.triggerDisabled : model.disabled;
  const menu = useLibraryExportMenu(triggerDisabled);
  const formats = ['bibtex', 'ris'].map((format) => (
    <Menu.Item key={format} disabled={model.disabled} aria-disabled={model.disabled}>
      <FileText size={14} aria-hidden='true' />
      {t(format === 'ris' ? 'conversation.libraryExport.ris' : 'conversation.libraryExport.bibtex')}
    </Menu.Item>
  ));
  const label = scope
    ? 'conversation.libraryExport.moreActions'
    : model.phase === 'loading'
      ? 'conversation.libraryExport.loading'
      : model.phase === 'success'
        ? 'conversation.libraryExport.success'
        : 'conversation.libraryExport.entry';
  return (
    <span className='library-selection-toolbar__export'>
      <Dropdown
        trigger='click'
        position={scope ? 'br' : 'tr'}
        popupVisible={menu.visible}
        onVisibleChange={menu.visibility}
        droplist={
          <div
            ref={menu.content}
            onPointerEnter={menu.clear}
            onPointerLeave={menu.leave}
            onPointerDown={menu.pin}
            onKeyDownCapture={(event) => {
              menu.pin();
              menu.escape(event);
            }}
          >
            <Menu
              onClickMenuItem={(key, event) =>
                menu.select(event, () => {
                  if (isLiteratureExportFormat(key) && !model.disabled) void model.start(key);
                  if (key === 'edit' && collectionActions && !collectionActions.disabled) collectionActions.edit();
                  if (key === 'delete' && collectionActions && !collectionActions.disabled) collectionActions.remove();
                })
              }
            >
              {scope && collectionActions && (
                <Menu.Item key='edit' disabled={collectionActions.disabled}>
                  <Pencil size={14} aria-hidden='true' />
                  {t('conversation.libraryWorkbench.editCollection')}
                </Menu.Item>
              )}
              {scope ? (
                <Menu.SubMenu
                  key='export'
                  title={t('conversation.libraryExport.entry')}
                  triggerProps={{ mouseEnterDelay: 120, mouseLeaveDelay: 180 }}
                >
                  {formats}
                </Menu.SubMenu>
              ) : (
                formats
              )}
              {scope && collectionActions && <div role='separator' className='library-export-menu__separator' />}
              {scope && collectionActions && (
                <Menu.Item key='delete' disabled={collectionActions.disabled}>
                  <Trash2 size={14} aria-hidden='true' />
                  {t('conversation.libraryWorkbench.deleteCollection')}
                </Menu.Item>
              )}
            </Menu>
          </div>
        }
      >
        <button
          ref={menu.trigger}
          type='button'
          className='library-workbench__button'
          disabled={triggerDisabled}
          aria-label={t(scope ? 'conversation.libraryExport.moreActions' : 'conversation.libraryExport.entry')}
          aria-busy={model.phase === 'loading'}
          aria-invalid={model.phase === 'error' || undefined}
          data-state={model.phase}
          aria-haspopup='menu'
          aria-expanded={menu.visible}
          onKeyDownCapture={menu.keyboard}
          onPointerDown={menu.pin}
          onPointerEnter={scope ? undefined : menu.enter}
          onPointerLeave={scope ? undefined : menu.leave}
        >
          {model.phase === 'loading' ? (
            <LoaderCircle size={14} aria-hidden='true' />
          ) : model.phase === 'success' ? (
            <Check size={14} aria-hidden='true' />
          ) : scope ? (
            <MoreHorizontal size={14} aria-hidden='true' />
          ) : (
            <Download size={14} aria-hidden='true' />
          )}
          {!scope && (
            <>
              {t(label)}
              <ChevronRight size={14} aria-hidden='true' />
            </>
          )}
        </button>
      </Dropdown>
      {model.phase === 'error' && <span role='alert'>{t('conversation.libraryExport.error')}</span>}
    </span>
  );
}

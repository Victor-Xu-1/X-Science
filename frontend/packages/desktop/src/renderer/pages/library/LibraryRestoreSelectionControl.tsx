import React, { useEffect, useRef } from 'react';
import { Dropdown, Menu } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import { useLibraryExportMenu } from './useLibraryExportMenu';
import type { LibraryRestoreSelectionModel } from './useLibraryRestoreSelection';

/** Controlled menu/confirmation only; preview and durable writes have their existing owners. */
export function LibraryRestoreSelectionControl({ model }: { model: LibraryRestoreSelectionModel }) {
  const { t } = useTranslation();
  const menu = useLibraryExportMenu(model.disabled);
  const open = model.loading || model.count !== undefined;
  const previous = useRef(false);
  useEffect(() => {
    if (
      previous.current &&
      !open &&
      model.canFocus() &&
      document.activeElement === document.body &&
      menu.trigger.current?.isConnected
    )
      menu.trigger.current.focus({ preventScroll: true });
    previous.current = open;
  }, [open, model, menu.trigger]);
  return (
    <>
      <Dropdown
        trigger='click'
        position='tr'
        popupVisible={menu.visible}
        onVisibleChange={menu.visibility}
        droplist={
          <div ref={menu.content} onKeyDownCapture={menu.escape}>
            <Menu
              onClickMenuItem={(_key, event) =>
                menu.select(event, () => {
                  if (!model.disabled) void model.preview();
                })
              }
            >
              <Menu.Item key='restore' disabled={model.disabled}>
                {t('conversation.libraryRestore.entry')}
              </Menu.Item>
            </Menu>
          </div>
        }
      >
        <button
          type='button'
          ref={menu.trigger}
          disabled={model.disabled}
          aria-haspopup='menu'
          aria-expanded={menu.visible}
          onKeyDownCapture={menu.keyboard}
        >
          {t('conversation.libraryRestore.entry')}
        </button>
      </Dropdown>
      {open && (
        <WorkbenchModal
          visible
          title={t('conversation.libraryRestore.entry')}
          footer={null}
          focusLock
          autoFocus
          escToExit
          maskStyle={{ backgroundColor: 'transparent' }}
          style={{ width: 420, maxWidth: 'calc(100vw - 32px)' }}
          onCancel={model.cancel}
        >
          <p role='status'>
            {model.loading
              ? t('conversation.libraryRestore.loading')
              : t('conversation.libraryRestore.confirmDescription', { count: model.count })}
          </p>
          <button type='button' onClick={model.cancel}>
            {t('common.cancel')}
          </button>
          {!model.loading && (
            <button type='button' onClick={() => void model.confirm()}>
              {t('conversation.libraryRestore.confirm', { count: model.count })}
            </button>
          )}
        </WorkbenchModal>
      )}
      {model.message && (
        <p role={model.message === 'emptyResolved' ? 'status' : 'alert'}>
          {t(`conversation.libraryRestore.${model.message}`)}
        </p>
      )}
    </>
  );
}

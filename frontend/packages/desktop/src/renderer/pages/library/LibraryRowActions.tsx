import React from 'react';
import { Dropdown, Menu } from '@arco-design/web-react';
import { MoreHorizontal, Pencil, RotateCcw, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { useLibraryActionMenu } from './useLibraryActionMenu';
import './libraryRowActions.css';

/** The existing detail route and reversible lifecycle owner serve the row menu. */
export function LibraryRowActions({
  item,
  disabled,
  change,
  openItem,
}: {
  item: LiteratureItem;
  disabled: boolean;
  openItem: (item: LiteratureItem) => void;
  change: (item: LiteratureItem, deleted: boolean) => void;
}) {
  const { t } = useTranslation();
  const menu = useLibraryActionMenu(disabled);
  return (
    <Dropdown
      trigger='click'
      position='br'
      popupVisible={menu.visible}
      onVisibleChange={menu.visibility}
      droplist={
        <Menu
          onKeyDownCapture={menu.escape}
          onClickMenuItem={(key, event) => {
            menu.select(event, () => {
              if (key === 'edit' && !item.deleted) openItem(item);
              else if (key === 'lifecycle') change(item, !item.deleted);
            });
          }}
        >
          <Menu.Item key='edit' disabled={disabled || item.deleted} aria-disabled={disabled || item.deleted}>
            <Pencil size={14} aria-hidden='true' />
            <span>{t('common.edit')}</span>
          </Menu.Item>
          <Menu.Item key='lifecycle' disabled={disabled} aria-disabled={disabled}>
            {item.deleted ? <RotateCcw size={14} aria-hidden='true' /> : <Trash2 size={14} aria-hidden='true' />}
            <span>
              {t(item.deleted ? 'conversation.libraryLifecycle.restore' : 'conversation.libraryLifecycle.moveToTrash')}
            </span>
          </Menu.Item>
        </Menu>
      }
    >
      <button
        ref={menu.trigger}
        onKeyDownCapture={menu.escape}
        type='button'
        className='library-row-actions__trigger'
        disabled={disabled}
        aria-label={t('conversation.libraryLifecycle.actionsFor', { title: item.title })}
        aria-haspopup='menu'
        aria-expanded={menu.visible}
      >
        <MoreHorizontal size={16} aria-hidden='true' />
      </button>
    </Dropdown>
  );
}

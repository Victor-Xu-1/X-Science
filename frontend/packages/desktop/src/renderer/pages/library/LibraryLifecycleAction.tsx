import React, { useEffect, useState } from 'react';
import { Dropdown, Menu } from '@arco-design/web-react';
import { MoreHorizontal, RotateCcw, Trash2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import './libraryLifecycle.css';

/** A single reversible action; batch and permanent deletion have no placeholder controls. */
export function LibraryLifecycleAction({
  item,
  disabled,
  change,
}: {
  item: LiteratureItem;
  disabled: boolean;
  change: (item: LiteratureItem, deleted: boolean) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  return (
    <Dropdown
      trigger='click'
      position='br'
      popupVisible={open && !disabled}
      onVisibleChange={setOpen}
      droplist={
        <Menu
          onClickMenuItem={() => {
            setOpen(false);
            if (!disabled) change(item, !item.deleted);
          }}
        >
          <Menu.Item key='lifecycle' disabled={disabled}>
            {item.deleted ? <RotateCcw size={14} aria-hidden='true' /> : <Trash2 size={14} aria-hidden='true' />}
            <span>
              {t(item.deleted ? 'conversation.libraryLifecycle.restore' : 'conversation.libraryLifecycle.moveToTrash')}
            </span>
          </Menu.Item>
        </Menu>
      }
    >
      <button
        type='button'
        className='library-lifecycle__trigger'
        disabled={disabled}
        aria-label={t('conversation.libraryLifecycle.actionsFor', { title: item.title })}
        aria-haspopup='menu'
        aria-expanded={open && !disabled}
      >
        <MoreHorizontal size={16} aria-hidden='true' />
      </button>
    </Dropdown>
  );
}

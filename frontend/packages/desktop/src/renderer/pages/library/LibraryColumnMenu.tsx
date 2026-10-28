import React, { useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import { Trigger } from '@arco-design/web-react';
import { Columns3, GripVertical, LoaderCircle, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type {
  LibraryColumnWireId as LibraryColumnId,
  LibraryColumnPreferencesWire as LibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';
import './libraryColumnMenu.css';

export type LibraryColumnMenuProps = {
  preferences: LibraryColumnPreferences;
  status: 'loading' | 'ready' | 'saving' | 'invalid' | 'read-error' | 'write-error';
  onPreferencesChange: (preferences: LibraryColumnPreferences) => void;
  onRetry: () => void;
  onReset: () => void;
};

function ColumnPanel({
  preferences,
  status,
  onPreferencesChange,
  onRetry,
  onReset,
  close,
  panelId,
}: LibraryColumnMenuProps & { close: () => void; panelId: string }) {
  const { t } = useTranslation();
  const panel = useRef<HTMLElement>(null);
  const handles = useRef(new Map<LibraryColumnId, HTMLButtonElement>());
  const focusAfterMove = useRef<LibraryColumnId | undefined>(undefined);
  const dragged = useRef<LibraryColumnId | undefined>(undefined);
  const [drop, setDrop] = useState<{ column: LibraryColumnId; before: boolean }>();
  const visibleColumns = preferences.visible as readonly LibraryColumnId[];
  const editable = status === 'ready' || status === 'saving' || status === 'write-error';
  const busy = status === 'loading' || status === 'saving';
  const errorKey =
    status === 'invalid'
      ? 'preferenceInvalid'
      : status === 'read-error'
        ? 'preferenceReadError'
        : status === 'write-error'
          ? 'preferenceWriteError'
          : undefined;
  const label = (column: LibraryColumnId) => t(`conversation.libraryTable.columns.${column}`);
  useEffect(() => {
    panel.current?.focus();
  }, []);
  useLayoutEffect(() => {
    const column = focusAfterMove.current;
    if (column) {
      handles.current.get(column)?.focus();
      focusAfterMove.current = undefined;
    }
  }, [preferences.order]);
  const move = (source: LibraryColumnId, target: LibraryColumnId, before: boolean) => {
    if (!editable || source === target) return;
    const order = preferences.order.filter((column) => column !== source);
    const targetIndex = order.indexOf(target);
    if (targetIndex < 0) return;
    order.splice(targetIndex + (before ? 0 : 1), 0, source);
    if (order.every((column, index) => column === preferences.order[index])) return;
    focusAfterMove.current = source;
    onPreferencesChange({ ...preferences, order } as LibraryColumnPreferences);
  };
  const stopDrag = () => {
    dragged.current = undefined;
    setDrop(undefined);
  };
  return (
    <section
      ref={panel}
      id={panelId}
      role='dialog'
      aria-modal='false'
      aria-labelledby={`${panelId}-title`}
      aria-describedby={`${panelId}-help`}
      aria-busy={busy}
      className='library-column-menu'
      tabIndex={-1}
      onKeyDown={(event) => {
        if (event.key === 'Escape') {
          event.preventDefault();
          event.stopPropagation();
          close();
        }
      }}
    >
      <header className='library-column-menu__heading'>
        <h2 id={`${panelId}-title`}>{t('conversation.libraryTable.customize')}</h2>
        <button
          type='button'
          className='library-column-menu__icon'
          onClick={close}
          aria-label={t('conversation.libraryTable.close')}
        >
          <X size={16} aria-hidden='true' />
        </button>
      </header>
      <p id={`${panelId}-help`} className='library-column-menu__help'>
        {t('conversation.libraryTable.columnHelp')}
      </p>
      {busy && (
        <p role='status' className='library-column-menu__message'>
          <LoaderCircle size={14} className='library-column-menu__spin' aria-hidden='true' />
          {t(
            status === 'loading'
              ? 'conversation.libraryTable.preferenceLoading'
              : 'conversation.libraryTable.preferenceSaving'
          )}
        </p>
      )}
      {errorKey && (
        <div role='alert' className='library-column-menu__message'>
          <span>{t(`conversation.libraryTable.${errorKey}`)}</span>
          <button type='button' onClick={onRetry}>
            {t('common.retry')}
          </button>
        </div>
      )}
      <ul className='library-column-menu__columns'>
        {preferences.order.map((column, index) => (
          <li
            key={column}
            data-column={column}
            data-drop={drop?.column === column ? (drop.before ? 'before' : 'after') : undefined}
            onDragOver={(event) => {
              if (!editable || !dragged.current || dragged.current === column) return;
              event.preventDefault();
              event.dataTransfer.dropEffect = 'move';
              const bounds = event.currentTarget.getBoundingClientRect();
              setDrop({ column, before: event.clientY < bounds.top + bounds.height / 2 });
            }}
            onDrop={(event) => {
              const source = dragged.current;
              if (!editable || !source) return;
              event.preventDefault();
              const bounds = event.currentTarget.getBoundingClientRect();
              move(source, column, event.clientY < bounds.top + bounds.height / 2);
              stopDrag();
            }}
          >
            <button
              type='button'
              className='library-column-menu__move'
              disabled={!editable}
              draggable={editable}
              ref={(element) => {
                if (element) handles.current.set(column, element);
                else handles.current.delete(column);
              }}
              aria-label={t('conversation.libraryTable.moveColumn', { column: label(column) })}
              onKeyDown={(event) => {
                if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
                event.preventDefault();
                const target = preferences.order[index + (event.key === 'ArrowUp' ? -1 : 1)];
                if (target) move(column, target, event.key === 'ArrowUp');
              }}
              onDragStart={(event) => {
                if (!editable) {
                  event.preventDefault();
                  return;
                }
                dragged.current = column;
                event.dataTransfer.effectAllowed = 'move';
                event.dataTransfer.setData('text/plain', column);
              }}
              onDragEnd={stopDrag}
            >
              <GripVertical size={14} aria-hidden='true' />
            </button>
            <label className='library-column-menu__visibility'>
              <span>{label(column)}</span>
              <input
                type='checkbox'
                checked={visibleColumns.includes(column)}
                disabled={!editable}
                aria-label={t('conversation.libraryTable.showColumn', { column: label(column) })}
                onChange={(event) => {
                  if (!editable) return;
                  const visible = event.currentTarget.checked
                    ? [...visibleColumns, column]
                    : visibleColumns.filter((value) => value !== column);
                  onPreferencesChange({ ...preferences, visible } as LibraryColumnPreferences);
                }}
              />
            </label>
          </li>
        ))}
      </ul>
      <footer className='library-column-menu__footer'>
        <button type='button' disabled={busy} onClick={onReset}>
          {t('conversation.libraryTable.reset')}
        </button>
      </footer>
    </section>
  );
}

/** Controlled preference presentation. The caller alone loads, saves and reconciles the layout. */
export function LibraryColumnMenu(props: LibraryColumnMenuProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  const close = () => {
    setOpen(false);
    trigger.current?.focus();
  };
  return (
    <Trigger
      trigger='click'
      position='br'
      popupVisible={open}
      onVisibleChange={setOpen}
      popupAlign={{ top: 6 }}
      unmountOnExit
      popup={() => <ColumnPanel {...props} panelId={id} close={close} />}
    >
      <button
        ref={trigger}
        type='button'
        className='library-column-menu-trigger'
        aria-haspopup='dialog'
        aria-expanded={open}
        aria-controls={open ? id : undefined}
      >
        <Columns3 size={14} aria-hidden='true' />
        {t('conversation.libraryTable.columnsButton')}
      </button>
    </Trigger>
  );
}

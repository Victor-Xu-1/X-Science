import React, { useCallback, useId, useLayoutEffect, useRef, useState } from 'react';
import {
  autoUpdate,
  flip,
  FloatingFocusManager,
  FloatingPortal,
  offset,
  shift,
  size,
  useClick,
  useDismiss,
  useFloating,
  useInteractions,
  useRole,
} from '@floating-ui/react';
import { Columns3, Eye, EyeOff, GripVertical, LoaderCircle, X } from 'lucide-react';
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

const canEditColumns = (status: LibraryColumnMenuProps['status']) =>
  status === 'ready' || status === 'saving' || status === 'write-error';

function ColumnFeedback({ status, onRetry }: Pick<LibraryColumnMenuProps, 'status' | 'onRetry'>) {
  const { t } = useTranslation();
  const errorKey =
    status === 'invalid'
      ? 'preferenceInvalid'
      : status === 'read-error'
        ? 'preferenceReadError'
        : status === 'write-error'
          ? 'preferenceWriteError'
          : undefined;
  const busy = status === 'loading' || status === 'saving';
  const message = errorKey
    ? t(`conversation.libraryTable.${errorKey}`)
    : busy
      ? t(
          status === 'loading'
            ? 'conversation.libraryTable.preferenceLoading'
            : 'conversation.libraryTable.preferenceSaving'
        )
      : '';
  return (
    <div
      className='library-column-menu__message'
      role={errorKey ? 'alert' : busy ? 'status' : undefined}
      aria-live={errorKey ? 'assertive' : 'polite'}
      aria-atomic='true'
    >
      {busy && <LoaderCircle size={14} className='library-column-menu__spin' aria-hidden='true' />}
      <span title={message}>{message}</span>
      {errorKey && (
        <button type='button' onClick={onRetry}>
          {t('common.retry')}
        </button>
      )}
    </div>
  );
}

function ColumnPanel({
  preferences,
  status,
  onPreferencesChange,
  onRetry,
  onReset,
  close,
  panelId,
  floatingRef,
  floatingStyles,
  getFloatingProps,
  firstHandle,
}: LibraryColumnMenuProps & {
  close: () => void;
  panelId: string;
  floatingRef: (element: HTMLElement | null) => void;
  floatingStyles: React.CSSProperties;
  getFloatingProps: ReturnType<typeof useInteractions>['getFloatingProps'];
  firstHandle: React.MutableRefObject<HTMLButtonElement | null>;
}) {
  const { t } = useTranslation();
  const panel = useRef<HTMLElement | null>(null);
  const setPanelRef = useCallback(
    (element: HTMLElement | null) => {
      panel.current = element;
      floatingRef(element);
    },
    [floatingRef]
  );
  const handles = useRef(new Map<LibraryColumnId, HTMLButtonElement>());
  const focusAfterMove = useRef<LibraryColumnId | undefined>(undefined);
  const [dragged, setDragged] = useState<LibraryColumnId>();
  const [drop, setDrop] = useState<{ column: LibraryColumnId; before: boolean }>();
  const visibleColumns = preferences.visible as readonly LibraryColumnId[];
  const editable = canEditColumns(status);
  const busy = status === 'loading' || status === 'saving';
  const label = (column: LibraryColumnId) => t(`conversation.libraryTable.columns.${column}`);
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
    setDragged(undefined);
    setDrop(undefined);
  };
  const retainActionFocus = (action: () => void) => () => {
    const element = panel.current;
    if (element?.contains(element.ownerDocument.activeElement)) element.focus({ preventScroll: true });
    action();
  };
  return (
    <section
      ref={setPanelRef}
      {...getFloatingProps()}
      id={panelId}
      role='dialog'
      aria-modal='false'
      aria-labelledby={panelId + '-title'}
      aria-describedby={panelId + '-help'}
      aria-busy={busy}
      className='library-column-menu'
      style={floatingStyles}
      tabIndex={-1}
    >
      <header className='library-column-menu__heading'>
        <h2 id={panelId + '-title'}>{t('conversation.libraryTable.customize')}</h2>
        <button
          type='button'
          className='library-column-menu__icon'
          onClick={close}
          aria-label={t('conversation.libraryTable.close')}
        >
          <X size={16} aria-hidden='true' />
        </button>
      </header>
      <p id={panelId + '-help'} className='library-column-menu__help'>
        {t('conversation.libraryTable.columnHelp')}
      </p>
      <ul className='library-column-menu__columns'>
        {preferences.order.map((column, index) => {
          const checked = visibleColumns.includes(column);
          const visibilityLabel = t(
            checked ? 'conversation.libraryTable.hideColumn' : 'conversation.libraryTable.showColumn',
            { column: label(column) }
          );
          return (
            <li
              key={column}
              data-column={column}
              data-dragging={dragged === column ? true : undefined}
              data-drop={drop?.column === column ? (drop.before ? 'before' : 'after') : undefined}
              draggable={editable}
              onDragStart={(event) => {
                if (!editable) {
                  event.preventDefault();
                  return;
                }
                setDragged(column);
                event.dataTransfer.effectAllowed = 'move';
                event.dataTransfer.setData('text/plain', column);
              }}
              onDragEnd={stopDrag}
              onDragOver={(event) => {
                if (!editable || !dragged || dragged === column) return;
                event.preventDefault();
                event.dataTransfer.dropEffect = 'move';
                const bounds = event.currentTarget.getBoundingClientRect();
                setDrop({ column, before: event.clientY < bounds.top + bounds.height / 2 });
              }}
              onDrop={(event) => {
                if (!editable || !dragged) return;
                event.preventDefault();
                const bounds = event.currentTarget.getBoundingClientRect();
                move(dragged, column, event.clientY < bounds.top + bounds.height / 2);
                stopDrag();
              }}
            >
              <button
                type='button'
                className='library-column-menu__move'
                disabled={!editable}
                ref={(element) => {
                  if (element) handles.current.set(column, element);
                  else handles.current.delete(column);
                  if (index === 0) firstHandle.current = element;
                }}
                aria-label={t('conversation.libraryTable.moveColumn', { column: label(column) })}
                onKeyDown={(event) => {
                  if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return;
                  event.preventDefault();
                  const target = preferences.order[index + (event.key === 'ArrowUp' ? -1 : 1)];
                  if (target) move(column, target, event.key === 'ArrowUp');
                }}
              >
                <GripVertical size={16} aria-hidden='true' />
              </button>
              <span className='library-column-menu__label'>{label(column)}</span>
              <button
                type='button'
                className='library-column-menu__visibility'
                disabled={!editable}
                aria-label={visibilityLabel}
                aria-pressed={checked}
                title={visibilityLabel}
                onClick={() => {
                  if (!editable) return;
                  const visible = checked
                    ? visibleColumns.filter((value) => value !== column)
                    : [...visibleColumns, column];
                  onPreferencesChange({ ...preferences, visible } as LibraryColumnPreferences);
                }}
              >
                {checked ? <Eye size={16} aria-hidden='true' /> : <EyeOff size={16} aria-hidden='true' />}
              </button>
            </li>
          );
        })}
      </ul>
      <footer className='library-column-menu__footer'>
        <ColumnFeedback status={status} onRetry={retainActionFocus(onRetry)} />
        <button
          type='button'
          disabled={busy}
          onClick={retainActionFocus(onReset)}
          aria-label={t('conversation.libraryTable.reset')}
          title={t('conversation.libraryTable.reset')}
        >
          {t('conversation.libraryTable.resetShort')}
        </button>
      </footer>
    </section>
  );
}

/** The existing caller alone loads, saves and reconciles the column preferences. */
export function LibraryColumnMenu(props: LibraryColumnMenuProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const firstHandle = useRef<HTMLButtonElement | null>(null);
  const id = useId();
  const { refs, floatingStyles, context, isPositioned } = useFloating({
    open,
    onOpenChange: setOpen,
    placement: 'bottom-end',
    strategy: 'fixed',
    whileElementsMounted: autoUpdate,
    middleware: [
      offset(4),
      flip({ padding: 8 }),
      shift({ padding: 8 }),
      size({
        padding: 8,
        apply({ availableHeight, elements }: { availableHeight: number; elements: { floating: HTMLElement } }) {
          elements.floating.style.setProperty('--library-column-available-height', Math.max(0, availableHeight) + 'px');
        },
      }),
    ],
  });
  const click = useClick(context);
  const dismiss = useDismiss(context);
  const role = useRole(context, { role: 'dialog' });
  const { getReferenceProps, getFloatingProps } = useInteractions([click, dismiss, role]);
  return (
    <>
      <button
        ref={refs.setReference}
        {...getReferenceProps()}
        type='button'
        className='library-column-menu-trigger'
        aria-label={t('conversation.libraryTable.customize')}
        title={t('conversation.libraryTable.customize')}
        aria-haspopup='dialog'
        aria-expanded={open}
        aria-controls={open ? id : undefined}
      >
        <Columns3 size={16} aria-hidden='true' />
      </button>
      {open && (
        <FloatingPortal>
          <FloatingFocusManager
            context={context}
            modal={false}
            disabled={!isPositioned}
            initialFocus={canEditColumns(props.status) ? firstHandle : 0}
            restoreFocus
          >
            <ColumnPanel
              {...props}
              panelId={id}
              close={() => setOpen(false)}
              floatingRef={refs.setFloating}
              floatingStyles={{ ...floatingStyles, visibility: isPositioned ? 'visible' : 'hidden' }}
              getFloatingProps={getFloatingProps}
              firstHandle={firstHandle}
            />
          </FloatingFocusManager>
        </FloatingPortal>
      )}
    </>
  );
}

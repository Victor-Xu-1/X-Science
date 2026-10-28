import React, { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import type {
  LibraryEditorDraftState,
  RegisterLibraryEditorDraft,
} from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { focusableElements } from '@/renderer/utils/focusScope';

type Draft = { owner: object; state?: LibraryEditorDraftState };
type Exit = {
  owner: object;
  action: () => void;
  focus: HTMLElement | null;
  root: HTMLElement | null;
  opener: Element | null;
  fallback?: HTMLElement;
};

/** Presentation-only exit intent. Draft and durable mutation ownership remain upstream. */
export function useLibraryDetailExit(account: string, pendingOperationId: string | undefined, close: () => void) {
  const { t } = useTranslation();
  const disposed = useRef(false);
  const draft = useRef<Draft | undefined>(undefined);
  const activeExit = useRef<Exit | undefined>(undefined);
  const returnFocus = useRef<Exit | undefined>(undefined);
  const closeCurrent = useRef(close);
  const pending = useRef(pendingOperationId);
  closeCurrent.current = close;
  pending.current = pendingOperationId;
  const [confirmation, setConfirmation] = useState<{ request: Exit; open: boolean }>();
  const cancelButton = useRef<HTMLButtonElement>(null);
  const current = useCallback(() => !disposed.current && account === getRendererAccountScopeToken(), [account]);
  useEffect(() => {
    disposed.current = false;
    return () => {
      disposed.current = true;
      activeExit.current = undefined;
      draft.current = undefined;
      returnFocus.current = undefined;
    };
  }, []);
  const registerDraft = useCallback<RegisterLibraryEditorDraft>(
    (owner) => {
      if (account !== getRendererAccountScopeToken()) return () => {};
      draft.current = { owner };
      return (state) => {
        if (draft.current?.owner !== owner || account !== getRendererAccountScopeToken()) return;
        if (state && !state.root?.isConnected) return;
        if (state) draft.current = { owner, state };
        else draft.current = undefined;
        if ((!state || !state.dirty) && activeExit.current?.owner === owner) {
          activeExit.current = undefined;
          if (current()) setConfirmation(undefined);
        }
      };
    },
    [account, current]
  );
  const requestExit = useCallback(
    (action: () => void) => {
      if (!current()) return;
      const observed = draft.current;
      const state = observed?.state;
      const ownSavedPending = state?.submittedOperationId && state.submittedOperationId === pending.current;
      if (!observed || !state?.dirty || ownSavedPending) {
        action();
        return;
      }
      const parent = state.root?.closest<HTMLElement>('[role="dialog"]') ?? null;
      const request = {
        owner: observed.owner,
        action,
        focus: state.focus,
        root: state.root,
        opener: document.activeElement,
        fallback: focusableElements(parent).find((element) => !element.hasAttribute('data-focus-guard')),
      };
      activeExit.current = request;
      returnFocus.current = undefined;
      setConfirmation({ request, open: true });
    },
    [current]
  );
  const requestClose = useCallback(() => requestExit(() => closeCurrent.current()), [requestExit]);
  const take = (request: Exit) => {
    if (!current() || activeExit.current !== request || draft.current?.owner !== request.owner) return false;
    activeExit.current = undefined;
    setConfirmation((previous) => (previous?.request === request ? { request, open: false } : previous));
    return true;
  };
  const keepEditing = (request: Exit) => {
    if (take(request)) returnFocus.current = request;
  };
  const discard = (request: Exit) => {
    if (!take(request)) return;
    returnFocus.current = undefined;
    request.action(); // This does not cancel or discard a durable operation.
  };
  const restore = useCallback(
    (request: Exit) => {
      if (returnFocus.current !== request) return;
      returnFocus.current = undefined;
      if (!current() || draft.current?.owner !== request.owner) return;
      const active = document.activeElement;
      if (
        active &&
        active !== document.body &&
        active !== request.opener &&
        active !== request.fallback &&
        !active.closest('[role="alertdialog"]')
      )
        return;
      const target = request.focus?.isConnected && !request.focus.matches(':disabled') ? request.focus : request.root;
      if (target?.isConnected) target.focus({ preventScroll: true });
    },
    [current]
  );
  const request = confirmation?.request;
  const dialog = request && (
    <WorkbenchModal
      visible={Boolean(confirmation?.open)}
      footer={null}
      closable={false}
      focusLock
      escToExit={false}
      unmountOnExit
      className='library-workbench__detail-dialog library-workbench__discard-dialog'
      maskStyle={{ backgroundColor: 'transparent' }}
      style={{ width: 420, maxWidth: 'calc(100vw - 32px)' }}
      onCancel={() => keepEditing(request)}
      afterOpen={() => {
        if (current() && activeExit.current === request) cancelButton.current?.focus();
      }}
      afterClose={() => {
        restore(request);
        setConfirmation((previous) => (previous?.request === request ? undefined : previous));
      }}
      modalRender={(node) =>
        React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
          ? React.cloneElement(node, {
              role: 'alertdialog',
              'aria-label': t('conversation.libraryWorkbench.discardTitle'),
              onKeyDown: (event) => {
                if (
                  event.key !== 'Escape' ||
                  event.defaultPrevented ||
                  event.nativeEvent.isComposing ||
                  event.nativeEvent.keyCode === 229
                )
                  return;
                event.preventDefault();
                event.stopPropagation();
                keepEditing(request);
              },
            })
          : node
      }
    >
      <h2>{t('conversation.libraryWorkbench.discardTitle')}</h2>
      <p>{t('conversation.libraryWorkbench.discardDescription')}</p>
      <div className='pdf-library-editor__buttons'>
        <button ref={cancelButton} type='button' onClick={() => keepEditing(request)}>
          {t('conversation.libraryWorkbench.keepEditing')}
        </button>
        <button type='button' onClick={() => discard(request)}>
          {t('conversation.libraryWorkbench.discardChanges')}
        </button>
      </div>
    </WorkbenchModal>
  );
  return { registerDraft, requestExit, close: requestClose, dialog };
}

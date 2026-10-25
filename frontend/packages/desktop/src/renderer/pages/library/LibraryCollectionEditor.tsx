import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import {
  normalizeLiteratureCollectionText,
  type LiteratureCollection,
  type LiteratureCollectionPage,
  type LiteratureCollectionCommand,
  type LiteratureCollectionReceipt,
} from '@/common/adapter/literatureCollections';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { LibraryReadView } from './useLibraryWorkbench';
import { libraryCollectionRows } from './LibraryCollectionSidebar';

export type LibraryCollectionEditorMode =
  | { kind: 'create'; parentId?: string }
  | { kind: 'update' | 'delete'; collection: LiteratureCollection };

type Props = {
  mode: LibraryCollectionEditorMode;
  account: string;
  collections: LibraryReadView<LiteratureCollectionPage>;
  busy: boolean;
  mutating: boolean;
  pendingOperationId?: string;
  receipt?: LiteratureReceipt | LiteratureCollectionReceipt;
  submit: (command: LiteratureCollectionCommand) => Promise<LiteratureCollectionReceipt>;
  retry: () => Promise<unknown>;
  refresh: () => void;
  close: () => void;
  committed: (receipt: LiteratureCollectionReceipt) => void;
  editChild: (collection: LiteratureCollection) => void;
};

/** Form state is local. The original account-wide mutation owner alone owns writes/retries. */
export function LibraryCollectionEditor({
  mode,
  account,
  collections,
  busy,
  mutating,
  pendingOperationId,
  receipt,
  submit,
  retry,
  refresh,
  close,
  committed,
  editChild,
}: Props) {
  const { t } = useTranslation();
  const titleId = useId();
  const panel = useRef<HTMLElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const alive = useRef(true);
  const latch = useRef(false);
  const operation = useRef<string | undefined>(undefined);
  const accepted = useRef<string | undefined>(undefined);
  const [original, setOriginal] = useState(mode.kind === 'create' ? undefined : mode.collection);
  const [name, setName] = useState(original?.name ?? '');
  const [description, setDescription] = useState(original?.description ?? '');
  const [parentId, setParentId] = useState(mode.kind === 'create' ? (mode.parentId ?? '') : '');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<'name' | 'conflict' | 'failed'>();
  const ownsPending = Boolean(operation.current && operation.current === pendingOperationId);
  const fieldsLocked = saving || ownsPending;
  const closeBlocked = saving || (ownsPending && mutating);
  const awaitingConfirmation = ownsPending && !closeBlocked;
  const values = collections.status === 'ready' ? (collections.value?.collections ?? []) : [];
  const latest = original && values.find((value) => value.id === original.id);
  const missing = Boolean(original && collections.status === 'ready' && !latest);
  const changed = mode.kind === 'update' && Boolean(latest && latest.revision !== original?.revision);
  const conflict = changed || error === 'conflict' || missing;
  const parentMissing = mode.kind === 'create' && parentId && !values.some((value) => value.id === parentId);
  const children = mode.kind === 'delete' ? values.filter((value) => value.parent_id === original?.id) : [];
  const rootNames = new Set(
    values
      .filter((value) => value.parent_id === null && value.id !== original?.id)
      .map((value) => value.name.toLowerCase())
  );
  const collisions = children.filter((value) => rootNames.has(value.name.toLowerCase()));
  let normalized: { name: string; description: string } | undefined;
  try {
    normalized = {
      name: normalizeLiteratureCollectionText(name),
      description: normalizeLiteratureCollectionText(description, true),
    };
  } catch {
    /* The shared contract owns text validity. */
  }
  const current = useCallback(() => alive.current && account === getRendererAccountScopeToken(), [account]);
  const accept = useCallback(
    (value: LiteratureCollectionReceipt) => {
      if (
        !current() ||
        !operation.current ||
        value.operation_id !== operation.current ||
        accepted.current === value.operation_id
      )
        return;
      accepted.current = value.operation_id;
      committed(value);
    },
    [committed, current]
  );
  useEffect(() => {
    alive.current = true;
    const previous = document.activeElement;
    (input.current ?? panel.current)?.focus();
    return () => {
      alive.current = false;
      if (account === getRendererAccountScopeToken() && previous instanceof HTMLElement && previous.isConnected)
        previous.focus();
    };
  }, [account]);
  useEffect(() => {
    if (receipt?.schema === 'x-science.literature-collection-receipt.v1') accept(receipt);
  }, [receipt, accept]);
  const save = async () => {
    if (latch.current || busy || fieldsLocked || collections.status !== 'ready' || !current()) return;
    if (mode.kind === 'delete' ? missing || collisions.length > 0 : !normalized || conflict || parentMissing) return;
    const operationId = crypto.randomUUID();
    const command: LiteratureCollectionCommand =
      mode.kind === 'create'
        ? {
            kind: 'collection-create',
            operation_id: operationId,
            ...normalized!,
            ...(parentId ? { parent_id: parentId } : {}),
          }
        : mode.kind === 'update'
          ? {
              kind: 'collection-update',
              operation_id: operationId,
              collection_id: original!.id,
              expected_revision: original!.revision,
              ...normalized!,
            }
          : { kind: 'collection-delete', operation_id: operationId, collection_id: original!.id };
    operation.current = operationId;
    latch.current = true;
    setSaving(true);
    setError(undefined);
    try {
      accept(await submit(command));
    } catch (reason) {
      if (!current()) return;
      if (isBackendHttpError(reason) && reason.code === 'LITERATURE_COLLECTION_NAME_CONFLICT') setError('name');
      else if (isBackendHttpError(reason) && reason.status === 409 && mode.kind === 'update') {
        setError('conflict');
        refresh();
      } else setError('failed');
    } finally {
      latch.current = false;
      if (current()) setSaving(false);
    }
  };
  const retryOriginal = async () => {
    if (latch.current || mutating || !current()) return;
    latch.current = true;
    setSaving(true);
    try {
      await retry();
    } catch {
      if (current()) setError('failed');
    } finally {
      latch.current = false;
      if (current()) setSaving(false);
    }
  };
  const closeCurrent = () => {
    if (!closeBlocked && !latch.current) close();
  };
  const adoptLatest = () => {
    if (!latest || fieldsLocked) return;
    setOriginal(latest);
    setName(latest.name);
    setDescription(latest.description);
    setError(undefined);
  };
  const title =
    mode.kind === 'create' ? 'newCollection' : mode.kind === 'delete' ? 'deleteCollection' : 'editCollection';
  if (account !== getRendererAccountScopeToken()) return null;
  return createPortal(
    <div
      className='library-workbench__collection-layer'
      onPointerDown={(event) => {
        if (event.target === event.currentTarget) closeCurrent();
      }}
    >
      <section
        ref={panel}
        tabIndex={-1}
        role='dialog'
        aria-modal='true'
        aria-labelledby={titleId}
        className='library-workbench__collection-dialog'
        data-kind={mode.kind}
        onKeyDown={(event) => {
          if (event.key === 'Escape') {
            event.stopPropagation();
            event.preventDefault();
            closeCurrent();
          }
          if (event.key === 'Tab') {
            const targets = panel.current?.querySelectorAll<HTMLElement>(
              'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled])'
            );
            const first = targets?.[0],
              last = targets?.[targets.length - 1];
            if (!first) {
              event.preventDefault();
              panel.current?.focus();
            } else if (
              event.shiftKey &&
              (document.activeElement === first || document.activeElement === panel.current)
            ) {
              event.preventDefault();
              last?.focus();
            } else if (!event.shiftKey && document.activeElement === last) {
              event.preventDefault();
              first.focus();
            }
          }
        }}
      >
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void save();
          }}
        >
          <div className='library-workbench__detail-heading'>
            <h2 id={titleId}>{t(`conversation.libraryWorkbench.${title}`)}</h2>
            <button
              type='button'
              className='library-workbench__icon'
              aria-label={t('common.close')}
              disabled={closeBlocked}
              onClick={closeCurrent}
            >
              <X size={16} aria-hidden='true' />
            </button>
          </div>
          {mode.kind === 'delete' ? (
            <>
              <p>
                {t('conversation.libraryWorkbench.deleteCollectionQuestion', { name: latest?.name ?? original?.name })}
              </p>
              <p>{t('conversation.libraryWorkbench.deleteCollectionHint')}</p>
              {children.length > 0 && (
                <div>
                  <p>{t('conversation.libraryWorkbench.promoteChildren')}</p>
                  <ul>
                    {children.map((child) => (
                      <li key={child.id}>
                        {child.name}
                        {collisions.some((value) => value.id === child.id) && (
                          <button type='button' disabled={fieldsLocked} onClick={() => editChild(child)}>
                            {t('conversation.libraryWorkbench.renameChild')}
                          </button>
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
              )}
              {(collisions.length > 0 || error === 'name') && (
                <p role='alert'>{t('conversation.libraryWorkbench.collectionDeleteConflict')}</p>
              )}
            </>
          ) : (
            <>
              <label>
                {t('conversation.libraryWorkbench.collectionName')}
                <input
                  ref={input}
                  value={name}
                  maxLength={200}
                  disabled={fieldsLocked}
                  onChange={(event) => {
                    setName(event.target.value);
                    setError((value) => (value === 'conflict' ? value : undefined));
                  }}
                  required
                />
              </label>
              <label>
                {t('conversation.libraryWorkbench.collectionDescription')}
                <textarea
                  value={description}
                  maxLength={1000}
                  rows={3}
                  disabled={fieldsLocked}
                  onChange={(event) => {
                    setDescription(event.target.value);
                    setError((value) => (value === 'conflict' ? value : undefined));
                  }}
                />
              </label>
              {mode.kind === 'create' && (
                <label>
                  {t('conversation.libraryWorkbench.collectionParent')}
                  <select
                    value={parentId}
                    disabled={fieldsLocked || collections.status !== 'ready'}
                    onChange={(event) => setParentId(event.target.value)}
                  >
                    <option value=''>{t('conversation.libraryWorkbench.collectionTopLevel')}</option>
                    {parentMissing && (
                      <option value={parentId} disabled>
                        {t('conversation.libraryWorkbench.collectionUnavailable')}
                      </option>
                    )}
                    {libraryCollectionRows(values).map(({ collection, path }) => (
                      <option key={collection.id} value={collection.id}>
                        {path}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {name.trim() && !normalized && <p role='alert'>{t('conversation.libraryWorkbench.collectionLimits')}</p>}
              {error === 'name' && <p role='alert'>{t('conversation.libraryWorkbench.collectionNameConflict')}</p>}
            </>
          )}
          {collections.status !== 'ready' && (
            <div role={collections.status === 'error' ? 'alert' : 'status'}>
              <p>
                {t(
                  collections.status === 'error' ? 'conversation.libraryWorkbench.collectionsFailed' : 'common.loading'
                )}
              </p>
              {collections.status === 'error' && (
                <button type='button' onClick={refresh}>
                  {t('common.retry')}
                </button>
              )}
            </div>
          )}
          {(missing || parentMissing) && <p role='alert'>{t('conversation.libraryWorkbench.collectionUnavailable')}</p>}
          {conflict && !missing && mode.kind === 'update' && (
            <div role='alert'>
              <p>{t('conversation.libraryWorkbench.collectionChanged')}</p>
              {latest && (
                <>
                  <p>{latest.name}</p>
                  <button type='button' disabled={fieldsLocked} onClick={adoptLatest}>
                    {t('conversation.libraryWorkbench.loadLatestCollection')}
                  </button>
                </>
              )}
              <button type='button' disabled={fieldsLocked} onClick={refresh}>
                {t('common.refresh')}
              </button>
            </div>
          )}
          {error === 'failed' && <p role='alert'>{t('conversation.libraryWorkbench.collectionSaveFailed')}</p>}
          {awaitingConfirmation && <p role='status'>{t('conversation.libraryWorkbench.collectionPendingNotice')}</p>}
          {busy && !mutating && !saving && (
            <button type='button' onClick={() => void retryOriginal()}>
              {t('conversation.libraryWorkbench.retryPendingCollection')}
            </button>
          )}
          <div className='library-workbench__collection-footer'>
            <button type='button' disabled={closeBlocked} onClick={closeCurrent}>
              {t(awaitingConfirmation ? 'common.close' : 'common.cancel')}
            </button>
            {!awaitingConfirmation && (
              <button
                type='submit'
                aria-busy={saving || (ownsPending && mutating)}
                disabled={
                  busy ||
                  fieldsLocked ||
                  collections.status !== 'ready' ||
                  (mode.kind === 'delete'
                    ? missing || collisions.length > 0
                    : !normalized || conflict || Boolean(parentMissing))
                }
              >
                {saving || (ownsPending && mutating)
                  ? t('conversation.libraryWorkbench.collectionSaving')
                  : t(
                      `conversation.libraryWorkbench.${mode.kind === 'create' ? 'createCollection' : mode.kind === 'delete' ? 'deleteCollection' : 'saveCollection'}`
                    )}
              </button>
            )}
          </div>
        </form>
      </section>
    </div>,
    document.body
  );
}

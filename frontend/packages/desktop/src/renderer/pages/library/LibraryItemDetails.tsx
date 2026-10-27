import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { Pencil, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand, LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import '@/renderer/components/chat/SendBox/reading/readingContext.css';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import type {
  LiteratureCollectionCommand,
  LiteratureCollectionReceipt,
  LiteratureCollectionPage,
  LiteratureCollectionMembership,
} from '@/common/adapter/literatureCollections';
import type { LibraryReadView } from './useLibraryWorkbench';
import { LibraryCollectionMembershipPicker } from './LibraryCollectionMembership';
import { LibraryAttachmentBrowser } from './LibraryAttachmentBrowser';
import { LibraryPublicationCheck } from './LibraryPublicationCheck';
import { LibraryReadingStatus } from './LibraryReadingStatus';
import { literatureItemReadingStatus } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

export const libraryAuthors = (item: LiteratureItem): string => {
  const value = item.metadata.authors;
  return typeof value === 'string'
    ? value
    : Array.isArray(value)
      ? value.filter((author): author is string => typeof author === 'string').join('; ')
      : '';
};

export function LibraryItemDetails({
  item,
  account,
  collections,
  memberships,
  submitCollection,
  refreshCollections,
  projects,
  projectStatus,
  busy,
  stale = false,
  changeReadingStatus,
  receipt,
  submit,
  close,
}: {
  item: LiteratureItem;
  account: string;
  collections: LibraryReadView<LiteratureCollectionPage>;
  memberships: LibraryReadView<LiteratureCollectionMembership | undefined>;
  submitCollection: (command: LiteratureCollectionCommand) => Promise<LiteratureCollectionReceipt>;
  refreshCollections: () => void;
  projects: XScienceProject[];
  projectStatus: 'loading' | 'ready' | 'error';
  busy: boolean;
  stale?: boolean;
  changeReadingStatus: (value: LiteratureReadingStatus) => void;
  receipt?: LiteratureReceipt;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  close: () => void;
}) {
  const { t } = useTranslation();
  const [editBase, setEditBase] = useState<LiteratureItem>();
  const [projectId, setProjectId] = useState('');
  const editorId = useId();
  const primaryHeader = useRef<HTMLDivElement>(null);
  const editTrigger = useRef<HTMLButtonElement>(null);
  const editorPanel = useRef<HTMLDivElement>(null);
  const scope = JSON.stringify([account, item.id]);
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const focusRequest = useRef<{ scope: string; target: 'editor' | 'trigger'; origin?: HTMLElement } | undefined>(
    undefined
  );
  const current = useCallback(
    () => scopeRef.current === scope && account === getRendererAccountScopeToken(),
    [account, scope]
  );
  const revealEditor = useCallback(() => {
    const panel = editorPanel.current;
    if (!current() || !panel?.isConnected) return;
    const input = panel.querySelector<HTMLInputElement>('input:not(:disabled):not([readonly]):not([type="hidden"])');
    const target = input ?? panel;
    const headerHeight = primaryHeader.current?.getBoundingClientRect().height ?? 0;
    if (Number.isFinite(headerHeight) && headerHeight > 0)
      target.style.scrollMarginBlockStart = `calc(${headerHeight}px + var(--ui-space-6))`;
    else target.style.removeProperty('scroll-margin-block-start');
    target.focus({ preventScroll: true });
    target.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
  }, [current]);
  useEffect(() => {
    const request = focusRequest.current;
    if (!request) return;
    if (request.scope !== scope || !current()) {
      focusRequest.current = undefined;
      return;
    }
    if (request.target === 'editor' && editBase) {
      focusRequest.current = undefined;
      revealEditor();
    } else if (request.target === 'trigger' && !editBase && !busy) {
      const focused = document.activeElement;
      if (focused?.isConnected && focused !== document.body && !request.origin?.contains(focused)) {
        focusRequest.current = undefined;
        return;
      }
      const trigger = editTrigger.current;
      if (!trigger?.isConnected || trigger.disabled) return;
      focusRequest.current = undefined;
      trigger.focus({ preventScroll: true });
    }
  }, [busy, current, editBase, revealEditor, scope]);
  useEffect(
    () => () => {
      focusRequest.current = undefined;
    },
    []
  );
  const openEditor = () => {
    if (busy || !current()) return;
    if (editBase) revealEditor();
    else {
      focusRequest.current = { scope, target: 'editor' };
      setEditBase(item);
    }
  };
  const closeEditor = useCallback(() => {
    if (!current()) return;
    focusRequest.current = { scope, target: 'trigger', origin: editorPanel.current ?? undefined };
    setEditBase(undefined);
  }, [current, scope]);
  const authors = libraryAuthors(item);
  const year =
    typeof item.metadata.year === 'string' || Number.isSafeInteger(item.metadata.year)
      ? String(item.metadata.year)
      : '';
  const members = projects.filter((project) => item.project_ids.includes(project.projectId));
  const externallyUpdated = editBase !== undefined && editBase.metadata_revision !== item.metadata_revision;
  return (
    <section className='library-workbench__detail' aria-label={t('conversation.libraryWorkbench.details')}>
      <div
        ref={primaryHeader}
        className='library-workbench__detail-main-header'
        role='group'
        aria-label={t('conversation.libraryWorkbench.details')}
      >
        <div className='library-workbench__detail-main-title'>
          <span className='library-workbench__eyebrow'>{t('conversation.libraryWorkbench.details')}</span>
          <h2 title={item.title}>{item.title}</h2>
        </div>
        <div className='library-workbench__detail-main-actions'>
          <button
            ref={editTrigger}
            type='button'
            className='library-workbench__button'
            disabled={busy}
            aria-expanded={Boolean(editBase)}
            aria-controls={editBase ? editorId : undefined}
            onClick={openEditor}
          >
            <Pencil size={14} aria-hidden='true' />
            {t('conversation.libraryWorkbench.edit')}
          </button>
          <button type='button' className='library-workbench__icon' onClick={close} aria-label={t('common.close')}>
            <X size={16} aria-hidden='true' />
          </button>
        </div>
      </div>
      <LibraryReadingStatus
        value={literatureItemReadingStatus(item)}
        disabled={busy || item.deleted}
        onChange={changeReadingStatus}
        accessibleLabel={t('conversation.libraryReadingStatus.forPaper', { title: item.title })}
      />
      <dl className='library-workbench__facts'>
        <div>
          <dt>{t('conversation.reading.libraryAuthors')}</dt>
          <dd>{authors || '—'}</dd>
        </div>
        <div>
          <dt>{t('conversation.reading.libraryYear')}</dt>
          <dd>{year || '—'}</dd>
        </div>
        {typeof item.metadata.doi === 'string' && item.metadata.doi && (
          <div>
            <dt>DOI</dt>
            <dd>{item.metadata.doi}</dd>
          </div>
        )}
        <div>
          <dt>{t('conversation.libraryWorkbench.projects')}</dt>
          <dd>
            {projectStatus === 'loading'
              ? t('common.loading')
              : projectStatus === 'error'
                ? t('conversation.libraryWorkbench.projectsFailed')
                : members.map((project) => project.name).join(', ') || '—'}
          </dd>
        </div>
      </dl>
      <LibraryPublicationCheck account={account} item={item} disabled={stale} />
      <LibraryCollectionMembershipPicker
        item={item}
        account={account}
        collections={collections}
        memberships={memberships}
        busy={busy}
        submit={submitCollection}
        retry={refreshCollections}
      />
      <LibraryAttachmentBrowser account={account} item={item} />
      {editBase && (
        <div
          ref={editorPanel}
          id={editorId}
          className='library-workbench__editor'
          role='region'
          aria-label={t('conversation.libraryWorkbench.edit')}
          tabIndex={-1}
        >
          {externallyUpdated && (
            <div role='alert' className='library-workbench__notice'>
              <span>{t('conversation.libraryReadingStatus.externalUpdate')}</span>
              <button type='button' disabled={busy} onClick={() => setEditBase(item)}>
                {t('conversation.libraryReadingStatus.loadLatest')}
              </button>
            </div>
          )}
          <label>
            {t('conversation.libraryWorkbench.projectAction')}
            <select
              value={projectId}
              onChange={(event) => setProjectId(event.target.value)}
              disabled={busy || projectStatus !== 'ready'}
            >
              <option value=''>{t('conversation.libraryWorkbench.chooseProject')}</option>
              {projects.map((project) => (
                <option key={project.projectId} value={project.projectId}>
                  {project.name}
                </option>
              ))}
            </select>
          </label>
          <LibraryItemEditor
            key={`${editBase.id}:${editBase.metadata_revision}`}
            item={{
              ...item,
              title: editBase.title,
              metadata: editBase.metadata,
              metadata_revision: editBase.metadata_revision,
            }}
            projectId={projectId || undefined}
            busy={busy}
            readOnly={externallyUpdated}
            receipt={receipt}
            submit={submit}
            close={closeEditor}
          />
        </div>
      )}
    </section>
  );
}

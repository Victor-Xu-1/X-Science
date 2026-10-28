import React, { useCallback, useEffect, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand, LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import '@/renderer/components/chat/SendBox/reading/readingContext.css';
import {
  LibraryItemEditor,
  type RegisterLibraryEditorDraft,
} from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { LibraryRelatedLookup } from '@/renderer/components/chat/SendBox/reading/LibraryRelatedLookup';
import type { LiteratureDirection } from '@/common/adapter/literatureRelated';
import type {
  LiteratureCollectionCommand,
  LiteratureCollectionReceipt,
  LiteratureCollectionPage,
  LiteratureCollectionMembership,
} from '@/common/adapter/literatureCollections';
import type { LibraryReadView } from './useLibraryWorkbench';
import { isLibraryReadFresh } from './useLibraryWorkbench';
import { LibraryDirectoryState } from './LibraryDirectoryState';
import { LibraryCollectionMembershipPicker } from './LibraryCollectionMembership';
import { LibraryProjectMembership } from './LibraryProjectMembership';
import { LibraryAttachmentBrowser } from './LibraryAttachmentBrowser';
import { LibraryAbstract } from './LibraryAbstract';
import { LibraryBibliographicDetails } from './LibraryBibliographicDetails';
import { LibraryDetailHeader } from './LibraryDetailHeader';
import { LibraryPublicationCheck } from './LibraryPublicationCheck';
import { LibraryReadingStatus } from './LibraryReadingStatus';
import { literatureItemReadingStatus } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { libraryAuthors } from './libraryBibliographicText';
export { libraryAuthors } from './libraryBibliographicText';

export function LibraryItemDetails({
  item,
  visible = true,
  account,
  collections,
  memberships,
  submitCollection,
  refreshCollections,
  projects,
  projectStatus,
  projectRefreshing,
  projectRefreshError,
  retryProjects,
  busy,
  stale = false,
  changeReadingStatus,
  receipt,
  submit,
  close,
  requestEditorClose,
  registerDraft,
}: {
  item: LiteratureItem;
  visible?: boolean;
  account: string;
  collections: LibraryReadView<LiteratureCollectionPage>;
  memberships: LibraryReadView<LiteratureCollectionMembership | undefined>;
  submitCollection: (command: LiteratureCollectionCommand) => Promise<LiteratureCollectionReceipt>;
  refreshCollections: () => void;
  projects: XScienceProject[];
  projectStatus: 'loading' | 'ready' | 'error';
  projectRefreshing?: boolean;
  projectRefreshError?: boolean;
  retryProjects?: () => void;
  busy: boolean;
  stale?: boolean;
  changeReadingStatus: (value: LiteratureReadingStatus) => void;
  receipt?: LiteratureReceipt;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  close: () => void;
  requestEditorClose?: (action: () => void) => void;
  registerDraft?: RegisterLibraryEditorDraft;
}) {
  const { t } = useTranslation();
  const [editBase, setEditBase] = useState<LiteratureItem>();
  const [relatedDirection, setRelatedDirection] = useState<LiteratureDirection | null>(null);
  const [projectId, setProjectId] = useState('');
  const editorId = useId();
  const primaryHeader = useRef<HTMLDivElement>(null);
  const editTrigger = useRef<HTMLButtonElement>(null);
  const editorPanel = useRef<HTMLDivElement>(null);
  const scope = JSON.stringify([account, item.id]);
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const focusRequest = useRef<
    | { scope: string; target: 'editor' | 'trigger'; origin?: HTMLElement; eligible?: boolean; parked?: HTMLElement }
    | undefined
  >(undefined);
  const visibility = useRef(visible);
  visibility.current = visible;
  const current = useCallback(
    () => scopeRef.current === scope && account === getRendererAccountScopeToken(),
    [account, scope]
  );
  const revealEditor = useCallback(() => {
    const panel = editorPanel.current;
    if (!visibility.current || !current() || !panel?.isConnected) return;
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
    if (!visible || request.scope !== scope || !current()) {
      focusRequest.current = undefined;
      return;
    }
    if (request.target === 'editor' && editBase) {
      focusRequest.current = undefined;
      revealEditor();
    } else if (request.target === 'trigger' && !editBase && !busy) {
      const focused = document.activeElement;
      if (
        !request.eligible ||
        (focused?.isConnected &&
          focused !== document.body &&
          !request.origin?.contains(focused) &&
          focused !== request.parked)
      ) {
        focusRequest.current = undefined;
        return;
      }
      const trigger = editTrigger.current;
      if (!trigger?.isConnected || trigger.disabled) return;
      focusRequest.current = undefined;
      trigger.focus({ preventScroll: true });
    }
  }, [busy, current, editBase, revealEditor, scope, visible]);
  useEffect(
    () => () => {
      focusRequest.current = undefined;
    },
    []
  );
  const openEditor = () => {
    if (!visibility.current || busy || !current()) return;
    if (editBase) revealEditor();
    else {
      focusRequest.current = { scope, target: 'editor' };
      setEditBase(item);
    }
  };
  const closeEditor = useCallback(
    (returnToTrigger = false) => {
      if (!current()) return;
      const origin = editorPanel.current ?? undefined;
      const eligible = Boolean(visibility.current && (returnToTrigger || origin?.contains(document.activeElement)));
      const request = {
        scope,
        target: 'trigger' as const,
        origin,
        eligible,
        parked: undefined as HTMLElement | undefined,
      };
      // Preserve the qualified return while the disabled trigger awaits readback.
      // A stable in-dialog anchor prevents the focus lock choosing an unrelated control.
      if (eligible) {
        const trigger = editTrigger.current;
        const anchor = trigger && !trigger.disabled && !trigger.hidden ? trigger : primaryHeader.current;
        if (anchor) {
          request.parked = anchor;
          anchor.focus({ preventScroll: true });
        }
      }
      focusRequest.current = request;
      setEditBase(undefined);
    },
    [current, scope]
  );
  const authors = libraryAuthors(item);
  const year =
    typeof item.metadata.year === 'string' || Number.isSafeInteger(item.metadata.year)
      ? String(item.metadata.year)
      : '';
  const projectRead = { status: projectStatus, refreshing: projectRefreshing, refreshError: projectRefreshError };
  const projectFresh = isLibraryReadFresh(projectRead);
  const externallyUpdated = editBase !== undefined && editBase.metadata_revision !== item.metadata_revision;
  const requestBack = () => {
    const leave = () => closeEditor(true);
    if (requestEditorClose) requestEditorClose(leave);
    else leave();
  };
  return (
    <section className='library-workbench__detail' aria-label={t('conversation.libraryWorkbench.details')}>
      <LibraryDetailHeader
        title={item.title}
        editing={Boolean(editBase)}
        busy={busy}
        editorId={editorId}
        headerRef={primaryHeader}
        editRef={editTrigger}
        edit={openEditor}
        back={requestBack}
        close={close}
      />
      <div className='library-workbench__overview' hidden={Boolean(editBase)} inert={Boolean(editBase) || undefined}>
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
        </dl>
        <LibraryAbstract value={item.metadata.abstract} />
        <LibraryBibliographicDetails metadata={item.metadata} />
        <LibraryProjectMembership
          item={item}
          account={account}
          projects={projects}
          view={projectRead}
          disabled={busy || !visible || Boolean(editBase)}
          submit={submit}
          retry={retryProjects}
        />
        {!relatedDirection && (
          <div className='pdf-library-editor__buttons' aria-label={t('conversation.reading.related.direction')}>
            {(['references', 'citations'] as const).map((direction) => (
              <button
                key={direction}
                type='button'
                className='library-workbench__button'
                disabled={stale}
                onClick={() => setRelatedDirection(direction)}
              >
                {t('conversation.reading.related.' + direction)}
              </button>
            ))}
          </div>
        )}
        {relatedDirection && (
          <div hidden={stale} inert={stale || undefined}>
            <LibraryRelatedLookup
              key={JSON.stringify([account, item.id, item.metadata_revision, relatedDirection])}
              item={item}
              initialDirection={relatedDirection}
              close={() => setRelatedDirection(null)}
            />
          </div>
        )}
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
      </div>
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
              onChange={(event) => {
                if (projectFresh) setProjectId(event.target.value);
              }}
              disabled={busy || !projectFresh}
            >
              <option value=''>{t('conversation.libraryWorkbench.chooseProject')}</option>
              {projectId && !projects.some((project) => project.projectId === projectId) && (
                <option value={projectId} disabled>
                  {t('conversation.libraryWorkbench.projectUnavailable')}
                </option>
              )}
              {projects.map((project) => (
                <option key={project.projectId} value={project.projectId}>
                  {project.name}
                </option>
              ))}
            </select>
          </label>
          <LibraryDirectoryState
            view={projectRead}
            errorText={t('conversation.libraryWorkbench.projectsFailed')}
            retry={retryProjects}
          />
          <LibraryItemEditor
            key={`${editBase.id}:${editBase.metadata_revision}`}
            item={{
              ...item,
              title: editBase.title,
              metadata: editBase.metadata,
              metadata_revision: editBase.metadata_revision,
            }}
            projectId={projectId || undefined}
            membershipDisabled={!projectFresh}
            busy={busy}
            readOnly={externallyUpdated}
            receipt={receipt}
            submit={submit}
            close={closeEditor}
            requestClose={requestEditorClose}
            registerDraft={registerDraft}
          />
        </div>
      )}
    </section>
  );
}

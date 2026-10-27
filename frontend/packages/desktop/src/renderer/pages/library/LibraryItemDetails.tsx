import React, { useState } from 'react';
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
  const authors = libraryAuthors(item);
  const year =
    typeof item.metadata.year === 'string' || Number.isSafeInteger(item.metadata.year)
      ? String(item.metadata.year)
      : '';
  const members = projects.filter((project) => item.project_ids.includes(project.projectId));
  const externallyUpdated = editBase !== undefined && editBase.metadata_revision !== item.metadata_revision;
  return (
    <section className='library-workbench__detail' aria-label={t('conversation.libraryWorkbench.details')}>
      <div className='library-workbench__detail-heading'>
        <span className='library-workbench__eyebrow'>{t('conversation.libraryWorkbench.details')}</span>
        <button type='button' className='library-workbench__icon' onClick={close} aria-label={t('common.close')}>
          <X size={16} aria-hidden='true' />
        </button>
      </div>
      <h2>{item.title}</h2>
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
      {editBase ? (
        <div className='library-workbench__editor'>
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
            close={() => setEditBase(undefined)}
          />
        </div>
      ) : (
        <button type='button' className='library-workbench__button' disabled={busy} onClick={() => setEditBase(item)}>
          <Pencil size={14} aria-hidden='true' />
          {t('conversation.libraryWorkbench.edit')}
        </button>
      )}
    </section>
  );
}

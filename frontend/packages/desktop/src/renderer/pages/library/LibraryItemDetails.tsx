import React, { useState } from 'react';
import { FileText, Pencil, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import '@/renderer/components/chat/SendBox/reading/readingContext.css';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';

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
  projects,
  projectStatus,
  busy,
  submit,
  close,
}: {
  item: LiteratureItem;
  projects: XScienceProject[];
  projectStatus: 'loading' | 'ready' | 'error';
  busy: boolean;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  close: () => void;
}) {
  const { t } = useTranslation();
  const [editing, setEditing] = useState(false);
  const [projectId, setProjectId] = useState('');
  const authors = libraryAuthors(item);
  const year =
    typeof item.metadata.year === 'string' || Number.isSafeInteger(item.metadata.year)
      ? String(item.metadata.year)
      : '';
  const members = projects.filter((project) => item.project_ids.includes(project.projectId));
  return (
    <section className='library-workbench__detail' aria-label={t('conversation.libraryWorkbench.details')}>
      <div className='library-workbench__detail-heading'>
        <span className='library-workbench__eyebrow'>{t('conversation.libraryWorkbench.details')}</span>
        <button type='button' className='library-workbench__icon' onClick={close} aria-label={t('common.close')}>
          <X size={16} aria-hidden='true' />
        </button>
      </div>
      <h2>{item.title}</h2>
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
      <div className='library-workbench__section-heading'>
        <h3>{t('conversation.libraryWorkbench.attachments')}</h3>
        <span>{item.attachments.length}</span>
      </div>
      {item.attachments.length === 0 ? (
        <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noAttachments')}</p>
      ) : (
        <ul className='library-workbench__attachments'>
          {item.attachments.map((attachment) => {
            const version = attachment.versions.reduce<(typeof attachment.versions)[number] | undefined>(
              (latest, value) => (!latest || value.version_number > latest.version_number ? value : latest),
              undefined
            );
            return (
              <li key={attachment.id}>
                <FileText size={15} aria-hidden='true' />
                <div>
                  <strong>{version?.filename || attachment.kind}</strong>
                  <span>
                    {version
                      ? t('conversation.libraryWorkbench.latestVersion', { number: version.version_number })
                      : t('conversation.libraryWorkbench.unavailable')}{' '}
                    ·{' '}
                    {version?.available
                      ? t('conversation.libraryWorkbench.available')
                      : t('conversation.libraryWorkbench.unavailable')}
                  </span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {editing ? (
        <div className='library-workbench__editor'>
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
            key={`${item.id}:${item.metadata_revision}`}
            item={item}
            projectId={projectId || undefined}
            busy={busy}
            submit={submit}
            close={() => setEditing(false)}
          />
        </div>
      ) : (
        <button type='button' className='library-workbench__button' disabled={busy} onClick={() => setEditing(true)}>
          <Pencil size={14} aria-hidden='true' />
          {t('conversation.libraryWorkbench.edit')}
        </button>
      )}
    </section>
  );
}

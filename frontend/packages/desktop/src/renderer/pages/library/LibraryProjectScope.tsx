import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import type { LibraryReadView } from './useLibraryWorkbench';
import { LibraryDirectoryState } from './LibraryDirectoryState';

/** Presentation only: URL scope and project reads remain owned by the workbench. */
export function LibraryProjectScope({
  projectId,
  projects,
  select,
}: {
  projectId: string;
  projects: LibraryReadView<XScienceProject[]>;
  select: (id: string) => void;
}) {
  const { t } = useTranslation();
  const values = projects.status === 'ready' ? (projects.value ?? []) : [];
  const unavailable = projectId && !values.some((project) => project.projectId === projectId);
  return (
    <>
      <label className='library-workbench__project-scope'>
        <span>{t('conversation.libraryWorkbench.projectScope')}</span>
        <select value={projectId} onChange={(event) => select(event.target.value)}>
          <option value='' disabled>
            {t('conversation.libraryWorkbench.chooseProject')}
          </option>
          {unavailable && (
            <option value={projectId} disabled>
              {t(
                projects.status === 'loading'
                  ? 'conversation.libraryWorkbench.projectsLoading'
                  : 'conversation.libraryWorkbench.projectUnavailable'
              )}
            </option>
          )}
          {!projectId && projects.status === 'loading' && (
            <option disabled>{t('conversation.libraryWorkbench.projectsLoading')}</option>
          )}
          {values.map((project) => (
            <option key={project.projectId} value={project.projectId}>
              {project.name}
            </option>
          ))}
        </select>
      </label>
      <LibraryDirectoryState view={projects} errorText={t('conversation.libraryWorkbench.projectsFailed')} />
    </>
  );
}

import React from 'react';
import { useTranslation } from 'react-i18next';
import type { LiteratureTaskOrigin } from '@/common/adapter/literatureCandidates';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import { isLibraryReadFresh, type LibraryReadView } from './useLibraryWorkbench';

/** Presents trusted readback with the Page's project directory; it owns no reads or writes. */
export function LibraryCandidateOrigin({
  origins,
  state,
  projects,
}: {
  origins?: readonly LiteratureTaskOrigin[];
  state: 'pending' | 'dismissed';
  projects?: LibraryReadView<XScienceProject[]>;
}) {
  const { t } = useTranslation();
  if (!origins?.length) return null;
  const destinations = [
    ...new Map(
      origins.flatMap((origin) =>
        origin.project_id
          ? [
              [
                JSON.stringify([origin.project_id, origin.project_available]),
                { id: origin.project_id, available: origin.project_available === true },
              ] as const,
            ]
          : []
      )
    ).values(),
  ];
  const fresh = projects !== undefined && isLibraryReadFresh(projects);
  const loading = !projects || projects.status === 'loading' || projects.refreshing;
  return (
    <section className='library-inbox__origins' aria-label={t('conversation.libraryInbox.taskOrigins')}>
      <h4>{t('conversation.libraryInbox.taskOrigins')}</h4>
      <p>{t(`conversation.libraryInbox.${state === 'pending' ? 'awaitingApproval' : 'restoreForApproval'}`)}</p>
      {destinations.length ? (
        <>
          <p>{t('conversation.libraryInbox.projectDestinations')}</p>
          <ul aria-label={t('conversation.libraryInbox.projectDestinations')}>
            {destinations.map(({ id, available }) => {
              const project = available && fresh ? projects?.value?.find((value) => value.projectId === id) : undefined;
              return (
                <li key={JSON.stringify([id, available])}>
                  {project
                    ? project.name
                    : t(
                        `conversation.libraryInbox.${available && loading ? 'destinationLoading' : 'destinationUnavailable'}`
                      )}
                </li>
              );
            })}
          </ul>
        </>
      ) : (
        <p>{t('conversation.libraryInbox.libraryDestination')}</p>
      )}
    </section>
  );
}

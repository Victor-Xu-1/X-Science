import React, { useEffect, useRef, useState } from 'react';
import { Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { LibraryReadPhase } from './useLibraryWorkbench';
import { isLibraryReadFresh } from './useLibraryWorkbench';
import { LibraryDirectoryState } from './LibraryDirectoryState';

/** Local filtering and controls only; Page's account controller owns the write, retry and receipt. */
export function LibraryProjectMembership({
  item,
  account,
  projects,
  view,
  disabled,
  submit,
  retry,
}: {
  item: LiteratureItem;
  account: string;
  projects: XScienceProject[];
  view: LibraryReadPhase;
  disabled: boolean;
  submit: (command: LiteratureCommand) => Promise<unknown>;
  retry?: () => void;
}) {
  const { t } = useTranslation();
  const searchScope = JSON.stringify([account, item.id]);
  const [search, setSearch] = useState({ scope: searchScope, value: '' });
  if (search.scope !== searchScope) setSearch({ scope: searchScope, value: '' });
  const identity = JSON.stringify([account, item.id, item.metadata_revision]);
  const exposure = useRef({ identity, active: true });
  if (exposure.current.identity !== identity) {
    exposure.current.active = false;
    exposure.current = { identity, active: true };
  }
  useEffect(() => {
    const current = exposure.current;
    current.active = true;
    return () => {
      current.active = false;
    };
  }, [identity]);
  if (account !== getRendererAccountScopeToken()) return null;
  const fresh = isLibraryReadFresh(view);
  const searchable = projects.length > 5;
  const query = searchable && search.scope === searchScope ? search.value.trim().toLocaleLowerCase() : '';
  const entries = projects.filter((project) => project.name.toLocaleLowerCase().includes(query));
  const change = (projectId: string, included: boolean) => {
    if (
      disabled ||
      !fresh ||
      item.deleted ||
      !exposure.current.active ||
      exposure.current.identity !== identity ||
      account !== getRendererAccountScopeToken() ||
      !projects.some((project) => project.projectId === projectId)
    )
      return;
    void submit({
      kind: 'membership',
      operation_id: crypto.randomUUID(),
      item_id: item.id,
      project_id: projectId,
      included,
    }).catch((): undefined => undefined);
  };
  return (
    <section className='library-workbench__membership' aria-label={t('conversation.libraryWorkbench.projects')}>
      <h3>{t('conversation.libraryWorkbench.projects')}</h3>
      <LibraryDirectoryState view={view} errorText={t('conversation.libraryWorkbench.projectsFailed')} retry={retry} />
      {view.status === 'ready' &&
        (projects.length === 0 ? (
          <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noActiveProjects')}</p>
        ) : (
          <>
            {searchable && (
              <label className='library-workbench__search library-workbench__membership-search'>
                <Search size={16} aria-hidden='true' />
                <input
                  type='search'
                  aria-label={t('conversation.libraryWorkbench.searchProjects')}
                  placeholder={t('conversation.libraryWorkbench.searchProjects')}
                  autoComplete='off'
                  value={search.scope === searchScope ? search.value : ''}
                  onChange={(event) => setSearch({ scope: searchScope, value: event.currentTarget.value })}
                />
              </label>
            )}
            {entries.length ? (
              <fieldset disabled={disabled || !fresh || item.deleted} aria-busy={Boolean(disabled || view.refreshing)}>
                <legend className='sr-only'>{t('conversation.libraryWorkbench.projects')}</legend>
                {entries.map((project) => (
                  <label key={project.projectId}>
                    <input
                      type='checkbox'
                      checked={item.project_ids.includes(project.projectId)}
                      onChange={(event) => change(project.projectId, event.target.checked)}
                    />
                    <span>{project.name}</span>
                  </label>
                ))}
              </fieldset>
            ) : (
              <p className='library-workbench__muted'>{t('conversation.libraryWorkbench.noMatchingProjects')}</p>
            )}
          </>
        ))}
    </section>
  );
}

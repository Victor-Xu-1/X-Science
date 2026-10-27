import React from 'react';
import { Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';

/** Presentation only; the workbench retains the URL, reads and mutation owner. */
export function LibraryWorkspaceHeader({
  title,
  description,
  query,
  onQueryChange,
  actions,
  collectionActions,
}: {
  title: string;
  description?: string;
  query: string;
  onQueryChange: (value: string) => void;
  actions: React.ReactNode;
  collectionActions?: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <header className='library-workbench__header'>
      <div className='library-workbench__heading'>
        <div className='library-workbench__title-row'>
          <h1 id='library-workbench-title'>{title}</h1>
          {collectionActions}
        </div>
        {description && <p>{description}</p>}
      </div>
      <div className='library-workbench__controls'>
        <label className='library-workbench__search'>
          <Search size={15} aria-hidden='true' />
          <input
            type='search'
            value={query}
            maxLength={512}
            onChange={(event) => onQueryChange(event.target.value)}
            aria-label={t('conversation.libraryWorkbench.search')}
            placeholder={t('conversation.libraryWorkbench.search')}
          />
        </label>
        <div className='library-workbench__actions'>{actions}</div>
      </div>
    </header>
  );
}

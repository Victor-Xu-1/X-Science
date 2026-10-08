/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

/**
 * SettingsLibraryTabHeader — one-line compact header for the merged library
 * page tabs (experts / skills / connectors / environments).
 *
 * Each catalog keeps one category filter and its primary action. A shared
 * search slot exposes the catalog's existing query authority; no duplicated
 * filters, hidden refinement panel or second maintenance header is added.
 */

import React from 'react';

type SettingsLibraryTabHeaderProps = {
  title: React.ReactNode;
  /** Optional inventory count rendered as a badge after the title. */
  count?: number;
  /** The tab's single domain/category filter, right-aligned before the primary action. */
  filters?: React.ReactNode;
  /** Search the current catalog; the owning module supplies query state. */
  search?: React.ReactNode;
  /** Right-aligned primary action slot. */
  actions?: React.ReactNode;
  /** Extra testid for the whole header block. */
  'data-testid'?: string;
};

const SettingsLibraryTabHeader: React.FC<SettingsLibraryTabHeaderProps> = ({
  title,
  count,
  filters,
  search,
  actions,
  'data-testid': dataTestId,
}) => (
  <header data-testid={dataTestId} className='settings-library-tab-header'>
    <div className='settings-library-tab-header__row'>
      <h1 className='settings-library-tab-header__title'>{title}</h1>
      {typeof count === 'number' ? (
        <span className='settings-library-tab-header__count' aria-live='polite' aria-atomic='true'>
          {count}
        </span>
      ) : null}
      {filters ? <div className='settings-library-tab-header__filters'>{filters}</div> : null}
      {actions ? <div className='settings-library-tab-header__actions'>{actions}</div> : null}
    </div>
    {search ? <div className='settings-library-tab-header__search'>{search}</div> : null}
  </header>
);

export default SettingsLibraryTabHeader;

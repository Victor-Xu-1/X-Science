import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import { SiderLibraryEntry } from '@/renderer/components/layout/Sider/SiderNav/SiderLibraryEntry';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { collection, installCollectionHTTP, json, mountCollections, page } from './libraryCollectionsFixtures';

describe('Library workspace scope and navigation hierarchy', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-layout-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it.each(['en-US', 'zh-CN'])(
    'keeps the current scope as the only page title with usable controls in %s',
    async (locale) => {
      const http = installCollectionHTTP({
        collections: () => [collection('methods', 'Methods')],
        items: () => [],
        intercept: (url) =>
          url.pathname === '/api/projects'
            ? json({ projects: [{ project_id: 'alpha', name: 'Alpha project' }] })
            : undefined,
      });
      const rendered = await mountCollections('/library?project=alpha');
      await screen.findByRole('heading', { name: 'Alpha project', level: 1 });
      await act(async () => {
        await rendered.i18n.changeLanguage(locale);
      });
      const label = (key: string) => rendered.i18n.t(key);
      const navigation = screen.getByRole('complementary', { name: label('conversation.libraryWorkbench.title') });
      expect(
        within(navigation).getByRole('combobox', { name: label('conversation.libraryWorkbench.projectScope') })
      ).toHaveValue('alpha');
      expect(
        within(navigation).getByRole('navigation', { name: label('conversation.libraryWorkbench.collections') })
      ).toBeInTheDocument();
      const main = screen.getByRole('main', { name: 'Alpha project' });
      expect(within(main).getAllByRole('heading', { level: 1 })).toHaveLength(1);
      const header = main.querySelector('header')!;
      expect(
        within(header).getByRole('searchbox', { name: label('conversation.libraryWorkbench.search') })
      ).toBeInTheDocument();
      expect(
        within(header).getByRole('button', { name: label('conversation.libraryManual.addMenu') })
      ).toBeInTheDocument();
      expect(within(header).getByRole('button', { name: label('common.refresh') })).toBeInTheDocument();
      await screen.findByRole('heading', { name: label('conversation.libraryWorkbench.projectEmpty') });
      expect(screen.queryByRole('button', { name: label('conversation.libraryWorkbench.firstPage') })).toBeNull();
      expect(screen.queryByRole('button', { name: label('conversation.libraryWorkbench.nextPage') })).toBeNull();
      fireEvent.click(
        within(navigation).getByRole('button', { name: label('conversation.libraryWorkbench.allReferences') })
      );
      expect(
        await screen.findByRole('heading', { name: label('conversation.libraryWorkbench.allReferences'), level: 1 })
      ).toBeInTheDocument();
      expect(screen.getByRole('main')).toHaveAccessibleName(label('conversation.libraryWorkbench.allReferences'));
      expect(http.requests.filter((request) => request.method !== 'GET')).toEqual([]);
      expect(
        http.requests.some(
          (request) => new URL(request.path, 'http://localhost').searchParams.get('project_id') === 'alpha'
        )
      ).toBe(true);
    }
  );

  it('keeps a return action for an empty deep-linked page without inventing a next page', async () => {
    installCollectionHTTP({
      collections: () => [],
      intercept: (url) => (url.pathname === '/api/literature/items' ? json(page([])) : undefined),
    });
    await mountCollections('/library?cursor=existing-page');
    await waitFor(() => expect(screen.getByRole('button', { name: 'Back to first page' })).toBeEnabled());
    expect(screen.getByRole('button', { name: 'Next page' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Back to first page' }));
    expect(screen.queryByRole('button', { name: 'Back to first page' })).toBeNull();
    expect(screen.getByLabelText('Library location')).toHaveTextContent('');
  });

  it('preserves a named native route and current-page state when the sidebar is collapsed', async () => {
    const onNavigate = vi.fn();
    await renderWithI18n(
      <MemoryRouter initialEntries={['/library']}>
        <SiderLibraryEntry collapsed onNavigate={onNavigate} />
      </MemoryRouter>,
      'en-US'
    );
    const entry = screen.getByRole('link', { name: 'Library' });
    expect(entry).toHaveAttribute('href', '/library');
    expect(entry).toHaveAttribute('title', 'Library');
    expect(entry).toHaveAttribute('aria-current', 'page');
    expect(entry).toHaveTextContent('');
    fireEvent.click(entry, { ctrlKey: true });
    fireEvent.click(entry, { metaKey: true });
    expect(onNavigate).not.toHaveBeenCalled();
    fireEvent.click(entry);
    expect(onNavigate).toHaveBeenCalledTimes(1);
  });
});

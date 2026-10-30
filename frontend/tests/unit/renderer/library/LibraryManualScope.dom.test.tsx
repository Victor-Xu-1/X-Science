import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { installCollectionHTTP, json } from './libraryCollectionsFixtures';

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('manual-history-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('retires the existing manual portal when browser history returns from All to Inbox', async () => {
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url) =>
      url.pathname === '/api/literature/candidates'
        ? json({ schema: 'x-science.literature-candidates.v1', candidates: [] })
        : undefined,
  });
  let back!: () => void;
  let forward!: () => void;
  function History() {
    const navigate = useNavigate();
    back = () => navigate(-1);
    forward = () => navigate(1);
    return <output aria-label='History scope'>{useLocation().search}</output>;
  }
  await renderWithI18n(
    <MemoryRouter initialEntries={['/library?inbox=1', '/library']} initialIndex={1}>
      <LibraryPage />
      <History />
    </MemoryRouter>,
    'en-US'
  );
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  const add = await screen.findByRole('menuitem', { name: 'Add reference' });
  await waitFor(() => expect(add).toHaveAttribute('aria-disabled', 'false'));
  fireEvent.click(add);
  const dialog = await screen.findByRole('dialog', { name: 'Add reference' });
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'Title' }), { target: { value: 'Unsubmitted draft' } });
  await act(async () => back());
  await screen.findByRole('heading', { name: 'Inbox', level: 2 });
  expect(screen.getByLabelText('History scope')).toHaveTextContent('inbox=1');
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Add reference' })).toBeNull());
  expect(document.activeElement?.closest('[hidden]')).toBeNull();
  await act(async () => forward());
  expect(screen.queryByRole('dialog', { name: 'Add reference' })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Add reference' }));
  const reopened = await screen.findByRole('dialog', { name: 'Add reference' });
  expect(within(reopened).getByRole('textbox', { name: 'Title' })).toHaveValue('');
  expect(http.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
});

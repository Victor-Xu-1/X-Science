import React, { useState } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { LibraryRowActions } from '@/renderer/pages/library/LibraryRowActions';
import { renderWithI18n } from '../../i18nTestUtils';
import { paper } from './libraryCollectionsFixtures';

afterEach(cleanup);

it('closes the row menu when its read or mutation becomes unavailable instead of reopening it later', async () => {
  let setDisabled: (value: boolean) => void = () => undefined;
  function Fixture() {
    const [disabled, update] = useState(false);
    setDisabled = update;
    return <LibraryRowActions item={paper()} disabled={disabled} change={() => undefined} openItem={() => undefined} />;
  }
  await renderWithI18n(<Fixture />, 'en-US');
  fireEvent.click(screen.getByRole('button', { name: 'Actions for Paper A' }));
  await screen.findByRole('menuitem', { name: 'Move to Trash' });
  await act(async () => setDisabled(true));
  expect(screen.getByRole('button', { name: 'Actions for Paper A' })).toBeDisabled();
  await act(async () => setDisabled(false));
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Move to Trash' })).toBeNull());
});

it('keeps reversible lifecycle dispatch separate from opening details', async () => {
  const change = vi.fn();
  const open = vi.fn();
  const item = paper();
  const view = await renderWithI18n(
    <LibraryRowActions item={item} disabled={false} change={change} openItem={open} />,
    'en-US'
  );
  fireEvent.click(screen.getByRole('button', { name: 'Actions for Paper A' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Move to Trash' }));
  expect(change).toHaveBeenLastCalledWith(item, true);
  expect(open).not.toHaveBeenCalled();
  view.unmount();
  const deleted = { ...item, deleted: true };
  await renderWithI18n(<LibraryRowActions item={deleted} disabled={false} change={change} openItem={open} />, 'en-US');
  fireEvent.click(screen.getByRole('button', { name: 'Actions for Paper A' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Restore' }));
  expect(change).toHaveBeenLastCalledWith(deleted, false);
  expect(open).not.toHaveBeenCalled();
});

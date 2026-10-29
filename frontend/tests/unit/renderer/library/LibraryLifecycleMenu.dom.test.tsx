import React, { useState } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import { LibraryLifecycleAction } from '@/renderer/pages/library/LibraryLifecycleAction';
import { renderWithI18n } from '../../i18nTestUtils';
import { paper } from './libraryCollectionsFixtures';

afterEach(cleanup);

it('closes the row menu when its read or mutation becomes unavailable instead of reopening it later', async () => {
  let setDisabled: (value: boolean) => void = () => undefined;
  function Fixture() {
    const [disabled, update] = useState(false);
    setDisabled = update;
    return <LibraryLifecycleAction item={paper()} disabled={disabled} change={() => undefined} />;
  }
  await renderWithI18n(<Fixture />, 'en-US');
  fireEvent.click(screen.getByRole('button', { name: 'Actions for Paper A' }));
  await screen.findByRole('menuitem', { name: 'Move to Trash' });
  await act(async () => setDisabled(true));
  expect(screen.getByRole('button', { name: 'Actions for Paper A' })).toBeDisabled();
  await act(async () => setDisabled(false));
  await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Move to Trash' })).toBeNull());
});

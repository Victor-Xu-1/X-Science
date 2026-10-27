import React from 'react';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand, LiteratureItem } from '@/common/adapter/literatureLibrary';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { renderWithI18n } from '../../i18nTestUtils';

const item: LiteratureItem = {
  id: 'paper',
  title: 'Original title',
  metadata_revision: 7,
  metadata: { authors: 'Original author', year: 2024, opaque: Infinity },
  deleted: false,
  project_ids: [],
  attachments: [],
};
async function mount(submit = vi.fn<(command: LiteratureCommand) => Promise<unknown>>().mockResolvedValue(undefined)) {
  const close = vi.fn();
  await renderWithI18n(
    <LibraryItemEditor item={item} projectId='project' busy={false} submit={submit} close={close} />,
    'en-US'
  );
  return { submit, close };
}
describe('Library field metadata editor controls', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });
  it('sets cleared authors and year to empty strings with the optional changed title', async () => {
    const fixture = await mount();
    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: '  Updated title  ' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Authors' }), { target: { value: '' } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Year' }), { target: { value: '' } });
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
    await waitFor(() => expect(fixture.close).toHaveBeenCalledTimes(1));
    expect(fixture.submit).toHaveBeenCalledExactlyOnceWith({
      kind: 'edit-metadata',
      operation_id: expect.any(String),
      item_id: 'paper',
      expected_revision: 7,
      title: 'Updated title',
      metadata_patch: { set: { authors: '', year: '' } },
    });
    expect(item.metadata).toEqual({ authors: 'Original author', year: 2024, opaque: Infinity });
  });
  it('retains rejection close membership and confirmed removal behavior without merging dirty metadata into other commands', async () => {
    const rejected = vi
      .fn<(command: LiteratureCommand) => Promise<unknown>>()
      .mockRejectedValue(new Error('save rejected'));
    const failed = await mount(rejected);
    fireEvent.change(screen.getByRole('textbox', { name: 'Authors' }), { target: { value: 'Edited author' } });
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
    await waitFor(() => expect(rejected).toHaveBeenCalledTimes(1));
    await Promise.resolve();
    expect(failed.close).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Authors' })).toHaveValue('Edited author');
    fireEvent.click(screen.getByRole('button', { name: /^Close$/ }));
    expect(failed.close).toHaveBeenCalledTimes(1);
    expect(rejected).toHaveBeenCalledTimes(1);
    cleanup();

    const linked = await mount();
    fireEvent.change(screen.getByRole('textbox', { name: 'Authors' }), { target: { value: 'Unsent edit' } });
    fireEvent.click(screen.getByRole('button', { name: 'Add to project' }));
    await waitFor(() => expect(linked.close).toHaveBeenCalledTimes(1));
    expect(linked.submit).toHaveBeenCalledExactlyOnceWith({
      kind: 'membership',
      operation_id: expect.any(String),
      item_id: 'paper',
      project_id: 'project',
      included: true,
    });
    cleanup();

    const removed = await mount();
    fireEvent.change(screen.getByRole('textbox', { name: 'Year' }), { target: { value: '2025' } });
    fireEvent.click(screen.getByRole('button', { name: 'Remove from Library' }));
    fireEvent.click(screen.getByRole('button', { name: /^Cancel$/ }));
    expect(removed.submit).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Remove from Library' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm removal' }));
    await waitFor(() => expect(removed.close).toHaveBeenCalledTimes(1));
    expect(removed.submit).toHaveBeenCalledExactlyOnceWith({
      kind: 'edit',
      operation_id: expect.any(String),
      item_id: 'paper',
      expected_revision: 7,
      deleted: true,
    });
  });
});

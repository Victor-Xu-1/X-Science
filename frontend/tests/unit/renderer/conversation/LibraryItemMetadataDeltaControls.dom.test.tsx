import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand, LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import {
  LibraryItemEditor,
  type LibraryEditorDraftState,
} from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
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
  it('removes cleared authors and year with the optional changed title', async () => {
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
      metadata_patch: { remove: ['authors', 'year'] },
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
    await waitFor(() => expect(linked.submit).toHaveBeenCalledTimes(1));
    await act(async () => {
      await Promise.resolve();
    });
    expect(linked.close).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Authors' })).toHaveValue('Unsent edit');
    fireEvent.click(screen.getByRole('button', { name: /^Close$/ }));
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

  it('settles confirmed removal only after success without claiming pending removal saved its dirty draft', async () => {
    let resolve!: (value: unknown) => void;
    const pending = new Promise<unknown>((done) => {
      resolve = done;
    });
    const submit = vi.fn<(command: LiteratureCommand) => Promise<unknown>>(() => pending);
    const close = vi.fn();
    const reports: LibraryEditorDraftState[] = [];
    const registerDraft = () => (state: LibraryEditorDraftState | null) => {
      if (state) reports.push(state);
    };
    const editor = (receipt?: LiteratureReceipt) => (
      <LibraryItemEditor
        item={item}
        projectId='project'
        busy={false}
        submit={submit}
        close={close}
        receipt={receipt}
        registerDraft={registerDraft}
      />
    );
    const rendered = await renderWithI18n(editor(), 'en-US');
    fireEvent.change(screen.getByRole('textbox', { name: 'Year' }), { target: { value: '2025' } });
    fireEvent.click(screen.getByRole('button', { name: 'Remove from Library' }));
    fireEvent.click(screen.getByRole('button', { name: 'Confirm removal' }));
    expect(submit).toHaveBeenCalledTimes(1);
    const command = submit.mock.calls[0][0];
    expect(command).toEqual({
      kind: 'edit',
      operation_id: expect.any(String),
      item_id: 'paper',
      expected_revision: 7,
      deleted: true,
    });
    expect(close).not.toHaveBeenCalled();
    expect(reports.at(-1)?.dirty).toBe(true);
    expect(reports.at(-1)?.submittedOperationId).toBeUndefined();
    expect(screen.getByRole('textbox', { name: 'Year' })).toHaveValue('2025');
    const receipt: LiteratureReceipt = {
      schema: 'x-science.literature-receipt.v1',
      operation_id: command.operation_id,
      item: { ...item, deleted: false },
    };
    rendered.rerender(editor(receipt));
    await act(async () => {
      await Promise.resolve();
    });
    expect(close).not.toHaveBeenCalled();
    const removed = { ...receipt, item: { ...item, deleted: true } };
    rendered.rerender(editor(removed));
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
    await act(async () => resolve(removed));
    expect(close).toHaveBeenCalledTimes(1);
    expect(submit).toHaveBeenCalledTimes(1);
  });
});

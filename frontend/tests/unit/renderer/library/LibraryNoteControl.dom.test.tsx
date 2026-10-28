import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { LibraryNoteControl, type LibraryNoteControlProps } from '@/renderer/pages/library/LibraryNoteControl';
import type { LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { renderWithI18n } from '../../i18nTestUtils';
import { deferred, paper } from './libraryCollectionsFixtures';

const source: LiteratureItem = { ...paper(), metadata: { personal_note: 'Original' } };
const acknowledgement = (operation_id: string, note = 'Changed'): LiteratureReceipt => ({
  schema: 'x-science.literature-receipt.v1',
  operation_id,
  item: { ...source, metadata_revision: 2, metadata: { personal_note: note } },
});
const options = (): LibraryNoteControlProps => ({
  item: source,
  scope: 'scope-a',
  readKey: 'read-1',
  readStatus: 'ready',
  disabled: false,
  retryDisabled: false,
  commit: vi.fn(),
  reload: vi.fn(() => true),
});
const noteInput = () => screen.getByRole('textbox', { name: 'Note for Paper A' }) as HTMLInputElement;
afterEach(cleanup);

describe('Library Note input follows the reference interaction and existing read outcomes', () => {
  it('keeps IME Enter local, cancels Escape without a write, and commits ordinary Enter once', async () => {
    const props = options();
    vi.mocked(props.commit).mockImplementation(async (command) => acknowledgement(command.operation_id));
    const mounted = await renderWithI18n(<LibraryNoteControl {...props} />, 'en-US');
    const input = noteInput();
    act(() => input.focus());
    fireEvent.change(input, { target: { value: 'Do not save' } });
    fireEvent.keyDown(input, { key: 'Enter', isComposing: true });
    expect(props.commit).not.toHaveBeenCalled();
    expect(input).toHaveFocus();
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(props.commit).not.toHaveBeenCalled();
    expect(input).toHaveValue('Original');
    act(() => input.focus());
    fireEvent.change(input, { target: { value: 'Changed' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(props.commit).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(input).toHaveAttribute('aria-busy', 'true'));
    const receipt = acknowledgement(vi.mocked(props.commit).mock.calls[0][0].operation_id);
    mounted.rerender(<LibraryNoteControl {...props} item={receipt.item} readKey='read-2' />);
    await waitFor(() => expect(input).toHaveAttribute('aria-busy', 'false'));
    expect(noteInput()).toBe(input);
    expect(input).toHaveValue('Changed');
    expect(props.commit).toHaveBeenCalledTimes(1);
  });

  it('retries the same uncertain command, then retries only readback after an ACK', async () => {
    const props = options();
    vi.mocked(props.commit)
      .mockRejectedValueOnce(new Error('Response lost'))
      .mockImplementationOnce(async (command) => acknowledgement(command.operation_id));
    const mounted = await renderWithI18n(<LibraryNoteControl {...props} />, 'en-US');
    const input = noteInput();
    act(() => input.focus());
    fireEvent.change(input, { target: { value: 'Changed' } });
    fireEvent.blur(input);
    await screen.findByRole('alert');
    const original = vi.mocked(props.commit).mock.calls[0][0];
    mounted.rerender(<LibraryNoteControl {...props} disabled pendingId={original.operation_id} />);
    expect(input).toHaveValue('Changed');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(props.commit).toHaveBeenCalledTimes(2));
    expect(vi.mocked(props.commit).mock.calls[1][0]).toBe(original);
    await act(async () => {});
    mounted.rerender(<LibraryNoteControl {...props} disabled readKey='read-2' refreshError />);
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Saved, but could not be reloaded'));
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(props.reload).toHaveBeenCalledExactlyOnceWith('scope-a');
    expect(props.commit).toHaveBeenCalledTimes(2);
    mounted.rerender(<LibraryNoteControl {...props} disabled readKey='read-3' refreshing />);
    expect(input).toHaveAttribute('readonly');
    mounted.rerender(
      <LibraryNoteControl {...props} readKey='read-3' item={acknowledgement(original.operation_id).item} />
    );
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
    expect(input).toHaveValue('Changed');
    expect(input).not.toHaveAttribute('readonly');
    expect(props.commit).toHaveBeenCalledTimes(2);
  });

  it('uses readOnly during another owner action without replacing or blurring the dirty input', async () => {
    const props = options();
    const mounted = await renderWithI18n(<LibraryNoteControl {...props} />, 'en-US');
    const input = noteInput();
    act(() => input.focus());
    fireEvent.change(input, { target: { value: 'Keep this draft' } });
    mounted.rerender(<LibraryNoteControl {...props} disabled />);
    expect(noteInput()).toBe(input);
    expect(input).toHaveFocus();
    expect(input).toHaveAttribute('readonly');
    expect(input).not.toBeDisabled();
    expect(input).toHaveValue('Keep this draft');
    fireEvent.blur(input);
    expect(props.commit).not.toHaveBeenCalled();
  });

  it('reports an oversized draft without a write or silent browser truncation', async () => {
    const props = options();
    await renderWithI18n(<LibraryNoteControl {...props} />, 'en-US');
    const input = noteInput();
    act(() => input.focus());
    const draft = 'x'.repeat(10_001);
    fireEvent.change(input, { target: { value: draft } });
    fireEvent.blur(input);
    expect(await screen.findByRole('alert')).toHaveTextContent('10,000');
    expect(input).toHaveValue(draft);
    expect(input).not.toHaveAttribute('maxlength');
    expect(props.commit).not.toHaveBeenCalled();
  });

  it('ignores a retired row completion after unmounting into another scope', async () => {
    const pending = deferred<LiteratureReceipt>();
    const props = options();
    vi.mocked(props.commit).mockReturnValue(pending.promise);
    const mounted = await renderWithI18n(<LibraryNoteControl {...props} />, 'en-US');
    act(() => noteInput().focus());
    fireEvent.change(noteInput(), { target: { value: 'Old scope write' } });
    fireEvent.blur(noteInput());
    await waitFor(() => expect(props.commit).toHaveBeenCalledTimes(1));
    const original = vi.mocked(props.commit).mock.calls[0][0];
    mounted.unmount();
    const next = { ...source, id: 'b', title: 'Paper B', metadata: { personal_note: 'New scope value' } };
    await renderWithI18n(<LibraryNoteControl {...options()} item={next} scope='scope-b' />, 'en-US');
    await act(async () => pending.resolve(acknowledgement(original.operation_id, 'Old scope write')));
    expect(screen.getByRole('textbox', { name: 'Note for Paper B' })).toHaveValue('New scope value');
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

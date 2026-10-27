import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand, LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { renderWithI18n } from '../../i18nTestUtils';

const firstOperation = '11111111-1111-4111-8111-111111111111';
const nextOperation = '22222222-2222-4222-8222-222222222222';
const otherOperation = '33333333-3333-4333-8333-333333333333';
const item: LiteratureItem = {
  id: 'paper',
  title: 'Original paper',
  metadata_revision: 7,
  metadata: { authors: ['Ada'], year: 2024 },
  deleted: false,
  project_ids: [],
  attachments: [],
};

function deferred() {
  let resolve!: (value?: unknown) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<unknown>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

function receipt(operationId: string, target: LiteratureItem = item): LiteratureReceipt {
  return {
    schema: 'x-science.literature-receipt.v1',
    operation_id: operationId,
    item: { ...target, title: 'Saved paper', metadata_revision: target.metadata_revision + 1 },
  };
}

type EditorProps = React.ComponentProps<typeof LibraryItemEditor>;
const editor = (props: EditorProps) => <LibraryItemEditor {...props} />;

describe('Library editor durable receipt lifecycle', () => {
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it('retains a rejected submission until its fresh item receipt arrives and closes only once', async () => {
    const pending = deferred();
    const submit = vi.fn((_command: LiteratureCommand) => pending.promise);
    const close = vi.fn();
    const props: EditorProps = { item, busy: false, submit, close };
    const rendered = await renderWithI18n(editor(props), 'en-US');
    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: 'Changed paper' } });
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
    expect(submit).toHaveBeenCalledTimes(1);
    const operation = submit.mock.calls[0][0].operation_id;
    await act(async () => pending.reject(new Error('ACK response was lost')));
    expect(close).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Title' })).toHaveValue('Changed paper');

    rendered.rerender(editor({ ...props, receipt: receipt(otherOperation) }));
    expect(close).not.toHaveBeenCalled();
    rendered.rerender(editor({ ...props, receipt: receipt(operation, { ...item, id: 'another-paper' }) }));
    expect(close).not.toHaveBeenCalled();
    rendered.rerender(editor(props));
    expect(close).not.toHaveBeenCalled();

    const acknowledged = receipt(operation);
    rendered.rerender(editor({ ...props, receipt: acknowledged }));
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
    rendered.rerender(editor({ ...props, receipt: { ...acknowledged } }));
    expect(close).toHaveBeenCalledTimes(1);
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it('does not treat a cached same-UUID receipt as a new ACK and settles the fresh ACK/direct result once', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValue(firstOperation);
    const cached = receipt(firstOperation);
    const pending = deferred();
    const submit = vi.fn((_command: LiteratureCommand) => pending.promise);
    const close = vi.fn();
    const props: EditorProps = { item, busy: false, receipt: cached, submit, close };
    const rendered = await renderWithI18n(editor(props), 'en-US');
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
    expect(submit.mock.calls[0][0].operation_id).toBe(firstOperation);
    rendered.rerender(editor({ ...props, busy: true }));
    rendered.rerender(editor({ ...props, busy: false }));
    expect(close).not.toHaveBeenCalled();

    const fresh = receipt(firstOperation);
    expect(fresh).not.toBe(cached);
    rendered.rerender(editor({ ...props, receipt: fresh }));
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
    await act(async () => pending.resolve(fresh));
    rendered.rerender(editor({ ...props, receipt: { ...fresh } }));
    expect(close).toHaveBeenCalledTimes(1);
    expect(submit).toHaveBeenCalledTimes(1);
  });

  it('disarms an unmounted attempt and keeps the replacement form usable despite its late result and old ACK', async () => {
    vi.spyOn(crypto, 'randomUUID').mockReturnValueOnce(firstOperation).mockReturnValueOnce(nextOperation);
    const oldPending = deferred();
    const oldSubmit = vi.fn((_command: LiteratureCommand) => oldPending.promise);
    const oldClose = vi.fn();
    const previous = await renderWithI18n(editor({ item, busy: false, submit: oldSubmit, close: oldClose }), 'en-US');
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
    expect(oldSubmit.mock.calls[0][0].operation_id).toBe(firstOperation);
    previous.unmount();

    const replacement = { ...item, id: 'next-paper', title: 'Next paper' };
    const oldAck = receipt(firstOperation);
    const nextPending = deferred();
    const submit = vi.fn((_command: LiteratureCommand) => nextPending.promise);
    const close = vi.fn();
    const props: EditorProps = { item: replacement, busy: false, receipt: oldAck, submit, close };
    const current = await renderWithI18n(editor(props), 'en-US');
    await act(async () => oldPending.resolve(oldAck));
    expect(oldClose).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
    expect(screen.getByRole('textbox', { name: 'Title' })).toHaveValue('Next paper');

    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: 'Next edit' } });
    fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
    expect(submit).toHaveBeenCalledTimes(1);
    expect(submit.mock.calls[0][0]).toMatchObject({
      operation_id: nextOperation,
      item_id: replacement.id,
      title: 'Next edit',
    });
    current.rerender(editor({ ...props, receipt: { ...oldAck } }));
    expect(close).not.toHaveBeenCalled();
    const nextAck = receipt(nextOperation, replacement);
    current.rerender(editor({ ...props, receipt: nextAck }));
    await waitFor(() => expect(close).toHaveBeenCalledTimes(1));
    await act(async () => nextPending.resolve(nextAck));
    expect(close).toHaveBeenCalledTimes(1);
    expect(oldClose).not.toHaveBeenCalled();
  });
});

import React, { useState, useSyncExternalStore } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type {
  LiteraturePublicationCheck,
  LiteraturePublicationCommand,
  LiteraturePublicationReceipt,
  LiteraturePublicationSnapshot,
} from '@/common/adapter/literaturePublicationCheck';
import { LibraryPublicationCheck } from '@/renderer/pages/library/LibraryPublicationCheck';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { useLibraryWorkbench } from '@/renderer/pages/library/useLibraryWorkbench';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { encodePendingRecord } from '@/renderer/services/runtime/literatureUploadIntent';
import {
  getRendererAccountScopeToken,
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks, literaturePendingStorage } from '../conversation/literatureMutationLockFixtures';

const owner = vi.hoisted(() => ({ controller: undefined as unknown, reads: undefined as unknown }));
vi.mock('@/renderer/hooks/chat/useLiteratureMutation', () => ({
  useLiteratureMutation: () => {
    const controller = owner.controller as LiteratureMutationController;
    return {
      controller,
      view: useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot),
    };
  },
}));
vi.mock('@/renderer/pages/library/useLibraryWorkbench', () => ({
  useLibraryWorkbench: () => {
    const [refreshing, setRefreshing] = useState(false);
    const reads = owner.reads as ReturnType<typeof useLibraryWorkbench>;
    return {
      ...reads,
      detail: refreshing ? { key: 'refreshing', status: 'loading' } : reads.detail,
      refresh: () => {
        reads.refresh();
        setRefreshing(true);
      },
    };
  },
}));
type Options = NonNullable<ConstructorParameters<typeof LiteratureMutationController>[1]>;
const PREFIX = 'conversation.libraryPublicationCheck.';
const storageKey = 'x-science.literature-mutation.v1:publication-owner';
const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const item = (extra: Partial<LiteratureItem> = {}): LiteratureItem => ({
  id: 'paper',
  title: 'Saved reference',
  metadata_revision: 1,
  metadata: { doi: '10.1234/paper' },
  deleted: false,
  project_ids: [],
  attachments: [],
  ...extra,
});
const snapshot = (extra: Partial<LiteraturePublicationSnapshot> = {}): LiteraturePublicationSnapshot => ({
  doi: '10.1234/paper',
  checked_at: '2026-10-20T08:09:10Z',
  notices: [{ doi: '10.1234/notice', type: 'retraction', source: 'publisher', date: '2024-01-02' }],
  ...extra,
});
const result = (extra: Partial<LiteraturePublicationCheck> = {}): LiteraturePublicationCheck => ({
  schema: 'x-science.literature-publication-check.v1',
  item_id: 'paper',
  metadata_revision: 1,
  doi: '10.1234/paper',
  ...extra,
});
const receipt = (operationId: string, value = result({ snapshot: snapshot() })): LiteraturePublicationReceipt => ({
  schema: 'x-science.literature-publication-receipt.v1',
  operation_id: operationId,
  result: value,
});
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
function setup(options: Options = {}, storage = literaturePendingStorage()) {
  const run = vi
    .fn<NonNullable<Options['runPublication']>>()
    .mockImplementation(async (command) => receipt(command.operation_id));
  const controller = new LiteratureMutationController(getRendererAccountOwnerId(), {
    storage,
    runPublication: run,
    ...options,
  });
  owner.controller = controller;
  return { controller, run, storage };
}
function savedRead(value: LiteraturePublicationCheck = result()) {
  const fetch = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => json(value));
  vi.stubGlobal('fetch', fetch);
  return fetch;
}
const panel = (reference = item(), account = getRendererAccountScopeToken()) => (
  <LibraryPublicationCheck account={account} item={reference} />
);
const unavailable = (status: number) =>
  new BackendHttpError({
    method: 'POST',
    path: '/api/literature/items/paper/publication-check',
    status,
    body: { error: 'Unavailable' },
  });

describe('Library publication-check detail and original mutation owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('publication-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it.each([
    ['en-US', 'Retractions and corrections', 'rate-limited'],
    ['zh-CN', '撤稿与更正', 'timeout'],
  ] as const)(
    'restores truthful notice evidence and retries a terminal provider result in %s',
    async (language, heading, issue) => {
      const { controller, run } = setup();
      const fetch = savedRead(
        result({
          snapshot: snapshot({
            notices: [
              ...snapshot().notices,
              { doi: '10.1234/unknown', type: '<b>new-type</b>', source: 'retraction-watch' },
            ],
          }),
        })
      );
      const view = await renderWithI18n(panel(), language);
      const t = (key: string) => view.i18n.t(PREFIX + key);
      expect(screen.getByRole('heading', { name: heading })).toBeInTheDocument();
      expect(await screen.findByText(t('notices.retraction'))).toBeInTheDocument();
      expect(screen.getByText('<b>new-type</b>')).toBeInTheDocument();
      expect(view.container.querySelector('b')).toBeNull();
      expect(screen.getByText(t('sources.publisher'), { exact: false })).toBeInTheDocument();
      expect(screen.getByText(t('sources.retraction-watch'), { exact: false })).toBeInTheDocument();
      expect(screen.getByRole('link', { name: '10.1234/notice' })).toHaveAttribute(
        'href',
        'https://doi.org/10.1234%2Fnotice'
      );
      expect(screen.getByText('2024-01-02')).toHaveAttribute('datetime', '2024-01-02');
      expect(view.container.querySelector('time[datetime="2026-10-20T08:09:10Z"]')).not.toBeNull();
      expect(fetch).toHaveBeenCalledTimes(1);
      expect(fetch.mock.calls[0][0]).toContain('expected_metadata_revision=1');
      expect(run).not.toHaveBeenCalled();
      run.mockImplementationOnce(async (command) => receipt(command.operation_id, result({ issue })));
      fireEvent.click(screen.getByRole('button', { name: t('checkAgain') }));
      fireEvent.click(screen.getByRole('button', { name: t('checking') }));
      expect(await screen.findByRole('alert')).toHaveTextContent(t('issues.' + issue));
      expect(run).toHaveBeenCalledTimes(1);
      expect(screen.getByText(t('notices.retraction'))).toBeInTheDocument();
      expect(controller.getSnapshot().pending).toBeNull();
      const firstId = run.mock.calls[0][0].operation_id;
      run.mockImplementationOnce(async (command) =>
        receipt(command.operation_id, result({ snapshot: snapshot({ notices: [] }) }))
      );
      fireEvent.click(screen.getByRole('button', { name: t('retry') }));
      expect(await screen.findByText(t('noNotices'))).toBeInTheDocument();
      expect(run.mock.calls[1][0].operation_id).not.toBe(firstId);
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
      expect(screen.getByText(t('disclosure'))).toBeInTheDocument();
    }
  );

  it('requests nothing for a missing DOI and treats backend doi-required as a prompt rather than a success', async () => {
    const { run } = setup();
    const fetch = savedRead(result({ doi: undefined, issue: 'doi-required' }));
    const view = await renderWithI18n(panel(item({ metadata: {} })), 'en-US');
    expect(screen.getByText('Add one valid DOI to check this reference.')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Check now' })).toBeDisabled();
    expect(fetch).not.toHaveBeenCalled();
    view.rerender(panel(item({ metadata: { doi: 'not a DOI' } })));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByText('Loading saved publication check…')).not.toBeInTheDocument());
    expect(screen.getByRole('button', { name: 'Check now' })).toBeDisabled();
    expect(screen.queryByText('Not checked')).not.toBeInTheDocument();
    expect(screen.queryByText('No related notices found.')).not.toBeInTheDocument();
    expect(run).not.toHaveBeenCalled();
  });

  it('retries a failed saved read with GET only and never fabricates an empty successful check', async () => {
    const { run } = setup();
    const fetch = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(json(result()));
    vi.stubGlobal('fetch', fetch);
    await renderWithI18n(panel(), 'en-US');
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load the saved publication check.');
    expect(screen.queryByText('Not checked')).not.toBeInTheDocument();
    expect(screen.queryByText('No related notices found.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Not checked')).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(fetch.mock.calls.every(([, init]) => init.method === 'GET')).toBe(true);
    expect(run).not.toHaveBeenCalled();
  });

  it('restores an uncertain original request after remount and rejects a late GET after its receipt', async () => {
    const { controller, run, storage } = setup();
    savedRead();
    const view = await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Not checked');
    run.mockRejectedValueOnce(unavailable(503));
    fireEvent.click(screen.getByRole('button', { name: 'Check now' }));
    await screen.findByRole('button', { name: 'Retry original check' });
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    const original = controller.getSnapshot().pending as LiteraturePublicationCommand;
    expect(original.operation_id).toBe(run.mock.calls[0][0].operation_id);
    view.unmount();
    const restored = setup({}, storage);
    const late = deferred<Response>();
    vi.stubGlobal('fetch', vi.fn().mockReturnValue(late.promise));
    restored.run.mockImplementation(async (command) =>
      receipt(
        command.operation_id,
        result({
          snapshot: snapshot({
            checked_at: '2026-10-27T01:02:03Z',
            notices: [],
          }),
        })
      )
    );
    await renderWithI18n(panel(), 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Retry original check' }));
    await screen.findByText('No related notices found.');
    expect(restored.run).toHaveBeenCalledTimes(1);
    expect(restored.run.mock.calls[0][0]).toEqual(original);
    await act(async () => late.resolve(json(result({ snapshot: snapshot() }))));
    expect(screen.queryByText('Retraction notice')).not.toBeInTheDocument();
    expect(restored.controller.getSnapshot().pending).toBeNull();
    expect(storage.getItem(storageKey)).toBeNull();
  });

  it('keeps reads available while another Library operation owns the pending slot', async () => {
    const storage = literaturePendingStorage();
    storage.setItem(
      storageKey,
      encodePendingRecord('publication-owner', {
        kind: 'edit',
        operation_id: id(50),
        item_id: 'other-paper',
        expected_revision: 1,
        title: 'Kept edit',
      })
    );
    const { run, controller } = setup({}, storage);
    const fetch = savedRead(result({ snapshot: snapshot() }));
    await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Retraction notice');
    expect(screen.getByRole('button', { name: 'Check again' })).toBeDisabled();
    expect(
      screen.getByText('Finish the existing Library change before starting a publication check.')
    ).toBeInTheDocument();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(run).not.toHaveBeenCalled();
    expect(controller.getSnapshot().pending?.operation_id).toBe(id(50));
  });

  it.each(['account', 'item', 'revision', 'doi'] as const)(
    'does not publish a delayed saved result after a %s generation change',
    async (change) => {
      setup();
      const late = deferred<Response>();
      const fetch = vi
        .fn()
        .mockReturnValueOnce(late.promise)
        .mockImplementation(async () =>
          json(
            result({
              item_id: change === 'item' ? 'next-paper' : 'paper',
              metadata_revision: change === 'revision' ? 2 : 1,
            })
          )
        );
      vi.stubGlobal('fetch', fetch);
      const view = await renderWithI18n(panel(), 'en-US');
      await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
      if (change === 'account') {
        act(() => setRendererAccountOwner('next-owner'));
        setup();
      }
      const next = item(
        change === 'item'
          ? { id: 'next-paper' }
          : change === 'revision'
            ? { metadata_revision: 2 }
            : change === 'doi'
              ? { metadata: { doi: 'https://doi.org/10.1234/paper' } }
              : {}
      );
      view.rerender(panel(next));
      await screen.findByText('Not checked');
      await act(async () => late.resolve(json(result({ snapshot: snapshot() }))));
      expect(screen.queryByText('Retraction notice')).not.toBeInTheDocument();
      expect(screen.getByText('Not checked')).toBeInTheDocument();
    }
  );

  it('leaves a running original check alive when details close and ignores its late result in another detail', async () => {
    const { controller, run, storage } = setup();
    const pending = deferred<LiteraturePublicationReceipt>();
    run.mockReturnValueOnce(pending.promise);
    savedRead();
    const view = await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Not checked');
    fireEvent.click(screen.getByRole('button', { name: 'Check now' }));
    await waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    const command = run.mock.calls[0][0];
    const signal = run.mock.calls[0][1]!;
    view.unmount();
    expect(signal.aborted).toBe(false);
    expect(controller.getSnapshot().pending?.operation_id).toBe(command.operation_id);
    expect(storage.getItem(storageKey)).not.toBeNull();
    savedRead(result({ item_id: 'next-paper' }));
    await renderWithI18n(panel(item({ id: 'next-paper' })), 'en-US');
    await screen.findByText('Not checked');
    await act(async () => pending.resolve(receipt(command.operation_id)));
    await waitFor(() => expect(controller.getSnapshot().pending).toBeNull());
    expect(screen.queryByText('Retraction notice')).not.toBeInTheDocument();
    expect(screen.getByText('Not checked')).toBeInTheDocument();
  });

  it('uses the retained action projection after definite rejection and starts a new operation on retry', async () => {
    const { controller, run } = setup();
    run.mockRejectedValueOnce(unavailable(409));
    savedRead();
    await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Not checked');
    fireEvent.click(screen.getByRole('button', { name: 'Check now' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('The publication check was rejected.');
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    const original = run.mock.calls[0][0].operation_id;
    expect(controller.getSnapshot().pending).toBeNull();
    expect(controller.getSnapshot().action?.operation_id).toBe(original);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByText('Retraction notice');
    expect(run.mock.calls[1][0].operation_id).not.toBe(original);
  });

  it('restores a provider failure settled while details were closed alongside the saved evidence and its time', async () => {
    const { controller, run } = setup();
    const pending = deferred<LiteraturePublicationReceipt>();
    run.mockReturnValueOnce(pending.promise);
    savedRead(result({ snapshot: snapshot() }));
    const view = await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Retraction notice');
    fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
    await waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    const command = run.mock.calls[0][0];
    view.unmount();
    await act(async () => pending.resolve(receipt(command.operation_id, result({ issue: 'timeout' }))));
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    expect(controller.getSnapshot().pending).toBeNull();
    expect(controller.getSnapshot().action?.operation_id).toBe(command.operation_id);
    const reopened = await renderWithI18n(panel(), 'en-US');
    expect(await screen.findByRole('alert')).toHaveTextContent('The publication check timed out.');
    await screen.findByText('Retraction notice');
    expect(reopened.container.querySelector('time[datetime="2026-10-20T08:09:10Z"]')).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it('reconciles reopened success and original-request replay with newer saved evidence', async () => {
    const { run, storage } = setup();
    const older = result({ snapshot: snapshot({ notices: [] }) });
    const newer = result({ snapshot: snapshot({ checked_at: '2026-10-27T08:09:10Z' }) });
    run.mockImplementationOnce(async (command) => receipt(command.operation_id, older));
    savedRead();
    const first = await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Not checked');
    fireEvent.click(screen.getByRole('button', { name: 'Check now' }));
    await screen.findByText('No related notices found.');
    const command = run.mock.calls[0][0];
    first.unmount();
    const saved = deferred<Response>();
    const fetch = vi.fn<typeof globalThis.fetch>().mockReturnValue(saved.promise);
    vi.stubGlobal('fetch', fetch);
    const reopened = await renderWithI18n(panel(), 'en-US');
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(false);
    await act(async () => saved.resolve(json(newer)));
    await screen.findByText('Retraction notice');
    expect(reopened.container.querySelector('time[datetime="2026-10-27T08:09:10Z"]')).not.toBeNull();
    expect(screen.queryByText('No related notices found.')).not.toBeInTheDocument();
    expect(run).toHaveBeenCalledTimes(1);
    reopened.unmount();

    storage.setItem(storageKey, encodePendingRecord('publication-owner', command));
    const restored = setup({}, storage);
    const replay = deferred<LiteraturePublicationReceipt>();
    restored.run.mockReturnValueOnce(replay.promise);
    const replaySaved = deferred<Response>();
    const replayFetch = vi.fn<typeof globalThis.fetch>().mockReturnValue(replaySaved.promise);
    vi.stubGlobal('fetch', replayFetch);
    const replayView = await renderWithI18n(panel(), 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Retry original check' }));
    await waitFor(() => expect(restored.run).toHaveBeenCalledTimes(1));
    expect(restored.run.mock.calls[0][0]).toEqual(command);
    await act(async () => replay.resolve(receipt(command.operation_id, older)));
    await screen.findByText('No related notices found.');
    expect(replayFetch).toHaveBeenCalledTimes(1);
    expect(replayFetch.mock.calls[0][1]?.signal?.aborted).toBe(false);
    await act(async () => replaySaved.resolve(json(newer)));
    await screen.findByText('Retraction notice');
    expect(replayView.container.querySelector('time[datetime="2026-10-27T08:09:10Z"]')).not.toBeNull();
    expect(screen.queryByText('No related notices found.')).not.toBeInTheDocument();
    replayView.unmount();

    // The page-level retry may already be running when the detail is reopened.
    storage.setItem(storageKey, encodePendingRecord('publication-owner', command));
    const outside = setup({}, storage);
    const outsideReply = deferred<LiteraturePublicationReceipt>();
    outside.run.mockReturnValueOnce(outsideReply.promise);
    const outsideRun = outside.controller.retry();
    await waitFor(() => expect(outside.run).toHaveBeenCalledTimes(1));
    const currentSaved = deferred<Response>();
    const currentFetch = vi.fn<typeof globalThis.fetch>().mockReturnValue(currentSaved.promise);
    vi.stubGlobal('fetch', currentFetch);
    const duringRetry = await renderWithI18n(panel(), 'en-US');
    await waitFor(() => expect(currentFetch).toHaveBeenCalledTimes(1));
    await act(async () => {
      outsideReply.resolve(receipt(command.operation_id, older));
      await outsideRun;
    });
    expect(currentFetch.mock.calls[0][1]?.signal?.aborted).toBe(false);
    await act(async () => currentSaved.resolve(json(newer)));
    await screen.findByText('Retraction notice');
    expect(duringRetry.container.querySelector('time[datetime="2026-10-27T08:09:10Z"]')).not.toBeNull();
  });

  it('repairs server-confirmed invalid saved evidence with a new publication check', async () => {
    const { controller, run } = setup();
    const fetch = savedRead(result({ issue: 'response-invalid' }));
    await renderWithI18n(panel(), 'en-US');
    expect(await screen.findByRole('alert')).toHaveTextContent('The publication check returned an invalid response.');
    expect(run).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByText('Retraction notice');
    expect(run).toHaveBeenCalledTimes(1);
    expect(run.mock.calls[0][0]).toMatchObject({
      kind: 'check-publication',
      item_id: 'paper',
      expected_metadata_revision: 1,
    });
    expect(run.mock.calls[0][0].operation_id).toMatch(/^[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}$/);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(controller.getSnapshot().pending).toBeNull();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('does not consume an old direct response after another page admits the next original operation', async () => {
    const { controller, run, storage } = setup();
    const late = deferred<LiteraturePublicationReceipt>();
    const next = deferred<LiteraturePublicationReceipt>();
    run.mockReturnValueOnce(late.promise);
    savedRead(result({ snapshot: snapshot() }));
    await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Retraction notice');
    fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
    await waitFor(() => expect(run).toHaveBeenCalledTimes(1));
    const original = run.mock.calls[0][0];
    const nextCommand: LiteraturePublicationCommand = { ...original, operation_id: id(77) };
    const other = new LiteratureMutationController('publication-owner', {
      storage,
      runPublication: async (command) =>
        command.operation_id === original.operation_id ? receipt(command.operation_id) : next.promise,
    });
    let nextFlight!: Promise<LiteraturePublicationReceipt>;
    await act(async () => {
      await other.retry();
      nextFlight = other.submit(nextCommand);
    });
    await waitFor(() => expect(other.getSnapshot().pending?.operation_id).toBe(nextCommand.operation_id));
    await act(async () =>
      late.resolve(receipt(original.operation_id, result({ snapshot: snapshot({ notices: [] }) })))
    );
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    expect(controller.getSnapshot().pending?.operation_id).toBe(nextCommand.operation_id);
    expect(screen.getByText('Retraction notice')).toBeInTheDocument();
    expect(screen.queryByText('No related notices found.')).not.toBeInTheDocument();
    await act(async () => next.resolve(receipt(nextCommand.operation_id)));
    await nextFlight;
  });

  it('restores storage with a read-only action before retrying the preserved original check', async () => {
    const storage = literaturePendingStorage();
    let inaccessible = false;
    const { controller, run } = setup(
      {
        storage: {
          ...storage,
          getItem: (key) => {
            if (inaccessible) throw new Error('Storage access denied');
            return storage.getItem(key);
          },
        },
      },
      storage
    );
    savedRead();
    run.mockRejectedValueOnce(unavailable(503));
    await renderWithI18n(panel(), 'en-US');
    await screen.findByText('Not checked');
    fireEvent.click(screen.getByRole('button', { name: 'Check now' }));
    await screen.findByRole('button', { name: 'Retry original check' });
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    const original = controller.getSnapshot().pending!;
    const stored = storage.getItem(storageKey);
    inaccessible = true;
    act(() => controller.restore());
    expect(await screen.findByRole('alert')).toHaveTextContent('Restore storage access and retry.');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
    inaccessible = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(controller.getSnapshot().pending?.operation_id).toBe(original.operation_id);
    expect(storage.getItem(storageKey)).toBe(stored);
    expect(run).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: 'Retry original check' }));
    await screen.findByText('Retraction notice');
    expect(run.mock.calls[1][0].operation_id).toBe(original.operation_id);
  });

  it('keeps the actual LibraryPage detail mounted and its provider issue visible after a publication receipt', async () => {
    const { run } = setup();
    const refresh = vi.fn();
    const reference = item();
    owner.reads = {
      account: getRendererAccountScopeToken(),
      list: { key: 'list', status: 'ready', value: { schema: 'x-science.literature-page.v1', items: [reference] } },
      detail: { key: 'detail', status: 'ready', value: reference },
      projects: { key: 'projects', status: 'ready', value: [] },
      collections: {
        key: 'collections',
        status: 'ready',
        value: { schema: 'x-science.literature-collections.v1', collections: [] },
      },
      memberships: {
        key: 'memberships',
        status: 'ready',
        value: { schema: 'x-science.literature-collection-membership.v1', item_id: reference.id, collection_ids: [] },
      },
      refresh,
    } satisfies ReturnType<typeof useLibraryWorkbench>;
    savedRead(result({ snapshot: snapshot() }));
    run.mockImplementationOnce(async (command) => receipt(command.operation_id, result({ issue: 'rate-limited' })));
    await renderWithI18n(
      <MemoryRouter initialEntries={['/library?item=paper']}>
        <LibraryPage />
      </MemoryRouter>,
      'en-US'
    );
    await screen.findByText('Retraction notice');
    fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Crossref is receiving too many requests.');
    expect(refresh).not.toHaveBeenCalled();
    expect(screen.getByRole('heading', { name: 'Retractions and corrections' })).toBeInTheDocument();
    expect(screen.getByText('Retraction notice')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
  });
});

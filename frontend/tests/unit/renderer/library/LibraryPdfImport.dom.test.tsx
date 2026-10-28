import React, { useCallback, useState, useSyncExternalStore } from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type { LiteratureItem, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { LiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  createUploadIntent,
  encodePendingRecord,
  withArtifactReceipt,
  withInitReceipt,
} from '@/renderer/services/runtime/literatureUploadIntent';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import type { XScienceProject } from '@/renderer/services/xScienceGateway';
import { LibraryPdfImport } from '@/renderer/pages/library/LibraryPdfImport';
import { LibraryPdfImportPanel } from '@/renderer/pages/library/LibraryPdfImportPanel';
import { useLibraryPdfImport, type LibraryImportProjects } from '@/renderer/pages/library/useLibraryPdfImport';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import type { useLibraryWorkbench } from '@/renderer/pages/library/useLibraryWorkbench';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks, literaturePendingStorage } from '../conversation/literatureMutationLockFixtures';

const pageState = vi.hoisted(() => ({ controller: undefined as unknown, reads: undefined as unknown }));
vi.mock('@/renderer/hooks/chat/useLiteratureMutation', () => ({
  useLiteratureMutation: () => {
    const controller = pageState.controller as LiteratureMutationController;
    return {
      controller,
      view: useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot),
    };
  },
}));
vi.mock('@/renderer/pages/library/useLibraryWorkbench', () => ({
  useLibraryWorkbench: (_query: string, _cursor: string, selectedId: string) => {
    const reads = pageState.reads as ReturnType<typeof useLibraryWorkbench>;
    return {
      ...reads,
      detail: { key: 'detail:' + selectedId, status: 'ready', value: selectedId ? item(selectedId) : undefined },
    };
  },
}));
type Options = NonNullable<ConstructorParameters<typeof LiteratureMutationController>[1]>;
const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const hash = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad';
const key = 'x-science.literature-mutation.v1:owner';
const pdf = () => new File(['abc'], 'reference.pdf', { type: 'application/pdf' });
const ack = { upload_id: id(3), chunk_size: 2, total_chunks: 2 };
const artifact = {
  artifactId: 'source-artifact',
  versionId: 'source-version',
  filename: 'reference.pdf',
  sizeBytes: 3,
  checksum: hash,
};
function item(itemId = 'saved-reference', title = 'reference'): LiteratureItem {
  return {
    id: itemId,
    title,
    metadata_revision: 1,
    metadata: {},
    deleted: false,
    project_ids: ['project'],
    attachments: [
      {
        id: 'library-attachment',
        kind: 'fullText',
        versions: [
          {
            id: 'library-version',
            attachment_id: 'library-attachment',
            version_number: 1,
            filename: 'reference.pdf',
            mime_type: 'application/pdf',
            size_bytes: 3,
            checksum: hash,
            page_count: 1,
            available: true,
          },
        ],
      },
    ],
  };
}
function receipt(operation = id(1), title = 'reference'): LiteratureReceipt {
  return {
    schema: 'x-science.literature-receipt.v1',
    operation_id: operation,
    item: item('saved-reference', title),
    attachment_id: 'library-attachment',
    version_id: 'library-version',
  };
}
const project = (): XScienceProject => ({
  projectId: 'project',
  name: 'Owned research',
  latestConversationId: null,
  description: null,
  context: null,
  conversationCount: 0,
  artifactCount: 0,
  createdAt: null,
  updatedAt: null,
  lastActiveAt: null,
});
const projects = (
  status: LibraryImportProjects['status'] = 'ready',
  value: XScienceProject[] = [project()]
): LibraryImportProjects => ({ key: status, status, value });
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}
function setup(storage = literaturePendingStorage(), overrides: Options = {}, ownerId = 'owner') {
  let sequence = 0;
  const initialize = vi.fn<NonNullable<Options['initialize']>>().mockResolvedValue(ack);
  const upload = vi.fn<NonNullable<Options['upload']>>().mockResolvedValue(artifact);
  const digest = vi.fn<NonNullable<Options['digest']>>().mockResolvedValue(hash);
  const run = vi
    .fn<NonNullable<Options['run']>>()
    .mockImplementation(async (command) =>
      receipt(command.operation_id, 'title' in command ? command.title : 'reference')
    );
  const controller = new LiteratureMutationController(ownerId, {
    storage,
    initialize,
    upload,
    digest,
    run,
    uuid: () => id(++sequence),
    ...overrides,
  });
  return { controller, storage, initialize, upload, digest, run };
}
function Harness({
  controller,
  account = getRendererAccountScopeToken(),
  projectView = projects(),
  refresh = () => undefined,
  observe,
}: {
  controller: LiteratureMutationController;
  account?: string;
  projectView?: LibraryImportProjects;
  refresh?: () => void;
  observe?: (model: ReturnType<typeof useLibraryPdfImport>) => void;
}) {
  const [panel, setPanel] = useState({ account, open: false });
  const setOpen = useCallback(
    (open: boolean) =>
      setPanel((previous) => (account === getRendererAccountScopeToken() ? { account, open } : previous)),
    [account]
  );
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  const model = useLibraryPdfImport({
    controller,
    account,
    view,
    projects: projectView,
    open: panel.account === account && panel.open,
    setOpen,
  });
  observe?.(model);
  return (
    <main className='library-workbench'>
      <LibraryPdfImport model={model} />
      <LibraryPdfImportPanel model={model} refreshProjects={refresh} viewReference={model.completeSuccess} />
    </main>
  );
}
const input = () => screen.getByLabelText('Select a PDF file') as HTMLInputElement;
async function choose(user: ReturnType<typeof userEvent.setup>, file = pdf()) {
  await user.upload(input(), file);
  return screen.findByRole('dialog', { name: 'Import a PDF' });
}
function seed(storage: ReturnType<typeof literaturePendingStorage>, phase: 'init' | 'upload' | 'catalogue') {
  const initial = createUploadIntent({
    projectId: 'project',
    title: 'Frozen title',
    filename: 'reference.pdf',
    size: 3,
    contentType: 'application/pdf',
    hash,
    catalogueOperationId: id(1),
    initOperationId: id(2),
  });
  const pending =
    phase === 'init'
      ? initial
      : phase === 'upload'
        ? withInitReceipt(initial, ack)
        : withArtifactReceipt(withInitReceipt(initial, ack), artifact);
  storage.setItem(key, encodePendingRecord('owner', pending));
  return pending;
}
function Location() {
  return <output aria-label='Library location'>{useLocation().search}</output>;
}
function PageRoot({ initial = '/library' }: { initial?: string }) {
  return (
    <MemoryRouter initialEntries={[initial]}>
      <Routes>
        <Route
          path='/library'
          element={
            <>
              <LibraryPage />
              <Location />
            </>
          }
        />
      </Routes>
    </MemoryRouter>
  );
}
function pageReads() {
  const ready = <T,>(value: T) => ({ key: 'ready', status: 'ready' as const, value });
  return {
    account: getRendererAccountScopeToken(),
    list: ready({ schema: 'x-science.literature-page.v1' as const, items: [] as LiteratureItem[] }),
    detail: ready(undefined),
    projects: projects(),
    collections: ready({ schema: 'x-science.literature-collections.v1' as const, collections: [] }),
    memberships: ready(undefined),
    refresh: vi.fn(),
  };
}

describe('Library PDF import UI', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    installLiteratureLocks();
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
        new URL(String(input), 'http://localhost').pathname === '/api/settings/client' &&
        (init?.method ?? 'GET') === 'GET'
          ? Promise.resolve(new Response('{}', { headers: { 'Content-Type': 'application/json' } }))
          : Promise.reject(new Error('Unexpected UI fixture HTTP'))
      )
    );
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('clears the single chooser for same-file reselection and admits one explicit ready draft', async () => {
    const x = setup();
    const user = userEvent.setup();
    const selected = pdf();
    const held = deferred<LiteratureReceipt>();
    x.run.mockImplementationOnce(() => held.promise);
    const start = vi.spyOn(x.controller, 'importPdf');
    const cancel = vi.spyOn(x.controller, 'cancelPdfImport');
    await renderWithI18n(<Harness controller={x.controller} />, 'en-US');
    const click = vi.spyOn(input(), 'click');
    await user.click(screen.getByRole('button', { name: 'Import PDF' }));
    expect(click).toHaveBeenCalledTimes(1);
    const dialog = await choose(user, selected);
    expect(input().multiple).toBe(false);
    expect(input().accept).toBe('application/pdf,.pdf');
    expect(input().value).toBe('');
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Title' }), { target: { value: 'Changed' } });
    await user.upload(input(), selected);
    expect(within(dialog).getByRole('textbox', { name: 'Title' })).toHaveValue('reference');
    expect(within(dialog).getByRole('button', { name: 'Import' })).toBeDisabled();
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
    fireEvent.submit(dialog.querySelector('form')!);
    fireEvent.submit(dialog.querySelector('form')!);
    expect(start).toHaveBeenCalledTimes(1);
    expect(start.mock.calls[0][0]).toEqual({ projectId: 'project', title: 'reference', file: selected });
    await waitFor(() => expect(x.run).toHaveBeenCalledTimes(1));
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeDisabled();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Close' })).toBeEnabled());
    expect(cancel).toHaveBeenCalledWith(start.mock.results[0].value);
    await user.click(within(dialog).getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByRole('button', { name: 'Resume PDF import' })).toHaveFocus();
    held.resolve(receipt());
  });

  it('blocks first admission for loading failed empty or replaced project lists and retries existing reads', async () => {
    for (const state of ['loading', 'error', 'empty'] as const) {
      const x = setup();
      const user = userEvent.setup();
      const refresh = vi.fn();
      const start = vi.spyOn(x.controller, 'importPdf');
      const view = state === 'empty' ? projects('ready', []) : projects(state, []);
      const rendered = await renderWithI18n(
        <Harness controller={x.controller} projectView={view} refresh={refresh} />,
        'en-US'
      );
      const dialog = await choose(user);
      fireEvent.submit(dialog.querySelector('form')!);
      expect(start).not.toHaveBeenCalled();
      expect(within(dialog).getByRole('button', { name: 'Import' })).toBeDisabled();
      await user.click(within(dialog).getByRole('button', { name: 'Retry projects' }));
      expect(refresh).toHaveBeenCalledTimes(1);
      rendered.rerender(<Harness controller={x.controller} projectView={projects()} refresh={refresh} />);
      fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
      expect(within(dialog).getByRole('button', { name: 'Import' })).toBeEnabled();
      rendered.rerender(<Harness controller={x.controller} projectView={projects('ready', [])} refresh={refresh} />);
      fireEvent.submit(dialog.querySelector('form')!);
      expect(start).not.toHaveBeenCalled();
      rendered.unmount();
    }
  });

  it('restores frozen pending phases and retries without depending on the current project directory', async () => {
    for (const phase of ['init', 'upload', 'catalogue'] as const) {
      const storage = literaturePendingStorage();
      const original = seed(storage, phase);
      const x = setup(storage);
      const user = userEvent.setup();
      const held = deferred<LiteratureReceipt>();
      x.run.mockImplementationOnce(() => held.promise);
      const retry = vi.spyOn(x.controller, 'retry');
      const cancel = vi.spyOn(x.controller, 'cancelPdfImport');
      const rendered = await renderWithI18n(
        <Harness controller={x.controller} projectView={projects('error', [])} />,
        'en-US'
      );
      const dialog = await screen.findByRole('dialog', { name: 'Import a PDF' });
      expect(within(dialog).getByText('Frozen title')).toBeInTheDocument();
      expect(within(dialog).getByText('project', { exact: true })).toBeInTheDocument();
      expect(within(dialog).queryByRole('textbox')).toBeNull();
      expect(within(dialog).queryByRole('combobox')).toBeNull();
      const selected = pdf();
      if (phase !== 'catalogue') {
        expect(within(dialog).getByRole('button', { name: 'Retry original import' })).toBeDisabled();
        await user.upload(input(), selected);
      } else expect(within(dialog).queryByRole('button', { name: 'Choose original PDF' })).toBeNull();
      await user.click(within(dialog).getByRole('button', { name: 'Retry original import' }));
      expect(retry).toHaveBeenCalledWith(phase === 'catalogue' ? undefined : selected);
      await waitFor(() => expect(x.run).toHaveBeenCalledTimes(1));
      expect(x.initialize).toHaveBeenCalledTimes(phase === 'init' ? 1 : 0);
      expect(x.upload).toHaveBeenCalledTimes(phase === 'catalogue' ? 0 : 1);
      await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
      await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Close' })).toBeEnabled());
      expect(cancel).toHaveBeenCalledWith(retry.mock.results[0].value);
      await user.click(within(dialog).getByRole('button', { name: 'Close' }));
      expect(JSON.parse(storage.getItem(key)!).upload.operation_id).toBe(original.operation_id);
      held.resolve(receipt());
      rendered.unmount();
    }
  });

  it('fences account replacement late promises and unconfirmed acknowledgements', async () => {
    const first = setup();
    const old = deferred<LiteratureReceipt>();
    const user = userEvent.setup();
    vi.spyOn(first.controller, 'importPdf').mockImplementationOnce(() => old.promise);
    const oldCancel = vi.spyOn(first.controller, 'cancelPdfImport');
    const rendered = await renderWithI18n(<Harness controller={first.controller} />, 'en-US');
    let dialog = await choose(user);
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
    fireEvent.submit(dialog.querySelector('form')!);
    act(() => setRendererAccountOwner('next-owner'));
    const next = setup(undefined, {}, 'next-owner');
    const current = deferred<LiteratureReceipt>();
    const nextStart = vi.spyOn(next.controller, 'importPdf').mockImplementationOnce(() => current.promise);
    rendered.rerender(<Harness controller={next.controller} account={getRendererAccountScopeToken()} />);
    expect(oldCancel).toHaveBeenCalledWith(old.promise);
    dialog = await choose(user, new File(['abc'], 'next.pdf', { type: 'application/pdf' }));
    expect(within(dialog).getByRole('combobox', { name: 'Project' })).toHaveValue('');
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
    fireEvent.submit(dialog.querySelector('form')!);
    await act(async () => {
      old.resolve(receipt(id(1), 'Old reference'));
      await old.promise;
    });
    expect(screen.queryByText('Old reference is saved in your Library.')).toBeNull();
    expect(within(dialog).getByRole('textbox', { name: 'Title' })).toHaveValue('next');
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeDisabled();
    await act(async () => {
      current.resolve(receipt(id(8), 'Next reference'));
      await current.promise;
    });
    expect(await within(dialog).findByText('Next reference is saved in your Library.')).toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: 'Close' }));
    nextStart.mockResolvedValueOnce({
      ...receipt(id(9), 'Unconfirmed'),
      attachment_id: undefined,
      version_id: undefined,
    });
    dialog = await choose(user, new File(['abc'], 'another.pdf', { type: 'application/pdf' }));
    fireEvent.submit(dialog.querySelector('form')!);
    expect(
      await within(dialog).findByText('The import did not return a confirmed Library reference.')
    ).toBeInTheDocument();
    expect(within(dialog).queryByRole('button', { name: 'View reference' })).toBeNull();
  });

  it('passes only its own original promise to route cleanup and leaves a subscriber transfer alone', async () => {
    const x = setup();
    const publicReply = deferred<LiteratureReceipt>();
    const user = userEvent.setup();
    vi.spyOn(x.controller, 'importPdf').mockImplementationOnce(() => publicReply.promise);
    const cancel = vi.spyOn(x.controller, 'cancelPdfImport');
    const rendered = await renderWithI18n(<Harness controller={x.controller} />, 'en-US');
    const dialog = await choose(user);
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
    fireEvent.submit(dialog.querySelector('form')!);
    rendered.unmount();
    expect(cancel).toHaveBeenCalledWith(publicReply.promise);
    await act(async () => {
      publicReply.resolve(receipt());
      await publicReply.promise;
    });
    const storage = literaturePendingStorage();
    seed(storage, 'upload');
    const subscriber = setup(storage);
    const held = deferred<typeof artifact>();
    subscriber.upload.mockImplementationOnce(() => held.promise);
    const external = subscriber.controller.retry(pdf());
    const rejected = expect(external).rejects.toMatchObject({ name: 'AbortError' });
    await waitFor(() => expect(subscriber.upload).toHaveBeenCalledTimes(1));
    const subscriberCancel = vi.spyOn(subscriber.controller, 'cancelPdfImport');
    const mounted = await renderWithI18n(<Harness controller={subscriber.controller} />, 'en-US');
    await screen.findByRole('dialog', { name: 'Import a PDF' });
    mounted.unmount();
    expect(subscriberCancel).not.toHaveBeenCalled();
    subscriber.controller.cancelPdfImport(external);
    await rejected;
    held.resolve(artifact);
  });

  it('offers discard only for the confirmed rejection and explains that project files remain', async () => {
    const storage = literaturePendingStorage();
    seed(storage, 'catalogue');
    const x = setup(storage);
    const user = userEvent.setup();
    x.run.mockRejectedValueOnce(
      new BackendHttpError({
        method: 'POST',
        path: '/api/literature/import-artifact-version',
        status: 400,
        body: { code: 'LITERATURE_INVALID' },
      })
    );
    const discard = vi.spyOn(x.controller, 'discardRejectedPdfImport');
    await renderWithI18n(<Harness controller={x.controller} projectView={projects('error', [])} />, 'en-US');
    const dialog = await screen.findByRole('dialog', { name: 'Import a PDF' });
    expect(within(dialog).queryByRole('button', { name: 'Discard pending import' })).toBeNull();
    await user.click(within(dialog).getByRole('button', { name: 'Retry original import' }));
    expect(
      await within(dialog).findByText('This removes only the local pending request. The uploaded project file is kept.')
    ).toBeInTheDocument();
    await user.click(within(dialog).getByRole('button', { name: 'Discard pending import' }));
    await waitFor(() => expect(storage.getItem(key)).toBeNull());
    expect(discard).toHaveBeenCalledTimes(1);
    expect(x.initialize).not.toHaveBeenCalled();
    expect(x.upload).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });

  it('refreshes on the real receipt once and uses read-only retry plus explicit reference navigation', async () => {
    const x = setup();
    const reads = pageReads();
    pageState.controller = x.controller;
    pageState.reads = reads;
    const held = deferred<LiteratureReceipt>();
    x.run.mockImplementationOnce(() => held.promise);
    const start = vi.spyOn(x.controller, 'importPdf');
    const user = userEvent.setup();
    const tree = <PageRoot initial='/library?q=keep&collection=col&cursor=next&item=old' />;
    const rendered = await renderWithI18n(tree, 'en-US');
    const dialog = await choose(user);
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
    fireEvent.submit(dialog.querySelector('form')!);
    await waitFor(() => expect(x.run).toHaveBeenCalledTimes(1));
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search Library' }), { target: { value: 'changed' } });
    const before = screen.getByLabelText('Library location').textContent;
    await act(async () => {
      held.resolve(receipt());
      await held.promise;
    });
    await screen.findByRole('button', { name: 'View reference' });
    expect(reads.refresh).toHaveBeenCalledTimes(1);
    expect(screen.getByLabelText('Library location').textContent).toBe(before);
    pageState.reads = { ...reads, list: { key: 'read-error', status: 'error' } };
    rendered.rerender(<PageRoot initial='/library?q=keep&collection=col&cursor=next&item=old' />);
    await user.click(
      within(screen.getByRole('region', { name: 'Library papers' })).getByRole('button', { name: 'Retry' })
    );
    expect(reads.refresh).toHaveBeenCalledTimes(2);
    expect(start).toHaveBeenCalledTimes(1);
    expect(x.upload).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('button', { name: 'View reference' }));
    expect(screen.getByLabelText('Library location')).toHaveTextContent('?item=saved-reference');
    expect(screen.getByRole('heading', { name: 'reference' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /Preview PDF/ })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Import a PDF' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Edit details' })).toBeEnabled();
    expect(screen.getByRole('combobox', { name: 'Reading status for reference' })).toBeEnabled();
  });

  it('hands off only its current completed receipt and retains a newly pending upload', async () => {
    const x = setup();
    const discard = vi.spyOn(x.controller, 'discardRejectedPdfImport');
    let latest!: ReturnType<typeof useLibraryPdfImport>;
    const user = userEvent.setup();
    await renderWithI18n(
      <Harness
        controller={x.controller}
        observe={(value) => {
          latest = value;
        }}
      />,
      'en-US'
    );
    const dialog = await choose(user);
    fireEvent.change(within(dialog).getByRole('combobox', { name: 'Project' }), { target: { value: 'project' } });
    fireEvent.submit(dialog.querySelector('form')!);
    await screen.findByRole('button', { name: 'View reference' });
    const completed = latest.success!;
    const handoff = latest.completeSuccess;
    expect(handoff({ ...completed, item: { ...completed.item, id: 'unrelated' } })).toBe(false);
    await user.click(screen.getByRole('button', { name: 'View reference' }));
    expect(screen.queryByRole('dialog', { name: 'Import a PDF' })).toBeNull();
    expect(x.controller.getSnapshot().receipt).toEqual(completed);
    act(() => latest.openPanel());
    const held = deferred<typeof artifact>();
    x.upload.mockImplementationOnce(() => held.promise);
    let flight!: Promise<import('@/renderer/services/runtime/LiteratureMutationController').LiteratureMutationReceipt>;
    act(() => {
      flight = x.controller.importPdf({ projectId: 'project', title: 'Next reference', file: pdf() });
      expect(handoff(completed)).toBe(false);
    });
    const rejected = expect(flight).rejects.toMatchObject({ name: 'AbortError' });
    await waitFor(() => expect(x.upload).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('dialog', { name: 'Import a PDF' })).toBeInTheDocument();
    expect(x.controller.getSnapshot().pending).toBeDefined();
    expect(discard).not.toHaveBeenCalled();
    act(() => x.controller.cancelPdfImport(flight));
    await rejected;
    expect(x.storage.getItem(key)).not.toBeNull();
    held.resolve(artifact);
  });

  it('coordinates the inline form with collection editing and provides the same Chinese controls', async () => {
    const x = setup();
    const reads = pageReads();
    pageState.controller = x.controller;
    pageState.reads = reads;
    const user = userEvent.setup();
    const rendered = await renderWithI18n(<PageRoot />, 'en-US');
    await user.click(screen.getByRole('button', { name: 'New collection' }));
    const collectionDialog = screen.getByRole('dialog');
    expect(collectionDialog).toHaveAttribute('aria-modal', 'true');
    expect(screen.getByRole('button', { name: 'Import PDF' })).toBeDisabled();
    await user.click(within(collectionDialog).getByRole('button', { name: 'Cancel' }));
    const dialog = await choose(user);
    expect(dialog).toHaveAttribute('aria-modal', 'false');
    expect(screen.getByRole('button', { name: 'New collection' })).toBeDisabled();
    await user.click(within(dialog).getByRole('button', { name: 'Close' }));
    expect(screen.getByRole('button', { name: 'Import PDF' })).toHaveFocus();
    const start = vi.spyOn(x.controller, 'importPdf');
    const legacy = deferred<LiteratureReceipt>();
    x.run.mockImplementationOnce(() => legacy.promise);
    let change!: Promise<LiteratureReceipt>;
    act(() => {
      change = x.controller.submit({
        kind: 'edit',
        operation_id: id(90),
        item_id: 'old',
        expected_revision: 1,
        title: 'Old title',
      });
    });
    await waitFor(() => expect(x.run).toHaveBeenCalledTimes(1));
    await user.click(screen.getByRole('button', { name: 'Import PDF' }));
    expect(screen.getByText('Finish the existing Library change before importing another PDF.')).toBeInTheDocument();
    fireEvent.submit(screen.getByRole('dialog').querySelector('form')!);
    expect(start).not.toHaveBeenCalled();
    await act(async () => {
      legacy.resolve({ ...receipt(id(90), 'Old title'), item: item('old', 'Old title') });
      await change;
    });
    rendered.unmount();
    const chinese = setup();
    pageState.controller = chinese.controller;
    pageState.reads = pageReads();
    await renderWithI18n(<PageRoot />, 'zh-CN');
    expect(screen.getByRole('button', { name: '导入 PDF' })).toBeInTheDocument();
    await user.upload(screen.getByLabelText('选择 PDF 文件'), pdf());
    const translated = await screen.findByRole('dialog', { name: '导入 PDF 文献' });
    expect(within(translated).getByRole('combobox', { name: '项目' })).toBeInTheDocument();
    expect(within(translated).getByRole('textbox', { name: '标题' })).toHaveValue('reference');
  });
});

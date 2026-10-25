import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ReadingContextPicker } from '@/renderer/components/chat/SendBox/reading/ReadingContextPicker';
import { PdfReadingBar } from '@/renderer/components/chat/SendBox/reading/PdfReadingBar';
import type { PdfReadingController } from '@/renderer/services/runtime/PdfReadingController';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { renderWithI18n } from '../../i18nTestUtils';
import { deferredPdf, pdfContext } from './pdfReadingFixtures';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from './literatureMutationLockFixtures';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';

const mocks = vi.hoisted(() => ({ project: vi.fn(), library: vi.fn(), run: vi.fn(), runCollection: vi.fn() }));
vi.mock('@/renderer/services/runtime/pdfReadingContext', async (original) => ({
  ...(await original<typeof import('@/renderer/services/runtime/pdfReadingContext')>()),
  loadProjectReadingOptions: mocks.project,
}));
vi.mock('@/renderer/services/runtime/literatureLibrary', async (original) => ({
  ...(await original<typeof import('@/renderer/services/runtime/literatureLibrary')>()),
  loadLibraryReadingOptions: mocks.library,
  runLiteratureCommand: mocks.run,
}));
vi.mock('@/renderer/services/runtime/literatureCollections', async (original) => ({
  ...(await original<typeof import('@/renderer/services/runtime/literatureCollections')>()),
  runLiteratureCollectionCommand: mocks.runCollection,
}));
const option = {
  name: 'Independent paper',
  pageCount: 9,
  source: {
    source_kind: 'literature-attachment-version' as const,
    item_id: 'item',
    attachment_id: 'attachment',
    version_id: 'library-version',
  },
};

describe('L382 Library reading picker', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    localStorage.clear();
    mocks.project.mockReset().mockResolvedValue({ options: [], unavailable: 0 });
    mocks.library.mockReset().mockImplementation(async (_context, request) => ({
      options: request.projectId ? [] : [option],
      unavailable: 0,
    }));
    mocks.run.mockReset();
    mocks.runCollection.mockReset();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it('keeps the mounted reading picker usable after the shared owner acknowledges a collection', async () => {
    const select = vi.fn().mockResolvedValue(undefined);
    mocks.runCollection.mockImplementation(async (command) => ({
      schema: 'x-science.literature-collection-receipt.v1',
      operation_id: command.operation_id,
      action: 'create',
      collection_id: 'collection',
      collection: {
        id: 'collection',
        revision: 1,
        name: 'Methods',
        description: '',
        parent_id: null,
        sort_order: 0,
        item_count: 0,
      },
    }));
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={select}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: /^Library$/ }));
    await screen.findByRole('option', { name: 'Independent paper' });
    const readsBefore = mocks.library.mock.calls.length;
    const command = {
      kind: 'collection-create' as const,
      operation_id: crypto.randomUUID(),
      name: 'Methods',
      description: '',
    };
    await act(async () => {
      await getLiteratureMutationController('owner').submit(command);
    });
    await waitFor(() => expect(mocks.library.mock.calls.length).toBeGreaterThan(readsBefore));
    fireEvent.click(await screen.findByRole('option', { name: 'Independent paper' }));
    await waitFor(() => expect(select).toHaveBeenCalledExactlyOnceWith(option.source));
    expect(mocks.runCollection).toHaveBeenCalledExactlyOnceWith(command);
    expect(mocks.run).not.toHaveBeenCalled();
  });
  it('offers a first Library selection from the real empty conversation bar', async () => {
    const link = vi.fn().mockResolvedValue(undefined);
    await renderWithI18n(
      <PdfReadingBar
        view={{
          context: pdfContext([], 0),
          loading: false,
          mutating: false,
          blocked: false,
          pending: null,
          error: null,
        }}
        controller={{ link } as unknown as PdfReadingController}
        onOpen={vi.fn()}
      />,
      'en-US'
    );
    fireEvent.click(screen.getByRole('button', { name: 'Choose PDFs for Reading' }));
    fireEvent.click(await screen.findByRole('button', { name: /^Library$/ }));
    fireEvent.click(await screen.findByRole('option', { name: 'Independent paper' }));
    expect(link).toHaveBeenCalledExactlyOnceWith([option.source]);
  });
  it('loads real Library sources and awaits the existing link owner without sending a message', async () => {
    const link = deferredPdf<void>();
    const select = vi.fn(() => link.promise);
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={select}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: /^Library$/ }));
    fireEvent.click(await screen.findByRole('option', { name: 'Independent paper' }));
    expect(select).toHaveBeenCalledExactlyOnceWith(option.source);
    expect((screen.getByRole('option', { name: 'Independent paper' }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => {
      link.resolve();
      await link.promise;
    });
    expect(screen.queryByRole('listbox')).toBeNull();
  });
  it('never publishes a closed or previous-source Library result after reopening Project', async () => {
    const old = deferredPdf<{ options: (typeof option)[]; unavailable: number }>();
    mocks.library.mockImplementation((_context, request) =>
      request.projectId ? Promise.resolve({ options: [], unavailable: 0 }) : old.promise
    );
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={vi.fn()}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: /^Library$/ }));
    fireEvent.click(screen.getByRole('button', { name: /^Project$/ }));
    await act(async () => {
      old.resolve({ options: [option], unavailable: 0 });
      await old.promise;
    });
    expect(screen.queryByRole('option', { name: 'Independent paper' })).toBeNull();
  });
  it('clears only its completed selection after closing and reopening during a link', async () => {
    const link = deferredPdf<void>();
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={() => link.promise}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: /^Library$/ }));
    fireEvent.click(await screen.findByRole('option', { name: 'Independent paper' }));
    fireEvent.keyDown(screen.getByRole('option', { name: 'Independent paper' }), { key: 'Escape' });
    fireEvent.click(screen.getByText('choose source'));
    await act(async () => {
      link.resolve();
      await link.promise;
    });
    expect(await screen.findByRole('option', { name: 'Independent paper' })).not.toBeDisabled();
    expect(screen.getByRole('button', { name: /^Project$/ })).not.toBeDisabled();
  });
  it('rejects an old continuation page when a Library receipt refreshes the list', async () => {
    const page = deferredPdf<{ options: (typeof option)[]; unavailable: number; nextCursor?: string }>();
    const editable: typeof option & { item: LiteratureItem } = {
      ...option,
      item: {
        id: 'item',
        title: option.name,
        metadata_revision: 1,
        metadata: {},
        deleted: false,
        project_ids: [],
        attachments: [],
      },
    };
    let reads = 0;
    mocks.library.mockImplementation(async (_context, request) => {
      if (request.projectId) return { options: [], unavailable: 0 };
      if (request.cursor) return page.promise;
      reads++;
      return { options: [editable], unavailable: 0, ...(reads === 1 ? { nextCursor: 'old-page' } : {}) };
    });
    mocks.run.mockImplementation(async (command) => ({
      schema: 'x-science.literature-receipt.v1',
      operation_id: command.operation_id,
      item: { ...editable.item, project_ids: ['project'] },
    }));
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={vi.fn()}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: /^Library$/ }));
    fireEvent.click(await screen.findByRole('button', { name: 'Load more papers' }));
    fireEvent.click(screen.getByRole('button', { name: 'Edit literature Independent paper' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Add to project' }));
    await screen.findByText('Library updated · Independent paper');
    await act(async () => {
      page.resolve({
        options: [{ ...option, name: 'Obsolete paper', source: { ...option.source, version_id: 'old-version' } }],
        unavailable: 0,
        nextCursor: 'obsolete-cursor',
      });
      await page.promise;
    });
    expect(screen.queryByRole('option', { name: 'Obsolete paper' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Load more papers' })).toBeNull();
  });
  it('imports the exact Project version and reconciles a lost receipt with the original command', async () => {
    mocks.project.mockResolvedValue({
      options: [
        { name: 'source.pdf', pageCount: 9, source: { artifact_id: 'artifact', version_id: 'exact-project-v1' } },
      ],
      unavailable: 0,
    });
    mocks.run.mockRejectedValueOnce(new TypeError('lost acknowledgement')).mockImplementationOnce(async (command) => ({
      schema: 'x-science.literature-receipt.v1',
      operation_id: command.operation_id,
      item: {
        id: 'item',
        title: command.title,
        metadata_revision: 1,
        metadata: {},
        deleted: false,
        project_ids: ['project'],
        attachments: [],
      },
      attachment_id: 'attachment',
      version_id: 'library-version',
    }));
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={vi.fn()}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: 'Save source.pdf to Library' }));
    await screen.findByText(/Library change was sent/);
    expect(mocks.run.mock.calls[0][0]).toMatchObject({
      kind: 'import',
      artifact_id: 'artifact',
      version_id: 'exact-project-v1',
      title: 'source.pdf',
      project_id: 'project',
    });
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByText('Library updated · source.pdf');
    expect(mocks.run.mock.calls).toHaveLength(2);
    expect(mocks.run.mock.calls[1][0]).toEqual(mocks.run.mock.calls[0][0]);
  });

  it('recovers the original pending import through Retry after storage access returns', async () => {
    const original = {
      kind: 'import',
      operation_id: '00000000-0000-4000-8000-000000000099',
      artifact_id: 'artifact',
      version_id: 'exact-project-v1',
      title: 'Recovered paper',
      project_id: 'project',
    };
    const key = 'x-science.literature-mutation.v1:owner';
    localStorage.setItem(key, JSON.stringify({ ownerId: 'owner', command: original }));
    let blocked = true;
    const remove = Storage.prototype.removeItem;
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(function (this: Storage, storageKey: string) {
      if (storageKey === key && blocked) throw new Error('storage access unavailable');
      return remove.call(this, storageKey);
    });
    mocks.project.mockResolvedValue({
      options: [{ name: 'next.pdf', pageCount: 9, source: { artifact_id: 'next-artifact', version_id: 'next-v1' } }],
      unavailable: 0,
    });
    mocks.run.mockImplementation(async (submitted) => ({
      schema: 'x-science.literature-receipt.v1',
      operation_id: submitted.operation_id,
      item: {
        id: 'recovered',
        title: submitted.title,
        metadata_revision: 1,
        metadata: {},
        deleted: false,
        project_ids: ['project'],
        attachments: [],
      },
      attachment_id: 'attachment',
      version_id: 'library-version',
    }));
    await renderWithI18n(
      <ReadingContextPicker context={pdfContext([], 0)} pending={false} onSelect={vi.fn()}>
        <button>choose source</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('choose source'));
    fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
    await screen.findByText(/original Library request could not be safely saved/);
    expect(mocks.run).toHaveBeenCalledTimes(1);
    expect(JSON.parse(localStorage.getItem(key)!)).toEqual({ ownerId: 'owner', command: original });
    blocked = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    // A restored historical request is not this window's lastAction success toast.
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
    expect(mocks.run.mock.calls.map(([value]) => value)).toEqual([original, original]);
    expect(localStorage.getItem(key)).toBeNull();
    const next = await screen.findByRole('button', { name: 'Save next.pdf to Library' });
    expect(next).not.toBeDisabled();
    fireEvent.click(next);
    await screen.findByText('Library updated · next.pdf');
    expect(mocks.run).toHaveBeenCalledTimes(3);
    expect(mocks.run.mock.calls[2][0]).toMatchObject({
      kind: 'import',
      artifact_id: 'next-artifact',
      version_id: 'next-v1',
      title: 'next.pdf',
      project_id: 'project',
    });
    expect(mocks.run.mock.calls[2][0].operation_id).not.toBe(original.operation_id);
    expect(localStorage.getItem(key)).toBeNull();
  });
});

import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import {
  createUploadIntent,
  encodePendingRecord,
  withArtifactReceipt,
  withInitReceipt,
} from '@/renderer/services/runtime/literatureUploadIntent';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';

const previewLifecycle = vi.hoisted(() => ({ mounts: 0, unmounts: 0 }));
// Instrument only the shared PDF child lifecycle/local state. This does not render or validate a PDF.
vi.mock('@/renderer/pages/library/LibraryAttachmentPreview', () => ({
  LibraryAttachmentPreview: ({ contentUrl }: { contentUrl: string }) => {
    const [page, setPage] = React.useState(1);
    React.useEffect(() => {
      ++previewLifecycle.mounts;
      return () => {
        ++previewLifecycle.unmounts;
      };
    }, []);
    return (
      <div data-testid='instrumented-library-pdf' data-content-url={contentUrl}>
        <output aria-label='Instrumented PDF page'>{page}</output>
        <button type='button' onClick={() => setPage(2)}>
          Advance fixture page
        </button>
      </div>
    );
  },
}));

const checksum = 'a'.repeat(64);
const checkedAt = '2026-10-20T08:09:10Z';
const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const paper = (itemId = 'paper-a', extra: Partial<LiteratureItem> = {}): LiteratureItem => ({
  id: itemId,
  title: `Synthetic ${itemId}`,
  metadata_revision: 1,
  reading_status: 'unread',
  metadata: { doi: `10.1234/${itemId}`, authors: ['A. Author'] },
  deleted: false,
  project_ids: ['project'],
  attachments: [
    {
      id: `attachment-${itemId}`,
      kind: 'fullText',
      versions: [
        {
          id: `version-${itemId}`,
          attachment_id: `attachment-${itemId}`,
          version_number: 1,
          filename: 'reference.pdf',
          mime_type: 'application/pdf',
          size_bytes: 3,
          checksum,
          page_count: 2,
          available: true,
        },
      ],
    },
  ],
  ...extra,
});
const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
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
type Call = { method: string; url: URL; body?: Record<string, unknown> };
function catalogue(initial = [paper()]) {
  const items = new Map(initial.map((item) => [item.id, item]));
  const calls: Call[] = [];
  const replies = new Map<string, Array<Response | Promise<Response>>>();
  const listReplies: Array<Response | Promise<Response>> = [];
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    const method = init?.method ?? 'GET';
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as Record<string, unknown>) : undefined;
    calls.push({ method, url, body });
    if (method === 'GET' && url.pathname === '/api/projects')
      return json({ projects: [{ project_id: 'project', name: 'Owned project' }] });
    if (method === 'GET' && url.pathname === '/api/literature/collections')
      return json({ schema: 'x-science.literature-collections.v1', collections: [] });
    const membership = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/collections$/);
    if (method === 'GET' && membership)
      return json({
        schema: 'x-science.literature-collection-membership.v1',
        item_id: decodeURIComponent(membership[1]),
        collection_ids: [],
      });
    const publication = url.pathname.match(/^\/api\/literature\/items\/([^/]+)\/publication-check$/);
    if (method === 'GET' && publication) {
      const item = items.get(decodeURIComponent(publication[1]))!;
      return json({
        schema: 'x-science.literature-publication-check.v1',
        item_id: item.id,
        metadata_revision: Number(url.searchParams.get('expected_metadata_revision')),
        doi: item.metadata.doi,
        snapshot: { doi: item.metadata.doi, checked_at: checkedAt, notices: [] },
      });
    }
    if (method === 'POST' && url.pathname === '/api/literature/items/reading-status') {
      const targets = [...new Set(body!.item_ids as string[])];
      for (const target of targets)
        items.set(target, { ...items.get(target)!, reading_status: body!.reading_status as LiteratureReadingStatus });
      return json({
        schema: 'x-science.literature-reading-status-receipt.v1',
        operation_id: body!.operation_id,
        item_ids: targets,
        reading_status: body!.reading_status,
      });
    }
    if (method === 'POST' && url.pathname === '/api/literature/import-artifact-version') {
      const item = paper('paper-imported', { title: String(body!.title) });
      items.set(item.id, item);
      return json({
        schema: 'x-science.literature-receipt.v1',
        operation_id: body!.operation_id,
        item,
        attachment_id: item.attachments[0].id,
        version_id: item.attachments[0].versions[0].id,
      });
    }
    if (method === 'GET' && url.pathname === '/api/literature/items')
      return (
        listReplies.shift() ??
        json({
          schema: 'x-science.literature-page.v1',
          items: [...items.values()].filter(
            (item) =>
              !item.deleted &&
              (!url.searchParams.has('reading_status') ||
                item.reading_status === url.searchParams.get('reading_status'))
          ),
        })
      );
    const detail = url.pathname.match(/^\/api\/literature\/items\/([^/]+)$/);
    if (method === 'GET' && detail) {
      const itemId = decodeURIComponent(detail[1]);
      const reply = replies.get(itemId)?.shift();
      return reply ?? json(items.get(itemId));
    }
    throw new Error(`Unexpected synthetic Library request: ${method} ${url.pathname}`);
  });
  vi.stubGlobal('fetch', fetch);
  return {
    items,
    calls,
    fetch,
    count: (path: string, method = 'GET') =>
      calls.filter((call) => call.method === method && call.url.pathname === path).length,
    nextDetail: (itemId: string, reply: Response | Promise<Response>) =>
      replies.set(itemId, [...(replies.get(itemId) ?? []), reply]),
    nextList: (reply: Response | Promise<Response>) => listReplies.push(reply),
  };
}
function RouteControls() {
  const location = useLocation();
  const navigate = useNavigate();
  return (
    <>
      <output aria-label='Library location'>{location.search}</output>
      <button type='button' onClick={() => navigate('/library?item=paper-b')}>
        Go to route B
      </button>
    </>
  );
}
const mount = (initial = '/library?item=paper-a') =>
  renderWithI18n(
    <MemoryRouter initialEntries={[initial]}>
      <LibraryPage />
      <RouteControls />
    </MemoryRouter>,
    'en-US'
  );
const detail = () => within(detailsAside()).getAllByRole('region', { name: 'Literature details' })[0];
const detailsAside = () => screen.getByRole('complementary', { name: 'Literature details' });
const list = () => screen.getByRole('region', { name: 'Library papers' });
const locationParams = () => new URLSearchParams(screen.getByLabelText('Library location').textContent ?? '');
const statusControl = () => within(detail()).getByRole('combobox', { name: /^Reading status for / });
async function ready(title = 'Synthetic paper-a') {
  await screen.findByRole('heading', { name: title });
  await waitFor(() => expect(statusControl()).toBeEnabled());
}
function draft(value = 'Unsaved local title') {
  fireEvent.click(within(detail()).getByRole('button', { name: 'Edit details' }));
  const input = within(detail()).getByRole('textbox', { name: 'Title' });
  fireEvent.change(input, { target: { value } });
  return input;
}
const changeStatus = (value: LiteratureReadingStatus) => fireEvent.change(statusControl(), { target: { value } });
async function savedStatus(value: LiteratureReadingStatus) {
  await waitFor(() => {
    expect(statusControl()).toBeEnabled();
    expect(statusControl()).toHaveValue(value);
  });
}
function openPreview() {
  fireEvent.click(within(detail()).getByRole('button', { name: /Preview PDF/ }));
  fireEvent.click(screen.getByRole('button', { name: 'Advance fixture page' }));
  return screen.getByTestId('instrumented-library-pdf');
}

describe('Reading status through the actual Library Page and mutation owner', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('reading-status-integration-owner');
    localStorage.clear();
    installLiteratureLocks();
    previewLifecycle.mounts = 0;
    previewLifecycle.unmounts = 0;
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('preserves the same-revision draft, PDF child and publication evidence after the real status ACK and readback', async () => {
    const api = catalogue();
    const rendered = await mount();
    await ready();
    await screen.findByText('No related notices found.');
    const publicationTime = rendered.container.querySelector(`time[datetime="${checkedAt}"]`);
    const preview = openPreview();
    const input = draft();
    const baseline = [
      '/api/projects',
      '/api/literature/collections',
      '/api/literature/items/paper-a/collections',
      '/api/literature/items/paper-a/publication-check',
    ].map((path) => api.count(path));
    changeStatus('read');
    await savedStatus('read');
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Unsaved local title');
    expect(screen.getByTestId('instrumented-library-pdf')).toBe(preview);
    expect(screen.getByLabelText('Instrumented PDF page')).toHaveTextContent('2');
    expect(previewLifecycle).toEqual({ mounts: 1, unmounts: 0 });
    expect(rendered.container.querySelector(`time[datetime="${checkedAt}"]`)).toBe(publicationTime);
    expect(
      [
        '/api/projects',
        '/api/literature/collections',
        '/api/literature/items/paper-a/collections',
        '/api/literature/items/paper-a/publication-check',
      ].map((path) => api.count(path))
    ).toEqual(baseline);
    const writes = api.calls.filter((call) => call.method !== 'GET');
    expect(writes).toHaveLength(1);
    expect(writes[0].url.pathname).toBe('/api/literature/items/reading-status');
    expect(writes[0].body).toEqual({ operation_id: expect.any(String), item_ids: ['paper-a'], reading_status: 'read' });
    expect(rendered.container.querySelector('button button')).toBeNull();
  });

  it('keeps an old edit-base draft when status readback observes external metadata and blocks stale saving until explicit reload', async () => {
    const api = catalogue();
    await mount();
    await ready();
    const input = draft();
    api.items.set('paper-a', paper('paper-a', { metadata_revision: 2, title: 'External title' }));
    changeStatus('reading');
    await savedStatus('reading');
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Unsaved local title');
    expect(input).toBeDisabled();
    expect(within(detail()).getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
    expect(await screen.findByText(/This paper was updated elsewhere/)).toBeInTheDocument();
    fireEvent.click(within(detail()).getByRole('button', { name: 'Save', exact: true }));
    expect(api.calls.filter((call) => call.method === 'PATCH')).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Discard draft and load latest' }));
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toHaveValue('External title');
    expect(within(detail()).getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
  });

  it('keeps the selected detail when its filtered row disappears and restores close focus to the stable list', async () => {
    catalogue();
    await mount('/library?reading_status=unread&item=paper-a');
    await ready();
    changeStatus('read');
    await savedStatus('read');
    await waitFor(() => expect(within(list()).queryByRole('button', { name: /Synthetic paper-a/ })).toBeNull());
    expect(locationParams().get('item')).toBe('paper-a');
    expect(locationParams().get('reading_status')).toBe('unread');
    expect(within(detail()).getByRole('heading', { name: 'Synthetic paper-a' })).toBeInTheDocument();
    const close = within(detail()).getByRole('button', { name: 'Close', exact: true });
    close.focus();
    fireEvent.click(close);
    expect(screen.queryByRole('region', { name: 'Literature details' })).toBeNull();
    expect(locationParams().has('item')).toBe(false);
    expect(list()).toHaveFocus();
  });

  it('retains read-only draft evidence after 503, retries only GET, and clears unavailable or invalid details', async () => {
    const api = catalogue();
    await mount();
    await ready();
    const input = draft();
    openPreview();
    api.nextDetail('paper-a', json({ error: 'Unavailable' }, 503));
    changeStatus('read');
    await screen.findByText('Details could not be refreshed. Your draft is retained; retry to continue editing.');
    expect(input).toHaveValue('Unsaved local title');
    expect(input).toBeDisabled();
    expect(within(detail()).getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
    expect(screen.getByTestId('instrumented-library-pdf')).toBeInTheDocument();
    fireEvent.click(within(detailsAside()).getByRole('button', { name: 'Retry', exact: true }));
    await savedStatus('read');
    expect(input).toBeEnabled();
    expect(input).toHaveValue('Unsaved local title');
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
    for (const [value, reply] of [
      ['reading', json({ error: 'Not found' }, 404)],
      ['unread', json(paper('wrong-item'))],
    ] as const) {
      api.nextDetail('paper-a', reply);
      changeStatus(value);
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Literature details' })).toBeNull());
      expect(screen.queryByRole('textbox', { name: 'Title' })).toBeNull();
      expect(screen.queryByTestId('instrumented-library-pdf')).toBeNull();
      expect(screen.queryByRole('heading', { name: 'Retractions and corrections' })).toBeNull();
      if (value === 'reading') {
        fireEvent.click(within(detailsAside()).getByRole('button', { name: 'Retry', exact: true }));
        await ready();
        draft('Second unsaved draft');
        openPreview();
      }
    }
  });

  it('preserves the draft and PDF instance through a failed list readback and its GET-only retry', async () => {
    const api = catalogue();
    await mount();
    await ready();
    const input = draft();
    const preview = openPreview();
    api.nextList(json({ error: 'Unavailable' }, 503));
    changeStatus('read');
    await savedStatus('read');
    const retry = await within(list()).findByRole('button', { name: 'Retry', exact: true });
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Unsaved local title');
    expect(screen.getByTestId('instrumented-library-pdf')).toBe(preview);
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
    fireEvent.click(retry);
    await within(list()).findByRole('button', { name: /Synthetic paper-a/ });
    await savedStatus('read');
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Unsaved local title');
    expect(screen.getByTestId('instrumented-library-pdf')).toBe(preview);
    expect(screen.getByLabelText('Instrumented PDF page')).toHaveTextContent('2');
    expect(previewLifecycle).toEqual({ mounts: 1, unmounts: 0 });
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
    expect(api.calls.filter((call) => call.method !== 'GET')).toHaveLength(1);
  });

  it('isolates late quiet readbacks from a different route item and a later account', async () => {
    const api = catalogue([paper(), paper('paper-b')]);
    await mount();
    await ready();
    draft('Account A draft');
    const oldA = deferred<Response>();
    api.nextDetail('paper-a', oldA.promise);
    changeStatus('read');
    await waitFor(() => expect(api.count('/api/literature/items/paper-a')).toBe(2));
    fireEvent.click(screen.getByRole('button', { name: 'Go to route B' }));
    await ready('Synthetic paper-b');
    const oldB = deferred<Response>();
    api.nextDetail('paper-b', oldB.promise);
    changeStatus('reading');
    await waitFor(() => expect(api.count('/api/literature/items/paper-b')).toBe(2));
    api.items.clear();
    api.items.set('paper-b', paper('paper-b', { title: 'Next account reference', reading_status: 'reading' }));
    act(() => {
      setRendererAccountOwner('next-integration-owner');
    });
    await ready('Next account reference');
    await act(async () => {
      oldA.resolve(json(paper('paper-a', { title: 'Late route A' })));
      oldB.resolve(json(paper('paper-b', { title: 'Late previous account' })));
      await Promise.all([oldA.promise, oldB.promise]);
    });
    expect(screen.getByRole('heading', { name: 'Next account reference' })).toBeInTheDocument();
    expect(screen.queryByText('Late route A')).toBeNull();
    expect(screen.queryByText('Late previous account')).toBeNull();
    expect(screen.queryByDisplayValue('Account A draft')).toBeNull();
    expect(locationParams().get('item')).toBe('paper-b');
  });

  it('resets paging and selection on filter changes and clears the filter only on explicit imported-reference navigation', async () => {
    const api = catalogue();
    const rendered = await mount('/library?reading_status=unread&cursor=old-page&item=paper-a&q=kept&project=project');
    await ready();
    fireEvent.change(screen.getByRole('combobox', { name: 'Reading status', exact: true }), {
      target: { value: 'reading' },
    });
    await waitFor(() => expect(locationParams().get('reading_status')).toBe('reading'));
    expect(locationParams().has('cursor')).toBe(false);
    expect(locationParams().has('item')).toBe(false);
    expect(locationParams().get('q')).toBe('kept');
    expect(locationParams().get('project')).toBe('project');
    rendered.unmount();
    setRendererAccountOwner('import-navigation-owner');
    const initial = createUploadIntent({
      projectId: 'project',
      title: 'Imported reference',
      filename: 'reference.pdf',
      size: 3,
      contentType: 'application/pdf',
      hash: checksum,
      catalogueOperationId: id(1),
      initOperationId: id(2),
    });
    const pending = withArtifactReceipt(
      withInitReceipt(initial, { upload_id: id(3), chunk_size: 2, total_chunks: 2 }),
      { artifactId: 'source-artifact', versionId: 'source-version', filename: 'reference.pdf', sizeBytes: 3, checksum }
    );
    localStorage.setItem(
      'x-science.literature-mutation.v1:import-navigation-owner',
      encodePendingRecord('import-navigation-owner', pending)
    );
    await mount('/library?reading_status=read&q=kept&collection=col&project=project&cursor=old-page&item=paper-a');
    const dialog = await screen.findByRole('dialog', { name: 'Import a PDF' });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry original import' }));
    const viewReference = await screen.findByRole('button', { name: 'View reference' });
    expect(locationParams().get('reading_status')).toBe('read');
    fireEvent.click(viewReference);
    await screen.findByRole('heading', { name: 'Imported reference' });
    expect(screen.getByLabelText('Library location')).toHaveTextContent('?item=paper-imported');
    expect([...locationParams().keys()]).toEqual(['item']);
    expect(
      api.calls.find(
        (call) => call.method === 'POST' && call.url.pathname === '/api/literature/import-artifact-version'
      )?.body?.operation_id
    ).toBe(id(1));
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(0);
  });
});

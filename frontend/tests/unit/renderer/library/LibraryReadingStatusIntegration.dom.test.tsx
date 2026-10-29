import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation, useNavigate } from 'react-router';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { LiteratureReadingStatus } from '@/common/adapter/literatureReadingStatus';
import type { RelatedRecord, RelatedRequest } from '@/common/adapter/literatureRelated';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
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
const relatedPaper = (letter: string, title: string): RelatedRecord => ({
  paper_id: letter.repeat(40),
  title,
  abstract: '',
  authors: [],
  year: 2024,
  issued_text: '',
  container_title: '',
  item_type: 'journalArticle',
  url: `https://www.semanticscholar.org/paper/${letter.repeat(40)}`,
  identifiers: [{ scheme: 'doi', value: `10.1234/related-${letter}` }],
  volume: '',
  pages: '',
});
function catalogue(initial = [paper()]) {
  const items = new Map(initial.map((item) => [item.id, item]));
  const calls: Call[] = [];
  const replies = new Map<string, Array<Response | Promise<Response>>>();
  const listReplies: Array<Response | Promise<Response>> = [];
  const stageReplies: Array<(receipt: Record<string, unknown>) => Response | Promise<Response>> = [];
  const stageReceipts = new Map<string, Record<string, unknown>>();
  let inboxPending = true;
  const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    const method = init?.method ?? 'GET';
    const body = typeof init?.body === 'string' ? (JSON.parse(init.body) as Record<string, unknown>) : undefined;
    calls.push({ method, url, body });
    if (method === 'GET' && url.pathname === '/api/settings/client') return json({});
    if (method === 'GET' && url.pathname === '/api/projects')
      return json({ projects: [{ project_id: 'project', name: 'Owned project' }] });
    if (method === 'GET' && url.pathname === '/api/literature/collections')
      return json({ schema: 'x-science.literature-collections.v1', collections: [] });
    if (method === 'POST' && url.pathname === '/api/literature/related') {
      const request = body as unknown as RelatedRequest;
      return json({
        schema: 'x-science.literature-related.v1',
        item_id: request.item_id,
        metadata_revision: request.expected_metadata_revision,
        direction: request.direction,
        state: 'ready',
        resolution_token: id(51),
        seed: relatedPaper('a', 'Identified source'),
        records: [relatedPaper('b', 'Related one'), relatedPaper('c', 'Related two')],
      });
    }
    if (method === 'POST' && url.pathname === '/api/literature/candidates') {
      const operation = String(body!.operation_id);
      const receipt = stageReceipts.get(operation) ?? {
        schema: 'x-science.literature-candidate-receipt.v1',
        operation_id: operation,
        action: 'stage',
        candidate: {
          id: `candidate-${String(body!.external_id)}`,
          title: body!.title,
          metadata: body!.metadata,
          provider: body!.provider,
          external_id: body!.external_id,
          state: 'pending',
        },
      };
      stageReceipts.set(operation, receipt);
      return stageReplies.shift()?.(receipt) ?? json(receipt);
    }
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
    if (method === 'GET' && url.pathname === '/api/literature/candidates')
      return json({
        schema: 'x-science.literature-candidates.v1',
        candidates:
          inboxPending && url.searchParams.get('state') === 'pending'
            ? [
                {
                  id: 'candidate-one',
                  title: 'Inbox paper',
                  provider: 'synthetic',
                  external_id: 'external-one',
                  state: 'pending',
                  metadata: {},
                },
              ]
            : [],
      });
    if (method === 'POST' && url.pathname === '/api/literature/candidates/candidate-one/accept') {
      inboxPending = false;
      items.set('paper-inbox', paper('paper-inbox', { metadata: {}, attachments: [], project_ids: [] }));
      return json({
        schema: 'x-science.literature-candidate-receipt.v1',
        operation_id: body!.operation_id,
        action: 'accept',
        candidate: {
          id: 'candidate-one',
          title: 'Inbox paper',
          provider: 'synthetic',
          external_id: 'external-one',
          state: 'accepted',
          item_id: 'paper-inbox',
          metadata: {},
        },
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
    nextStage: (reply: (receipt: Record<string, unknown>) => Response | Promise<Response>) => stageReplies.push(reply),
  };
}
let navigateRoute: ReturnType<typeof useNavigate>;
function RouteControls() {
  const location = useLocation();
  const navigate = useNavigate();
  navigateRoute = navigate;
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
const detail = () => within(detailsDialog()).getAllByRole('region', { name: 'Literature details' })[0];
const detailsDialog = () => screen.getByRole('dialog');
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
    // Read the original retained node's state; never act on the hidden overview.
    const retained = within(detailsDialog()).getByLabelText(/^Reading status for /, { selector: 'select' });
    expect(retained).toBeEnabled();
    expect(retained).toHaveValue(value);
  });
}
async function notifyStatus(value: LiteratureReadingStatus) {
  const itemId = locationParams().get('item');
  if (!itemId) throw new Error('The fixture must have a current original item');
  // Controlled other-source input through the exported original owner. Its
  // real receipt subscription drives Page readback while edit mode is active.
  await act(async () => {
    await getLiteratureMutationController(getRendererAccountOwnerId())
      .submit({
        kind: 'set-reading-status',
        operation_id: crypto.randomUUID(),
        item_ids: [itemId],
        reading_status: value,
      })
      .catch((): undefined => undefined);
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

  it('keeps related stage ACKs in view and retains its query while metadata editing', async () => {
    const api = catalogue();
    const firstStage = deferred<void>();
    api.nextStage((receipt) => firstStage.promise.then(() => json(receipt)));
    await mount();
    await ready();
    fireEvent.click(within(detail()).getByRole('button', { name: 'References' }));
    await screen.findByText('Related two');
    const query = screen.getByRole('region', { name: 'Related literature' });
    fireEvent.click(within(query).getByRole('checkbox', { name: 'Select Related one' }));
    fireEvent.click(within(query).getByRole('checkbox', { name: 'Select Related two' }));
    const before = [api.count('/api/literature/items/paper-a'), api.count('/api/literature/related', 'POST')];
    fireEvent.click(within(query).getByRole('button', { name: 'Add selected to Inbox (2)' }));
    await waitFor(() => expect(api.count('/api/literature/candidates', 'POST')).toBe(1));
    expect(within(detail()).getByRole('button', { name: 'Edit details' })).toBeDisabled();
    await act(async () => firstStage.resolve());
    await waitFor(() => expect(api.count('/api/literature/candidates', 'POST')).toBe(2));
    await waitFor(() => expect(within(query).getAllByText('Added to Inbox.')).toHaveLength(2));
    expect(screen.getByRole('region', { name: 'Related literature' })).toBe(query);
    expect([api.count('/api/literature/items/paper-a'), api.count('/api/literature/related', 'POST')]).toEqual(before);
    await waitFor(() => expect(within(detail()).getByRole('button', { name: 'Edit details' })).toBeEnabled());
    const input = draft('Draft after staged ACKs');
    expect(query).toBeInTheDocument();
    expect(query).not.toBeVisible();
    expect(query.closest('.library-workbench__overview')).toHaveAttribute('inert');
    await notifyStatus('read');
    await savedStatus('read');
    expect(input).toBe(within(detail()).getByRole('textbox', { name: 'Title' }));
    expect(input).toHaveValue('Draft after staged ACKs');
    expect(query).toBeInTheDocument();
    expect(within(query).getAllByText('Added to Inbox.')).toHaveLength(2);
    expect(api.count('/api/literature/items/paper-a')).toBe(before[0] + 1);
    expect(api.count('/api/literature/related', 'POST')).toBe(before[1]);
    expect(api.count('/api/literature/candidates', 'POST')).toBe(2);
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
  });

  it('retains original uncertain stage recovery in view and isolates a later metadata draft', async () => {
    const api = catalogue();
    api.nextStage(() => Promise.reject(new TypeError('Lost committed stage acknowledgement')));
    await mount();
    await ready();
    fireEvent.click(within(detail()).getByRole('button', { name: 'References' }));
    await screen.findByText('Related two');
    const query = screen.getByRole('region', { name: 'Related literature' });
    fireEvent.click(within(query).getByRole('checkbox', { name: 'Select Related one' }));
    fireEvent.click(within(query).getByRole('checkbox', { name: 'Select Related two' }));
    fireEvent.click(within(query).getByRole('button', { name: 'Add selected to Inbox (2)' }));
    const retry = await within(query).findByRole('button', { name: 'Retry' });
    await waitFor(() => expect(retry).toBeEnabled());
    expect(within(detail()).getByRole('button', { name: 'Edit details' })).toBeDisabled();
    fireEvent.click(retry);
    await within(query).findByText('Added to Inbox.');
    expect(screen.getByRole('region', { name: 'Related literature' })).toBe(query);
    const writes = api.calls.filter(
      (call) => call.method === 'POST' && call.url.pathname === '/api/literature/candidates'
    );
    expect(writes).toHaveLength(2);
    expect(writes[1].body).toEqual(writes[0].body);
    expect(within(query).getByRole('checkbox', { name: 'Select Related one' })).not.toBeChecked();
    expect(within(query).getByRole('checkbox', { name: 'Select Related two' })).toBeChecked();
    fireEvent.click(within(query).getByRole('button', { name: 'Add selected to Inbox (1)' }));
    await waitFor(() => expect(within(query).getAllByText('Added to Inbox.')).toHaveLength(2));
    expect(api.count('/api/literature/candidates', 'POST')).toBe(3);
    expect(api.count('/api/literature/related', 'POST')).toBe(1);
    expect(api.count('/api/literature/items/paper-a')).toBe(1);
    await waitFor(() => expect(within(detail()).getByRole('button', { name: 'Edit details' })).toBeEnabled());
    const input = draft('Draft after stage recovery');
    expect(query).toBeInTheDocument();
    expect(query).not.toBeVisible();
    expect(query.closest('.library-workbench__overview')).toHaveAttribute('hidden');
    await notifyStatus('reading');
    await savedStatus('reading');
    expect(input).toBe(within(detail()).getByRole('textbox', { name: 'Title' }));
    expect(input).toHaveValue('Draft after stage recovery');
    expect(query).toBeInTheDocument();
    expect(within(query).getAllByText('Added to Inbox.')).toHaveLength(2);
    expect(api.count('/api/literature/candidates', 'POST')).toBe(3);
    expect(api.count('/api/literature/related', 'POST')).toBe(1);
    expect(api.count('/api/literature/items/paper-a')).toBe(2);
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
  });

  it('retires the old related batch when an actual source revision readback changes', async () => {
    const api = catalogue();
    const response = deferred<void>();
    api.nextStage((receipt) => response.promise.then(() => json(receipt)));
    await mount();
    await ready();
    fireEvent.click(within(detail()).getByRole('button', { name: 'References' }));
    await screen.findByText('Related two');
    const previous = screen.getByRole('region', { name: 'Related literature' });
    fireEvent.click(within(previous).getByRole('checkbox', { name: 'Select Related one' }));
    fireEvent.click(within(previous).getByRole('checkbox', { name: 'Select Related two' }));
    fireEvent.click(within(previous).getByRole('button', { name: 'Add selected to Inbox (2)' }));
    await waitFor(() => expect(api.count('/api/literature/candidates', 'POST')).toBe(1));
    api.items.set('paper-a', paper('paper-a', { metadata_revision: 2, title: 'Changed source' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await screen.findByRole('heading', { name: 'Changed source' });
    await screen.findByText('Related two');
    const current = screen.getByRole('region', { name: 'Related literature' });
    expect(current).not.toBe(previous);
    await act(async () => {
      response.resolve();
      await response.promise;
    });
    await waitFor(() =>
      expect(within(current).getByRole('button', { name: 'Add selected to Inbox (0)' })).toBeDisabled()
    );
    expect(api.count('/api/literature/candidates', 'POST')).toBe(1);
    expect(within(current).queryByText('Added to Inbox.')).toBeNull();
  });

  it('opens Inbox through the actual page, accepts and opens the persisted Library item', async () => {
    const api = catalogue();
    await mount();
    await ready();
    const input = draft();
    fireEvent.click(await screen.findByRole('button', { name: 'Inbox' }));
    await screen.findByText('Inbox paper');
    fireEvent.click(screen.getByRole('button', { name: 'Back to Library' }));
    expect(input).toHaveValue('Unsaved local title');
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    fireEvent.click(screen.getByRole('button', { name: 'Inbox' }));
    await screen.findByText('Inbox paper');
    fireEvent.click(screen.getByRole('button', { name: 'Add to Library' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Open in Library' }));
    await screen.findByRole('heading', { name: 'Synthetic paper-inbox' });
    expect(
      api.calls.some(
        (call) => call.method === 'POST' && call.url.pathname === '/api/literature/candidates/candidate-one/accept'
      )
    ).toBe(true);
  });

  it('retains the active detail draft when an Inbox candidate is accepted', async () => {
    catalogue();
    await mount();
    await ready();
    const input = draft('Draft after candidate acceptance');
    fireEvent.click(screen.getByRole('button', { name: 'Inbox' }));
    await screen.findByText('Inbox paper');
    fireEvent.click(screen.getByRole('button', { name: 'Add to Library' }));
    await screen.findByRole('button', { name: 'Open in Library' });
    fireEvent.click(screen.getByRole('button', { name: 'Back to Library' }));
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Draft after candidate acceptance');
    fireEvent.click(screen.getByRole('button', { name: 'Inbox' }));
    await screen.findByText('No candidates in this view.');
    expect(screen.queryByRole('button', { name: 'Open in Library' })).toBeNull();
  });

  it('parks an opened detail inertly in Inbox without stale dismissal or focus and resumes its original draft and PDF', async () => {
    const api = catalogue();
    await mount('/library?inbox=1&item=paper-a');
    await screen.findByRole('region', { name: 'Inbox' });
    expect(document.querySelector('.library-workbench__detail-dialog')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Back to Library' }));
    await ready();
    const pdf = openPreview();
    const input = draft('The same parked draft');
    const dialog = detailsDialog();
    act(() => input.focus());
    const timers = vi.spyOn(globalThis, 'setTimeout');
    const wrapper = dialog.closest('.arco-modal-wrapper')!;
    fireEvent.mouseDown(wrapper);
    fireEvent.click(wrapper);
    const delayedCancel = [...timers.mock.calls].reverse().find((call) => call[1] === 100)?.[0];
    expect(delayedCancel).toBeTypeOf('function');
    fireEvent.keyDown(input, { key: 'Escape' });
    await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    // Simulate an external route transition; never click through the modal overlay.
    await act(async () => {
      await navigateRoute('/library?inbox=1&item=paper-a');
    });
    const inbox = await screen.findByRole('region', { name: 'Inbox' });
    const destination = within(inbox).getByRole('button', { name: 'Refresh' });
    act(() => destination.focus());
    expect(dialog).toHaveAttribute('aria-hidden', 'true');
    expect(dialog).toHaveAttribute('inert');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByRole('alertdialog')).toBeNull();
    act(() => (delayedCancel as () => void)());
    await waitFor(() => expect(wrapper).toHaveStyle({ display: 'none' }));
    expect(destination).toHaveFocus();
    expect(locationParams().get('item')).toBe('paper-a');
    expect(previewLifecycle).toEqual({ mounts: 1, unmounts: 0 });
    fireEvent.click(screen.getByRole('button', { name: 'Back to Library' }));
    await screen.findByRole('region', { name: 'Edit details' });
    expect(within(detail()).getByRole('heading', { name: 'Edit details' })).toBeVisible();
    expect(
      within(detail()).getByText('Synthetic paper-a', { selector: '.library-workbench__detail-subtitle' })
    ).toBeVisible();
    expect(detailsDialog()).toBe(dialog);
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('The same parked draft');
    expect(screen.getByTestId('instrumented-library-pdf')).toBe(pdf);
    expect(screen.getByLabelText('Instrumented PDF page')).toHaveTextContent('2');
    act(() => (delayedCancel as () => void)());
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(detailsDialog()).toBe(dialog);
    expect(api.calls.every((call) => call.method === 'GET')).toBe(true);
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
    await notifyStatus('read');
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
    await notifyStatus('reading');
    await savedStatus('reading');
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Unsaved local title');
    expect(input).toBeDisabled();
    expect(within(detail()).getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(await screen.findByText(/This paper was updated elsewhere/)).toBeInTheDocument();
    fireEvent.click(within(detail()).getByRole('button', { name: 'Save' }));
    expect(api.calls.filter((call) => call.method === 'PATCH')).toHaveLength(0);
    fireEvent.click(screen.getByRole('button', { name: 'Discard draft and load latest' }));
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toHaveValue('External title');
    expect(within(detail()).getByRole('button', { name: 'Save' })).toBeEnabled();
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
  });

  it('keeps the selected detail when its filtered row disappears and restores close focus to the stable list', async () => {
    catalogue();
    await mount('/library?reading_status=unread&item=paper-a');
    await ready();
    changeStatus('read');
    await savedStatus('read');
    await waitFor(() => expect(within(list()).queryByRole('button', { name: 'Synthetic paper-a' })).toBeNull());
    expect(locationParams().get('item')).toBe('paper-a');
    expect(locationParams().get('reading_status')).toBe('unread');
    expect(within(detail()).getByRole('heading', { name: 'Synthetic paper-a' })).toBeInTheDocument();
    const close = within(detail()).getByRole('button', { name: 'Close' });
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
    openPreview();
    const input = draft();
    api.nextDetail('paper-a', json({ error: 'Unavailable' }, 503));
    await notifyStatus('read');
    await screen.findByText('Details could not be refreshed. Your draft is retained; retry to continue editing.');
    expect(input).toHaveValue('Unsaved local title');
    expect(input).toBeDisabled();
    expect(within(detail()).getByRole('button', { name: 'Save' })).toBeDisabled();
    expect(screen.getByTestId('instrumented-library-pdf')).toBeInTheDocument();
    fireEvent.click(within(detailsDialog()).getByRole('button', { name: 'Retry' }));
    await savedStatus('read');
    expect(input).toBeEnabled();
    expect(input).toHaveValue('Unsaved local title');
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
    for (const [value, reply] of [
      ['reading', json({ error: 'Not found' }, 404)],
      ['unread', json(paper('wrong-item'))],
    ] as const) {
      api.nextDetail('paper-a', reply);
      await notifyStatus(value);
      await waitFor(() => expect(screen.queryByRole('region', { name: 'Literature details' })).toBeNull());
      expect(screen.queryByRole('textbox', { name: 'Title' })).toBeNull();
      expect(screen.queryByTestId('instrumented-library-pdf')).toBeNull();
      expect(screen.queryByRole('heading', { name: 'Retractions and corrections' })).toBeNull();
      if (value === 'reading') {
        fireEvent.click(within(detailsDialog()).getByRole('button', { name: 'Retry' }));
        await ready();
        openPreview();
        draft('Second unsaved draft');
      }
    }
  });

  it('preserves the draft and PDF instance through a failed list readback and its GET-only retry', async () => {
    const api = catalogue();
    await mount();
    await ready();
    const preview = openPreview();
    const input = draft();
    api.nextList(json({ error: 'Unavailable' }, 503));
    await notifyStatus('read');
    await savedStatus('read');
    const retry = await within(list()).findByRole('button', { name: 'Retry' });
    expect(within(detail()).getByRole('textbox', { name: 'Title' })).toBe(input);
    expect(input).toHaveValue('Unsaved local title');
    expect(screen.getByTestId('instrumented-library-pdf')).toBe(preview);
    expect(api.count('/api/literature/items/reading-status', 'POST')).toBe(1);
    fireEvent.click(retry);
    await within(list()).findByRole('button', { name: 'Synthetic paper-a' });
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
    await notifyStatus('read');
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
    fireEvent.change(screen.getByRole('combobox', { name: 'Reading status' }), {
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

import React from 'react';
import { act, cleanup, createEvent, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { renderWithI18n } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, paper } from './libraryCollectionsFixtures';
import '@arco-design/web-react/lib/_util/react-19-adapter';

// Only instrument the PDF leaf; the real Browser/Pane/immutable target/focus owners remain.
vi.mock('@/renderer/pages/library/LibraryAttachmentPreview', () => ({
  LibraryAttachmentPreview: () => <button type='button'>PDF fixture action</button>,
}));
let navigate: ReturnType<typeof useNavigate>;
const release: Array<() => void> = [];
function Location() {
  navigate = useNavigate();
  return <output aria-label='Detail fixture location'>{useLocation().search}</output>;
}
async function mount(path = '/library') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
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
    </MemoryRouter>,
    'en-US'
  );
}
const withPdf = {
  ...paper(),
  attachments: [
    {
      id: 'attachment-a',
      kind: 'fullText',
      versions: [
        {
          id: 'version-a',
          attachment_id: 'attachment-a',
          version_number: 1,
          filename: 'reference.pdf',
          mime_type: 'application/pdf',
          size_bytes: 3,
          checksum: 'a'.repeat(64),
          page_count: 2,
          available: true,
        },
      ],
    },
  ],
};
function http(intercept?: Parameters<typeof installCollectionHTTP>[0]['intercept']) {
  return installCollectionHTTP({
    collections: () => [],
    items: () => [withPdf, paper('b', 'Paper B')],
    detail: (id) => (id === 'a' ? withPdf : paper(id, `Paper ${id.toUpperCase()}`)),
    intercept,
  });
}
async function openA() {
  const row = await screen.findByRole('button', { name: /^Paper A$/ });
  act(() => row.focus());
  fireEvent.click(row);
  return { row, dialog: await screen.findByRole('dialog', { name: 'Paper A' }) };
}
describe('Library detail through its real Page/read/mutation/modal owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('detail-dialog-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const done of release.splice(0)) done();
      await Promise.resolve();
    });
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('opens a single responsive detail dialog outside the list and returns focus to its actual row', async () => {
    const data = http();
    await mount();
    const { row, dialog } = await openA();
    expect(dialog).toHaveStyle({ width: '760px', maxWidth: 'calc(100vw - 32px)' });
    const mask = document.querySelector<HTMLElement>('.arco-modal-mask')!;
    expect(mask.style.backgroundColor).toBe('transparent');
    expect(getComputedStyle(mask).backgroundColor).toBe('rgba(0, 0, 0, 0)');
    expect(document.querySelector('.library-workbench__panes > aside')).toBeNull();
    await waitFor(() => expect(dialog.contains(document.activeElement)).toBe(true));
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Edit details' })).toHaveFocus());
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    await waitFor(() => expect(row).toHaveFocus());
    expect(screen.getByLabelText('Detail fixture location')).not.toHaveTextContent('item=');
    expect(data.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it('dirty exit: keeps unsaved metadata and returns focus when editing resumes', async () => {
    http();
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Unsaved title' } });
    act(() => title.focus());
    fireEvent.keyDown(title, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    const confirmation = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    fireEvent.click(within(confirmation).getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(title).toHaveValue('Unsaved title');
    await waitFor(() => expect(title).toHaveFocus());
  });

  it('dirty exit: keeps the original failed save retry reachable inside its dialog', async () => {
    const attempts: string[] = [];
    let saved: typeof withPdf | undefined;
    http((url, init) => {
      if (url.pathname !== '/api/literature/items/a') return undefined;
      if (init.method !== 'PATCH') return saved ? json(saved) : undefined;
      attempts.push(String(init.body));
      if (attempts.length === 1) return json({ code: 'LITERATURE_INTERNAL' }, 503);
      const body = JSON.parse(String(init.body)) as { operation_id: string; title: string };
      saved = { ...withPdf, title: body.title, metadata_revision: 2 };
      return json({ schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item: saved });
    });
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Saved by original retry' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    const retry = await within(dialog).findByRole('button', { name: 'Retry' });
    expect(title).toHaveValue('Saved by original retry');
    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
    const controller = getLiteratureMutationController(getRendererAccountOwnerId());
    expect(controller.getSnapshot().pending?.operation_id).toBe(JSON.parse(attempts[0]).operation_id);
    fireEvent.click(retry);
    await waitFor(() => expect(controller.getSnapshot().pending).toBeNull());
    expect(attempts).toHaveLength(2);
    expect(attempts[1]).toBe(attempts[0]);
    expect(screen.queryByRole('alertdialog')).toBeNull();
    expect(dialog).toBeInTheDocument();
  });

  it('dirty exit: preserves a newer focus destination while the confirmation finishes closing', async () => {
    http();
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Draft stays here' } });
    act(() => title.focus());
    fireEvent.keyDown(title, { key: 'Escape' });
    const confirmation = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    fireEvent.click(within(confirmation).getByRole('button', { name: 'Keep editing' }));
    const destination = within(dialog)
      .getByRole('group', { name: 'Literature details' })
      .querySelector<HTMLButtonElement>('button[aria-label="Close"]')!;
    act(() => destination.focus());
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(destination).toHaveFocus();
    expect(title).toHaveValue('Draft stays here');
  });

  it('dirty exit: keeps mixed Inbox item links inside Inbox without mounting a detail portal', async () => {
    http((url) =>
      url.pathname === '/api/literature/candidates'
        ? json({ schema: 'x-science.literature-candidates.v1', candidates: [] })
        : undefined
    );
    await mount('/library?inbox=1&item=a');
    const inbox = await screen.findByRole('region', { name: 'Inbox' });
    const refresh = within(inbox).getByRole('button', { name: 'Refresh' });
    act(() => refresh.focus());
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(refresh).toHaveFocus();
    expect(screen.getByLabelText('Detail fixture location')).toHaveTextContent('inbox=1');
    expect(screen.getByLabelText('Detail fixture location')).toHaveTextContent('item=a');
  });

  it('dirty exit: retries a failed reading status inside the dialog without saving its metadata draft', async () => {
    const attempts: string[] = [];
    let saved = false;
    http((url, init) => {
      if (url.pathname === '/api/literature/items/a' && saved) return json({ ...withPdf, reading_status: 'read' });
      if (url.pathname !== '/api/literature/items/reading-status' || init.method !== 'POST') return undefined;
      attempts.push(String(init.body));
      if (attempts.length === 1) return json({ code: 'LITERATURE_INTERNAL' }, 503);
      saved = true;
      return json({ schema: 'x-science.literature-reading-status-receipt.v1', ...JSON.parse(String(init.body)) });
    });
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Reading is not metadata' } });
    // A current-client/other-source status notification uses the original
    // account owner; edit mode does not expose the overview's status control.
    await act(async () => {
      await getLiteratureMutationController(getRendererAccountOwnerId())
        .submit({
          kind: 'set-reading-status',
          operation_id: crypto.randomUUID(),
          item_ids: ['a'],
          reading_status: 'read',
        })
        .catch((): undefined => undefined);
    });
    const retry = await within(dialog).findByRole('button', { name: 'Retry' });
    expect(screen.getAllByRole('button', { name: 'Retry' })).toHaveLength(1);
    fireEvent.click(retry);
    await waitFor(() => expect(title).not.toBeDisabled());
    expect(attempts).toHaveLength(2);
    expect(attempts[1]).toBe(attempts[0]);
    expect(title).toHaveValue('Reading is not metadata');
    fireEvent.click(
      within(dialog).getByRole('group', { name: 'Literature details' }).querySelector('button[aria-label="Close"]')!
    );
    await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
  });

  it.each(['mask', 'header', 'editor'] as const)(
    'dirty exit: requires explicit discard for the %s close',
    async (source) => {
      const data = http();
      await mount();
      const { dialog } = await openA();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
      const title = within(dialog).getByRole('textbox', { name: 'Title' });
      fireEvent.change(title, { target: { value: 'Discard only with intent' } });
      act(() => title.focus());
      if (source === 'mask') {
        const target = dialog.closest('.arco-modal-wrapper')!;
        fireEvent.mouseDown(target);
        fireEvent.click(target);
      } else if (source === 'header') {
        fireEvent.click(
          within(dialog).getByRole('group', { name: 'Literature details' }).querySelector('button[aria-label="Close"]')!
        );
      } else {
        fireEvent.click(
          within(within(dialog).getByRole('region', { name: 'Edit details' })).getByRole('button', { name: /^Close$/ })
        );
      }
      const confirmation = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
      expect(title).toHaveValue('Discard only with intent');
      const mask = confirmation.closest('.arco-modal-wrapper')?.previousElementSibling as HTMLElement;
      expect(mask.style.backgroundColor).toBe('transparent');
      fireEvent.click(within(confirmation).getByRole('button', { name: 'Discard changes' }));
      if (source === 'editor') {
        await waitFor(() => expect(within(dialog).queryByRole('textbox', { name: 'Title' })).toBeNull());
        expect(dialog).toBeInTheDocument();
      } else await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
      expect(data.requests.every((request) => request.method === 'GET')).toBe(true);
    }
  );

  it('dirty exit: treats reverted values as clean and nontext explicit clear as dirty', async () => {
    const original = { ...withPdf, metadata: { ...withPdf.metadata, abstract: { source: 'provider' } } };
    const data = http((url, init) =>
      init.method !== 'PATCH' && url.pathname === '/api/literature/items/a' ? json(original) : undefined
    );
    await mount();
    let { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Temporary' } });
    fireEvent.change(title, { target: { value: 'Paper A' } });
    fireEvent.keyDown(title, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.queryByRole('alertdialog')).toBeNull();
    ({ dialog } = await openA());
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Clear Abstract' }));
    fireEvent.keyDown(within(dialog).getByRole('textbox', { name: 'Abstract' }), { key: 'Escape' });
    const confirmation = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    fireEvent.keyDown(confirmation, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    expect(dialog).toBeInTheDocument();
    expect(data.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it('dirty exit: preserves unsubmitted metadata through a pending and acknowledged membership action', async () => {
    const held = deferred<Response>();
    let acknowledge: (() => void) | undefined;
    const data = http((url, init) => {
      if (url.pathname === '/api/projects') return json({ projects: [{ project_id: 'project-p', name: 'Project P' }] });
      if (url.pathname !== '/api/literature/items/a/projects/project-p' || init.method !== 'PUT') return undefined;
      const body = JSON.parse(String(init.body)) as { operation_id: string };
      acknowledge = () =>
        held.resolve(
          json({
            schema: 'x-science.literature-receipt.v1',
            operation_id: body.operation_id,
            item: { ...withPdf, project_ids: ['project-p'] },
          })
        );
      release.push(acknowledge);
      return held.promise;
    });
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Metadata is not membership' } });
    const project = within(dialog).getByRole('combobox', { name: 'Project membership' });
    await waitFor(() => expect(project).not.toBeDisabled());
    fireEvent.change(project, { target: { value: 'project-p' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Add to project' }));
    await waitFor(() => expect(acknowledge).toBeTypeOf('function'));
    fireEvent.click(
      within(dialog).getByRole('group', { name: 'Literature details' }).querySelector('button[aria-label="Close"]')!
    );
    const confirmation = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    fireEvent.click(within(confirmation).getByRole('button', { name: 'Keep editing' }));
    await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
    await act(async () => acknowledge!());
    await waitFor(() => expect(title).not.toBeDisabled());
    expect(title).toHaveValue('Metadata is not membership');
    expect(data.requests.filter((request) => request.method === 'PUT')).toHaveLength(1);
    expect(data.requests.filter((request) => request.method === 'PATCH')).toHaveLength(0);
  });

  it('dirty exit: retains a rejected save as unsaved instead of claiming durable coverage', async () => {
    http((url, init) =>
      url.pathname === '/api/literature/items/a' && init.method === 'PATCH'
        ? json({ code: 'LITERATURE_VALIDATION' }, 400)
        : undefined
    );
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const title = within(dialog).getByRole('textbox', { name: 'Title' });
    fireEvent.change(title, { target: { value: 'Rejected draft stays' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await within(dialog).findByRole('button', { name: 'Retry' });
    await waitFor(() => expect(title).not.toBeDisabled());
    fireEvent.click(
      within(dialog).getByRole('group', { name: 'Literature details' }).querySelector('button[aria-label="Close"]')!
    );
    await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
    expect(title).toHaveValue('Rejected draft stays');
  });

  it.each(['item', 'account'] as const)(
    'dirty exit: ignores an old confirmation callback after the %s changes',
    async (scope) => {
      http();
      await mount();
      const { dialog } = await openA();
      fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
      fireEvent.change(within(dialog).getByRole('textbox', { name: 'Title' }), { target: { value: 'Old draft' } });
      fireEvent.keyDown(dialog, { key: 'Escape' });
      const old = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
      const timers = vi.spyOn(globalThis, 'setTimeout');
      const target = old.closest('.arco-modal-wrapper')!;
      fireEvent.mouseDown(target);
      fireEvent.click(target);
      const delayed = [...timers.mock.calls].reverse().find((call) => call[1] === 100)?.[0];
      expect(delayed).toBeTypeOf('function');
      await act(async () => {
        if (scope === 'item') await navigate('/library?item=b');
        else setRendererAccountOwner('new-dirty-owner');
      });
      const replacement = await screen.findByRole('dialog', { name: scope === 'item' ? 'Paper B' : 'Paper A' });
      fireEvent.click(within(replacement).getByRole('button', { name: 'Edit details' }));
      const title = within(replacement).getByRole('textbox', { name: 'Title' });
      fireEvent.change(title, { target: { value: 'New draft' } });
      fireEvent.keyDown(title, { key: 'Escape' });
      const current = await screen.findByRole('alertdialog', { name: 'Discard unsaved changes?' });
      act(() => (delayed as () => void)());
      expect(current).toBeInTheDocument();
      fireEvent.click(within(current).getByRole('button', { name: 'Keep editing' }));
      await waitFor(() => expect(screen.queryByRole('alertdialog')).toBeNull());
      expect(title).toHaveValue('New draft');
    }
  );

  it('closes a loading opening without allowing its late result to reopen or replace the next detail', async () => {
    const held = deferred<Response>();
    release.push(() => held.resolve(json(withPdf)));
    const data = http((url) => (url.pathname === '/api/literature/items/a' ? held.promise : undefined));
    await mount();
    const row = await screen.findByRole('button', { name: /^Paper A$/ });
    act(() => row.focus());
    fireEvent.click(row);
    const loading = await screen.findByRole('dialog', { name: 'Literature details' });
    fireEvent.click(within(loading).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    fireEvent.click(screen.getByRole('button', { name: /^Paper B$/ }));
    await screen.findByRole('dialog', { name: 'Paper B' });
    await act(async () => held.resolve(json(withPdf)));
    expect(screen.getByRole('dialog', { name: 'Paper B' })).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Paper A' })).toBeNull();
    expect(data.requests.filter((request) => request.path === '/api/literature/items/a')).toHaveLength(1);
  });

  it.each(['item', 'account'] as const)(
    'ignores an old delayed mask close after the %s opening changes',
    async (scope) => {
      http();
      await mount();
      const { dialog } = await openA();
      const timers = vi.spyOn(globalThis, 'setTimeout');
      const maskTarget = dialog.closest('.arco-modal-wrapper')!;
      fireEvent.mouseDown(maskTarget);
      fireEvent.click(maskTarget);
      const delayed = [...timers.mock.calls].reverse().find((call) => call[1] === 100)?.[0];
      expect(delayed).toBeTypeOf('function');
      await act(async () => {
        if (scope === 'item') await navigate('/library?item=b');
        else setRendererAccountOwner('next-dialog-owner');
      });
      const replacement = await screen.findByRole('dialog', { name: scope === 'item' ? 'Paper B' : 'Paper A' });
      expect(replacement).not.toBe(dialog);
      act(() => (delayed as () => void)());
      expect(replacement).toBeInTheDocument();
    }
  );

  it('keeps failure and retry in the same URL-owned opening and returns to the list when no row exists', async () => {
    let fail = true;
    http((url) =>
      url.pathname === '/api/literature/items/c' && fail ? json({ code: 'LITERATURE_INTERNAL' }, 503) : undefined
    );
    await mount('/library?item=c');
    const failed = await screen.findByRole('dialog', { name: 'Literature details' });
    await within(failed).findByRole('alert');
    fail = false;
    fireEvent.click(within(failed).getByRole('button', { name: 'Retry' }));
    const ready = await screen.findByRole('dialog', { name: 'Paper C' });
    expect(ready).toBe(failed);
    fireEvent.click(within(ready).getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(screen.getByRole('region', { name: 'Library papers' })).toHaveFocus();
  });

  it('preserves composing/handled Escape and lets a native select dismiss before closing its parent', async () => {
    http();
    await mount();
    const { dialog } = await openA();
    fireEvent.keyDown(dialog, { key: 'Escape', isComposing: true });
    expect(dialog).toBeInTheDocument();
    const handled = createEvent.keyDown(dialog, { key: 'Escape', cancelable: true });
    handled.preventDefault();
    fireEvent(dialog, handled);
    expect(dialog).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    const referenceType = within(dialog).getByRole('combobox', { name: 'Reference type' });
    fireEvent.pointerDown(referenceType);
    fireEvent.keyDown(referenceType, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    expect(referenceType).toBeInTheDocument();
    fireEvent.click(
      within(within(dialog).getByRole('region', { name: 'Edit details' })).getByRole('button', { name: /^Close$/ })
    );
    await waitFor(() => expect(within(dialog).queryByRole('combobox', { name: 'Reference type' })).toBeNull());
    const select = within(dialog).getByRole('combobox', { name: /Reading status/ });
    fireEvent.pointerDown(select);
    fireEvent.keyDown(select, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    fireEvent.keyDown(select, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('preserves a durable pending edit when the UI closes and does not reopen on its late ACK', async () => {
    const held = deferred<Response>();
    let acknowledge: (() => void) | undefined;
    http((url, init) => {
      if (init.method !== 'PATCH' || url.pathname !== '/api/literature/items/a') return undefined;
      const body = JSON.parse(String(init.body)) as { operation_id: string; title: string };
      acknowledge = () =>
        held.resolve(
          json({
            schema: 'x-science.literature-receipt.v1',
            operation_id: body.operation_id,
            item: { ...withPdf, title: body.title, metadata_revision: 2 },
          })
        );
      release.push(acknowledge);
      return held.promise;
    });
    await mount();
    const { dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Edit details' }));
    fireEvent.change(within(dialog).getByRole('textbox', { name: 'Title' }), { target: { value: 'Saved later' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
    await waitFor(() => expect(acknowledge).toBeTypeOf('function'));
    const controller = getLiteratureMutationController(getRendererAccountOwnerId());
    const pending = controller.getSnapshot().pending;
    expect(pending).toBeDefined();
    fireEvent.click(
      within(dialog).getByRole('group', { name: 'Literature details' }).querySelector('button[aria-label="Close"]')!
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(controller.getSnapshot().pending?.operation_id).toBe(pending?.operation_id);
    await act(async () => acknowledge!());
    await waitFor(() => expect(controller.getSnapshot().mutating).toBe(false));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('keeps related lookup and inline PDF preview inside the dialog; preview Escape closes only the child', async () => {
    http((url, init) =>
      url.pathname === '/api/literature/related'
        ? json({
            schema: 'x-science.literature-related.v1',
            item_id: 'a',
            metadata_revision: 1,
            direction: JSON.parse(String(init.body)).direction,
            state: 'unavailable',
            records: [],
            issue: 'no-match',
          })
        : undefined
    );
    await mount();
    const { row, dialog } = await openA();
    fireEvent.click(within(dialog).getByRole('button', { name: 'References' }));
    const related = await within(dialog).findByRole('region', { name: 'Related literature' });
    await within(related).findByText(/Could not identify this paper/);
    fireEvent.click(within(related).getByRole('button', { name: 'Close' }));
    const preview = within(dialog).getByRole('button', { name: /Preview PDF/ });
    fireEvent.click(preview);
    const child = await within(dialog).findByRole('region', { name: 'Attachment preview' });
    expect(within(child).getByRole('button', { name: 'PDF fixture action' })).toBeInTheDocument();
    fireEvent.keyDown(child, { key: 'Escape' });
    expect(dialog).toBeInTheDocument();
    expect(within(dialog).queryByRole('region', { name: 'Attachment preview' })).toBeNull();
    expect(preview).toHaveFocus();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(row).toHaveFocus();
  });
});

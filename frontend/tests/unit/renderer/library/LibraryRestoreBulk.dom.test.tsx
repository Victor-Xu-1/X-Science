import React from 'react';
import { act, cleanup, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { renderWithI18n } from '../../i18nTestUtils';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { useLiteratureMutation } from '@/renderer/hooks/chat/useLiteratureMutation';
import { projectLibraryRestoreSummary } from '@/renderer/pages/library/useLibraryRestoreSelection';
import { isLiteratureRestoreBulk } from '@/renderer/services/runtime/literatureRestoreBulkIntent';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('restore-bulk-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function fixture(count = 2) {
  const items = Array.from({ length: count }, (_, i) => ({
    ...paper(
      i === 0 ? 'a' : i === 1 ? 'b' : `paper-${i}`,
      i === 0 ? 'Paper A' : i === 1 ? 'Paper B' : `Paper ${i + 1}`
    ),
    deleted: true,
  }));
  const writes: Record<string, unknown>[] = [];
  const ledger = new Map<string, object>();
  let previewGate: Promise<void> | undefined;
  let respond = (_body: Record<string, unknown>, receipt: object, _index: number): Response | Promise<Response> =>
    json(receipt);
  let readFailed = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: (url) => items.filter((item) => item.deleted === (url.searchParams.get('lifecycle') === 'deleted')),
    detail: (id) => items.find((item) => item.id === id)!,
    intercept: async (url, init) => {
      if (url.searchParams.get('restore_preview') === 'true') {
        const snapshot = {
          schema: 'x-science.literature-restore-preview.v1',
          items: items.filter((item) => item.deleted).map(({ id, metadata_revision }) => ({ id, metadata_revision })),
          total_count: items.filter((item) => item.deleted).length,
        };
        if (previewGate) await previewGate;
        return json(snapshot);
      }
      if (init.method === 'GET' && url.pathname === '/api/literature/items') {
        if (readFailed) return json({ code: 'UNAVAILABLE' }, 503);
        const matches = items.filter((item) => item.deleted === (url.searchParams.get('lifecycle') === 'deleted'));
        const offset = Number(url.searchParams.get('cursor') ?? '0');
        const limit = Number(url.searchParams.get('limit') ?? '25');
        return json({
          schema: 'x-science.literature-page.v1',
          items: matches.slice(offset, offset + limit),
          total_count: matches.length,
          ...(offset + limit < matches.length ? { next_cursor: String(offset + limit) } : {}),
        });
      }
      if (init.method !== 'PATCH') return;
      const body = JSON.parse(String(init.body));
      writes.push(body);
      const prior = ledger.get(body.operation_id);
      if (prior) return respond(body, prior, writes.length);
      const item = items.find((value) => url.pathname.endsWith('/' + value.id))!;
      expect(body.expected_revision).toBe(item.metadata_revision);
      Object.assign(item, { deleted: false, metadata_revision: item.metadata_revision + 1 });
      const receipt = structuredClone({
        schema: 'x-science.literature-receipt.v1',
        operation_id: body.operation_id,
        item,
      });
      ledger.set(body.operation_id, receipt);
      return respond(body, receipt, writes.length);
    },
  });
  return {
    writes,
    http,
    items,
    ledger,
    delayPreview: (gate: Promise<void>) => {
      previewGate = gate;
    },
    reply: (fn: typeof respond) => {
      respond = fn;
    },
    failRead: (value: boolean) => {
      readFailed = value;
    },
  };
}

function Navigation() {
  const navigate = useNavigate();
  const location = useLocation();
  return (
    <>
      <button onClick={() => navigate('/library?q=different')}>Go active</button>
      <button onClick={() => navigate('/library?lifecycle=deleted')}>Go Trash</button>
      <output aria-label='Restore test route'>{location.search}</output>
    </>
  );
}
function mount(path = '/library?lifecycle=deleted') {
  return renderWithI18n(
    <MemoryRouter initialEntries={[path]}>
      <Navigation />
      <Routes>
        <Route path='/library' element={<LibraryPage />} />
      </Routes>
    </MemoryRouter>,
    'en-US'
  );
}
async function selectPage() {
  await screen.findByLabelText('Select Paper A');
  const check = screen.getByLabelText('Select this page');
  await waitFor(() => expect(check).toBeEnabled());
  fireEvent.click(check);
}
async function openPreview() {
  fireEvent.click(screen.getByText('Restore selected'));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Restore selected' }));
}

it('restores an explicit Trash selection after a complete revision preview and confirmation', async () => {
  const f = fixture();
  await mountCollections('/library?lifecycle=deleted');
  await screen.findByRole('button', { name: 'Paper A' });
  const first = screen.getByRole('checkbox', { name: 'Select Paper A' });
  await waitFor(() => expect(first).toBeEnabled());
  fireEvent.click(first);
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Paper B' }));
  fireEvent.click(screen.getByRole('button', { name: 'Restore selected' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Restore selected' }));
  const confirm = await screen.findByRole('button', { name: 'Restore 2 references' });
  expect(screen.queryByRole('menu')).toBeNull();
  expect(f.writes).toHaveLength(0);
  fireEvent.click(confirm);
  await waitFor(() => expect(f.writes).toHaveLength(2));
  await waitFor(() => expect(screen.queryByRole('button', { name: 'Paper A' })).toBeNull());
  expect(f.items.every((item) => !item.deleted && item.metadata_revision === 2)).toBe(true);
  expect(f.writes[0].operation_id).not.toBe(f.writes[1].operation_id);
});

it('resolves all 205 matching Trash rows with one exclusion and restores the exact 204 snapshot members', async () => {
  const f = fixture(205);
  const controller = getLiteratureMutationController(getRendererAccountOwnerId());
  const started = vi.spyOn(controller, 'startRestoreBulk');
  await mount();
  await selectPage();
  fireEvent.click(screen.getByText('Select all matching references'));
  fireEvent.click(screen.getByLabelText('Select Paper B'));
  await openPreview();
  fireEvent.click(await screen.findByText('Restore 204 references'));
  expect(started).toHaveBeenCalledTimes(1);
  // Observe the real UI-started flight once; do not scan the whole table on every progress mutation.
  await act(async () => {
    await started.mock.results[0].value;
  });
  expect(screen.getByText('204 references restored.')).toBeTruthy();
  await waitFor(() =>
    expect(
      f.http.requests.filter(
        (request) =>
          request.method === 'GET' &&
          request.path.startsWith('/api/literature/items?') &&
          !request.path.includes('restore_preview=true')
      )
    ).toHaveLength(2)
  );
  await waitFor(() => expect(screen.queryByLabelText('Select Paper A')).toBeNull());
  expect(screen.getByLabelText('Select Paper B')).toBeTruthy();
  expect(f.writes).toHaveLength(204);
  expect(new Set(f.writes.map((body) => body.operation_id)).size).toBe(204);
  expect(f.items.filter((item) => item.deleted).map((item) => item.id)).toEqual(['b']);
  expect(f.http.requests.filter((request) => request.path.includes('restore_preview=true'))).toHaveLength(1);
  expect(
    f.http.requests.some((request) => request.path.includes('restore_preview=true') && request.path.includes('cursor='))
  ).toBe(false);
});

it.each(['cancel', 'Escape'])(
  'closes the preview with %s before any write and restores protected trigger focus',
  async (close) => {
    const f = fixture();
    await mount();
    await selectPage();
    const trigger = screen.getByRole('button', { name: 'Restore selected' });
    trigger.focus();
    await openPreview();
    await screen.findByRole('button', { name: 'Restore 2 references' });
    if (close === 'cancel') fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    else
      fireEvent.keyDown(screen.getByRole('button', { name: 'Restore 2 references' }), {
        key: 'Escape',
        code: 'Escape',
        keyCode: 27,
      });
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Restore 2 references' })).toBeNull());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Restore selected' })).toHaveFocus());
    expect(f.writes).toHaveLength(0);
  }
);

it('rejects a late preview after route ABA without granting the returned visit confirmation authority', async () => {
  const f = fixture();
  const gate = deferred<void>();
  f.delayPreview(gate.promise);
  await mount();
  await selectPage();
  await openPreview();
  await screen.findByText('Loading the complete Trash selection…');
  fireEvent.click(screen.getByRole('button', { name: 'Go active' }));
  fireEvent.click(screen.getByRole('button', { name: 'Go Trash' }));
  await act(async () => {
    gate.resolve();
    await Promise.resolve();
  });
  await screen.findByRole('button', { name: 'Paper A' });
  expect(screen.queryByRole('button', { name: 'Restore 2 references' })).toBeNull();
  expect(f.writes).toHaveLength(0);
});

it('keeps a real partial result across remount and resumes only the original unknown item before the suffix', async () => {
  const f = fixture(3);
  f.reply((_body, receipt, index) => (index === 2 ? json({ code: 'UNAVAILABLE' }, 503) : json(receipt)));
  let rendered = await mount();
  await selectPage();
  await openPreview();
  fireEvent.click(await screen.findByRole('button', { name: 'Restore 3 references' }));
  await screen.findByText('1 of 3 confirmed. The original current restore needs confirmation.');
  expect(f.writes).toHaveLength(2);
  rendered.unmount();
  rendered = await mount();
  const resume = await screen.findByRole('button', { name: 'Resume restore' });
  await waitFor(() => expect(resume).toBeEnabled());
  fireEvent.click(resume);
  await screen.findByText('3 references restored.');
  expect(f.writes).toHaveLength(4);
  expect(f.writes[2]).toEqual(f.writes[1]);
  expect(f.ledger.size).toBe(3);
  expect(f.items.every((item) => item.metadata_revision === 2)).toBe(true);
  expect(screen.queryByRole('toolbar', { name: 'Reference selection' })).toBeNull();
});

it('settles an in-flight ACK after navigation without sending the suffix or clearing the new route or focus', async () => {
  const f = fixture(3);
  const gate = deferred<void>();
  f.reply(async (_body, receipt, index) => {
    if (index === 1) await gate.promise;
    return json(receipt);
  });
  await mount();
  await selectPage();
  await openPreview();
  fireEvent.click(await screen.findByRole('button', { name: 'Restore 3 references' }));
  await waitFor(() => expect(f.writes).toHaveLength(1));
  const target = screen.getByRole('button', { name: 'Go active' });
  fireEvent.click(target);
  await screen.findByRole('button', { name: 'Paper A' });
  const focusTarget = screen.getByRole('searchbox', { name: 'Search Library' });
  focusTarget.focus();
  await act(async () => {
    gate.resolve();
    await Promise.resolve();
  });
  await screen.findByText('1 of 3 restored. The remaining references are paused.');
  expect(f.writes).toHaveLength(1);
  expect(screen.getByLabelText('Restore test route')).toHaveTextContent('q=different');
  expect(document.activeElement).toBe(focusTarget);
});

it('reports a failed current GET after confirmed restore and retries that read without replaying writes', async () => {
  const f = fixture();
  f.reply((_body, receipt, index) => {
    if (index === 2) f.failRead(true);
    return json(receipt);
  });
  await mount();
  await selectPage();
  await openPreview();
  fireEvent.click(await screen.findByRole('button', { name: 'Restore 2 references' }));
  await screen.findByText(/Confirmed restores are saved, but the current Library could not be refreshed/);
  expect(f.writes).toHaveLength(2);
  f.failRead(false);
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() => expect(screen.queryByText(/Confirmed restores are saved, but/)).toBeNull());
  expect(f.writes).toHaveLength(2);
});

it('invalidates a held read-only preview on account reset without showing or submitting its old confirmation', async () => {
  const f = fixture();
  const gate = deferred<void>();
  f.delayPreview(gate.promise);
  await mount();
  await selectPage();
  await openPreview();
  await screen.findByText('Loading the complete Trash selection…');
  await act(async () => {
    setRendererAccountOwner('next-restore-account');
    gate.resolve();
    await Promise.resolve();
  });
  expect(screen.queryByRole('button', { name: 'Restore 2 references' })).toBeNull();
  expect(f.writes).toHaveLength(0);
});

it('uses native Enter menu dispatch once and closes the old popup before opening restore confirmation', async () => {
  const f = fixture();
  await mount();
  await selectPage();
  const trigger = screen.getByRole('button', { name: 'Restore selected' });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: 'Enter', code: 'Enter', keyCode: 13 });
  const item = await screen.findByRole('menuitem', { name: 'Restore selected' });
  item.focus();
  fireEvent.keyDown(item, { key: 'Enter', code: 'Enter', keyCode: 13 });
  await screen.findByRole('button', { name: 'Restore 2 references' });
  await waitFor(() => {
    expect(screen.getByRole('button', { name: 'Restore selected' })).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('menu')).toBeNull();
  });
  expect(f.http.requests.filter((request) => request.path.includes('restore_preview=true'))).toHaveLength(1);
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(f.writes).toHaveLength(0);
});

it('caches summary by exact account controller while default progress, Stop and settlement remain live', async () => {
  const f = fixture(3);
  const first = deferred<void>();
  const second = deferred<void>();
  f.reply(async (_body, receipt, index) => {
    if (index === 1) await first.promise;
    if (index === 2) await second.promise;
    return json(receipt);
  });
  const hook = renderHook(() => ({
    summary: useLiteratureMutation(projectLibraryRestoreSummary),
    raw: useLiteratureMutation(),
  }));
  const controller = hook.result.current.summary.controller;
  let flight!: Promise<unknown>;
  act(() => {
    flight = controller.startRestoreBulk(
      f.items.map(({ id, metadata_revision }) => ({ id, metadata_revision })),
      'trash'
    );
  });
  await waitFor(() => expect(f.writes).toHaveLength(1));
  await act(async () => {
    first.resolve();
    await Promise.resolve();
  });
  await waitFor(() => expect(f.writes).toHaveLength(2));
  const raw = hook.result.current.raw.view.pending;
  const summary = hook.result.current.summary.view.pending;
  expect(isLiteratureRestoreBulk(raw) && raw.confirmed_items).toBe(1);
  expect(isLiteratureRestoreBulk(summary) && summary.confirmed_items).toBe(0);
  if (!isLiteratureRestoreBulk(raw)) throw new Error('Missing original plan');
  await act(async () => {
    await controller.pauseRestoreBulk(raw.operation_id);
  });
  expect(hook.result.current.summary.view.pending).toMatchObject({ paused: true, confirmed_items: 1 });
  await act(async () => {
    second.resolve();
    await flight;
  });
  expect(f.writes).toHaveLength(2);
  expect(hook.result.current.summary.view).toMatchObject({
    mutating: false,
    pending: { confirmed_items: 2, paused: true },
  });
  await act(async () => {
    setRendererAccountOwner('projection-next-account');
  });
  expect(hook.result.current.summary.controller).not.toBe(controller);
  expect(hook.result.current.summary.controller).toBe(hook.result.current.raw.controller);
  expect(hook.result.current.summary.view.pending).toBeNull();
});

import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import ArchivedSettings from '@/renderer/pages/settings/ArchivedSettings';
import ArchiveUndoNotice from '@/renderer/components/chat/ArchiveUndoNotice';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { runConversationArchive } from '@/renderer/services/runtime/conversationArchiveOperations';
import type { TChatConversation } from '@/common/config/storage';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/pages/settings/components/SettingsPageWrapper', () => ({
  default: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
}));
const archived = (id = 'frame-a', name = '研究记录'): TChatConversation => ({
  id,
  name,
  type: 'acp',
  created_at: 1,
  modified_at: 2,
  status: 'finished',
  extra: { backend: 'xscience', frame_incarnation_id: `inc-${id}`, presentation_revision: 5, archived_at: 1000 },
});
const active = (item = archived()) => {
  const result = { ...item, extra: { ...item.extra, presentation_revision: 6 } };
  delete result.extra.archived_at;
  return result;
};
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const page = (items: TChatConversation[], cursor?: string) =>
  json({ items, total: items.length, has_more: Boolean(cursor), next_cursor: cursor ?? null });
beforeEach(() => {
  setRendererAccountOwner('archive-dom-owner');
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
});

describe('real archived conversation settings owner', () => {
  it('lists canonical archived data, restores once with the observed CAS and never resumes or deletes a task', async () => {
    let restored = false;
    let finish!: () => void;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/conversations' && url.searchParams.get('section') === 'archived') {
        expect(url.searchParams.get('limit')).toBe('50');
        return page(restored ? [] : [archived()]);
      }
      if (url.pathname === '/api/conversations/frame-a/archive' && options?.method === 'POST')
        return new Promise<Response>((resolve) => {
          expect(JSON.parse(String(options.body))).toEqual({
            archived: false,
            expected_incarnation_id: 'inc-frame-a',
            expected_presentation_revision: 5,
          });
          finish = () => {
            restored = true;
            resolve(json({ conversation: active(), changed: true }));
          };
        });
      throw new Error(`unexpected task operation: ${url.pathname}`);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(<ArchivedSettings />, 'en-US');
    expect(await screen.findByText('研究记录')).toBeVisible();
    const restore = screen.getByRole('button', { name: 'Restore 研究记录' });
    fireEvent.click(restore);
    fireEvent.click(restore);
    await waitFor(() => expect(restore).toBeDisabled());
    expect(fetchMock.mock.calls.filter(([, options]) => options?.method === 'POST')).toHaveLength(1);
    await act(async () => finish());
    expect(await screen.findByText('No archived conversations.')).toBeVisible();
    expect(screen.queryByText('研究记录')).not.toBeInTheDocument();
    expect(fetchMock.mock.calls.every(([input]) => !String(input).match(/resume|messages|cancel|kernel|compute/))).toBe(
      true
    );
    expect(fetchMock.mock.calls.every(([, options]) => options?.method !== 'DELETE')).toBe(true);
  });

  it('preserves previously loaded rows when a page fails and retries that same opaque cursor', async () => {
    let moreReads = 0;
    const observed: Array<string | null> = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), 'http://localhost');
      const cursor = url.searchParams.get('cursor');
      observed.push(cursor);
      if (!cursor) return page([archived()], 'opaque-page-2');
      if (cursor !== 'opaque-page-2') throw new Error('cursor was rebound');
      moreReads += 1;
      return moreReads === 1
        ? json({ detail: 'temporarily unavailable' }, 503)
        : page([archived('frame-b', 'Second result')]);
    });
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(<ArchivedSettings />, 'en-US');
    expect(await screen.findByText('研究记录')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Load more tasks' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Previously loaded items are retained.');
    expect(screen.getByText('研究记录')).toBeVisible();
    expect(screen.queryByText('No archived conversations.')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry', exact: true }));
    expect(await screen.findByText('Second result')).toBeVisible();
    expect(screen.getByText('研究记录')).toBeVisible();
    expect(observed).toEqual([null, 'opaque-page-2', 'opaque-page-2']);
  });

  it('clears old owner rows immediately and rejects a late previous-account listing', async () => {
    let finish!: (value: Response) => void;
    const fetchMock = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<Response>((resolve) => {
            finish = resolve;
          })
      )
      .mockResolvedValue(page([]));
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(<ArchivedSettings />, 'en-US');
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    act(() => {
      setRendererAccountOwner('next-owner');
    });
    expect(await screen.findByText('No archived conversations.')).toBeVisible();
    await act(async () => {
      finish(page([archived()]));
    });
    expect(screen.queryByText('研究记录')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Restore 研究记录' })).not.toBeInTheDocument();
  });

  it('switches English/Chinese controls without translating user-authored titles', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => page([archived()]))
    );
    const { i18n } = await renderWithI18n(<ArchivedSettings />, 'en-US');
    expect(await screen.findByRole('heading', { name: 'Archived' })).toBeVisible();
    expect(await screen.findByRole('button', { name: 'Restore 研究记录' })).toBeVisible();
    await act(async () => {
      await i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('heading', { name: '已归档' })).toBeVisible();
    expect(screen.getByRole('button', { name: '恢复 研究记录' })).toBeVisible();
    expect(screen.getByText('研究记录')).toBeVisible();
  });

  it('shows a real eight-second Undo using the captured presentation, with no save dialog or task action', async () => {
    const item = archived();
    const first = active(item);
    first.extra.presentation_revision = 4;
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(json({ conversation: item, changed: true }))
      .mockResolvedValueOnce(json({ conversation: active(item), changed: true }));
    vi.stubGlobal('fetch', fetchMock);
    await renderWithI18n(<ArchiveUndoNotice />, 'en-US');
    await act(async () => {
      await runConversationArchive(first, true);
    });
    expect(screen.getByRole('status')).toHaveTextContent('Archived “研究记录”.');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Undo', exact: true }));
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument());
    const sent = fetchMock.mock.calls.map((call) => JSON.parse(String(call[1].body)));
    expect(sent).toEqual([
      { archived: true, expected_incarnation_id: 'inc-frame-a', expected_presentation_revision: 4 },
      { archived: false, expected_incarnation_id: 'inc-frame-a', expected_presentation_revision: 5 },
    ]);
  });
});

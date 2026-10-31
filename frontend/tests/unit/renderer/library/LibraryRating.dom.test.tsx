import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Route, Routes } from 'react-router';
import LibraryPage from '@/renderer/pages/library/LibraryPage';
import { decodeLiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import { renderWithI18n, type TestLanguage } from '../../i18nTestUtils';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, paper } from './libraryCollectionsFixtures';
import '@arco-design/web-react/lib/_util/react-19-adapter';

const releases: Array<() => void> = [];
const rated = (value: number, revision = 1) => ({
  ...paper(),
  metadata_revision: revision,
  metadata: { rating: value, opaque: 'Keep this value' },
  rating_view: { known: true, value },
});
async function mount(language: TestLanguage = 'en-US') {
  await renderWithI18n(
    <MemoryRouter initialEntries={['/library?item=a']}>
      <Routes>
        <Route path='/library' element={<LibraryPage />} />
      </Routes>
    </MemoryRouter>,
    language
  );
  return screen.findByRole('dialog', { name: 'Paper A' });
}

describe('Library rating through the real Page/read/field-mutation owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('rating-owner');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const release of releases.splice(0)) release();
      await Promise.resolve();
    });
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it.each(['en-US', 'zh-CN'] as const)(
    'writes and clears real stars with a single field patch in %s',
    async (language) => {
      let item = rated(2);
      const writes: Record<string, unknown>[] = [];
      installCollectionHTTP({
        collections: () => [],
        items: () => [item],
        detail: () => item,
        intercept: (_url, init) => {
          if (init.method !== 'PATCH') return undefined;
          const body = JSON.parse(String(init.body));
          writes.push(body);
          item = rated(body.metadata_patch.set.rating, item.metadata_revision + 1);
          return json({ schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item });
        },
      });
      const dialog = await mount(language);
      const stars = () => within(within(dialog).getByRole('group', { name: language === 'en-US' ? 'Rating' : '评分' }));
      const fiveName = language === 'en-US' ? '5 stars' : '5 星';
      const five = await stars().findByRole('button', { name: fiveName });
      await waitFor(() => expect(five).toBeEnabled());
      const user = userEvent.setup();
      await user.click(five);
      await waitFor(() =>
        expect(stars().getByRole('button', { name: fiveName })).toHaveAttribute('aria-pressed', 'true')
      );
      await waitFor(() => expect(stars().getByRole('button', { name: fiveName })).toBeEnabled());
      expect(writes[0]).toEqual({
        operation_id: expect.any(String),
        expected_revision: 1,
        metadata_patch: { set: { rating: 5 } },
      });
      await user.click(stars().getByRole('button', { name: fiveName }));
      await waitFor(() =>
        expect(stars().getByRole('button', { name: fiveName })).toHaveAttribute('aria-pressed', 'false')
      );
      await waitFor(() => expect(stars().getByRole('button', { name: fiveName })).toBeEnabled());
      expect(writes).toHaveLength(2);
      expect(writes[1]).toEqual({
        operation_id: expect.any(String),
        expected_revision: 2,
        metadata_patch: { set: { rating: 0 } },
      });
      expect(writes[1].operation_id).not.toBe(writes[0].operation_id);
      expect(item.metadata.opaque).toBe('Keep this value');
    }
  );

  it('waits for live readback and retries an uncertain write using the original operation, never rounded metadata', async () => {
    // response.json necessarily rounds the raw metadata, but never the canonical view.
    const item = {
      ...rated(0),
      metadata: { rating: Number('4.9999999999999999999'), opaque: 'Keep this value' },
      rating_view: { known: false as const },
    };
    let saved: ReturnType<typeof rated> | undefined;
    let gateReadback = false;
    const readback = deferred<Response>();
    const seenReadback = deferred<void>();
    const writes: string[] = [];
    releases.push(() => readback.resolve(json(saved ?? item)));
    installCollectionHTTP({
      collections: () => [],
      items: () => [saved ?? item],
      detail: () => saved ?? item,
      intercept: (url, init) => {
        if (url.pathname !== '/api/literature/items/a') return undefined;
        if ((init.method ?? 'GET') === 'GET' && gateReadback) {
          seenReadback.resolve();
          return readback.promise;
        }
        if (init.method !== 'PATCH') return undefined;
        writes.push(String(init.body));
        if (writes.length === 1) return json({ code: 'LITERATURE_INTERNAL' }, 503);
        const body = JSON.parse(String(init.body));
        saved = rated(body.metadata_patch.set.rating, 2);
        gateReadback = true;
        return json({ schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item: saved });
      },
    });
    const dialog = await mount();
    const group = within(dialog).getByRole('group', { name: 'Rating' });
    await within(dialog).findByText('Unrecognized rating');
    expect(
      within(group)
        .getAllByRole('button', { name: /stars?$/ })
        .every((button) => button.getAttribute('aria-pressed') === 'false')
    ).toBe(true);
    await userEvent.setup().click(within(group).getByRole('button', { name: '4 stars' }));
    await within(dialog).findByText('Could not confirm the rating. Check the operation above before retrying.');
    const controller = getLiteratureMutationController(getRendererAccountOwnerId());
    expect(controller.getSnapshot().pending?.operation_id).toBe(JSON.parse(writes[0]).operation_id);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));
    await act(async () => {
      await seenReadback.promise;
    });
    expect(within(group).getByRole('button', { name: '4 stars' })).toBeDisabled();
    expect(writes).toHaveLength(2);
    expect(writes[1]).toBe(writes[0]);
    await act(async () => readback.resolve(json(saved)));
    await waitFor(() => expect(within(group).getByRole('button', { name: '4 stars' })).toBeEnabled());
    expect(within(group).getByRole('button', { name: '4 stars' })).toHaveAttribute('aria-pressed', 'true');
  });

  it('keeps account changes fenced from a late original rating ACK and rejects malformed read-only views', async () => {
    const oldACK = deferred<Response>();
    let finish: (() => void) | undefined;
    let item = rated(1);
    let calls = 0;
    installCollectionHTTP({
      collections: () => [],
      items: () => [item],
      detail: () => item,
      intercept: (_url, init) => {
        if (init.method !== 'PATCH') return undefined;
        calls++;
        const body = JSON.parse(String(init.body));
        finish = () =>
          oldACK.resolve(
            json({ schema: 'x-science.literature-receipt.v1', operation_id: body.operation_id, item: rated(5, 2) })
          );
        releases.push(finish);
        return oldACK.promise;
      },
    });
    const original = await mount();
    await userEvent.setup().click(within(original).getByRole('button', { name: '5 stars' }));
    await waitFor(() => expect(finish).toBeTypeOf('function'));
    item = rated(2, 3);
    await act(async () => {
      setRendererAccountOwner('rating-next-owner');
    });
    const current = await screen.findByRole('dialog', { name: 'Paper A' });
    await waitFor(() => expect(within(current).getByRole('button', { name: '2 stars' })).toBeEnabled());
    await act(async () => finish!());
    expect(within(current).getByRole('button', { name: '2 stars' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(current).getByRole('button', { name: '5 stars' })).toHaveAttribute('aria-pressed', 'false');
    expect(calls).toBe(1);
    const legacy = decodeLiteratureItem({ ...paper(), metadata: { rating: Number('4.999999999999999999') } });
    expect(Object.hasOwn(legacy, 'rating_view')).toBe(false);
    for (const view of [
      { known: true },
      { known: true, value: 6 },
      { known: false, value: 0 },
      { known: true, value: '5' },
      null,
    ]) {
      expect(() => decodeLiteratureItem({ ...paper(), rating_view: view })).toThrow();
    }
  });
});

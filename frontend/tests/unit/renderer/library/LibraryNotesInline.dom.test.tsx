import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  parseLibraryColumnPreferencesWire,
  type LibraryColumnPreferencesWire,
} from '@/common/adapter/libraryColumnPreferences';
import { LIBRARY_COLUMN_PREFERENCES_SETTING } from '@/renderer/services/runtime/libraryColumnPreferences';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

describe('Library inline Notes through the existing Page owners', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('notes-inline-account');
    localStorage.clear();
    installLiteratureLocks();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it('enables the optional Notes column through the original setting and restores its saved six-column layout', async () => {
    let saved: LibraryColumnPreferencesWire = DEFAULT_LIBRARY_COLUMN_PREFERENCES;
    const settingWrites: LibraryColumnPreferencesWire[] = [];
    const item = { ...paper(), metadata: { ...paper().metadata, personal_note: 'A saved note' } };
    const http = installCollectionHTTP({
      collections: () => [],
      items: () => [item],
      intercept: (url, request) => {
        if (url.pathname !== '/api/settings/client') return undefined;
        if ((request.method ?? 'GET') === 'GET') return json({ [LIBRARY_COLUMN_PREFERENCES_SETTING]: saved });
        const body = JSON.parse(String(request.body)) as Record<string, unknown>;
        expect(Object.keys(body)).toEqual([LIBRARY_COLUMN_PREFERENCES_SETTING]);
        const value = parseLibraryColumnPreferencesWire(body[LIBRARY_COLUMN_PREFERENCES_SETTING]);
        if (!value) throw new Error('Invalid Notes-column preference wire');
        saved = value;
        settingWrites.push(value);
        return json({ success: true });
      },
    });
    const first = await mountCollections();
    await screen.findByRole('button', { name: 'Paper A' });
    expect(settingWrites).toEqual([]);
    expect(screen.queryByRole('textbox', { name: 'Note for Paper A' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Customize columns' }));
    const menu = await screen.findByRole('dialog', { name: 'Customize columns' });
    await waitFor(() => expect(within(menu).getByRole('button', { name: 'Hide Authors' })).toBeEnabled());
    const notes = within(menu).queryByRole('button', { name: 'Show Notes' });
    expect(notes, 'the optional Notes column must exist in the real column menu').not.toBeNull();
    expect(notes).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(notes!);
    await waitFor(() => expect(settingWrites).toHaveLength(1));
    expect(saved).toEqual({
      version: 2,
      order: [...DEFAULT_LIBRARY_COLUMN_PREFERENCES.order, 'notes'],
      visible: [...DEFAULT_LIBRARY_COLUMN_PREFERENCES.visible, 'notes'],
    });
    fireEvent.click(within(menu).getByRole('button', { name: 'Close column settings' }));
    expect(await screen.findByRole('textbox', { name: 'Note for Paper A' })).toHaveValue('A saved note');
    first.unmount();
    await mountCollections();
    expect(await screen.findByRole('textbox', { name: 'Note for Paper A' })).toHaveValue('A saved note');
    expect(settingWrites).toHaveLength(1);
    expect(
      http.requests.filter((request) => request.method !== 'GET').every((request) => request.method === 'PUT')
    ).toBe(true);
  });

  it('uses the original pending operation for uncertain PATCH retry and only GET after an acknowledged readback failure', async () => {
    let item: LiteratureItem = {
      ...paper(),
      metadata: { ...paper().metadata, personal_note: 'Original', opaque: { keep: ['unchanged'] } },
    };
    const receipts = new Map<string, unknown>();
    const patches: string[] = [];
    let loseResponse = true;
    let failReadback = true;
    const columns = {
      version: 2,
      order: [...DEFAULT_LIBRARY_COLUMN_PREFERENCES.order, 'notes'],
      visible: [...DEFAULT_LIBRARY_COLUMN_PREFERENCES.visible, 'notes'],
    };
    const http = installCollectionHTTP({
      collections: () => [],
      items: (url) => {
        const query = url.searchParams.get('query');
        return !query || String(item.metadata.personal_note ?? '').includes(query) ? [item] : [];
      },
      intercept: (url, request) => {
        const method = request.method ?? 'GET';
        if (url.pathname === '/api/settings/client') return json({ [LIBRARY_COLUMN_PREFERENCES_SETTING]: columns });
        if (method === 'GET' && url.pathname === '/api/literature/items' && patches.length === 2 && failReadback) {
          failReadback = false;
          return json({ error: 'Synthetic readback unavailable' }, 503);
        }
        if (method !== 'PATCH' || url.pathname !== '/api/literature/items/a') return undefined;
        patches.push(String(request.body));
        const body = JSON.parse(String(request.body)) as {
          operation_id: string;
          expected_revision: number;
          metadata_patch: { set?: Record<string, unknown>; remove?: string[] };
        };
        expect(Object.keys(body).sort()).toEqual(['expected_revision', 'metadata_patch', 'operation_id']);
        if (!receipts.has(body.operation_id)) {
          if (body.expected_revision !== item.metadata_revision) return json({ error: 'Revision changed' }, 409);
          const metadata: Record<string, unknown> = { ...item.metadata, ...body.metadata_patch.set };
          body.metadata_patch.remove?.forEach((key) => delete metadata[key]);
          item = { ...item, metadata_revision: item.metadata_revision + 1, metadata };
          receipts.set(body.operation_id, {
            schema: 'x-science.literature-receipt.v1',
            operation_id: body.operation_id,
            item,
          });
        }
        if (loseResponse) {
          loseResponse = false;
          throw new TypeError('Synthetic response lost after durable commit');
        }
        return json(receipts.get(body.operation_id));
      },
    });
    const first = await mountCollections();
    const input = (await screen.findByRole('textbox', { name: 'Note for Paper A' })) as HTMLInputElement;
    act(() => input.focus());
    fireEvent.change(input, { target: { value: '  Changed note  ' } });
    fireEvent.blur(input);
    const control = within(input.closest('.library-note') as HTMLElement);
    await waitFor(() => expect(control.getByRole('alert')).toHaveTextContent('Draft preserved'));
    expect(input).toHaveValue('  Changed note  ');
    expect(patches).toHaveLength(1);
    expect(JSON.parse(patches[0]).metadata_patch).toEqual({ set: { personal_note: 'Changed note' } });
    fireEvent.click(control.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(control.getByRole('alert')).toHaveTextContent('Saved, but could not be reloaded'));
    expect(
      screen.queryByText(first.i18n.t('conversation.reading.libraryRejected')),
      'an acknowledged row retry must clear its old global failure feedback'
    ).toBeNull();
    expect(patches).toHaveLength(2);
    expect(patches[1]).toBe(patches[0]);
    expect(screen.getByRole('textbox', { name: 'Note for Paper A' })).toBe(input);
    const readCount = http.requests.filter(
      (request) => request.method === 'GET' && request.path.startsWith('/api/literature/items?')
    ).length;
    fireEvent.click(control.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(input).toHaveValue('Changed note'));
    await waitFor(() => expect(input).not.toHaveAttribute('readonly'));
    expect(control.queryByRole('alert')).toBeNull();
    expect(screen.queryByText(first.i18n.t('conversation.reading.libraryRejected'))).toBeNull();
    expect(patches).toHaveLength(2);
    expect(
      http.requests.filter((request) => request.method === 'GET' && request.path.startsWith('/api/literature/items?'))
    ).toHaveLength(readCount + 1);
    expect(item.metadata.opaque).toEqual({ keep: ['unchanged'] });
    first.unmount();
    await mountCollections('/library?q=Changed');
    const searched = (await screen.findByRole('textbox', { name: 'Note for Paper A' })) as HTMLInputElement;
    expect(searched).toHaveValue('Changed note');
    expect(http.requests.some((request) => request.method === 'GET' && request.path.includes('query=Changed'))).toBe(
      true
    );
    act(() => searched.focus());
    fireEvent.change(searched, { target: { value: '' } });
    fireEvent.blur(searched);
    await waitFor(() => expect(patches).toHaveLength(3));
    expect(JSON.parse(patches[2]).metadata_patch).toEqual({ remove: ['personal_note'] });
    await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Note for Paper A' })).toBeNull());
    expect(Object.hasOwn(item.metadata, 'personal_note')).toBe(false);
    expect(item.metadata.opaque).toEqual({ keep: ['unchanged'] });
  });
});

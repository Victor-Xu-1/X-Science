import React from 'react';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand } from '@/common/adapter/literatureLibrary';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { loadLiteraturePage, runLiteratureCommand } from '@/renderer/services/runtime/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';

const originalTitle = 'Original paper';
const opaqueMetadata = `{"authors":["Ada","Grace"],"year":2024,"opaque":{"integer":9007199254740993,"fraction":0.12345678901234567890123456789,"exponent":1e400}}`;
const safeTypedMetadata = `{"authors":["Ada","Grace"],"year":2024,"reviewed":false,"other":["x",{"n":1.25}]}`;
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;
type CapturedWrite = { path: string; raw: string; body: Record<string, unknown> };

const jsonResponse = (raw: string) =>
  new Response(raw, { status: 200, headers: { 'Content-Type': 'application/json' } });
const itemJSON = (metadata: string, title = originalTitle, revision = 7) =>
  `{"id":"paper","title":${JSON.stringify(title)},"metadata_revision":${revision},"metadata":${metadata},"deleted":false,"project_ids":[],"attachments":[]}`;

async function renderEditor(metadataJSON: string) {
  const writes: CapturedWrite[] = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (init?.method === 'GET' && url.pathname === '/api/literature/items') {
      expect(url.searchParams.get('limit')).toBe('20');
      // Do not stringify a JS object here: the real Response.json() must parse the original lexemes.
      return jsonResponse(`{"schema":"x-science.literature-page.v1","items":[${itemJSON(metadataJSON)}]}`);
    }
    if (init?.method !== 'PATCH' || url.pathname !== '/api/literature/items/paper' || typeof init.body !== 'string')
      throw new Error(`Unexpected fixture request: ${init?.method} ${url.pathname}`);
    const body = JSON.parse(init.body) as Record<string, unknown>;
    writes.push({ path: url.pathname, raw: init.body, body });
    // Controlled replacement/retention response only; this is not a fake Store precision proof.
    const patch = body.metadata_patch as { set?: Record<string, unknown>; remove?: string[] } | undefined;
    const patchedMetadata = patch ? { ...JSON.parse(metadataJSON), ...patch.set } : undefined;
    for (const key of patch?.remove ?? []) delete patchedMetadata[key];
    const metadata = Object.hasOwn(body, 'metadata')
      ? JSON.stringify(body.metadata)
      : patch
        ? JSON.stringify(patchedMetadata)
        : metadataJSON;
    if (typeof metadata !== 'string') throw new Error('Fixture metadata must remain JSON');
    const title = typeof body.title === 'string' ? body.title : originalTitle;
    return jsonResponse(
      `{"schema":"x-science.literature-receipt.v1","operation_id":${JSON.stringify(body.operation_id)},"item":${itemJSON(metadata, title, 8)}}`
    );
  });
  vi.stubGlobal('fetch', fetchMock);
  const page = await loadLiteraturePage();
  expect(page.items).toHaveLength(1);
  const item = page.items[0];
  const close = vi.fn();
  const submit = vi.fn((command: LiteratureCommand) => runLiteratureCommand(command));
  await renderWithI18n(
    <LibraryItemEditor item={item} projectId='project' busy={false} submit={submit} close={close} />,
    'en-US'
  );
  return { item, close, submit, fetchMock, writes };
}

async function saveEditor(fixture: Awaited<ReturnType<typeof renderEditor>>, kind: 'edit' | 'edit-metadata' = 'edit') {
  fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
  await waitFor(() => expect(fixture.close).toHaveBeenCalledTimes(1));
  expect(fixture.submit).toHaveBeenCalledTimes(1);
  expect(fixture.fetchMock).toHaveBeenCalledTimes(2);
  expect(fixture.writes).toHaveLength(1);
  const write = fixture.writes[0];
  expect(write.path).toBe('/api/literature/items/paper');
  expect(write.body.operation_id).toMatch(uuid);
  expect(write.body.expected_revision).toBe(7);
  expect(fixture.submit.mock.calls[0][0]).toMatchObject({
    kind,
    item_id: 'paper',
    operation_id: write.body.operation_id,
    expected_revision: 7,
  });
  return write;
}

describe('Library item editor metadata wire preservation', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-editor-owner');
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it.each([
    { next: ' 10.5555/updated ', patch: { set: { doi: '10.5555/updated' } } },
    { next: '', patch: { remove: ['doi'] } },
  ])('edits the DOI through a field patch without replacing untouched metadata: $next', async ({ next, patch }) => {
    const fixture = await renderEditor(opaqueMetadata.replace('"year":2024', '"year":2024,"doi":"10.5555/original"'));
    fireEvent.change(screen.getByRole('textbox', { name: /^DOI$/ }), { target: { value: next } });
    const write = await saveEditor(fixture, 'edit-metadata');
    expect(write.body.metadata_patch).toEqual(patch);
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(Object.hasOwn(write.body, 'title')).toBe(false);
    expect(write.raw).not.toContain('9007199254740993');
    expect(fixture.item.metadata.authors).toEqual(['Ada', 'Grace']);
    expect(fixture.item.metadata.year).toBe(2024);
    expect(fixture.item.metadata.doi).toBe('10.5555/original');
  });

  it('omits untouched opaque metadata from a title-only PATCH after native JSON parsing', async () => {
    const fixture = await renderEditor(opaqueMetadata);
    expect(fixture.item.metadata.authors).toEqual(['Ada', 'Grace']);
    expect(fixture.item.metadata.year).toBe(2024);
    const opaque = fixture.item.metadata.opaque as Record<string, unknown>;
    expect(opaque.integer).toBe(9007199254740992);
    expect(opaque.exponent).toBe(Infinity);
    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: '  Renamed paper  ' } });
    const write = await saveEditor(fixture);
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(write.raw).not.toMatch(/"metadata"\s*:/);
    expect(write.body).toEqual({
      operation_id: expect.stringMatching(uuid),
      expected_revision: 7,
      title: 'Renamed paper',
    });
  });

  it('omits metadata when temporary authors and year changes return to the initial form values', async () => {
    const fixture = await renderEditor(opaqueMetadata);
    const authors = screen.getByRole('textbox', { name: 'Authors' }) as HTMLInputElement;
    const year = screen.getByRole('textbox', { name: 'Year' }) as HTMLInputElement;
    const initialAuthors = authors.value;
    const initialYear = year.value;
    fireEvent.change(authors, { target: { value: 'Temporary author' } });
    fireEvent.change(year, { target: { value: '2025' } });
    fireEvent.change(authors, { target: { value: initialAuthors } });
    fireEvent.change(year, { target: { value: initialYear } });
    fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), {
      target: { value: 'Title after reverted edits' },
    });
    const write = await saveEditor(fixture);
    expect(write.body.title).toBe('Title after reverted edits');
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(write.raw).not.toMatch(/"metadata"\s*:/);
  });

  it('preserves numeric year and unedited typed fields when only authors actually change', async () => {
    const fixture = await renderEditor(safeTypedMetadata);
    fireEvent.change(screen.getByRole('textbox', { name: 'Authors' }), { target: { value: 'Edited authors' } });
    const write = await saveEditor(fixture, 'edit-metadata');
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(write.body.metadata_patch).toEqual({ set: { authors: 'Edited authors' } });
    expect(fixture.item.metadata.year).toBe(2024);
    expect(fixture.item.metadata).toEqual({
      authors: ['Ada', 'Grace'],
      year: 2024,
      reviewed: false,
      other: ['x', { n: 1.25 }],
    });
  });

  it('preserves array authors and unedited typed fields when only year actually changes', async () => {
    const fixture = await renderEditor(safeTypedMetadata);
    fireEvent.change(screen.getByRole('textbox', { name: 'Year' }), { target: { value: '2025' } });
    const write = await saveEditor(fixture, 'edit-metadata');
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(write.body.metadata_patch).toEqual({ set: { year: '2025' } });
    expect(fixture.item.metadata.authors).toEqual(['Ada', 'Grace']);
    expect(fixture.item.metadata).toEqual({
      authors: ['Ada', 'Grace'],
      year: 2024,
      reviewed: false,
      other: ['x', { n: 1.25 }],
    });
  });
});

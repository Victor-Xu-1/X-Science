import React from 'react';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureCommand } from '@/common/adapter/literatureLibrary';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { loadLiteraturePage, runLiteratureCommand } from '@/renderer/services/runtime/literatureLibrary';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';

const originalTitle = 'Original paper';
const opaqueJSON = '{"integer":9007199254740993,"fraction":0.12345678901234567890123456789,"exponent":1e400}';
const metadataJSON = `{"authors":["Ada","Grace"],"year":2024,"opaque":${opaqueJSON}}`;
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;
const jsonResponse = (raw: string) =>
  new Response(raw, { status: 200, headers: { 'Content-Type': 'application/json' } });
const itemJSON = (metadata: string, title = originalTitle, revision = 7) =>
  `{"id":"paper","title":${JSON.stringify(title)},"metadata_revision":${revision},"metadata":${metadata},"deleted":false,"project_ids":[],"attachments":[]}`;

async function renderEditor() {
  const writes: Array<{ raw: string; body: Record<string, unknown> }> = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), 'http://localhost');
    if (init?.method === 'GET' && url.pathname === '/api/literature/items')
      return jsonResponse(`{"schema":"x-science.literature-page.v1","items":[${itemJSON(metadataJSON)}]}`);
    if (init?.method !== 'PATCH' || url.pathname !== '/api/literature/items/paper' || typeof init.body !== 'string')
      throw new Error(`Unexpected fixture request: ${init?.method} ${url.pathname}`);
    const body = JSON.parse(init.body) as Record<string, unknown>;
    writes.push({ raw: init.body, body });
    const patch = body.metadata_patch as { set?: Record<string, unknown> } | undefined;
    const set = patch?.set ?? {};
    // This response preserves fixed untouched JSON lexemes. It is a controlled
    // transport ACK, not an implementation or proof of a backend Store merge.
    const authors = Object.hasOwn(set, 'authors') ? JSON.stringify(set.authors) : '["Ada","Grace"]';
    const year = Object.hasOwn(set, 'year') ? JSON.stringify(set.year) : '2024';
    const retained = `{"authors":${authors},"year":${year},"opaque":${opaqueJSON}}`;
    // Let old full-replacement code reach its old ACK, so RED is the actual
    // retransmitted metadata assertion rather than a fixture decode failure.
    const metadata = Object.hasOwn(body, 'metadata') ? JSON.stringify(body.metadata) : retained;
    if (typeof metadata !== 'string') throw new Error('Fixture metadata must be JSON');
    return jsonResponse(
      `{"schema":"x-science.literature-receipt.v1","operation_id":${JSON.stringify(body.operation_id)},"item":${itemJSON(metadata, typeof body.title === 'string' ? body.title : originalTitle, 8)}}`
    );
  });
  vi.stubGlobal('fetch', fetchMock);
  const page = await loadLiteraturePage();
  const item = page.items[0];
  expect(item.metadata.authors).toEqual(['Ada', 'Grace']);
  expect(item.metadata.year).toBe(2024);
  const opaque = item.metadata.opaque as Record<string, unknown>;
  expect(opaque.integer).toBe(9007199254740992);
  expect(opaque.exponent).toBe(Infinity);
  const close = vi.fn();
  const submit = vi.fn((command: LiteratureCommand) => runLiteratureCommand(command));
  await renderWithI18n(
    <LibraryItemEditor item={item} projectId='project' busy={false} submit={submit} close={close} />,
    'en-US'
  );
  return { item, close, submit, fetchMock, writes };
}

async function saveEditor(fixture: Awaited<ReturnType<typeof renderEditor>>) {
  fireEvent.click(screen.getByRole('button', { name: /^Save$/ }));
  await waitFor(() => expect(fixture.close).toHaveBeenCalledTimes(1));
  expect(fixture.submit).toHaveBeenCalledTimes(1);
  expect(fixture.fetchMock).toHaveBeenCalledTimes(2);
  expect(fixture.writes).toHaveLength(1);
  return fixture.writes[0];
}

describe('Library editor field metadata delta wire', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('library-field-editor-owner');
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('sends only changed authors and omits untouched opaque metadata after native JSON parsing', async () => {
    const fixture = await renderEditor();
    fireEvent.change(screen.getByRole('textbox', { name: 'Authors' }), { target: { value: '  Edited authors  ' } });
    const write = await saveEditor(fixture);
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(write.body).toEqual({
      operation_id: expect.stringMatching(uuid),
      expected_revision: 7,
      metadata_patch: { set: { authors: 'Edited authors' } },
    });
    expect(fixture.submit.mock.calls[0][0]).toMatchObject({ kind: 'edit-metadata', item_id: 'paper' });
    expect(write.raw).not.toMatch(/"opaque"|"integer"|"fraction"|"exponent"|"year"|"kind"|"item_id"/);
    expect(fixture.item.metadata.year).toBe(2024);
    expect((fixture.item.metadata.opaque as Record<string, unknown>).exponent).toBe(Infinity);
  });

  it('sends only changed year and retains untouched array authors and opaque metadata', async () => {
    const fixture = await renderEditor();
    fireEvent.change(screen.getByRole('textbox', { name: 'Year' }), { target: { value: '2025' } });
    const write = await saveEditor(fixture);
    expect(Object.hasOwn(write.body, 'metadata')).toBe(false);
    expect(write.body).toEqual({
      operation_id: expect.stringMatching(uuid),
      expected_revision: 7,
      metadata_patch: { set: { year: '2025' } },
    });
    expect(fixture.submit.mock.calls[0][0]).toMatchObject({ kind: 'edit-metadata', item_id: 'paper' });
    expect(write.raw).not.toMatch(/"opaque"|"integer"|"fraction"|"exponent"|"authors"|"kind"|"item_id"/);
    expect(fixture.item.metadata.authors).toEqual(['Ada', 'Grace']);
    expect((fixture.item.metadata.opaque as Record<string, unknown>).exponent).toBe(Infinity);
  });
});

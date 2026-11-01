import React, { type ComponentProps } from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { LibraryPdfAnnotations } from '@/renderer/pages/library/LibraryPdfAnnotations';
import { ArtifactSelectionAnnotationModal } from '@/renderer/pages/artifact/ArtifactSelectionAnnotationModal';
import type PdfViewer from '@/renderer/pages/artifact/XSciencePdfArtifactViewer';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from './conversation/literatureMutationLockFixtures';
import { renderWithI18n } from '../i18nTestUtils';

const checksum = 'a'.repeat(64);
const page = (annotations: unknown[] = []) =>
  new Response(
    JSON.stringify({
      target_key: 'litv:litv_one',
      current_checksum: checksum,
      annotations,
      total: annotations.length,
      next_cursor: null,
    }),
    { status: 200 }
  );
const dto = (target = 'litv:litv_one', type = 'text_selection') => ({
  id: '00000000-0000-4000-8000-000000000001',
  label: '①',
  type,
  text: 'Current edited note',
  target_key: target,
  content_checksum: checksum,
  page_number: type === 'text_selection' ? 2 : null,
  selection_text: type === 'text_selection' ? 'Selected PDF text' : null,
  selection_prefix: type === 'text_selection' ? 'before' : null,
  x_percent: type === 'point' ? 25 : null,
  y_percent: type === 'point' ? 50 : null,
  created_at: '2026-11-01T00:00:00Z',
});
const Viewer = ({ onSelectionChange, annotations }: ComponentProps<typeof PdfViewer>) => (
  <div>
    <button
      type='button'
      onClick={() =>
        onSelectionChange?.({
          type: 'text_selection',
          text: 'Selected PDF text',
          x: 0,
          y: 0,
          startLine: 1,
          startColumn: null,
          endLine: 1,
          endColumn: null,
          selectionPrefix: 'before',
          pageNumber: 2,
        })
      }
    >
      Select PDF text
    </button>
    <span data-testid='note-count'>{annotations?.length}</span>
  </div>
);
beforeEach(() => {
  setRendererAccountOwner('');
  localStorage.clear();
  setRendererAccountOwner('owner');
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner('');
  localStorage.clear();
  vi.unstubAllGlobals();
});

it('restores the Library intent through a failed list without using the new over-limit draft', async () => {
  let gets = 0;
  const posts: Array<{ path: string; body: unknown; key: string | null }> = [];
  const fetch = vi.fn(async (path: string, init: RequestInit = {}) => {
    if (!init.method || init.method === 'GET') {
      ++gets;
      return gets === 1 ? page() : new Response(JSON.stringify({ detail: 'Source unavailable' }), { status: 404 });
    }
    posts.push({ path, body: JSON.parse(String(init.body)), key: new Headers(init.headers).get('Idempotency-Key') });
    if (posts.length === 1) throw new TypeError('ACK lost');
    return new Response(JSON.stringify(dto()), { status: 201 });
  });
  vi.stubGlobal('fetch', fetch);
  const first = await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='paper.pdf' contentUrl='/content' versionId='litv_one' />,
    'en-US'
  );
  await waitFor(() => expect(gets).toBe(1));
  fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Original note' } });
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Resume original save' })).toBeEnabled());
  first.unmount();
  await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='paper.pdf' contentUrl='/content' versionId='litv_one' />,
    'en-US'
  );
  await waitFor(() => expect(gets).toBe(2));
  fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
  const draft = 'B'.repeat(4001);
  fireEvent.change(screen.getByRole('textbox'), { target: { value: draft } });
  expect(screen.getByRole('button', { name: 'Add' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Resume original save' }));
  expect(await screen.findByRole('button', { name: '① · Current edited note' })).toBeInTheDocument();
  expect(posts).toHaveLength(2);
  expect(posts[1]).toEqual(posts[0]);
  expect(posts[0].key).toBeTruthy();
  expect(posts[0].body).toMatchObject({ text: 'Original note', content_checksum: checksum, page_number: 2 });
  expect(screen.getByRole('textbox')).toHaveValue(draft);
  expect(screen.getByTestId('note-count')).toHaveTextContent('1');
  expect(gets).toBe(2);
});

it('retries a frozen Artifact selection despite an invalid new draft and preserves that draft', async () => {
  const posts: Array<{ body: unknown; key: string | null }> = [];
  const fetch = vi.fn(async (_path: string, init: RequestInit = {}) => {
    expect(init.method).toBe('POST');
    posts.push({ body: JSON.parse(String(init.body)), key: new Headers(init.headers).get('Idempotency-Key') });
    if (posts.length === 1) throw new TypeError('ACK lost');
    return new Response(JSON.stringify(dto('av:version-1', 'point')), { status: 201 });
  });
  vi.stubGlobal('fetch', fetch);
  const onCreated = vi.fn();
  const onRecovered = vi.fn();
  await renderWithI18n(
    <ArtifactSelectionAnnotationModal
      artifactId='artifact-1'
      versionId='version-1'
      selection={{ type: 'point', text: 'Image point', x: 0, y: 0, xPercent: 25, yPercent: 50, pageNumber: null }}
      sourceHint={{ targetKey: 'av:version-1', checksum }}
      onCancel={vi.fn()}
      onCreated={onCreated}
      onRecovered={onRecovered}
    />,
    'en-US'
  );
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'Original note' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Resume original save' })).toBeEnabled());
  const draft = 'C'.repeat(4001);
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: draft } });
  expect(within(dialog).getByRole('button', { name: 'Add' })).toBeDisabled();
  fireEvent.click(within(dialog).getByRole('button', { name: 'Resume original save' }));
  await waitFor(() => expect(onRecovered).toHaveBeenCalledOnce());
  expect(onCreated).not.toHaveBeenCalled();
  expect(within(dialog).getByRole('textbox')).toHaveValue(draft);
  expect(posts).toHaveLength(2);
  expect(posts[1]).toEqual(posts[0]);
  expect(posts[0].body).toMatchObject({
    text: 'Original note',
    x_percent: 25,
    y_percent: 50,
    content_checksum: checksum,
  });
});

it('refreshes a committed-missing note and fences an older list response without clearing a new draft', async () => {
  let gets = 0;
  let staleReply!: (response: Response) => void;
  const staleRead = new Promise<Response>((resolve) => {
    staleReply = resolve;
  });
  const posts: Array<{ path: string; body: unknown; key: string | null }> = [];
  const saved = dto();
  const fetch = vi.fn(async (path: string, init: RequestInit = {}) => {
    if (!init.method || init.method === 'GET') {
      ++gets;
      if (gets === 2) return page([saved]);
      if (gets === 3) return staleRead;
      return page();
    }
    posts.push({ path, body: JSON.parse(String(init.body)), key: new Headers(init.headers).get('Idempotency-Key') });
    if (posts.length === 1) throw new TypeError('ACK lost after commit');
    // Protocol fixture: the other tab deleted the accepted result before this exact retry.
    return new Response(
      JSON.stringify({ code: 'ANNOTATION_COMMITTED_RESULT_MISSING', detail: 'Committed note was deleted' }),
      { status: 410 }
    );
  });
  vi.stubGlobal('fetch', fetch);
  await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='paper.pdf' contentUrl='/content' versionId='litv_one' />,
    'en-US'
  );
  await waitFor(() => expect(gets).toBe(1));
  fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Original note' } });
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Resume original save' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Refresh annotations' }));
  expect(await screen.findByRole('button', { name: '① · Current edited note' })).toBeInTheDocument();
  const draft = 'New draft remains unchanged';
  fireEvent.change(screen.getByRole('textbox'), { target: { value: draft } });
  fireEvent.click(screen.getByRole('button', { name: 'Refresh annotations' }));
  await waitFor(() => expect(gets).toBe(3));
  fireEvent.click(screen.getByRole('button', { name: 'Resume original save' }));
  await waitFor(() => expect(gets).toBe(4));
  await waitFor(() => expect(screen.getByTestId('note-count')).toHaveTextContent('0'));
  await act(async () => {
    staleReply(page([saved]));
    await staleRead;
  });
  expect(screen.queryByRole('button', { name: '① · Current edited note' })).not.toBeInTheDocument();
  expect(screen.getByTestId('note-count')).toHaveTextContent('0');
  expect(screen.getByRole('textbox')).toHaveValue(draft);
  expect(posts).toHaveLength(2);
  expect(posts[1]).toEqual(posts[0]);
  expect(Object.keys(localStorage).filter((key) => key.startsWith('x-science.annotation-create.v1:'))).toEqual([]);
});

it('reports a missing Artifact selection result without replaying success or clearing the current draft', async () => {
  const posts: Array<{ body: unknown; key: string | null }> = [];
  vi.stubGlobal(
    'fetch',
    vi.fn(async (_path: string, init: RequestInit = {}) => {
      expect(init.method).toBe('POST');
      posts.push({ body: JSON.parse(String(init.body)), key: new Headers(init.headers).get('Idempotency-Key') });
      if (posts.length === 1) throw new TypeError('ACK lost after commit');
      return new Response(
        JSON.stringify({ code: 'ANNOTATION_COMMITTED_RESULT_MISSING', detail: 'Committed note was deleted' }),
        { status: 410 }
      );
    })
  );
  const onCreated = vi.fn();
  const onRecovered = vi.fn();
  const onMissing = vi.fn();
  await renderWithI18n(
    <ArtifactSelectionAnnotationModal
      artifactId='artifact-1'
      versionId='version-1'
      selection={{ type: 'point', text: 'Image point', x: 0, y: 0, xPercent: 25, yPercent: 50, pageNumber: null }}
      sourceHint={{ targetKey: 'av:version-1', checksum }}
      onCancel={vi.fn()}
      onCreated={onCreated}
      onRecovered={onRecovered}
      onMissing={onMissing}
    />,
    'en-US'
  );
  const dialog = await screen.findByRole('dialog');
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: 'Original note' } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Resume original save' })).toBeEnabled());
  const draft = 'D'.repeat(4001);
  fireEvent.change(within(dialog).getByRole('textbox'), { target: { value: draft } });
  fireEvent.click(within(dialog).getByRole('button', { name: 'Resume original save' }));
  await waitFor(() => expect(onMissing).toHaveBeenCalledOnce());
  expect(onCreated).not.toHaveBeenCalled();
  expect(onRecovered).not.toHaveBeenCalled();
  expect(within(dialog).getByRole('textbox')).toHaveValue(draft);
  expect(within(dialog).getByRole('button', { name: 'Add' })).toBeDisabled();
  expect(posts).toHaveLength(2);
  expect(posts[1]).toEqual(posts[0]);
});

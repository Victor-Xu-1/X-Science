import React, { type ComponentProps } from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { LibraryPdfAnnotations } from '@/renderer/pages/library/LibraryPdfAnnotations';
import type PdfViewer from '@/renderer/pages/artifact/XSciencePdfArtifactViewer';
import { renderWithI18n } from '../../i18nTestUtils';

const checksum = 'a'.repeat(64);
const row = {
  id: 'note-1',
  label: '①',
  type: 'text_selection',
  text: 'Saved Library note',
  selection_text: 'Selected text 🧪',
  page_number: 2,
  target_key: 'litv:litv_one',
  content_checksum: checksum,
};
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
const Viewer = ({ onSelectionChange, annotations, targetPage }: ComponentProps<typeof PdfViewer>) => (
  <div>
    <button
      type='button'
      onClick={() =>
        onSelectionChange?.({
          type: 'text_selection',
          text: 'Selected text 🧪',
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
    <button
      type='button'
      onClick={() =>
        onSelectionChange?.({
          type: 'text_selection',
          text: 'Selected text 🧪',
          x: 400,
          y: 800,
          startLine: 1,
          startColumn: null,
          endLine: 1,
          endColumn: null,
          selectionPrefix: 'before',
          pageNumber: 2,
        })
      }
    >
      Repeat same PDF anchor
    </button>
    <span data-testid='visible-note-count'>{annotations?.length}</span>
    <span data-testid='target-page'>{targetPage}</span>
  </div>
);

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each(['en-US', 'zh-CN'] as const)(
  'saves the real PDF selection, refreshes the canonical note and reveals it in %s',
  async (language) => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(page())
      .mockResolvedValueOnce(new Response(JSON.stringify(row), { status: 201 }))
      .mockResolvedValueOnce(page([row]));
    vi.stubGlobal('fetch', fetch);
    const view = await renderWithI18n(
      <LibraryPdfAnnotations Viewer={Viewer} filename='paper.pdf' contentUrl='/content' versionId='litv_one' />,
      language
    );
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.getByTestId('library-pdf-annotations')).not.toHaveAttribute('aria-busy', 'true'));
    fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
    const field = screen.getByRole('textbox');
    fireEvent.change(field, { target: { value: 'Saved Library note' } });
    fireEvent.click(screen.getByRole('button', { name: view.i18n.t('preview.artifactAnnotations.add') }));
    await screen.findByRole('button', { name: '① · Saved Library note' });
    const [path, init] = fetch.mock.calls[1];
    expect(path).toBe('/api/literature/attachment-versions/litv_one/annotations');
    expect(JSON.parse(init.body)).toMatchObject({
      type: 'text_selection',
      selection_text: 'Selected text 🧪',
      selection_prefix: 'before',
      page_number: 2,
      text: 'Saved Library note',
      content_checksum: checksum,
    });
    expect(screen.getByTestId('visible-note-count')).toHaveTextContent('1');
    fireEvent.click(screen.getByRole('button', { name: view.i18n.t('preview.artifactAnnotations.refresh') }));
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(3));
    expect(await screen.findByRole('button', { name: '① · Saved Library note' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '① · Saved Library note' }));
    expect(screen.getByTestId('target-page')).toHaveTextContent('2');
  }
);

it('discards a previous account/target response after the preview owner unmounts', async () => {
  let resolve!: (value: Response) => void;
  const fetch = vi
    .fn()
    .mockReturnValueOnce(
      new Promise<Response>((done) => {
        resolve = done;
      })
    )
    .mockResolvedValue(page());
  vi.stubGlobal('fetch', fetch);
  const old = await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='old.pdf' contentUrl='/old' versionId='litv_old' />,
    'en-US'
  );
  old.unmount();
  await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='new.pdf' contentUrl='/new' versionId='litv_one' />,
    'en-US'
  );
  await act(async () => {
    resolve(page([row]));
  });
  expect(screen.queryByRole('button', { name: '① · Saved Library note' })).not.toBeInTheDocument();
  expect(screen.getByTestId('visible-note-count')).toHaveTextContent('0');
});

it('does not deliver an old save ACK into the next account or document owner', async () => {
  let acknowledge!: (value: Response) => void;
  const fetch = vi
    .fn()
    .mockResolvedValueOnce(page())
    .mockReturnValueOnce(
      new Promise<Response>((done) => {
        acknowledge = done;
      })
    )
    .mockResolvedValueOnce(page());
  vi.stubGlobal('fetch', fetch);
  const previous = await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='old.pdf' contentUrl='/old' versionId='litv_old' />,
    'en-US'
  );
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Saved Library note' } });
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
  previous.unmount();
  await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='new.pdf' contentUrl='/new' versionId='litv_one' />,
    'en-US'
  );
  await act(async () => {
    acknowledge(new Response(JSON.stringify(row), { status: 201 }));
  });
  expect(screen.queryByRole('button', { name: '① · Saved Library note' })).not.toBeInTheDocument();
  expect(screen.getByTestId('visible-note-count')).toHaveTextContent('0');
});

it('retains a draft when the same PDF source anchor is emitted at different screen coordinates', async () => {
  const fetch = vi.fn().mockResolvedValue(page());
  vi.stubGlobal('fetch', fetch);
  await renderWithI18n(
    <LibraryPdfAnnotations Viewer={Viewer} filename='paper.pdf' contentUrl='/content' versionId='litv_one' />,
    'en-US'
  );
  await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
  fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
  fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Keep this unfinished note' } });
  expect(screen.getByRole('textbox')).toHaveValue('Keep this unfinished note');
  fireEvent.click(screen.getByRole('button', { name: 'Repeat same PDF anchor' }));
  expect(screen.getByRole('textbox')).toHaveValue('Keep this unfinished note');
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
  expect(screen.getByRole('textbox')).toHaveValue('');
});

it.each(['en-US', 'zh-CN'] as const)(
  'retains an over-limit Library draft and saves the complete UTF16 boundary in %s',
  async (language) => {
    const text = (language === 'zh-CN' ? '界' : 'A').repeat(3998) + '🧪';
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(page())
      .mockResolvedValueOnce(new Response(JSON.stringify({ ...row, text }), { status: 201 }));
    vi.stubGlobal('fetch', fetch);
    const view = await renderWithI18n(
      <LibraryPdfAnnotations Viewer={Viewer} filename='paper.pdf' contentUrl='/content' versionId='litv_one' />,
      language
    );
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: 'Select PDF text' }));
    const field = screen.getByRole('textbox') as HTMLTextAreaElement;
    const add = screen.getByRole('button', { name: view.i18n.t('preview.artifactAnnotations.add') });
    fireEvent.change(field, { target: { value: text + 'x' } });
    expect(field.value.length).toBe(4001);
    expect(add).toBeDisabled();
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(field).not.toHaveAttribute('maxlength');
    expect(screen.getByTestId('library-pdf-annotations').textContent).toMatch(/4001\s*\/\s*4000/);
    fireEvent.change(field, { target: { value: text } });
    await waitFor(() => expect(add).toBeEnabled());
    fireEvent.click(add);
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2));
    const [path, init] = fetch.mock.calls[1];
    expect(path).toBe('/api/literature/attachment-versions/litv_one/annotations');
    expect(JSON.parse(init.body)).toMatchObject({ text, content_checksum: checksum, page_number: 2 });
    expect(await screen.findByRole('button', { name: '① · ' + text })).toBeInTheDocument();
    expect(screen.getByTestId('visible-note-count')).toHaveTextContent('1');
  }
);

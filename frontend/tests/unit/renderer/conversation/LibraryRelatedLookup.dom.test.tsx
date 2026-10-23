import React from 'react';
import { act, cleanup, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { RelatedIssue, RelatedRecord, RelatedRequest, RelatedResult } from '@/common/adapter/literatureRelated';
import { LibraryItemEditor } from '@/renderer/components/chat/SendBox/reading/LibraryItemEditor';
import { LibraryRelatedLookup } from '@/renderer/components/chat/SendBox/reading/LibraryRelatedLookup';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { deferredPdf } from './pdfReadingFixtures';

// Keep the real runtime service and strict response decoder; control only the HTTP boundary.
const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', async (original) => ({
  ...(await original<typeof import('@/common/adapter/httpBridge')>()),
  httpRequest: mocks.http,
}));
const item: LiteratureItem = {
  id: 'library-item',
  title: 'Original Library paper',
  metadata_revision: 3,
  metadata: { authors: 'Original Author', year: '2024', retained: { note: 'original metadata' } },
  deleted: false,
  project_ids: [],
  attachments: [],
};
const token = '11111111-1111-4111-8111-111111111111';
function paper(id = 'b', title = 'Related paper'): RelatedRecord {
  const paperId = id.repeat(40);
  return {
    paper_id: paperId,
    title,
    abstract: 'The complete returned abstract.',
    authors: ['Family, Given', 'Second Author'],
    year: 2024,
    issued_text: '',
    container_title: 'Example journal',
    item_type: 'journalArticle',
    url: `https://www.semanticscholar.org/paper/${paperId}`,
    identifiers: [{ scheme: 'doi', value: '10.1000/example' }],
    volume: '1',
    pages: '1–4',
  };
}
function result(request: RelatedRequest, patch: Partial<RelatedResult> = {}): RelatedResult {
  return {
    schema: 'x-science.literature-related.v1',
    item_id: request.item_id,
    metadata_revision: request.expected_metadata_revision,
    direction: request.direction,
    state: 'ready',
    resolution_token: token,
    seed: paper('a', 'Identified source'),
    records: [paper()],
    ...patch,
  };
}
function captured(index = 0): RelatedRequest {
  return mocks.http.mock.calls[index][2] as RelatedRequest;
}
function signal(index = 0): AbortSignal {
  return (mocks.http.mock.calls[index][3] as { signal: AbortSignal }).signal;
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('owner');
  mocks.http
    .mockReset()
    .mockImplementation(async (_method: string, _path: string, request: RelatedRequest) => result(request));
});
afterEach(cleanup);

it.each(['en-US', 'zh-CN'] as const)(
  'opens both real query directions inside details and preserves the editor draft in %s',
  async (locale) => {
    const submit = vi.fn(),
      close = vi.fn();
    const view = await renderWithI18n(
      <LibraryItemEditor item={item} projectId='project' busy={false} submit={submit} close={close} />,
      locale
    );
    const text = (key: string) => view.i18n.t(`conversation.reading.${key}`);
    fireEvent.change(screen.getByRole('textbox', { name: text('libraryTitle') }), {
      target: { value: 'Unsaved title' },
    });
    fireEvent.click(screen.getByRole('button', { name: text('related.references') }));
    await screen.findByText('Related paper');
    expect(mocks.http).toHaveBeenCalledWith(
      'POST',
      '/api/literature/related',
      {
        item_id: item.id,
        expected_metadata_revision: 3,
        direction: 'references',
        offset: 0,
      },
      expect.objectContaining({ signal: expect.any(AbortSignal), timeoutMs: 35_000 })
    );
    expect(screen.getByText(text('related.readOnlyHint'))).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: view.i18n.t('common.save') })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: view.i18n.t('common.close') }));
    expect(screen.getByRole('textbox', { name: text('libraryTitle') })).toHaveValue('Unsaved title');
    fireEvent.click(screen.getByRole('button', { name: text('related.citations') }));
    await screen.findByText('Related paper');
    expect(captured(1).direction).toBe('citations');
    expect(submit).not.toHaveBeenCalled();
    expect(close).not.toHaveBeenCalled();
  }
);

it('requires explicit title-match confirmation before requesting relationships', async () => {
  mocks.http.mockImplementationOnce(async (_method: string, _path: string, request: RelatedRequest) =>
    result(request, {
      state: 'match-required',
      records: [],
      seed: paper('a', 'Possible title match'),
    })
  );
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  await screen.findByText('Possible title match');
  expect(mocks.http).toHaveBeenCalledTimes(1);
  expect(screen.queryByText('Related paper')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Use this paper' }));
  await screen.findByText('Related paper');
  expect(captured(1)).toMatchObject({ offset: 0, resolution_token: token });
});

it('loads more through the original resolution and deduplicates overlapping pages', async () => {
  mocks.http.mockImplementation(async (_method: string, _path: string, request: RelatedRequest) =>
    result(
      request,
      request.offset === 0 ? { next_offset: 20 } : { records: [paper(), paper('c', 'Next page paper')], omitted: 1 }
    )
  );
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  fireEvent.click(await screen.findByRole('button', { name: 'Load more related papers' }));
  await screen.findByText('Next page paper');
  expect(screen.getAllByText('Related paper')).toHaveLength(1);
  expect(captured(1)).toMatchObject({ offset: 20, resolution_token: token, direction: 'references' });
  expect(screen.getByText('Some relationships have no identifiable paper and were omitted.')).toBeInTheDocument();
  expect(screen.queryByRole('button', { name: 'Load more related papers' })).toBeNull();
});

it.each(['en-US', 'zh-CN'] as const)(
  'distinguishes successful empty results from closed failures in %s',
  async (locale) => {
    mocks.http.mockImplementation(async (_method: string, _path: string, request: RelatedRequest) =>
      result(request, { records: [] })
    );
    const view = await renderWithI18n(
      <LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />,
      locale
    );
    const text = (key: string) => view.i18n.t(`conversation.reading.related.${key}`);
    await screen.findByText(text('emptyReferences'));
    mocks.http.mockImplementationOnce(async (_method: string, _path: string, request: RelatedRequest) =>
      result(request, {
        state: 'unavailable',
        issue: 'no-match',
        records: [],
      })
    );
    fireEvent.click(screen.getByRole('tab', { name: text('citations') }));
    expect(await screen.findByRole('alert')).toHaveTextContent(text('issues.no-match'));
    expect(screen.queryByText(text('emptyCitations'))).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: view.i18n.t('common.retry') }));
    await screen.findByText(text('emptyCitations'));
    expect(screen.queryByRole('alert')).toBeNull();
  }
);

it.each(['rate-limited', 'authentication', 'timeout', 'response-invalid'] as const)(
  'presents %s without turning it into an empty success',
  async (issue: RelatedIssue) => {
    mocks.http.mockImplementation(async (_method: string, _path: string, request: RelatedRequest) =>
      result(request, {
        state: 'unavailable',
        issue,
        records: [],
        ...(issue === 'rate-limited' ? { retry_at: 1_800_000_000_000 } : {}),
      })
    );
    const view = await renderWithI18n(
      <LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />,
      'en-US'
    );
    expect(await screen.findByRole('alert')).toHaveTextContent(
      view.i18n.t(`conversation.reading.related.issues.${issue}`)
    );
    expect(screen.queryByText('No references found.')).toBeNull();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
  }
);

it.each(['expired', 'reference-changed'] as const)(
  'discards an invalidated seed/page for %s and retries from the beginning without its token',
  async (issue) => {
    mocks.http.mockImplementation(async (_method: string, _path: string, request: RelatedRequest) =>
      result(request, {
        next_offset: 20,
      })
    );
    await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
    mocks.http.mockImplementationOnce(async (_method: string, _path: string, request: RelatedRequest) =>
      result(request, {
        state: 'unavailable',
        issue,
        records: [],
      })
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Load more related papers' }));
    await screen.findByRole('alert');
    expect(screen.queryByText('Related paper')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('button', { name: 'Load more related papers' });
    expect(captured(2)).toEqual({
      item_id: item.id,
      expected_metadata_revision: 3,
      direction: 'references',
      offset: 0,
    });
  }
);

it('retains a valid first page after a temporary continuation error and retries the exact page', async () => {
  mocks.http.mockImplementation(async (_method: string, _path: string, request: RelatedRequest) =>
    result(request, request.offset === 0 ? { next_offset: 20 } : { records: [paper('c', 'Recovered continuation')] })
  );
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  mocks.http.mockImplementationOnce(async (_method: string, _path: string, request: RelatedRequest) =>
    result(request, {
      state: 'unavailable',
      issue: 'timeout',
      records: [],
    })
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Load more related papers' }));
  await screen.findByRole('alert');
  expect(screen.getByText('Related paper')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByText('Recovered continuation');
  expect(captured(2)).toEqual(captured(1));
});

it('aborts a cancelled lookup and ignores its late response until an explicit retry', async () => {
  const pending = deferredPdf<RelatedResult>();
  mocks.http.mockReturnValueOnce(pending.promise);
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
  expect(signal().aborted).toBe(true);
  await act(async () => pending.resolve(result(captured(), { records: [paper('c', 'Cancelled response')] })));
  expect(screen.queryByText('Cancelled response')).toBeNull();
  expect(screen.getByRole('status')).toHaveTextContent('Lookup cancelled.');
  expect(mocks.http).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByText('Related paper');
  expect(captured(1)).toEqual(captured());
});

it('aborts an old direction and supports keyboard tab navigation without accepting its late records', async () => {
  const pending = deferredPdf<RelatedResult>();
  mocks.http.mockReturnValueOnce(pending.promise);
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  fireEvent.keyDown(screen.getByRole('tab', { name: 'References' }), { key: 'ArrowRight' });
  await screen.findByText('Related paper');
  expect(signal().aborted).toBe(true);
  expect(captured(1)).toMatchObject({ direction: 'citations', offset: 0 });
  expect(screen.getByRole('tab', { name: 'Cited by' })).toHaveFocus();
  await act(async () => pending.resolve(result(captured(), { records: [paper('c', 'Old direction')] })));
  expect(screen.queryByText('Old direction')).toBeNull();
});

it('hides the old source on item/revision changes and rejects its late response', async () => {
  const pending = deferredPdf<RelatedResult>();
  mocks.http.mockReturnValueOnce(pending.promise);
  const view = await renderWithI18n(
    <LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />,
    'en-US'
  );
  view.rerender(
    <LibraryRelatedLookup
      item={{ ...item, id: 'next-item', metadata_revision: 4, title: 'New source' }}
      initialDirection='references'
      close={vi.fn()}
    />
  );
  await screen.findByText('Related paper');
  expect(signal().aborted).toBe(true);
  expect(captured(1)).toMatchObject({ item_id: 'next-item', expected_metadata_revision: 4 });
  await act(async () => pending.resolve(result(captured(), { records: [paper('c', 'Old source')] })));
  expect(screen.queryByText('Old source')).toBeNull();
  expect(screen.getByText('New source')).toBeInTheDocument();
});

it('clears results and blocks the retained source after an account switch', async () => {
  const pending = deferredPdf<RelatedResult>();
  mocks.http.mockReturnValueOnce(pending.promise);
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  act(() => {
    setRendererAccountOwner('different-owner');
  });
  expect(signal().aborted).toBe(true);
  expect(screen.getByRole('alert')).toHaveTextContent('The account changed.');
  expect(screen.getByRole('tab', { name: 'Cited by' })).toBeDisabled();
  await act(async () => pending.resolve(result(captured(), { records: [paper('c', 'Previous account')] })));
  expect(screen.queryByText('Previous account')).toBeNull();
  expect(mocks.http).toHaveBeenCalledTimes(1);
});

it('does not commit an already-resolved response queued in the same batch as an account change', async () => {
  const pending = deferredPdf<RelatedResult>();
  mocks.http.mockReturnValueOnce(pending.promise);
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  await act(async () => {
    pending.resolve(result(captured(), { records: [paper('c', 'Queued previous account')] }));
    await pending.promise;
    await Promise.resolve();
    await Promise.resolve();
    setRendererAccountOwner('different-owner');
  });
  expect(screen.getByRole('alert')).toHaveTextContent('The account changed.');
  expect(screen.queryByText('Queued previous account')).toBeNull();
  expect(screen.queryByText(item.title)).toBeNull();
});

it('unmounting the query through Close aborts the request without modifying the metadata draft', async () => {
  const pending = deferredPdf<RelatedResult>(),
    submit = vi.fn();
  mocks.http.mockReturnValueOnce(pending.promise);
  await renderWithI18n(
    <LibraryItemEditor item={item} projectId='project' busy={false} submit={submit} close={vi.fn()} />,
    'en-US'
  );
  fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: 'Retained edit' } });
  fireEvent.click(screen.getByRole('button', { name: 'References' }));
  await screen.findByRole('button', { name: 'Cancel' });
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  expect(signal().aborted).toBe(true);
  await act(async () => pending.resolve(result(captured())));
  expect(screen.getByRole('textbox', { name: 'Title' })).toHaveValue('Retained edit');
  expect(screen.queryByText('Related paper')).toBeNull();
  expect(submit).not.toHaveBeenCalled();
});

it('uses the strict response decoder on retry and renders full returned text only as text with a fixed HTTPS source', async () => {
  mocks.http.mockImplementationOnce(async (_method: string, _path: string, request: RelatedRequest) =>
    result(request, { item_id: 'wrong-item' })
  );
  const fullAbstract = 'Complete abstract. '.repeat(400);
  mocks.http.mockImplementation(async (_method: string, _path: string, request: RelatedRequest) =>
    result(request, {
      records: [{ ...paper('b', '<b>Literal bibliographic title</b>'), abstract: fullAbstract }],
    })
  );
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  await screen.findByRole('alert');
  expect(screen.queryByText('No references found.')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  const title = await screen.findByText('<b>Literal bibliographic title</b>');
  expect(title.querySelector('b')).toBeNull();
  expect(screen.getByText((_, element) => element?.className === 'pdf-library-related__abstract')).toHaveTextContent(
    fullAbstract.trim()
  );
  const link = screen.getByRole('link', { name: 'View on Semantic Scholar' });
  expect(link).toHaveAttribute('href', `https://www.semanticscholar.org/paper/${'b'.repeat(40)}`);
  expect(link).toHaveAttribute('rel', 'noopener noreferrer');
});

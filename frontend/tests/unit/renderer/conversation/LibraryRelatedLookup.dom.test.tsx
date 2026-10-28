import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { RelatedIssue, RelatedRecord, RelatedRequest, RelatedResult } from '@/common/adapter/literatureRelated';
import { LibraryItemDetails } from '@/renderer/pages/library/LibraryItemDetails';
import { LibraryRelatedLookup } from '@/renderer/components/chat/SendBox/reading/LibraryRelatedLookup';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { deferredPdf } from './pdfReadingFixtures';
import { installLiteratureLocks } from './literatureMutationLockFixtures';

vi.mock('@/renderer/pages/library/LibraryPublicationCheck', () => ({ LibraryPublicationCheck: (): null => null }));
vi.mock('@/renderer/pages/library/LibraryCollectionMembership', () => ({
  LibraryCollectionMembershipPicker: (): null => null,
}));
vi.mock('@/renderer/pages/library/LibraryAttachmentBrowser', () => ({ LibraryAttachmentBrowser: (): null => null }));
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
  localStorage.clear();
  mocks.http
    .mockReset()
    .mockImplementation(async (_method: string, _path: string, request: RelatedRequest) => result(request));
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it.each(['en-US', 'zh-CN'] as const)(
  'keeps the query mounted but inert during editing and restores view controls in %s',
  async (locale) => {
    const submit = vi.fn(),
      close = vi.fn();
    const view = await renderWithI18n(
      <LibraryItemDetails
        item={item}
        account={getRendererAccountScopeToken()}
        collections={{ status: 'ready' } as never}
        memberships={{ status: 'ready' } as never}
        submitCollection={vi.fn()}
        refreshCollections={vi.fn()}
        projects={[]}
        projectStatus='ready'
        busy={false}
        changeReadingStatus={vi.fn()}
        submit={submit}
        close={close}
      />,
      locale
    );
    const text = (key: string) => view.i18n.t('conversation.reading.' + key);
    fireEvent.click(screen.getByRole('button', { name: text('related.references') }));
    await screen.findByText('Related paper');
    expect(captured()).toMatchObject({
      item_id: item.id,
      expected_metadata_revision: 3,
      direction: 'references',
      offset: 0,
    });
    const related = screen.getByRole('region', { name: text('related.title') });
    fireEvent.click(screen.getByRole('button', { name: view.i18n.t('conversation.libraryWorkbench.edit') }));
    fireEvent.change(screen.getByRole('textbox', { name: text('libraryTitle') }), {
      target: { value: 'Unsaved title' },
    });
    expect(screen.getByRole('textbox', { name: text('libraryTitle') })).toHaveValue('Unsaved title');
    expect(related).toBeInTheDocument();
    expect(related).not.toBeVisible();
    expect(related.closest('.library-workbench__overview')).toHaveAttribute('inert');
    // This bare Details fixture has no Page dirty-exit guard. Back ends this
    // editor; only the now-visible query is interactive again.
    fireEvent.click(screen.getByRole('button', { name: view.i18n.t('conversation.libraryWorkbench.back') }));
    expect(related).toBeVisible();
    expect(screen.queryByRole('textbox', { name: text('libraryTitle') })).toBeNull();
    fireEvent.click(within(related).getByRole('tab', { name: text('related.citations') }));
    await screen.findByText('Related paper');
    expect(captured(1).direction).toBe('citations');
    expect(screen.queryByRole('button', { name: text('related.references') })).toBeNull();
    fireEvent.click(within(related).getByRole('button', { name: view.i18n.t('common.close') }));
    fireEvent.click(screen.getByRole('button', { name: text('related.references') }));
    await waitFor(() => expect(captured(2)).toMatchObject({ direction: 'references' }));
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
  fireEvent.click(screen.getByRole('tab', { name: 'Cited by' }));
  await waitFor(() =>
    expect(captured(2)).toMatchObject({ direction: 'citations', offset: 0, resolution_token: token })
  );
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

it('closing a view-mode query aborts the request and protects the later editor draft', async () => {
  const pending = deferredPdf<RelatedResult>(),
    submit = vi.fn();
  mocks.http.mockReturnValueOnce(pending.promise);
  await renderWithI18n(
    <LibraryItemDetails
      item={item}
      account={getRendererAccountScopeToken()}
      collections={{ status: 'ready' } as never}
      memberships={{ status: 'ready' } as never}
      submitCollection={vi.fn()}
      refreshCollections={vi.fn()}
      projects={[]}
      projectStatus='ready'
      busy={false}
      changeReadingStatus={vi.fn()}
      submit={submit}
      close={vi.fn()}
    />,
    'en-US'
  );
  fireEvent.click(screen.getByRole('button', { name: 'References' }));
  const related = await screen.findByRole('region', { name: 'Related literature' });
  fireEvent.click(within(related).getByRole('button', { name: 'Close' }));
  expect(signal().aborted).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Edit details' }));
  fireEvent.change(screen.getByRole('textbox', { name: 'Title' }), { target: { value: 'Retained edit' } });
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

function stageReceipt(body: Record<string, unknown>, state: 'pending' | 'accepted' | 'dismissed' = 'pending') {
  return {
    schema: 'x-science.literature-candidate-receipt.v1',
    operation_id: body.operation_id,
    action: 'stage',
    candidate: {
      id: 'candidate-one',
      title: body.title,
      provider: 'semantic-scholar',
      external_id: body.external_id,
      state,
      metadata: body.metadata,
      ...(state === 'accepted' ? { item_id: 'existing-item' } : {}),
    },
  };
}
it('stages selected references serially with source and complete identifiers', async () => {
  installLiteratureLocks();
  const first = deferredPdf<unknown>();
  mocks.http.mockImplementation(async (_method: string, path: string, body: Record<string, unknown>) => {
    if (path.endsWith('/related'))
      return result(body as unknown as RelatedRequest, {
        records: [paper('b', 'First paper'), paper('c', 'Second paper')],
      });
    if (path.endsWith('/candidates'))
      return body.external_id === 'b'.repeat(40) ? first.promise : stageReceipt(body, 'accepted');
    throw new Error('unexpected path');
  });
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Select First paper' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Second paper' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add selected to Inbox (2)' }));
  await waitFor(() =>
    expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(1)
  );
  const firstBody = mocks.http.mock.calls.find((call) => String(call[1]).endsWith('/candidates'))![2] as Record<
    string,
    unknown
  >;
  expect(firstBody).toMatchObject({
    provider: 'semantic-scholar',
    external_id: 'b'.repeat(40),
    source: { item_id: item.id, metadata_revision: 3, direction: 'references' },
    metadata: {
      doi: '10.1000/example',
      identifiers: [{ scheme: 'doi', value: '10.1000/example' }],
      authors: ['Family, Given', 'Second Author'],
      abstract: 'The complete returned abstract.',
    },
  });
  await act(async () => first.resolve(stageReceipt(firstBody)));
  await screen.findByText('Already in Library.');
  expect(screen.getByText('Added to Inbox.')).toBeInTheDocument();
  expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(2);
});
it('reopens an uncertain stage and retries the identical durable operation', async () => {
  installLiteratureLocks();
  let attempt = 0;
  mocks.http.mockImplementation(async (_method: string, path: string, body: Record<string, unknown>) => {
    if (path.endsWith('/related')) return result(body as unknown as RelatedRequest);
    if (path.endsWith('/candidates')) {
      if (++attempt === 1) throw new TypeError('lost receipt');
      return stageReceipt(body, 'dismissed');
    }
    throw new Error('unexpected path');
  });
  const render = await renderWithI18n(
    <LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />,
    'en-US'
  );
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Related paper' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add selected to Inbox (1)' }));
  await screen.findByText(/previous Library action needs recovery/);
  render.unmount();
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  await screen.findByRole('checkbox', { name: 'Select Related paper' });
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByText('Previously dismissed; review it in Inbox.');
  const calls = mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'));
  expect(calls).toHaveLength(2);
  expect(calls[1][2]).toEqual(calls[0][2]);
});
it('reports oversized bibliographic metadata without making a staging request', async () => {
  installLiteratureLocks();
  mocks.http.mockImplementation(async (_method: string, path: string, request: RelatedRequest) => {
    if (path.endsWith('/related')) return result(request, { records: [{ ...paper(), abstract: 'X'.repeat(17000) }] });
    throw new Error('should not stage oversized metadata');
  });
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Select Related paper' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add selected to Inbox (1)' }));
  await screen.findByText('This paper has invalid or oversized metadata and cannot be added.');
  expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(0);
});

it('stops a multi-selection batch when its view closes', async () => {
  installLiteratureLocks();
  const first = deferredPdf<unknown>();
  mocks.http.mockImplementation(async (_method: string, path: string, body: Record<string, unknown>) => {
    if (path.endsWith('/related'))
      return result(body as unknown as RelatedRequest, {
        records: [paper('b', 'First paper'), paper('c', 'Second paper')],
      });
    if (path.endsWith('/candidates')) return first.promise;
    throw new Error('unexpected path');
  });
  const rendered = await renderWithI18n(
    <LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />,
    'en-US'
  );
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Select First paper' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Second paper' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add selected to Inbox (2)' }));
  await waitFor(() =>
    expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(1)
  );
  const body = mocks.http.mock.calls.find((call) => String(call[1]).endsWith('/candidates'))![2] as Record<
    string,
    unknown
  >;
  rendered.unmount();
  await act(async () => {
    first.resolve(stageReceipt(body));
    await first.promise;
    await Promise.resolve();
  });
  expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(1);
});
it('stops a multi-selection batch when the account changes', async () => {
  installLiteratureLocks();
  const first = deferredPdf<unknown>();
  mocks.http.mockImplementation(async (_method: string, path: string, body: Record<string, unknown>) => {
    if (path.endsWith('/related'))
      return result(body as unknown as RelatedRequest, {
        records: [paper('b', 'First paper'), paper('c', 'Second paper')],
      });
    if (path.endsWith('/candidates')) return first.promise;
    throw new Error('unexpected path');
  });
  await renderWithI18n(<LibraryRelatedLookup item={item} initialDirection='references' close={vi.fn()} />, 'en-US');
  fireEvent.click(await screen.findByRole('checkbox', { name: 'Select First paper' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Second paper' }));
  fireEvent.click(screen.getByRole('button', { name: 'Add selected to Inbox (2)' }));
  await waitFor(() =>
    expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(1)
  );
  const body = mocks.http.mock.calls.find((call) => String(call[1]).endsWith('/candidates'))![2] as Record<
    string,
    unknown
  >;
  act(() => setRendererAccountOwner('other-owner'));
  await act(async () => {
    first.resolve(stageReceipt(body));
    await first.promise;
    await Promise.resolve();
  });
  expect(mocks.http.mock.calls.filter((call) => String(call[1]).endsWith('/candidates'))).toHaveLength(1);
  expect(screen.queryByText('Added to Inbox.')).toBeNull();
});

import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { decodeLiteratureMetadataDraft } from '@/common/adapter/literatureMetadataLookup';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

const reference = 'https://doi.org/10.1234/SOURCE';
const draft = {
  schema: 'x-science.literature-metadata-draft.v1',
  reference,
  doi: '10.1234/source',
  title: 'Synthetic source reference',
  metadata: {
    doi: '10.1234/source',
    item_type: 'journalArticle',
    authors: ['Ada Researcher', 'Institute; Unit'],
    year: '2024',
    container_title: 'Synthetic journal',
    url: 'https://doi.org/10.1234/source',
  },
};
const saved = {
  ...paper('doi-created', 'Edited source reference'),
  metadata: draft.metadata,
  project_ids: ['project'],
};
const receipt = (operation_id: string) => ({ schema: 'x-science.literature-receipt.v1', operation_id, item: saved });
it('rejects control characters in a returned DOI proposal without weakening the canonical identity boundary', () => {
  expect(decodeLiteratureMetadataDraft(draft, reference).doi).toBe('10.1234/source');
  for (const control of ['\0', '\x01', '\x1f', '\x7f', '\u0085']) {
    const doi = `10.1234/a${control}b`;
    expect(() =>
      decodeLiteratureMetadataDraft({ ...draft, doi, metadata: { ...draft.metadata, doi } }, reference)
    ).toThrow();
  }
});
async function openLookup() {
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Add from DOI' }));
  const dialog = await screen.findByRole('dialog', { name: 'Add from DOI' });
  fireEvent.change(within(dialog).getByRole('textbox', { name: 'DOI or doi.org link' }), {
    target: { value: reference },
  });
  return dialog;
}
async function findDraft() {
  const lookup = await openLookup();
  fireEvent.click(within(lookup).getByRole('button', { name: 'Find reference' }));
  return screen.findByRole('dialog', { name: 'Review DOI reference' });
}

beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('doi-owner');
  localStorage.clear();
  installLiteratureLocks();
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it('offers a DOI lookup from the real Add menu without creating a catalogue operation', async () => {
  const http = installCollectionHTTP({ collections: () => [], items: () => [] });
  await mountCollections();
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Add from DOI' }));
  expect(await screen.findByRole('dialog', { name: 'Add from DOI' })).toBeInTheDocument();
  expect(http.requests.filter((request) => request.method !== 'GET')).toHaveLength(0);
});

it('reviews, edits and returns without writing, then saves once through the original project create owner', async () => {
  let acknowledged = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (acknowledged ? [saved] : []),
    detail: () => saved,
    intercept: (url, init) => {
      if (url.pathname === '/api/projects')
        return json({ projects: [{ project_id: 'project', name: 'Research project' }] });
      if (url.pathname === '/api/literature/metadata-lookup') return json(draft);
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        acknowledged = true;
        return json(receipt(JSON.parse(String(init.body)).operation_id));
      }
    },
  });
  await mountCollections('/library?project=project');
  const lookup = await openLookup();
  const find = within(lookup).getByRole('button', { name: 'Find reference' });
  fireEvent.click(find);
  fireEvent.click(find);
  const review = await screen.findByRole('dialog', { name: 'Review DOI reference' });
  expect(within(review).getByRole('heading', { name: draft.title })).toBeInTheDocument();
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
  fireEvent.click(within(review).getByRole('button', { name: 'Edit details' }));
  fireEvent.change(within(review).getByRole('textbox', { name: 'Title' }), { target: { value: saved.title } });
  fireEvent.click(within(review).getByRole('button', { name: 'Back to review' }));
  expect(within(review).getByRole('heading', { name: saved.title })).toBeInTheDocument();
  expect(within(review).getByRole('button', { name: 'Edit details' })).toHaveFocus();
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
  const save = within(review).getByRole('button', { name: 'Save reference' });
  fireEvent.click(save);
  fireEvent.click(save);
  await waitFor(() => expect(screen.getByLabelText('Library location')).toHaveTextContent('item=doi-created'));
  const lookups = http.requests.filter((r) => r.path === '/api/literature/metadata-lookup');
  const writes = http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST');
  expect(lookups).toHaveLength(1);
  expect(JSON.parse(lookups[0].body!)).toEqual({ reference });
  expect(writes).toHaveLength(1);
  expect(JSON.parse(writes[0].body!)).toMatchObject({
    title: saved.title,
    project_id: 'project',
    metadata: draft.metadata,
  });
});

it('edits one DOI author without splitting an unchanged organization or changing creator order', async () => {
  let acknowledged = false;
  const canonical = { ...saved, metadata: { ...draft.metadata, authors: ['Ada B', 'Institute; Unit'] } };
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (acknowledged ? [canonical] : []),
    detail: () => canonical,
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/metadata-lookup') return json(draft);
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        acknowledged = true;
        return json({ ...receipt(JSON.parse(String(init.body)).operation_id), item: canonical });
      }
    },
  });
  await mountCollections();
  const review = await findDraft();
  fireEvent.click(within(review).getByRole('button', { name: 'Edit details' }));
  const first = within(review).getAllByRole('textbox', { name: /^Authors(?: \d+)?$/ })[0] as HTMLInputElement;
  // Edit only the first displayed name; leave every other character unchanged.
  fireEvent.change(first, { target: { value: first.value.replace('Ada Researcher', 'Ada B') } });
  fireEvent.click(within(review).getByRole('button', { name: 'Back to review' }));
  fireEvent.click(within(review).getByRole('button', { name: 'Save reference' }));
  await waitFor(() => expect(screen.getByLabelText('Library location')).toHaveTextContent('item=doi-created'));
  const writes = http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST');
  expect(writes).toHaveLength(1);
  expect(JSON.parse(writes[0].body!).metadata.authors).toEqual(['Ada B', 'Institute; Unit']);
});

it('adds and removes individual DOI creators with localized controls and keeps literal names in order', async () => {
  let acknowledged = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (acknowledged ? [saved] : []),
    detail: () => saved,
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/metadata-lookup') return json(draft);
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        acknowledged = true;
        return json(receipt(JSON.parse(String(init.body)).operation_id));
      }
    },
  });
  const view = await mountCollections();
  const review = await findDraft();
  fireEvent.click(within(review).getByRole('button', { name: 'Edit details' }));
  fireEvent.click(within(review).getByRole('button', { name: 'Add author' }));
  fireEvent.change(within(review).getByRole('textbox', { name: 'Authors 3' }), {
    target: { value: 'New; Consortium' },
  });
  await act(async () => view.i18n.changeLanguage('zh-CN'));
  expect(within(review).getByRole('button', { name: '添加作者' })).toBeEnabled();
  fireEvent.click(within(review).getByRole('button', { name: '移除作者 1' }));
  expect(within(review).getByRole('textbox', { name: '作者 1' })).toHaveValue('Institute; Unit');
  expect(within(review).getByRole('textbox', { name: '作者 2' })).toHaveValue('New; Consortium');
  fireEvent.click(within(review).getByRole('button', { name: '返回复核' }));
  fireEvent.click(within(review).getByRole('button', { name: '保存文献' }));
  await waitFor(() => expect(screen.getByLabelText('Library location')).toHaveTextContent('item=doi-created'));
  const writes = http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST');
  expect(writes).toHaveLength(1);
  expect(JSON.parse(writes[0].body!).metadata.authors).toEqual(['Institute; Unit', 'New; Consortium']);
});

it('restoring all seeded creator values removes only the unsaved edit state', async () => {
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url) => (url.pathname === '/api/literature/metadata-lookup' ? json(draft) : undefined),
  });
  await mountCollections();
  const review = await findDraft();
  fireEvent.click(within(review).getByRole('button', { name: 'Edit details' }));
  const first = within(review).getByRole('textbox', { name: 'Authors 1' });
  fireEvent.change(first, { target: { value: 'Edited name' } });
  fireEvent.change(first, { target: { value: draft.metadata.authors[0] } });
  fireEvent.click(within(review).getByRole('button', { name: 'Add author' }));
  fireEvent.click(within(review).getByRole('button', { name: 'Remove author 3' }));
  fireEvent.click(within(review).getByRole('button', { name: 'Back to review' }));
  fireEvent.click(within(review).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Review DOI reference' })).toBeNull());
  expect(screen.queryByText('Discard this unsaved reference?')).toBeNull();
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
});

it('closes an untouched lookup seed without a discard gate but retains dirty edit protection', async () => {
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url) => (url.pathname === '/api/literature/metadata-lookup' ? json(draft) : undefined),
  });
  await mountCollections();
  const review = await findDraft();
  fireEvent.click(within(review).getByRole('button', { name: 'Cancel' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Review DOI reference' })).toBeNull());
  expect(screen.queryByText('Discard this unsaved reference?')).toBeNull();
  const edited = await findDraft();
  fireEvent.click(within(edited).getByRole('button', { name: 'Edit details' }));
  fireEvent.change(within(edited).getByRole('textbox', { name: 'Title' }), { target: { value: 'Changed draft' } });
  fireEvent.click(within(edited).getByRole('button', { name: 'Back to review' }));
  fireEvent.click(within(edited).getByRole('button', { name: 'Cancel' }));
  expect(within(edited).getByText('Discard this unsaved reference?')).toBeInTheDocument();
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
});

it('keeps lookup input after a provider failure and retries only the read', async () => {
  let count = 0;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url) =>
      url.pathname === '/api/literature/metadata-lookup'
        ? ++count === 1
          ? json({ code: 'LITERATURE_LOOKUP_UNAVAILABLE' }, 503)
          : json(draft)
        : undefined,
  });
  await mountCollections();
  const lookup = await openLookup();
  fireEvent.click(within(lookup).getByRole('button', { name: 'Find reference' }));
  expect(await within(lookup).findByRole('alert')).toHaveTextContent('could not be retrieved');
  expect(within(lookup).getByRole('textbox', { name: 'DOI or doi.org link' })).toHaveValue(reference);
  fireEvent.click(within(lookup).getByRole('button', { name: 'Find reference' }));
  await screen.findByRole('dialog', { name: 'Review DOI reference' });
  expect(count).toBe(2);
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
});

it.each(['cancel', 'scope'] as const)('does not accept a late metadata draft after %s', async (change) => {
  const held = deferred<Response>();
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url) => (url.pathname === '/api/literature/metadata-lookup' ? held.promise : undefined),
  });
  await mountCollections();
  const lookup = await openLookup();
  fireEvent.click(within(lookup).getByRole('button', { name: 'Find reference' }));
  await waitFor(() => expect(http.requests.some((r) => r.path === '/api/literature/metadata-lookup')).toBe(true));
  if (change === 'cancel') {
    expect(within(lookup).getByRole('button', { name: 'Cancel' })).toBeEnabled();
    fireEvent.click(within(lookup).getByRole('button', { name: 'Cancel' }));
  } else {
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search Library' }), { target: { value: 'new-scope' } });
    await act(async () => setRendererAccountOwner('other-owner'));
  }
  await act(async () => {
    held.resolve(json(draft));
    await held.promise;
  });
  expect(screen.queryByRole('dialog', { name: 'Review DOI reference' })).toBeNull();
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
});

it('recovers a lost create acknowledgement with the original operation and no new lookup after remount', async () => {
  let creates = 0;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (creates > 1 ? [saved] : []),
    detail: () => saved,
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/metadata-lookup') return json(draft);
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        if (++creates === 1) throw new TypeError('Controlled lost response');
        return json(receipt(JSON.parse(String(init.body)).operation_id));
      }
    },
  });
  await mountCollections();
  const review = await findDraft();
  fireEvent.click(within(review).getByRole('button', { name: 'Save reference' }));
  await waitFor(() => expect(within(review).getByRole('button', { name: 'Retry' })).toBeEnabled());
  fireEvent.click(within(review).getByRole('button', { name: 'Cancel' }));
  expect(screen.queryByText('Discard this unsaved reference?')).toBeNull();
  cleanup();
  await mountCollections();
  fireEvent.click(await screen.findByRole('button', { name: 'Retry' }));
  await screen.findByRole('button', { name: saved.title });
  const writes = http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST');
  expect(writes).toHaveLength(2);
  expect(writes[0].body).toBe(writes[1].body);
  expect(http.requests.filter((r) => r.path === '/api/literature/metadata-lookup')).toHaveLength(1);
});

it.each(['reference', 'doi'] as const)(
  'rejects an unbound metadata draft instead of presenting it for saving: %s',
  async (field) => {
    const bad =
      field === 'reference'
        ? { ...draft, reference: '10.1234/other' }
        : { ...draft, metadata: { ...draft.metadata, doi: '10.1234/other' } };
    const http = installCollectionHTTP({
      collections: () => [],
      items: () => [],
      intercept: (url) => (url.pathname === '/api/literature/metadata-lookup' ? json(bad) : undefined),
    });
    await mountCollections();
    const lookup = await openLookup();
    fireEvent.click(within(lookup).getByRole('button', { name: 'Find reference' }));
    await within(lookup).findByRole('alert');
    expect(screen.queryByRole('dialog', { name: 'Review DOI reference' })).toBeNull();
    expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
  }
);

it('uses the current Chinese preference for DOI lookup and untouched review', async () => {
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [],
    intercept: (url) => (url.pathname === '/api/literature/metadata-lookup' ? json(draft) : undefined),
  });
  const rendered = await mountCollections();
  await act(async () => {
    await rendered.i18n.changeLanguage('zh-CN');
  });
  fireEvent.click(await screen.findByRole('button', { name: '添加' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: '从 DOI 添加' }));
  const lookup = await screen.findByRole('dialog', { name: '从 DOI 添加' });
  fireEvent.change(within(lookup).getByRole('textbox', { name: 'DOI 或 doi.org 链接' }), {
    target: { value: reference },
  });
  fireEvent.click(within(lookup).getByRole('button', { name: '查询文献' }));
  const review = await screen.findByRole('dialog', { name: '复核 DOI 文献' });
  expect(within(review).getByRole('button', { name: '编辑信息' })).toBeInTheDocument();
  expect(within(review).getByRole('button', { name: '保存文献' })).toBeInTheDocument();
  fireEvent.click(within(review).getByRole('button', { name: '取消' }));
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '复核 DOI 文献' })).toBeNull());
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
});

it.each([
  '12345',
  '  pMiD : 12345  ',
  'https://pubmed.ncbi.nlm.nih.gov/12345/',
  'PMID: http://www.PubMed.NCBI.nlm.nih.gov/12345?from=search#article',
])('binds both returned PMID fields to the lookup reference %s', (reference) => {
  const response = (pmid: string) => ({
    schema: 'x-science.literature-pmid-draft.v1',
    reference,
    pmid,
    doi: '',
    title: 'Verified PMID record',
    metadata: { pmid, authors: [] },
  });

  expect(decodeLiteratureMetadataDraft(response('12345'), reference, 'pmid')).toEqual(response('12345'));
  expect(() => decodeLiteratureMetadataDraft(response('12346'), reference, 'pmid')).toThrow(
    'Invalid Literature metadata draft'
  );
});

it('preserves an exact PMID-only draft through existing review and manual save', async () => {
  const reference = 'https://pubmed.ncbi.nlm.nih.gov/12345/';
  const metadata = { pmid: '12345', authors: ['Ada Researcher'], url: reference, item_type: 'journalArticle' };
  const draft = {
    schema: 'x-science.literature-pmid-draft.v1',
    reference,
    pmid: '12345',
    doi: '',
    title: 'PMID-only reference',
    metadata,
  };
  expect(() => decodeLiteratureMetadataDraft({ ...draft, pmid: '12346' }, reference, 'pmid')).toThrow();
  expect(() => decodeLiteratureMetadataDraft(draft, reference)).toThrow();
  let acknowledged = false;
  const item = { ...paper('pmid-created', draft.title), metadata };
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => (acknowledged ? [item] : []),
    detail: () => item,
    intercept: (url, init) => {
      if (url.pathname === '/api/literature/metadata-lookup') return json(draft);
      if (url.pathname === '/api/literature/items' && init.method === 'POST') {
        acknowledged = true;
        return json({ ...receipt(JSON.parse(String(init.body)).operation_id), item });
      }
    },
  });
  await mountCollections();
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add' })).toBeEnabled());
  fireEvent.click(screen.getByRole('button', { name: 'Add' }));
  fireEvent.click(await screen.findByRole('menuitem', { name: 'Add from PMID' }));
  const lookup = await screen.findByRole('dialog', { name: 'Add from PMID' });
  fireEvent.change(within(lookup).getByRole('textbox', { name: 'PMID or PubMed link' }), {
    target: { value: '  ' + reference + '  ' },
  });
  fireEvent.click(within(lookup).getByRole('button', { name: 'Find reference' }));
  const review = await screen.findByRole('dialog', { name: 'Review PMID reference' });
  expect(within(review).getByText('12345')).toBeInTheDocument();
  expect(http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST')).toHaveLength(0);
  fireEvent.click(within(review).getByRole('button', { name: 'Save reference' }));
  await waitFor(() => expect(screen.getByLabelText('Library location')).toHaveTextContent('item=pmid-created'));
  const request = http.requests.find((r) => r.path === '/api/literature/metadata-lookup');
  expect(JSON.parse(request!.body!)).toEqual({ reference, scheme: 'pmid' });
  const writes = http.requests.filter((r) => r.path === '/api/literature/items' && r.method === 'POST');
  expect(writes).toHaveLength(1);
  expect(JSON.parse(writes[0].body!).metadata).toEqual(metadata);
});

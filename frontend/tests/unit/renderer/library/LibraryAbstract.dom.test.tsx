import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import {
  getRendererAccountOwnerId,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks } from '../conversation/literatureMutationLockFixtures';
import { deferred, installCollectionHTTP, json, mountCollections, paper } from './libraryCollectionsFixtures';

const preview = vi.hoisted(() => ({ mounts: 0, unmounts: 0 }));
// Only shared PDF child lifetime/page state is instrumented; this is not PDF rendering acceptance.
vi.mock('@/renderer/pages/library/LibraryAttachmentPreview', () => ({
  LibraryAttachmentPreview: () => {
    const [page, setPage] = React.useState(1);
    React.useEffect(() => {
      ++preview.mounts;
      return () => {
        ++preview.unmounts;
      };
    }, []);
    return (
      <div data-testid='abstract-pdf'>
        <output aria-label='Abstract fixture PDF page'>{page}</output>
        <button type='button' onClick={() => setPage(2)}>
          Advance abstract fixture PDF
        </button>
      </div>
    );
  },
}));

const ownerA = 'library-abstract-owner-a';
const ownerB = 'library-abstract-owner-b';
const longText = 'A saved abstract with line breaks.\n<em>Literal source text</em>.\n'.repeat(12);
const release: Array<() => void> = [];
const observers: ControlledResizeObserver[] = [];
class ControlledResizeObserver {
  target?: Element;
  disconnected = false;
  private readonly callback: ResizeObserverCallback;
  observe = vi.fn((target: Element) => {
    this.target = target;
  });
  disconnect = vi.fn(() => {
    this.disconnected = true;
  });
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    observers.push(this);
  }
  emit() {
    // Deliberately allow a queued notification after disconnect to exercise cleanup fencing.
    this.callback([], this as unknown as ResizeObserver);
  }
}
const textObserver = (text: string) => {
  const observer = [...observers]
    .reverse()
    .find((value) => !value.disconnected && value.target?.isConnected && value.target.textContent === text);
  if (!observer) throw new Error('Current abstract observer was not connected');
  return observer;
};
function metrics(observer: ControlledResizeObserver, full = 240) {
  const node = observer.target as HTMLElement;
  const state = { full, client: 192, reads: 0 };
  node.style.lineHeight = '24px';
  node.style.fontSize = '16px';
  Object.defineProperties(node, {
    scrollHeight: {
      configurable: true,
      get: () => {
        ++state.reads;
        return state.full;
      },
    },
    clientHeight: { configurable: true, get: () => state.client },
  });
  act(() => observer.emit());
  return state;
}
const summary = () => screen.getByRole('region', { name: 'Abstract' });
const titleInput = () =>
  within(screen.getByRole('region', { name: 'Edit details' })).getByRole('textbox', { name: 'Title' });
function item(value: unknown, id = 'a', title = 'Paper A'): LiteratureItem {
  const base = paper(id, title);
  return { ...base, metadata: { ...base.metadata, abstract: value } };
}
function savedAbstract(value: unknown, withPdf = false) {
  let current = item(value);
  if (withPdf)
    current.attachments = [
      {
        id: 'attachment-a',
        kind: 'fullText',
        versions: [
          {
            id: 'version-a',
            attachment_id: 'attachment-a',
            version_number: 1,
            filename: 'reference.pdf',
            mime_type: 'application/pdf',
            size_bytes: 3,
            checksum: 'a'.repeat(64),
            page_count: 2,
            available: true,
          },
        ],
      },
    ];
  let failed = false;
  const http = installCollectionHTTP({
    collections: () => [],
    items: () => [current],
    detail: () => current,
    intercept: (url, init) =>
      failed && (init.method ?? 'GET') === 'GET' && url.pathname === '/api/literature/items/a'
        ? json({ code: 'UNAVAILABLE' }, 503)
        : undefined,
  });
  return {
    http,
    fail: (value: boolean) => {
      failed = value;
    },
    set: (value: unknown) => {
      current = {
        ...current,
        metadata_revision: current.metadata_revision + 1,
        metadata: { ...current.metadata, abstract: value },
      };
    },
    reads: () =>
      http.requests.filter((request) => request.method === 'GET' && request.path === '/api/literature/items/a').length,
  };
}

describe('Saved Library abstracts through the existing Page reader', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner(ownerA);
    localStorage.clear();
    installLiteratureLocks();
    observers.length = 0;
    preview.mounts = 0;
    preview.unmounts = 0;
    vi.stubGlobal('ResizeObserver', ControlledResizeObserver);
  });
  afterEach(async () => {
    cleanup();
    await act(async () => {
      for (const done of release.splice(0)) done();
      setRendererAccountOwner(null);
    });
    vi.unstubAllGlobals();
  });

  it('omits missing, empty and nonstring values, and shows short saved text literally in both languages', async () => {
    const data = savedAbstract(undefined);
    const mounted = await mountCollections('/library?item=a');
    await screen.findByRole('heading', { name: 'Paper A' });
    for (const value of [undefined, '', ' \n ', null, 42, ['not prose'], { text: 'not a string' }]) {
      const before = data.reads();
      data.set(value);
      fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
      await waitFor(() => expect(data.reads()).toBe(before + 1));
      await waitFor(() => expect(screen.getByRole('button', { name: 'Edit details' })).toBeEnabled());
      expect(screen.queryByRole('region', { name: 'Abstract' })).toBeNull();
    }
    const text = 'Short saved text: <script>literal only</script>.';
    data.set(text);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    const region = await screen.findByRole('region', { name: 'Abstract' });
    expect(region.querySelector('p')?.textContent).toBe(text);
    expect(region.querySelector('script')).toBeNull();
    expect(within(region).queryByRole('button')).toBeNull();
    await act(async () => {
      await mounted.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('region', { name: '摘要' })).toBe(region);
    expect(data.http.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it('measures eight real lines, keeps text mounted and supports native keyboard disclosure with cleanup', async () => {
    savedAbstract(longText);
    const mounted = await mountCollections('/library?item=a');
    await screen.findByRole('heading', { name: 'Paper A' });
    await screen.findByRole('region', { name: 'Abstract' });
    const observer = textObserver(longText);
    const size = metrics(observer, 193);
    const node = observer.target!;
    expect(within(summary()).queryByRole('button')).toBeNull();
    size.full = 194;
    act(() => observer.emit());
    const toggle = within(summary()).getByRole('button', { name: 'Show more' });
    expect(toggle).toHaveAttribute('aria-controls', node.id);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    const user = userEvent.setup();
    toggle.focus();
    await user.keyboard('{Enter}');
    expect(toggle).toHaveAccessibleName('Show less');
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(observer.target).toBe(node);
    expect(node.textContent).toBe(longText);
    await user.keyboard(' ');
    expect(toggle).toHaveAccessibleName('Show more');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    size.full = 192;
    act(() => observer.emit());
    expect(within(summary()).queryByRole('button')).toBeNull();
    size.full = 300;
    act(() => observer.emit());
    fireEvent.click(within(summary()).getByRole('button', { name: 'Show more' }));
    size.full = 400;
    act(() => observer.emit());
    expect(within(summary()).getByRole('button', { name: 'Show less' })).toHaveAttribute('aria-expanded', 'true');
    mounted.unmount();
    expect(observer.disconnect).toHaveBeenCalledOnce();
    const reads = size.reads;
    act(() => observer.emit());
    expect(size.reads).toBe(reads);
  });

  it('retains the abstract, draft and PDF on a failed refresh, then resets only summary expansion for new saved text', async () => {
    const data = savedAbstract(longText, true);
    await mountCollections('/library?item=a');
    await screen.findByRole('region', { name: 'Abstract' });
    const oldObserver = textObserver(longText);
    metrics(oldObserver);
    const savedSummary = summary();
    const summaryToggle = within(savedSummary).getByRole('button', { name: 'Show more' });
    fireEvent.click(summaryToggle);
    fireEvent.click(screen.getByRole('button', { name: /Preview PDF/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Advance abstract fixture PDF' }));
    const pdf = screen.getByTestId('abstract-pdf');
    fireEvent.click(screen.getByRole('button', { name: 'Edit details' }));
    const draft = titleInput();
    fireEvent.change(draft, { target: { value: 'Unsaved title remains' } });
    expect(savedSummary).not.toBeVisible();
    expect(savedSummary.closest('.library-workbench__overview')).toHaveAttribute('hidden');
    expect(savedSummary.closest('.library-workbench__overview')).toHaveAttribute('inert');
    data.fail(true);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    const error = await screen.findByText(
      'Details could not be refreshed. Your draft is retained; retry to continue editing.'
    );
    expect(summaryToggle).toHaveTextContent('Show less');
    expect(summaryToggle).toHaveAttribute('aria-expanded', 'true');
    expect(titleInput()).toBe(draft);
    expect(draft).toHaveValue('Unsaved title remains');
    expect(screen.getByTestId('abstract-pdf')).toBe(pdf);
    data.fail(false);
    const updated = longText + '\nA newly saved abstract version.';
    data.set(updated);
    fireEvent.click(within(error.closest('[role="alert"]') as HTMLElement).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(savedSummary.querySelector('p')?.textContent).toBe(updated));
    const nextObserver = textObserver(updated);
    metrics(nextObserver);
    expect(oldObserver.disconnect).toHaveBeenCalledOnce();
    expect(summaryToggle).toHaveTextContent('Show more');
    expect(summaryToggle).toHaveAttribute('aria-expanded', 'false');
    expect(titleInput()).toBe(draft);
    expect(draft).toHaveValue('Unsaved title remains');
    expect(draft).toBeDisabled();
    expect(screen.getByTestId('abstract-pdf')).toBe(pdf);
    expect(screen.getByLabelText('Abstract fixture PDF page')).toHaveTextContent('2');
    expect(preview).toEqual({ mounts: 1, unmounts: 0 });
    expect(data.http.requests.every((request) => request.method === 'GET')).toBe(true);
  });

  it('resets expansion on item and account changes and rejects their old detail replies and observer callbacks', async () => {
    const gates = { a: deferred<Response>(), b: deferred<Response>() };
    release.push(
      () => gates.a.resolve(json(item('Late old A', 'a'))),
      () => gates.b.resolve(json(item('Late old B', 'b')))
    );
    const held = new Set<string>();
    const started = new Set<string>();
    const currentItem = (id: string) =>
      item(
        longText,
        id,
        `${getRendererAccountOwnerId() === ownerA ? 'Paper' : 'Other account paper'} ${id.toUpperCase()}`
      );
    const http = installCollectionHTTP({
      collections: () => [],
      items: () => [currentItem('a'), currentItem('b')],
      detail: currentItem,
      intercept: (url, init) => {
        const match = url.pathname.match(/^\/api\/literature\/items\/([ab])$/);
        if ((init.method ?? 'GET') !== 'GET' || !match || getRendererAccountOwnerId() !== ownerA || !held.has(match[1]))
          return undefined;
        started.add(match[1]);
        return gates[match[1] as 'a' | 'b'].promise;
      },
    });
    await mountCollections('/library?item=a');
    await screen.findByRole('region', { name: 'Abstract' });
    const oldA = textObserver(longText);
    metrics(oldA);
    fireEvent.click(within(summary()).getByRole('button', { name: 'Show more' }));
    held.add('a');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(started.has('a')).toBe(true));
    fireEvent.click(await screen.findByRole('button', { name: 'Paper B' }));
    await screen.findByRole('heading', { name: 'Paper B' });
    const oldB = textObserver(longText);
    metrics(oldB);
    expect(oldA.disconnect).toHaveBeenCalledOnce();
    expect(within(summary()).getByRole('button', { name: 'Show more' })).toHaveAttribute('aria-expanded', 'false');
    await act(async () => {
      gates.a.resolve(json(item('Late old A', 'a')));
      oldA.emit();
    });
    expect(summary().querySelector('p')?.textContent).toBe(longText);
    fireEvent.click(within(summary()).getByRole('button', { name: 'Show more' }));
    held.add('b');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(started.has('b')).toBe(true));
    act(() => setRendererAccountOwner(ownerB));
    await screen.findByRole('heading', { name: 'Other account paper B' });
    const next = textObserver(longText);
    metrics(next);
    expect(oldB.disconnect).toHaveBeenCalledOnce();
    await act(async () => {
      gates.b.resolve(json(item('Late old B', 'b')));
      oldB.emit();
    });
    expect(within(summary()).getByRole('button', { name: 'Show more' })).toHaveAttribute('aria-expanded', 'false');
    expect(summary().querySelector('p')?.textContent).toBe(longText);
    expect(screen.queryByText('Late old A')).toBeNull();
    expect(screen.queryByText('Late old B')).toBeNull();
    expect(http.requests.every((request) => request.method === 'GET')).toBe(true);
  });
});

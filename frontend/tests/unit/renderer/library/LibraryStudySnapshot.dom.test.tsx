import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { decodeLiteratureStudy, literatureStudySourceKey } from '@/common/adapter/literatureStudySnapshot';
import { LibraryStudySnapshot } from '@/renderer/pages/library/LibraryStudySnapshot';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';

const paper = (): LiteratureItem => ({
  id: 'study-paper',
  title: 'Synthetic paper',
  metadata_revision: 1,
  metadata: { abstract: 'A synthetic association study.' },
  deleted: false,
  project_ids: [],
  attachments: [],
});
const ready = (item: LiteratureItem, question = 'A synthetic question') => ({
  schema: 'x-science.literature-study.v1',
  source_key: literatureStudySourceKey(item),
  revision: 1,
  available: true,
  snapshot: {
    source_key: literatureStudySourceKey(item),
    model: 'synthetic-model',
    coverage: 'abstract',
    sections: { question, population: null, methods: null, results: null, limitations: null },
  },
});
const initial = (item: LiteratureItem) => ({
  schema: 'x-science.literature-study.v1',
  source_key: literatureStudySourceKey(item),
  revision: 0,
  available: true,
});
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
function pending<T>() {
  let resolve!: (value: T) => void;
  return {
    promise: new Promise<T>((done) => {
      resolve = done;
    }),
    resolve: (value: T) => resolve(value),
  };
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('study-owner');
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetRendererAccountScopeForTest();
});

describe('single-paper study overview', () => {
  it('reads without generation, explicitly publishes five fields, and reads the saved result on reopen', async () => {
    const item = paper();
    let saved = false;
    let posts = 0;
    const request = pending<Response>();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method === 'POST') {
          posts++;
          return request.promise;
        }
        return response(saved ? ready(item) : initial(item));
      })
    );
    const account = getRendererAccountScopeToken();
    const view = await renderWithI18n(<LibraryStudySnapshot account={account} item={item} />, 'en-US');
    const generate = await screen.findByRole('button', { name: 'Generate overview' });
    await waitFor(() => expect(generate).toBeEnabled());
    expect(posts).toBe(0);
    fireEvent.click(generate);
    expect(await screen.findByRole('status')).toHaveTextContent('Reading the available source');
    expect(screen.getByRole('button', { name: 'Generating overview…' })).toBeDisabled();
    saved = true;
    await act(async () => request.resolve(response(ready(item))));
    expect(await screen.findByText('A synthetic question')).toBeVisible();
    expect(screen.getAllByText('Not reported in the available source')).toHaveLength(4);
    expect(posts).toBe(1);
    expect(screen.getByText('Based on abstract')).toBeVisible();
    expect(screen.getByText('AI-generated. Verify against the original paper.')).toBeVisible();
    view.unmount();
    await renderWithI18n(<LibraryStudySnapshot account={account} item={item} />, 'en-US');
    expect(await screen.findByText('A synthetic question')).toBeVisible();
    expect(posts).toBe(1);
  });
  it('retains the same-source ready view after regeneration failure and replays the exact operation', async () => {
    const item = paper();
    const bodies: string[] = [];
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method === 'POST') {
          bodies.push(String(init.body));
          return bodies.length === 1
            ? response({ code: 'LITERATURE_STUDY_FAILED' }, 503)
            : response({ ...ready(item, 'Regenerated question'), revision: 2 });
        }
        return response(ready(item));
      })
    );
    await renderWithI18n(<LibraryStudySnapshot account={getRendererAccountScopeToken()} item={item} />, 'en-US');
    await screen.findByText('A synthetic question');
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('previous result is still shown');
    expect(screen.getByText('A synthetic question')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Regenerated question')).toBeVisible();
    expect(bodies).toHaveLength(2);
    expect(bodies[1]).toBe(bodies[0]);
  });
  it('aborts an obsolete source and isolates late generation plus language/account changes', async () => {
    const item = paper();
    const next = { ...item, metadata_revision: 2 };
    const delayed = pending<Response>();
    let signal: AbortSignal | undefined;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (url, init) => {
        if (init?.method === 'POST') {
          signal = init.signal as AbortSignal;
          return delayed.promise;
        }
        return response(
          ready(
            String(url).includes('zh-CN') ? next : item,
            String(url).includes('zh-CN') ? '新的研究问题' : 'A synthetic question'
          )
        );
      })
    );
    const account = getRendererAccountScopeToken();
    const view = await renderWithI18n(<LibraryStudySnapshot account={account} item={item} />, 'en-US');
    await screen.findByText('A synthetic question');
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
    await waitFor(() => expect(signal).toBeDefined());
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    view.rerender(<LibraryStudySnapshot account={account} item={next} />);
    expect(await screen.findByText('新的研究问题')).toBeVisible();
    expect(signal?.aborted).toBe(true);
    await act(async () => delayed.resolve(response({ ...ready(item, 'Late old result'), revision: 2 })));
    expect(screen.queryByText('Late old result')).not.toBeInTheDocument();
    act(() => setRendererAccountOwner('different-owner'));
    expect(screen.queryByText('新的研究问题')).not.toBeInTheDocument();
  });
});

describe('study publication receiver', () => {
  it.each(['non-string-coverage', 'unselected-full-text'])('rejects the %s evidence DTO', (kind) => {
    const item = paper();
    const value = ready(item);
    expect(() =>
      decodeLiteratureStudy(
        {
          ...value,
          snapshot: { ...value.snapshot, coverage: kind === 'non-string-coverage' ? ['abstract'] : 'full-text' },
        },
        item
      )
    ).toThrow();
  });
  it('clears ready on typed source-unavailable, makes no automatic POST and recovers via explicit read', async () => {
    const item = paper();
    let posts = 0;
    let gets = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method === 'POST') {
          posts++;
          return response({ code: 'LITERATURE_STUDY_SOURCE_UNAVAILABLE' }, 422);
        }
        gets++;
        return response(ready(item));
      })
    );
    await renderWithI18n(<LibraryStudySnapshot account={getRendererAccountScopeToken()} item={item} />, 'en-US');
    await screen.findByText('A synthetic question');
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Add an abstract or readable');
    expect(screen.queryByText('A synthetic question')).not.toBeInTheDocument();
    expect(posts).toBe(1);
    expect(gets).toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('A synthetic question')).toBeVisible();
    expect(posts).toBe(1);
    expect(gets).toBe(2);
  });
  it.each(['stale-revision', 'no-snapshot'])(
    'rejects unproven %s POST and retains ready plus the exact retry operation',
    async (kind) => {
      const item = paper();
      const bodies: string[] = [];
      vi.stubGlobal(
        'fetch',
        vi.fn(async (_url, init) => {
          if (init?.method !== 'POST') return response(ready(item));
          bodies.push(String(init.body));
          if (bodies.length === 1)
            return response(
              kind === 'no-snapshot' ? { ...initial(item), revision: 2 } : ready(item, 'Unpublished response')
            );
          return response({ ...ready(item, 'Verified publication'), revision: 2 });
        })
      );
      await renderWithI18n(<LibraryStudySnapshot account={getRendererAccountScopeToken()} item={item} />, 'en-US');
      await screen.findByText('A synthetic question');
      fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
      await screen.findByRole('alert');
      expect(screen.getByText('A synthetic question')).toBeVisible();
      expect(screen.queryByText('Unpublished response')).not.toBeInTheDocument();
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      expect(await screen.findByText('Verified publication')).toBeVisible();
      expect(bodies).toHaveLength(2);
      expect(bodies[1]).toBe(bodies[0]);
    }
  );
});

describe('study publication receiver capacity', () => {
  it('allows historical immutable PDF read despite an unavailable content hint', () => {
    const item = paper();
    item.attachments = [
      {
        id: 'attachment',
        kind: 'fullText',
        versions: [
          {
            id: 'pdf-version',
            attachment_id: 'attachment',
            version_number: 1,
            filename: 'fixture.pdf',
            mime_type: 'application/pdf',
            size_bytes: 10,
            checksum: 'a'.repeat(64),
            page_count: 2,
            available: false,
          },
        ],
      },
    ];
    const value = ready(item);
    const read = decodeLiteratureStudy(
      {
        ...value,
        available: false,
        snapshot: { ...value.snapshot, coverage: 'passages', attachment_version_id: 'pdf-version' },
      },
      item
    );
    expect(read.snapshot?.sections.question).toBe('A synthetic question');
  });
  it('shows an explicit retryable busy reason and retains ready without automatic requests', async () => {
    const item = paper();
    let posts = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) => {
        if (init?.method !== 'POST') return response(ready(item));
        posts++;
        return posts === 1
          ? response({ code: 'LITERATURE_STUDY_BUSY' }, 429)
          : response({ ...ready(item, 'After explicit retry'), revision: 2 });
      })
    );
    await renderWithI18n(<LibraryStudySnapshot account={getRendererAccountScopeToken()} item={item} />, 'en-US');
    await screen.findByText('A synthetic question');
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Too many overviews');
    expect(screen.getByText('A synthetic question')).toBeVisible();
    expect(posts).toBe(1);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('After explicit retry')).toBeVisible();
    expect(posts).toBe(2);
  });
  it('does not treat unrelated 422 as source or access invalidation', async () => {
    const item = paper();
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_url, init) =>
        init?.method === 'POST' ? response({ code: 'ANOTHER_UNPROCESSABLE_RESULT' }, 422) : response(ready(item))
      )
    );
    await renderWithI18n(<LibraryStudySnapshot account={getRendererAccountScopeToken()} item={item} />, 'en-US');
    await screen.findByText('A synthetic question');
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));
    await screen.findByRole('alert');
    expect(screen.getByText('A synthetic question')).toBeVisible();
    expect(screen.getByRole('button', { name: 'Retry' })).toBeEnabled();
  });
});

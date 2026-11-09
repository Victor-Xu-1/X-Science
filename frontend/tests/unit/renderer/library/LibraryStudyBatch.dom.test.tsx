/* oxlint-disable oxc/no-map-spread -- Immutable snapshots model independent HTTP responses. */
import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type {
  LiteratureStudyBatchCommand,
  LiteratureStudyBatchView,
} from '@/renderer/pages/library/literatureStudyBatchContract';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import { literatureStudySourceKey } from '@/common/adapter/literatureStudySnapshot';
import {
  getRendererAccountScopeToken,
  resetRendererAccountScopeForTest,
  setRendererAccountOwner,
} from '@/renderer/services/rendererAccountScope';
import { LibraryStudyBatchPanel } from '@/renderer/pages/library/LibraryStudyBatchPanel';
import { LibrarySelectionToolbar } from '@/renderer/pages/library/LibrarySelectionToolbar';
import { useLibraryStudyBatch } from '@/renderer/pages/library/useLibraryStudyBatch';
import { requestStudyBatch, readStudyBatchResult } from '@/renderer/pages/library/literatureStudyBatchApi';
import { loadLiteratureItem } from '@/renderer/services/runtime/literatureLibrary';
import { renderWithI18n } from '../../i18nTestUtils';

vi.mock('@/renderer/pages/library/literatureStudyBatchApi', () => ({
  requestStudyBatch: vi.fn(),
  readStudyBatchResult: vi.fn(),
}));
vi.mock('@/renderer/services/runtime/literatureLibrary', () => ({ loadLiteratureItem: vi.fn() }));
const uuid = (i: number) => `00000000-0000-4000-8000-${String(i).padStart(12, '0')}`;
const paper = (id: string): LiteratureItem => ({
  id,
  title: `Synthetic ${id}`,
  metadata_revision: 1,
  metadata: { abstract: 'Synthetic evidence' },
  deleted: false,
  project_ids: [],
  attachments: [],
});
const checkpoint = (count = 2): LiteratureStudyBatchView => ({
  schema: 'x-science.literature-study-batch.v1',
  limit: 5,
  concurrency: 2,
  batch: {
    id: uuid(1),
    revision: 1,
    paused: false,
    rows: Array.from({ length: count }, (_, i) => ({
      item_id: `paper-${i}`,
      state: 'pending',
      request: {
        operation_id: uuid(i + 2),
        language: 'en-US',
        expected_source_key: literatureStudySourceKey(paper(`paper-${i}`)),
        expected_revision: 0,
      },
    })),
  },
});
const copy = <T,>(value: T): T => JSON.parse(JSON.stringify(value));
const completed = (value: LiteratureStudyBatchView, resultRevision = 1): LiteratureStudyBatchView => ({
  ...value,
  batch: {
    ...value.batch!,
    revision: value.batch!.revision + 3,
    rows: value.batch!.rows.map((row) => ({ ...row, state: 'succeeded', result_revision: resultRevision })),
  },
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  return {
    promise: new Promise<T>((done) => {
      resolve = done;
    }),
    resolve: (value: T) => resolve(value),
  };
}
function Harness({
  count = 2,
  account = getRendererAccountScopeToken(),
  details = vi.fn(),
}: {
  count?: number;
  account?: string;
  details?: (id: string) => void;
}) {
  const ids = Array.from({ length: count }, (_, i) => `paper-${i}`);
  const model = useLibraryStudyBatch(account, 'en-US', JSON.stringify(ids));
  return (
    <>
      <output data-testid='checkpoint-status' hidden>
        {model.view === undefined ? 'unread' : model.view.batch === null ? 'empty' : 'present'}
      </output>
      <LibrarySelectionToolbar
        selection={{ allMatchingSelected: false, selectedIds: ids, excludedMatchingIds: [] }}
        canSelectAllMatching={false}
        disabled={false}
        onClear={() => {}}
        onSelectAllMatching={() => {}}
        onChangeStatus={() => {}}
        overviewControl={
          <button type='button' disabled={model.busy} onClick={() => void model.start(async () => ids)}>
            Generate selected overviews
          </button>
        }
      />
      <LibraryStudyBatchPanel account={account} model={model} onOpenDetails={details} />
    </>
  );
}
beforeEach(() => {
  resetRendererAccountScopeForTest();
  setRendererAccountOwner('batch-owner');
  vi.clearAllMocks();
  vi.mocked(loadLiteratureItem).mockImplementation(async (id) => paper(id));
  vi.mocked(readStudyBatchResult).mockImplementation(async (_id, _index, row) => ({
    item: paper(row.item_id),
    view: {
      schema: 'x-science.literature-study.v1',
      source_key: row.request.expected_source_key,
      revision: row.result_revision!,
      available: true,
      snapshot: {
        source_key: row.request.expected_source_key,
        model: 'synthetic-model',
        coverage: 'abstract',
        sections: {
          question: 'Frozen batch result',
          population: null,
          methods: null,
          results: null,
          limitations: null,
        },
      },
    },
  }));
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  resetRendererAccountScopeForTest();
});

describe('resumable selected-paper overviews', () => {
  it('accepts an initial empty checkpoint without showing a failed batch panel', async () => {
    const initial = deferred<LiteratureStudyBatchView>();
    vi.mocked(requestStudyBatch).mockReturnValue(initial.promise);
    await renderWithI18n(<Harness />, 'en-US');
    await act(async () => initial.resolve({ ...checkpoint(), batch: null }));
    expect(screen.getByTestId('checkpoint-status')).toHaveTextContent('empty');
    expect(screen.queryByRole('region', { name: 'Batch overviews' })).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(requestStudyBatch).toHaveBeenCalledTimes(1);
    expect(vi.mocked(requestStudyBatch).mock.calls[0][1]).toBeUndefined();
    expect(loadLiteratureItem).not.toHaveBeenCalled();
  });

  it('refreshes a saved checkpoint to empty and can start a new batch afterwards', async () => {
    const completedBatch = completed(checkpoint());
    const saved = { ...completedBatch, batch: { ...completedBatch.batch!, revision: 20 } };
    let reads = 0;
    let next = checkpoint();
    const commands: LiteratureStudyBatchCommand[] = [];
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (!command) return ++reads === 1 ? saved : { ...checkpoint(), batch: null };
      commands.push(command);
      if (command.action === 'start') {
        next = { ...checkpoint(), batch: { ...checkpoint().batch!, id: command.operation_id } };
        return copy(next);
      }
      if (command.action === 'advance') return completed(next);
      throw new Error('Unexpected checkpoint command');
    });
    await renderWithI18n(<Harness />, 'en-US');
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Batch overviews/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh checkpoint' }));
    await waitFor(() => expect(screen.getByTestId('checkpoint-status')).toHaveTextContent('empty'));
    expect(screen.queryByRole('region', { name: 'Batch overviews' })).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    expect(commands.map((command) => command.action)).toEqual(['start', 'advance']);
    expect(next.batch!.id).not.toBe(saved.batch!.id);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps a newer checkpoint revision when an older advance response arrives', async () => {
    let value: LiteratureStudyBatchView = { ...checkpoint(), batch: null };
    const advance = deferred<LiteratureStudyBatchView>();
    let tick: (() => void) | undefined;
    const originalInterval = globalThis.setInterval;
    vi.spyOn(globalThis, 'setInterval').mockImplementation((handler, delay, ...args) => {
      if (delay !== 1000) return originalInterval(handler, delay, ...args);
      tick = handler as () => void;
      return 124 as unknown as ReturnType<typeof setInterval>;
    });
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action === 'start')
        value = { ...checkpoint(), batch: { ...checkpoint().batch!, id: command.operation_id } };
      if (command?.action === 'advance') return advance.promise;
      return copy(value);
    });
    await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    await waitFor(() => expect(tick).toBeTypeOf('function'));
    const older = { ...copy(value), batch: { ...value.batch!, revision: 2, paused: true } };
    const completedBatch = completed(value);
    value = { ...completedBatch, batch: { ...completedBatch.batch!, revision: 10 } };
    await act(async () => tick!());
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    await act(async () => advance.resolve(older));
    expect(screen.getByText('2 / 2 saved')).toBeVisible();
    expect(screen.queryByText('0 / 2 saved')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Generate selected overviews' })).toBeEnabled();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('keeps the next account wave progress owner when the old advance arrives late', async () => {
    const oldAdvance = deferred<LiteratureStudyBatchView>();
    const nextAdvance = deferred<LiteratureStudyBatchView>();
    let first = checkpoint();
    let second = checkpoint();
    const accountA = getRendererAccountScopeToken();
    const timers = new Map<number, () => void>();
    let timerId = 1000;
    const originalInterval = globalThis.setInterval;
    const originalClear = globalThis.clearInterval;
    vi.spyOn(globalThis, 'setInterval').mockImplementation((handler, delay, ...args) => {
      if (delay !== 1000) return originalInterval(handler, delay, ...args);
      timers.set(++timerId, handler as () => void);
      return timerId as unknown as ReturnType<typeof setInterval>;
    });
    vi.spyOn(globalThis, 'clearInterval').mockImplementation((id) => {
      if (typeof id === 'number' && timers.delete(id)) return;
      originalClear(id);
    });
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      const isA = getRendererAccountScopeToken() === accountA;
      if (command?.action === 'start') {
        const value = { ...checkpoint(), batch: { ...checkpoint().batch!, id: command.operation_id } };
        if (isA) first = value;
        else second = value;
        return copy(value);
      }
      if (command?.action === 'advance') return isA ? oldAdvance.promise : nextAdvance.promise;
      return copy(isA ? { ...checkpoint(), batch: null } : second);
    });
    const mounted = await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    await waitFor(() => expect(timers.size).toBe(1));
    act(() => setRendererAccountOwner('account-b'));
    mounted.rerender(<Harness account={getRendererAccountScopeToken()} />);
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    await waitFor(() =>
      expect(
        vi.mocked(requestStudyBatch).mock.calls.filter(([, command]) => command?.action === 'advance')
      ).toHaveLength(2)
    );
    await waitFor(() => expect(timers.size).toBe(1));
    await act(async () => oldAdvance.resolve(completed(first)));
    second = {
      ...second,
      batch: {
        ...second.batch!,
        revision: 3,
        rows: second.batch!.rows.map((row, index) =>
          index === 0 ? { ...row, state: 'succeeded', result_revision: 1 } : { ...row, state: 'running' }
        ),
      },
    };
    await act(async () => {
      for (const tick of timers.values()) tick();
    });
    expect(await screen.findByText('1 / 2 saved')).toBeVisible();
    await act(async () => nextAdvance.resolve(completed(second)));
  });

  it('retires a confirmed start identity so the same selection gets new saved results', async () => {
    const saved = new Map<string, LiteratureStudyBatchView>();
    const starts: string[] = [];
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action !== 'start') return { ...checkpoint(), batch: null };
      starts.push(command.operation_id);
      if (!saved.has(command.operation_id))
        saved.set(
          command.operation_id,
          completed({ ...checkpoint(), batch: { ...checkpoint().batch!, id: command.operation_id } }, saved.size + 1)
        );
      return copy(saved.get(command.operation_id)!);
    });
    vi.mocked(readStudyBatchResult).mockImplementation(async (_id, _index, row) => ({
      item: paper(row.item_id),
      view: {
        schema: 'x-science.literature-study.v1',
        source_key: row.request.expected_source_key,
        revision: row.result_revision!,
        available: true,
        snapshot: {
          source_key: row.request.expected_source_key,
          model: 'synthetic-model',
          coverage: 'abstract',
          sections: {
            question: `Saved revision ${row.result_revision}`,
            population: null,
            methods: null,
            results: null,
            limitations: null,
          },
        },
      },
    }));
    await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Synthetic paper-0' }));
    expect(await screen.findByText('Saved revision 1')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    await waitFor(() => expect(starts).toHaveLength(2));
    expect(starts[1]).not.toBe(starts[0]);
    fireEvent.click(await screen.findByRole('button', { name: 'Synthetic paper-0' }));
    expect(await screen.findByText('Saved revision 2')).toBeVisible();
  });

  it('preserves an unknown start identity for an exact request retry', async () => {
    const starts: string[] = [];
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action !== 'start') return { ...checkpoint(), batch: null };
      starts.push(command.operation_id);
      if (starts.length === 1) throw new Error('unknown start response');
      return completed({ ...checkpoint(), batch: { ...checkpoint().batch!, id: command.operation_id } });
    });
    await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('could not be confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    expect(starts).toHaveLength(2);
    expect(starts[1]).toBe(starts[0]);
  });

  it('hides the prior result immediately when the same paper receives a new batch reference', async () => {
    const account = getRendererAccountScopeToken();
    const oldView = completed(checkpoint(1));
    const nextBase = completed(checkpoint(1), 2);
    const nextView: LiteratureStudyBatchView = {
      ...nextBase,
      batch: {
        ...nextBase.batch!,
        id: uuid(99),
        rows: nextBase.batch!.rows.map((row) => ({ ...row, request: { ...row.request, operation_id: uuid(100) } })),
      },
    };
    const nextRead = deferred<Awaited<ReturnType<typeof readStudyBatchResult>>>();
    const saved = (question: string, revision: number): Awaited<ReturnType<typeof readStudyBatchResult>> => ({
      item: paper('paper-0'),
      view: {
        schema: 'x-science.literature-study.v1' as const,
        source_key: oldView.batch!.rows[0].request.expected_source_key,
        revision,
        available: true,
        snapshot: {
          source_key: oldView.batch!.rows[0].request.expected_source_key,
          model: 'synthetic-model',
          coverage: 'abstract' as const,
          sections: { question, population: null, methods: null, results: null, limitations: null },
        },
      },
    });
    vi.mocked(readStudyBatchResult).mockImplementation(async (id) =>
      id === oldView.batch!.id ? saved('Previous frozen result', 1) : nextRead.promise
    );
    const model = (view: LiteratureStudyBatchView): ReturnType<typeof useLibraryStudyBatch> => ({
      view,
      busy: false,
      pausing: false,
      issue: undefined,
      open: true,
      setOpen: vi.fn(),
      start: async () => {},
      pause: async () => {},
      resume: async () => {},
      retry: async () => {},
      refresh: async () => {},
    });
    const observedCommits: string[] = [];
    function Observe({ current }: { current: ReturnType<typeof useLibraryStudyBatch> }) {
      const host = React.useRef<HTMLDivElement>(null);
      React.useLayoutEffect(() => {
        if (current.view?.batch?.id === nextView.batch!.id) observedCommits.push(host.current?.textContent ?? '');
      }, [current]);
      return (
        <div ref={host}>
          <LibraryStudyBatchPanel account={account} model={current} onOpenDetails={vi.fn()} />
        </div>
      );
    }
    const mounted = await renderWithI18n(<Observe current={model(oldView)} />, 'en-US');
    fireEvent.click(await screen.findByRole('button', { name: 'Synthetic paper-0' }));
    expect(await screen.findByText('Previous frozen result')).toBeVisible();
    mounted.rerender(<Observe current={model(nextView)} />);
    expect(observedCommits).toHaveLength(1);
    expect(observedCommits[0]).not.toContain('Previous frozen result');
    expect(observedCommits[0]).not.toContain('View details');
    fireEvent.click(await screen.findByRole('button', { name: 'Synthetic paper-0' }));
    expect(screen.queryByText('Previous frozen result')).toBeNull();
    expect(screen.queryByRole('button', { name: 'View details' })).toBeNull();
    await act(async () => nextRead.resolve(saved('New frozen result', 2)));
    expect(await screen.findByText('New frozen result')).toBeVisible();
  });

  it('connects the selection action to bounded work, exact saved expansion and original details', async () => {
    let value: LiteratureStudyBatchView = { ...checkpoint(), batch: null };
    const commands: LiteratureStudyBatchCommand[] = [];
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command) {
        commands.push(command);
        if (command.action === 'start') value = checkpoint();
        if (command.action === 'advance')
          value = {
            ...value,
            batch: {
              ...value.batch!,
              revision: 4,
              rows: value.batch!.rows.map((row) => ({ ...row, state: 'succeeded', result_revision: 1 })),
            },
          };
      }
      return copy(value);
    });
    const details = vi.fn();
    await renderWithI18n(<Harness details={details} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    expect(commands.map((v) => v.action)).toEqual(['start', 'advance']);
    expect(commands[1]).toMatchObject({ rows: [0, 1] });
    fireEvent.click(await screen.findByRole('button', { name: 'Synthetic paper-0' }));
    expect(await screen.findByText('Frozen batch result')).toBeVisible();
    expect(readStudyBatchResult).toHaveBeenCalledWith(
      uuid(1),
      0,
      expect.objectContaining({ result_revision: 1 }),
      expect.any(AbortSignal)
    );
    fireEvent.click(screen.getByRole('button', { name: 'View details' }));
    expect(details).toHaveBeenCalledWith('paper-0');
    expect(screen.queryByText('Frozen batch result')).toBeNull();
  });

  it('pauses after a held wave, then resumes only its unsent suffix', async () => {
    let value: LiteratureStudyBatchView = { ...checkpoint(3), batch: null };
    const held = deferred<LiteratureStudyBatchView>();
    const waves: readonly number[][] = [];
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action === 'start') value = checkpoint(3);
      if (command?.action === 'advance') {
        (waves as number[][]).push([...command.rows]);
        if (waves.length === 1) return held.promise;
        value = {
          ...value,
          batch: {
            ...value.batch!,
            revision: value.batch!.revision + 2,
            rows: value.batch!.rows.map((row) => ({ ...row, state: 'succeeded', result_revision: 1 })),
          },
        };
      }
      if (command?.action === 'pause' || command?.action === 'resume')
        value = {
          ...value,
          batch: { ...value.batch!, revision: value.batch!.revision + 1, paused: command.action === 'pause' },
        };
      return copy(value);
    });
    await renderWithI18n(<Harness count={3} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    const pause = await screen.findByRole('button', { name: 'Pause after this wave' });
    await waitFor(() => expect(waves).toHaveLength(1));
    fireEvent.click(pause);
    expect(await screen.findByText('Finishing the current wave before pausing…')).toBeVisible();
    value = {
      ...value,
      batch: {
        ...value.batch!,
        revision: 4,
        rows: value.batch!.rows.map((row, index) =>
          index < 2 ? { ...row, state: 'succeeded', result_revision: 1 } : row
        ),
      },
    };
    await act(async () => held.resolve(copy(value)));
    expect(await screen.findByText('Paused. Continue from the saved checkpoint.')).toBeVisible();
    expect(waves).toEqual([[0, 1]]);
    fireEvent.click(screen.getByRole('button', { name: 'Continue batch' }));
    expect(await screen.findByText('3 / 3 saved')).toBeVisible();
    expect(waves).toEqual([[0, 1], [2]]);
  });

  it('reads actual per-row progress during a held request and stops its reader when collapsed', async () => {
    let value: LiteratureStudyBatchView = { ...checkpoint(), batch: null };
    const held = deferred<LiteratureStudyBatchView>();
    let tick: (() => void) | undefined;
    const originalInterval = globalThis.setInterval;
    vi.spyOn(globalThis, 'setInterval').mockImplementation((handler, delay, ...args) => {
      if (delay === 1000) {
        tick = handler as () => void;
        return 123 as unknown as ReturnType<typeof setInterval>;
      }
      return originalInterval(handler, delay, ...args);
    });
    const clear = vi.spyOn(globalThis, 'clearInterval');
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action === 'start') value = checkpoint();
      if (command?.action === 'advance') return held.promise;
      return copy(value);
    });
    await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    await waitFor(() => expect(tick).toBeTypeOf('function'));
    value = {
      ...value,
      batch: {
        ...value.batch!,
        revision: 3,
        rows: value.batch!.rows.map((row, i) =>
          i === 0 ? { ...row, state: 'succeeded', result_revision: 1 } : { ...row, state: 'running' }
        ),
      },
    };
    await act(async () => tick!());
    expect(await screen.findByText('1 / 2 saved')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: 'Close batch overviews' }));
    expect(clear).toHaveBeenCalledWith(123);
    value = {
      ...value,
      batch: {
        ...value.batch!,
        revision: 5,
        rows: value.batch!.rows.map((row) => ({ ...row, state: 'succeeded', result_revision: 1 })),
      },
    };
    await act(async () => held.resolve(copy(value)));
  });

  it('recovers a lost response from the saved checkpoint without resending a committed row', async () => {
    let value: LiteratureStudyBatchView = { ...checkpoint(), batch: null };
    const waves: number[][] = [];
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action === 'start') value = checkpoint();
      if (command?.action === 'advance') {
        waves.push([...command.rows]);
        value = {
          ...value,
          batch: {
            ...value.batch!,
            revision: value.batch!.revision + 2,
            paused: waves.length === 1,
            rows: value.batch!.rows.map((row, i) =>
              waves.length > 1 || i === 0 ? { ...row, state: 'succeeded', result_revision: 1 } : row
            ),
          },
        };
        if (waves.length === 1) throw new Error('response lost after commit');
      }
      if (command?.action === 'resume')
        value = { ...value, batch: { ...value.batch!, revision: value.batch!.revision + 1, paused: false } };
      return copy(value);
    });
    const mounted = await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('could not be confirmed');
    mounted.unmount();
    await renderWithI18n(<Harness />, 'en-US');
    expect(await screen.findByText('1 / 2 saved')).toBeVisible();
    fireEvent.click(screen.getByRole('button', { name: /Batch overviews.*1 \/ 2 saved/ }));
    fireEvent.click(screen.getByRole('button', { name: 'Continue batch' }));
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    expect(waves).toEqual([[0, 1], [1]]);
  });

  it('rejects an over-limit selection without submitting a batch', async () => {
    vi.mocked(requestStudyBatch).mockResolvedValue({ ...checkpoint(), batch: null });
    await renderWithI18n(<Harness count={6} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('between 1 and 5');
    expect(screen.queryByText('All available overviews have been saved.')).toBeNull();
    expect(vi.mocked(requestStudyBatch).mock.calls.some(([, command]) => !!command)).toBe(false);
  });

  it('does not let an initial delayed checkpoint read erase a newer completed batch', async () => {
    const initial = deferred<LiteratureStudyBatchView>();
    let reads = 0;
    let value = checkpoint();
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (!command && reads++ === 0) return initial.promise;
      if (command?.action === 'advance')
        value = {
          ...value,
          batch: {
            ...value.batch!,
            revision: 4,
            rows: value.batch!.rows.map((row) => ({ ...row, state: 'succeeded', result_revision: 1 })),
          },
        };
      return copy(value);
    });
    await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    expect(await screen.findByText('2 / 2 saved')).toBeVisible();
    await act(async () => initial.resolve({ ...checkpoint(), batch: null }));
    expect(screen.getByText('2 / 2 saved')).toBeVisible();
  });

  it('cancels the old account owner and ignores an advance response that arrives late', async () => {
    const held = deferred<LiteratureStudyBatchView>();
    let value = checkpoint();
    vi.mocked(requestStudyBatch).mockImplementation(async (_signal, command) => {
      if (command?.action === 'advance') return held.promise;
      if (!command) return { ...checkpoint(), batch: null };
      return copy(value);
    });
    const mounted = await renderWithI18n(<Harness />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Generate selected overviews' }));
    await waitFor(() =>
      expect(vi.mocked(requestStudyBatch).mock.calls.some(([, c]) => c?.action === 'advance')).toBe(true)
    );
    const request = vi.mocked(requestStudyBatch).mock.calls.find(([, c]) => c?.action === 'advance')!;
    act(() => setRendererAccountOwner('next-owner'));
    mounted.rerender(<Harness account={getRendererAccountScopeToken()} />);
    expect(request[0].aborted).toBe(true);
    value = {
      ...value,
      batch: {
        ...value.batch!,
        revision: 4,
        rows: value.batch!.rows.map((row) => ({ ...row, state: 'succeeded', result_revision: 1 })),
      },
    };
    await act(async () => held.resolve(value));
    expect(screen.queryByText('2 / 2 saved')).toBeNull();
  });
});

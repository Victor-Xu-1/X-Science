import type { IToolStdoutChunkEvent } from '@/common/adapter/ipcBridge';
import type { ConversationStreamingFrame } from '@/renderer/services/runtime/conversationStreamingModel';
import {
  MAX_TOOL_STDOUT_BYTES,
  MAX_PENDING_TOOL_STDOUT_BYTES,
  MAX_PENDING_TOOL_STDOUT_CHUNKS,
  MAX_TOOL_STDOUT_ENTRIES,
  ConversationToolStdoutStore,
} from '@/renderer/services/runtime/conversationToolStdoutStore';
import { describe, expect, it, vi } from 'vitest';

function harness() {
  const chunkListeners = new Set<(event: IToolStdoutChunkEvent) => void>();
  const reconnectListeners = new Set<() => void>();
  const hydrate = vi.fn(async () => []);
  const store = new ConversationToolStdoutStore({
    subscribeChunks: (listener) => {
      chunkListeners.add(listener);
      return () => chunkListeners.delete(listener);
    },
    subscribeReconnect: (listener) => {
      reconnectListeners.add(listener);
      return () => reconnectListeners.delete(listener);
    },
    hydrate,
  });
  return {
    store,
    hydrate,
    emit: (event: IToolStdoutChunkEvent) => chunkListeners.forEach((listener) => listener(event)),
    reconnect: () => reconnectListeners.forEach((listener) => listener()),
    chunkListeners,
  };
}

const event = (
  root: string,
  tool: string,
  chunk: string,
  sequence: number,
  bytes?: { start: number; end: number }
): IToolStdoutChunkEvent => ({
  frame_id: root,
  root_frame_id: root,
  tool_use_id: tool,
  tool_name: 'python',
  chunk,
  chunk_sequence: sequence,
  ...(bytes ? { chunk_start_byte: bytes.start, chunk_end_byte: bytes.end } : {}),
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((next) => {
    resolve = next;
  });
  return { promise, resolve };
}

describe('ConversationToolStdoutStore', () => {
  it('routes, deduplicates and unsubscribes by root conversation', () => {
    const { store, emit, chunkListeners } = harness();
    const aChanged = vi.fn();
    const bChanged = vi.fn();
    const disposeA = store.subscribe('conversation-a', aChanged);
    const disposeB = store.subscribe('conversation-b', bChanged);

    emit(event('conversation-a', 'tool-a', 'first\n', 1));
    emit(event('conversation-b', 'tool-b', 'other\n', 1));
    emit(event('conversation-a', 'tool-a', 'first\n', 1));
    emit({
      ...event('conversation-a', 'foreign-tool', 'must not cross roots\n', 1),
      root_frame_id: 'conversation-b',
    });

    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('first\n');
    expect(store.getSnapshot('conversation-a')[0].toolStdout).toHaveLength(1);
    expect(store.getSnapshot('conversation-b')[0].toolStdout[0].stdout).toBe('other\n');
    expect(aChanged).toHaveBeenCalledTimes(1);
    expect(bChanged).toHaveBeenCalledTimes(1);
    disposeA();
    expect(store.getSnapshot('conversation-a')).toEqual([]);
    expect(store.getSnapshot('conversation-b')[0].toolStdout[0].stdout).toBe('other\n');
    disposeB();
    expect(store.getSnapshot('conversation-b')).toEqual([]);
    expect(chunkListeners.size).toBe(0);
  });

  it('keeps validated execution metadata current and rejects malformed metadata', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    const base = {
      ...event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }),
      exec_id: 'exec-a',
      started_at: '2026-09-11T08:00:00Z',
      background: true,
      status: 'running' as const,
    };

    emit({ ...base, started_at: 'not-a-timestamp' });
    expect(store.getSnapshot('conversation-a')).toEqual([]);

    emit(base);
    emit({
      ...event('conversation-a', 'tool-a', 'B', 2, { start: 1, end: 2 }),
      exec_id: 'exec-a',
      started_at: '2026-09-11T08:00:00Z',
      background: true,
      status: 'persisting',
    });

    expect(store.getSnapshot('conversation-a')[0].toolStdout[0]).toMatchObject({
      execId: 'exec-a',
      startedAt: '2026-09-11T08:00:00Z',
      background: true,
      status: 'persisting',
      stdout: 'AB',
    });
    emit({ ...base, chunk: 'must not append', chunk_sequence: 2.5 });
    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('AB');
    dispose();
  });

  it('bounds retained pending UTF-8 bytes while a sequence gap is unresolved', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    emit(event('conversation-a', 'tool-a', '生'.repeat(700_000), 2));

    // Published stdout alone cannot reveal memory retained behind a missing sequence.
    const entries = (
      store as unknown as {
        entries: Map<string, { pendingChunks: Map<number, { text: string }> }>;
      }
    ).entries;
    const retainedBytes = [...entries.values()].reduce(
      (total, entry) =>
        total +
        [...entry.pendingChunks.values()].reduce(
          (bytes, chunk) => bytes + new TextEncoder().encode(chunk.text).byteLength,
          0
        ),
      0
    );
    expect(retainedBytes).toBeLessThanOrEqual(MAX_TOOL_STDOUT_BYTES);
    expect(store.getCurrentSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('');
    dispose();
  });

  it('bounds accumulated pending chunks and does not skip discarded sequences on late gap fill', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    for (let sequence = 3; sequence <= 5; sequence += 1) {
      emit(event('conversation-a', 'tool-a', '生'.repeat(40_000), sequence));
    }
    emit(event('conversation-a', 'tool-a', 'A', 1));
    emit(event('conversation-a', 'tool-a', 'B', 2));
    emit(event('conversation-a', 'tool-a', 'F', 6));
    const output = store.getCurrentSnapshot('conversation-a')[0].toolStdout[0];
    expect(output.stdout).toBe('AB');
    expect(output.throughChunkSequence).toBe(2);
    const entry = [
      ...(
        store as unknown as {
          entries: Map<string, { pendingChunks: Map<number, { text: string }> }>;
        }
      ).entries.values(),
    ][0];
    expect(
      [...entry.pendingChunks.values()].reduce(
        (bytes, chunk) => bytes + new TextEncoder().encode(chunk.text).byteLength,
        0
      )
    ).toBeLessThanOrEqual(MAX_PENDING_TOOL_STDOUT_BYTES);
    dispose();
  });

  it('drains an oversized contiguous chunk without retaining it in the pending budget', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    emit(event('conversation-a', 'tool-a', '生'.repeat(100_000), 2, { start: 1, end: 300_001 }));
    emit(event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }));
    expect(store.getCurrentSnapshot('conversation-a')[0].toolStdout[0].throughChunkSequence).toBe(1);
    // Replaying the discarded sequence supplies its bytes rather than skipping it.
    emit(event('conversation-a', 'tool-a', '生'.repeat(100_000), 2, { start: 1, end: 300_001 }));
    const output = store.getCurrentSnapshot('conversation-a')[0].toolStdout[0];
    expect(output.throughChunkSequence).toBe(2);
    expect(output.stdoutEndByte).toBe(300_001);
    expect(new TextEncoder().encode(output.stdout).byteLength).toBeLessThanOrEqual(MAX_TOOL_STDOUT_BYTES);
    expect(output.stdout).not.toContain('\uFFFD');
    expect(output.recoveryError).toBeUndefined();
    dispose();
  });

  it('keeps discarded gaps recoverable through older and empty snapshots and stops retrying', async () => {
    const { store, emit, hydrate, reconnect } = harness();
    const disposeA = store.subscribe('conversation-a', vi.fn());
    const disposeB = store.subscribe('conversation-b', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(2));
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            execId: 'exec-a',
            stdout: 'A',
            stderr: '',
            throughChunkSequence: 1,
            stdoutStartByte: 0,
            stdoutEndByte: 1,
          },
        ],
      },
    ]);
    emit({ ...event('conversation-a', 'tool-a', '生'.repeat(100_000), 2), exec_id: 'exec-a', status: 'running' });
    await vi.waitFor(() => expect(store.getCurrentSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('A'));
    emit(event('conversation-b', 'tool-a', 'independent', 1));
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBe(true));
    const failed = store.getSnapshot('conversation-a')[0].toolStdout[0];
    expect(failed.execId).toBe('exec-a');
    expect(failed.throughChunkSequence).toBe(1);
    expect(failed.stdout).toBe('A');
    expect(store.getSnapshot('conversation-b')[0].toolStdout[0].stdout).toBe('independent');
    const callsAfterExhaustion = hydrate.mock.calls.length;
    emit({ ...event('conversation-a', 'tool-a', 'C', 3), exec_id: 'exec-a' });
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(hydrate).toHaveBeenCalledTimes(callsAfterExhaustion);
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            execId: 'exec-a',
            stdout: 'AB',
            stderr: '',
            throughChunkSequence: 2,
            stdoutStartByte: 0,
            stdoutEndByte: 2,
          },
        ],
      },
    ]);
    reconnect();
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('ABC'));
    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBeUndefined();
    disposeA();
    disposeB();
  });

  it('retains a recoverable gap when the pending chunk count overflows', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    // Hold hydration so all events belong to one unresolved recovery attempt.
    hydrate.mockImplementationOnce(() => new Promise(() => undefined));
    for (let sequence = 2; sequence <= MAX_PENDING_TOOL_STDOUT_CHUNKS + 2; sequence += 1) {
      emit(event('conversation-a', 'tool-a', 'x', sequence));
    }
    emit(event('conversation-a', 'tool-a', 'A', 1));
    const output = store.getCurrentSnapshot('conversation-a')[0].toolStdout[0];
    expect(output.stdout).toBe('A');
    expect(output.throughChunkSequence).toBe(1);
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(2));
    dispose();
  });

  it('keeps a marker-only gap after unsequenced hydration and preserves the entry limit', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [{ toolUseId: 'tool-a', stdout: 'unsequenced', stderr: '' }],
      },
    ]);
    emit(event('conversation-a', 'tool-a', 'x'.repeat(MAX_PENDING_TOOL_STDOUT_BYTES + 1), 2));
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBe(true));
    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].throughChunkSequence).toBeUndefined();
    for (let index = 0; index < MAX_TOOL_STDOUT_ENTRIES; index += 1) {
      emit(event('conversation-a', `overflow-${index}`, 'x'.repeat(MAX_PENDING_TOOL_STDOUT_BYTES + 1), 2));
    }
    expect(store.getCurrentSnapshot('conversation-a')[0].toolStdout).toHaveLength(MAX_TOOL_STDOUT_ENTRIES);
    dispose();
  });

  it('recovers by contiguous replay after an unwatermarked snapshot without poisoning byte offsets', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [{ toolUseId: 'tool-a', stdout: 'legacy', stderr: '' }],
      },
    ]);
    const oversized = '生'.repeat(100_000);
    emit(event('conversation-a', 'tool-a', oversized, 2, { start: 1, end: 300_001 }));
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBe(true));
    emit(event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }));
    emit(event('conversation-a', 'tool-a', oversized, 2, { start: 1, end: 300_001 }));
    const output = store.getCurrentSnapshot('conversation-a')[0].toolStdout[0];
    expect(output.throughChunkSequence).toBe(2);
    expect(output.stdoutEndByte).toBe(300_001);
    expect(output.stdout).not.toContain('legacy');
    expect(output.stdout).not.toContain('\uFFFD');
    expect(output.recoveryError).toBeUndefined();
    dispose();
  });

  it('gives a new execution its own recovery budget after another execution exhausts the same root', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    emit({
      ...event('conversation-a', 'tool-a', 'x'.repeat(MAX_PENDING_TOOL_STDOUT_BYTES + 1), 2),
      exec_id: 'old-exec',
    });
    await vi.waitFor(() =>
      expect(
        store.getSnapshot('conversation-a')[0].toolStdout.find((output) => output.execId === 'old-exec')?.recoveryError
      ).toBe(true)
    );
    const exhaustedCalls = hydrate.mock.calls.length;
    for (let sequence = 3; sequence <= 8; sequence += 1) {
      emit({ ...event('conversation-a', 'tool-a', 'old traffic', sequence), exec_id: 'old-exec' });
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(hydrate).toHaveBeenCalledTimes(exhaustedCalls);
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            execId: 'new-exec',
            stdout: 'A',
            stderr: '',
            throughChunkSequence: 1,
            stdoutStartByte: 0,
            stdoutEndByte: 1,
          },
        ],
      },
    ]);
    emit({ ...event('conversation-a', 'tool-a', 'B', 2, { start: 1, end: 2 }), exec_id: 'new-exec' });
    await vi.waitFor(() =>
      expect(
        store.getSnapshot('conversation-a')[0].toolStdout.find((output) => output.execId === 'new-exec')?.stdout
      ).toBe('AB')
    );
    expect(hydrate).toHaveBeenCalledTimes(exhaustedCalls + 1);
    expect(
      store.getSnapshot('conversation-a')[0].toolStdout.find((output) => output.execId === 'old-exec')?.recoveryError
    ).toBe(true);
    for (let sequence = 9; sequence <= 14; sequence += 1) {
      emit({ ...event('conversation-a', 'tool-a', 'old traffic', sequence), exec_id: 'old-exec' });
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
    expect(hydrate).toHaveBeenCalledTimes(exhaustedCalls + 1);
    dispose();
  });

  it('preserves small gaps through empty snapshots and fairly budgets an execution joining in-flight recovery', async () => {
    vi.useFakeTimers();
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    try {
      await vi.runAllTimersAsync();
      const inFlight = deferred<ConversationStreamingFrame[]>();
      hydrate.mockImplementationOnce(() => inFlight.promise);
      emit({
        ...event('conversation-a', 'tool-a', 'x'.repeat(MAX_PENDING_TOOL_STDOUT_BYTES + 1), 2),
        exec_id: 'old-exec',
      });
      emit({ ...event('conversation-a', 'tool-a', 'B', 2, { start: 1, end: 2 }), exec_id: 'new-exec' });
      expect(hydrate).toHaveBeenCalledTimes(2);
      inFlight.resolve([]);
      await vi.runAllTimersAsync();
      const outputs = store.getSnapshot('conversation-a')[0].toolStdout;
      expect(outputs.find((output) => output.execId === 'old-exec')?.recoveryError).toBe(true);
      expect(outputs.find((output) => output.execId === 'new-exec')?.recoveryError).toBe(true);
      const entries = (
        store as unknown as {
          entries: Map<
            string,
            { execId?: string; gapHydrationAttempts?: number; pendingChunks: Map<number, { text: string }> }
          >;
        }
      ).entries;
      expect([...entries.values()].find((output) => output.execId === 'old-exec')?.gapHydrationAttempts).toBe(4);
      expect([...entries.values()].find((output) => output.execId === 'new-exec')?.gapHydrationAttempts).toBe(4);
      expect([...entries.values()].find((output) => output.execId === 'new-exec')?.pendingChunks.get(2)?.text).toBe(
        'B'
      );
      const exhaustedCalls = hydrate.mock.calls.length;
      emit({ ...event('conversation-a', 'tool-a', 'C', 3), exec_id: 'new-exec' });
      emit({ ...event('conversation-a', 'tool-a', 'old traffic', 3), exec_id: 'old-exec' });
      await vi.runAllTimersAsync();
      expect(hydrate).toHaveBeenCalledTimes(exhaustedCalls);
      // Actual contiguous replay resolves this gap and allows a later gap on the same execution.
      emit({ ...event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }), exec_id: 'new-exec' });
      expect(
        store.getCurrentSnapshot('conversation-a')[0].toolStdout.find((output) => output.execId === 'new-exec')?.stdout
      ).toBe('ABC');
      hydrate.mockResolvedValueOnce([
        {
          frameId: 'conversation-a',
          toolStdout: [
            {
              toolUseId: 'tool-a',
              execId: 'new-exec',
              stdout: 'ABCD',
              stderr: '',
              throughChunkSequence: 4,
              stdoutStartByte: 0,
              stdoutEndByte: 4,
            },
          ],
        },
      ]);
      emit({ ...event('conversation-a', 'tool-a', 'E', 5, { start: 4, end: 5 }), exec_id: 'new-exec' });
      await vi.runAllTimersAsync();
      expect(hydrate).toHaveBeenCalledTimes(exhaustedCalls + 1);
      expect(
        store.getSnapshot('conversation-a')[0].toolStdout.find((output) => output.execId === 'new-exec')?.stdout
      ).toBe('ABCDE');
      expect(
        store.getSnapshot('conversation-a')[0].toolStdout.find((output) => output.execId === 'old-exec')?.recoveryError
      ).toBe(true);
    } finally {
      dispose();
      vi.useRealTimers();
    }
  });

  it('does not let an exhausted root affect another root and clears budgets with account caches', async () => {
    vi.useFakeTimers();
    const { store, emit, hydrate } = harness();
    const disposeA = store.subscribe('conversation-a', vi.fn());
    const disposeB = store.subscribe('conversation-b', vi.fn());
    try {
      await vi.runAllTimersAsync();
      emit({
        ...event('conversation-a', 'tool-a', 'x'.repeat(MAX_PENDING_TOOL_STDOUT_BYTES + 1), 2),
        exec_id: 'exec-a',
      });
      await vi.runAllTimersAsync();
      const exhaustedCalls = hydrate.mock.calls.length;
      emit({ ...event('conversation-b', 'tool-a', 'B', 2), exec_id: 'exec-b' });
      await vi.runAllTimersAsync();
      expect(hydrate.mock.calls.length).toBe(exhaustedCalls + 4);
      expect(store.getSnapshot('conversation-b')[0].toolStdout[0].recoveryError).toBe(true);
      store.clearCachedData();
      expect(store.getSnapshot('conversation-a')).toEqual([]);
      expect(store.getSnapshot('conversation-b')).toEqual([]);
      hydrate.mockResolvedValueOnce([
        {
          frameId: 'conversation-a',
          toolStdout: [{ toolUseId: 'tool-a', execId: 'exec-a', stdout: 'A', stderr: '', throughChunkSequence: 1 }],
        },
      ]);
      emit({ ...event('conversation-a', 'tool-a', 'B', 2), exec_id: 'exec-a' });
      await vi.runAllTimersAsync();
      expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('AB');
      expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBeUndefined();
    } finally {
      disposeA();
      disposeB();
      vi.useRealTimers();
    }
  });

  it('does not reset attempts or poison a small sequenced gap with repeated legacy snapshots', async () => {
    vi.useFakeTimers();
    const { store, emit, hydrate, reconnect } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    try {
      await vi.runAllTimersAsync();
      hydrate.mockResolvedValue([
        {
          frameId: 'conversation-a',
          toolStdout: [{ toolUseId: 'tool-a', execId: 'exec-a', stdout: 'legacy', stderr: '' }],
        },
      ]);
      emit({ ...event('conversation-a', 'tool-a', 'B', 2, { start: 1, end: 2 }), exec_id: 'exec-a' });
      await vi.runAllTimersAsync();
      expect(hydrate).toHaveBeenCalledTimes(5);
      expect(store.getSnapshot('conversation-a')[0].toolStdout[0]).toMatchObject({ stdout: '', recoveryError: true });
      emit({ ...event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }), exec_id: 'exec-a' });
      expect(store.getCurrentSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('AB');
      emit({ ...event('conversation-a', 'tool-a', 'D', 4, { start: 3, end: 4 }), exec_id: 'exec-a' });
      await vi.runAllTimersAsync();
      expect(hydrate).toHaveBeenCalledTimes(9);
      expect(store.getSnapshot('conversation-a')[0].toolStdout[0]).toMatchObject({ stdout: 'AB', recoveryError: true });
      hydrate.mockResolvedValueOnce([
        {
          frameId: 'conversation-a',
          toolStdout: [
            {
              toolUseId: 'tool-a',
              execId: 'exec-a',
              stdout: 'ABC',
              stderr: '',
              throughChunkSequence: 3,
              stdoutStartByte: 0,
              stdoutEndByte: 3,
            },
          ],
        },
      ]);
      reconnect();
      await vi.runAllTimersAsync();
      expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('ABCD');
      expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBeUndefined();
    } finally {
      dispose();
      vi.useRealTimers();
    }
  });

  it('buffers out-of-order chunks until the missing sequence arrives', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());

    emit(event('conversation-a', 'tool-a', 'A', 1));
    emit(event('conversation-a', 'tool-a', 'C', 3));
    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('A');

    emit(event('conversation-a', 'tool-a', 'B', 2));
    emit(event('conversation-a', 'tool-a', 'C duplicate', 3));
    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('ABC');
    dispose();
  });

  it('rehydrates immediately when a sequence gap cannot be drained', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            toolName: 'python',
            stdout: 'AB',
            stderr: '',
            throughChunkSequence: 2,
            stdoutStartByte: 0,
            stdoutEndByte: 2,
          },
        ],
      },
    ]);

    emit(event('conversation-a', 'tool-a', 'A', 1));
    emit(event('conversation-a', 'tool-a', 'C', 3));

    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('ABC'));
    expect(hydrate).toHaveBeenCalledTimes(2);
    dispose();
  });

  it('retries a gap recovery when an in-flight snapshot is older than newly buffered chunks', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    const stale = deferred<ConversationStreamingFrame[]>();
    hydrate.mockImplementationOnce(() => stale.promise);
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            stdout: 'ABCD',
            stderr: '',
            throughChunkSequence: 4,
            stdoutStartByte: 0,
            stdoutEndByte: 4,
          },
        ],
      },
    ]);

    emit(event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }));
    emit(event('conversation-a', 'tool-a', 'C', 3, { start: 2, end: 3 }));
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(2));
    emit(event('conversation-a', 'tool-a', 'D', 4, { start: 3, end: 4 }));
    stale.resolve([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            stdout: 'A',
            stderr: '',
            throughChunkSequence: 1,
            stdoutStartByte: 0,
            stdoutEndByte: 1,
          },
        ],
      },
    ]);

    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(3));
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('ABCD'));
    dispose();
  });

  it('rejects inconsistent byte watermarks and recovers from the authoritative snapshot', async () => {
    const { store, emit, hydrate } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            stdout: 'A生',
            stderr: '',
            throughChunkSequence: 2,
            stdoutStartByte: 0,
            stdoutEndByte: 4,
          },
        ],
      },
    ]);

    emit(event('conversation-a', 'tool-a', 'A', 1, { start: 0, end: 1 }));
    emit(event('conversation-a', 'tool-a', '生', 2, { start: 1, end: 2 }));

    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('A');
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('A生'));
    expect(hydrate).toHaveBeenCalledTimes(2);
    dispose();
  });

  it('enforces its output budget in UTF-8 bytes without splitting a character', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    emit(event('conversation-a', 'tool-a', `prefix-${'生'.repeat(MAX_TOOL_STDOUT_BYTES)}`, 1));

    const stdout = store.getSnapshot('conversation-a')[0].toolStdout[0].stdout;
    expect(new TextEncoder().encode(stdout).byteLength).toBeLessThanOrEqual(MAX_TOOL_STDOUT_BYTES);
    expect(stdout).not.toContain('\uFFFD');
    dispose();
  });

  it('trims only the required UTF-8 prefix when a later chunk crosses the output budget', () => {
    const { store, emit } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    const prefix = '生'.repeat(Math.floor(MAX_TOOL_STDOUT_BYTES / 3));
    const prefixBytes = new TextEncoder().encode(prefix).byteLength;
    emit(event('conversation-a', 'tool-a', prefix, 1, { start: 0, end: prefixBytes }));
    emit(event('conversation-a', 'tool-a', 'ABCD', 2, { start: prefixBytes, end: prefixBytes + 4 }));

    const output = store.getSnapshot('conversation-a')[0].toolStdout[0];
    expect(new TextEncoder().encode(output.stdout).byteLength).toBeLessThanOrEqual(MAX_TOOL_STDOUT_BYTES);
    expect(output.stdout).toMatch(/生ABCD$/u);
    expect(output.stdout).not.toContain('\uFFFD');
    expect(output.stdoutEndByte! - output.stdoutStartByte!).toBe(new TextEncoder().encode(output.stdout).byteLength);
    dispose();
  });

  it('coalesces 50,000 chunks into one browser-frame publication', () => {
    let chunkListener: ((event: IToolStdoutChunkEvent) => void) | undefined;
    let scheduled: (() => void) | undefined;
    const changed = vi.fn();
    const store = new ConversationToolStdoutStore({
      subscribeChunks: (listener) => {
        chunkListener = listener;
        return () => {
          chunkListener = undefined;
        };
      },
      subscribeReconnect: () => () => undefined,
      hydrate: () => new Promise<ConversationStreamingFrame[]>(() => undefined),
      schedulePublish: (callback) => {
        scheduled = callback;
        return 1;
      },
      cancelScheduledPublish: () => {
        scheduled = undefined;
      },
    });
    const dispose = store.subscribe('conversation-a', changed);

    for (let index = 0; index < 50_000; index += 1) {
      chunkListener?.(
        event('conversation-a', 'tool-a', 'x', index + 1, {
          start: index,
          end: index + 1,
        })
      );
    }
    expect(changed).not.toHaveBeenCalled();
    expect(scheduled).toBeTypeOf('function');
    scheduled?.();

    const output = store.getSnapshot('conversation-a')[0].toolStdout[0];
    expect(output.stdout).toHaveLength(50_000);
    expect(output.throughChunkSequence).toBe(50_000);
    expect(changed).toHaveBeenCalledTimes(1);
    dispose();
  });

  it('never replays a previous subscription snapshot when returning from another conversation', () => {
    const { store, emit } = harness();
    const disposeA = store.subscribe('conversation-a', vi.fn());
    emit(event('conversation-a', 'tool-a', 'stale bytes', 1));
    expect(store.getSnapshot('conversation-a')).not.toEqual([]);
    disposeA();

    const disposeB = store.subscribe('conversation-b', vi.fn());
    expect(store.getSnapshot('conversation-a')).toEqual([]);
    disposeB();

    const disposeReturnedA = store.subscribe('conversation-a', vi.fn());
    expect(store.getSnapshot('conversation-a')).toEqual([]);
    disposeReturnedA();
  });

  it('bounds output, evicts the oldest entry and rehydrates after reconnect', async () => {
    const { store, emit, hydrate, reconnect } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    emit(event('conversation-a', 'large', 'x'.repeat(MAX_TOOL_STDOUT_BYTES + 10), 1));
    expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toHaveLength(MAX_TOOL_STDOUT_BYTES);

    for (let index = 0; index < MAX_TOOL_STDOUT_ENTRIES; index += 1) {
      emit(event('conversation-a', `tool-${index}`, `${index}`, 1));
    }
    expect(store.getSnapshot('conversation-a')[0].toolStdout).toHaveLength(MAX_TOOL_STDOUT_ENTRIES);
    expect(store.getSnapshot('conversation-a')[0].toolStdout.some((output) => output.toolUseId === 'large')).toBe(
      false
    );

    reconnect();
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(2));
    dispose();
  });

  it('hydrates the root snapshot and replaces it with a longer reconnect snapshot', async () => {
    const { store, hydrate, reconnect } = harness();
    hydrate
      .mockResolvedValueOnce([
        {
          frameId: 'conversation-a',
          toolStdout: [{ toolUseId: 'tool-a', toolName: 'python', stdout: 'seed', stderr: '' }],
        },
      ])
      .mockResolvedValueOnce([
        {
          frameId: 'conversation-a',
          toolStdout: [{ toolUseId: 'tool-a', toolName: 'python', stdout: 'seed recovered', stderr: '' }],
        },
      ]);
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('seed'));
    reconnect();
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].stdout).toBe('seed recovered'));
    dispose();
  });

  it('surfaces a failed recovery on the affected tool and clears it after a successful snapshot', async () => {
    const { store, emit, hydrate, reconnect } = harness();
    const dispose = store.subscribe('conversation-a', vi.fn());
    await vi.waitFor(() => expect(hydrate).toHaveBeenCalledTimes(1));
    emit(event('conversation-a', 'tool-a', 'live', 1));

    hydrate.mockRejectedValueOnce(new Error('recovery unavailable'));
    reconnect();
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBe(true));

    hydrate.mockResolvedValueOnce([
      {
        frameId: 'conversation-a',
        toolStdout: [
          {
            toolUseId: 'tool-a',
            stdout: 'recovered',
            stderr: '',
            throughChunkSequence: 2,
          },
        ],
      },
    ]);
    reconnect();
    await vi.waitFor(() => expect(store.getSnapshot('conversation-a')[0].toolStdout[0].recoveryError).toBeUndefined());
    dispose();
  });
});

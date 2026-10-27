import { describe, expect, it, vi } from 'vitest';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES as defaults,
  type LibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';
import { LibraryColumnPreferenceOwner } from '@/renderer/services/libraryColumnPreferenceOwner';

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
const tick = () => new Promise<void>((resolve) => setImmediate(resolve));
const value = (visible: LibraryColumnPreferences['visible']): LibraryColumnPreferences => ({ ...defaults, visible });

describe('Library column preference view owner', () => {
  it('publishes ready only for the confirmed remote layout across queued edits', async () => {
    const writes = [deferred<void>(), deferred<void>()];
    let writeIndex = 0;
    let remote = defaults;
    const save = vi.fn((_value: LibraryColumnPreferences, _signal: AbortSignal) => writes[writeIndex++].promise);
    const owner = new LibraryColumnPreferenceOwner({ load: async () => remote, save }, () => true);
    const readySnapshots: Array<{ shown: LibraryColumnPreferences; remote: LibraryColumnPreferences }> = [];
    owner.subscribe(() => {
      const snapshot = owner.getSnapshot();
      if (snapshot.status === 'ready') readySnapshots.push({ shown: snapshot.preferences, remote });
    });
    owner.start();
    await tick();
    const first = value(['year']);
    const second = value(['year', 'doi']);
    owner.change(first);
    owner.change(second);
    remote = first;
    writes[0].resolve();
    await tick();
    const afterFirstAck = owner.getSnapshot();
    remote = second;
    writes[1].resolve();
    await tick();
    expect(save).toHaveBeenCalledTimes(2);
    expect(afterFirstAck.status).toBe('saving');
    expect(afterFirstAck.preferences).toEqual(second);
    expect(owner.getSnapshot()).toEqual({ preferences: second, status: 'ready' });
    for (const snapshot of readySnapshots) expect(snapshot.shown).toEqual(snapshot.remote);
    owner.dispose();
  });

  it('serializes edits and retains a newer intent when an earlier PUT completes', async () => {
    const writes = [deferred<void>(), deferred<void>()];
    const load = vi.fn(async () => defaults);
    let writeIndex = 0;
    const save = vi.fn((_value: LibraryColumnPreferences, _signal: AbortSignal) => writes[writeIndex++].promise);
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    await tick();
    expect(save).not.toHaveBeenCalled();
    owner.change(value(['year']));
    owner.change(value(['year', 'doi']));
    expect(save).toHaveBeenCalledTimes(1);
    expect(owner.getSnapshot().preferences.visible).toEqual(['year', 'doi']);
    writes[0].resolve();
    await tick();
    expect(save).toHaveBeenCalledTimes(2);
    expect(save.mock.calls[1][0].visible).toEqual(['year', 'doi']);
    writes[1].resolve();
    await tick();
    expect(owner.getSnapshot().status).toBe('ready');
    owner.dispose();
  });

  it('merges only local edits over a later authoritative load instead of losing remote columns', async () => {
    const pending = deferred<unknown>();
    const load = vi.fn(() => pending.promise);
    const save = vi.fn(async (_value: LibraryColumnPreferences, _signal: AbortSignal) => {});
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    owner.change(value(['readingStatus', 'year', 'authors', 'attachments', 'doi']));
    pending.resolve(value(['year']));
    await tick();
    expect(save.mock.calls[0][0].visible).toEqual(['year', 'doi']);
    expect(owner.getSnapshot().preferences.visible).toEqual(['year', 'doi']);
    owner.dispose();
  });

  it('distinguishes invalid and failed reads and writes defaults only after explicit reset', async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce({ ...defaults, order: ['title'] })
      .mockRejectedValueOnce(new Error('offline'));
    const save = vi.fn(async (_value: LibraryColumnPreferences, _signal: AbortSignal) => {});
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    await tick();
    expect(owner.getSnapshot().status).toBe('invalid');
    owner.change(value([]));
    expect(save).not.toHaveBeenCalled();
    owner.retry();
    await tick();
    expect(owner.getSnapshot().status).toBe('read-error');
    expect(save).not.toHaveBeenCalled();
    owner.reset();
    await tick();
    expect(save.mock.calls[0][0]).toEqual(defaults);
    expect(owner.getSnapshot().status).toBe('ready');
    owner.dispose();
  });

  it('confirms an unknown PUT by one readback and requires explicit retry after a mismatch', async () => {
    const desired = value(['year']);
    const load = vi
      .fn()
      .mockResolvedValueOnce(defaults)
      .mockResolvedValueOnce(desired)
      .mockResolvedValueOnce(desired)
      .mockResolvedValueOnce(desired);
    const save = vi
      .fn()
      .mockRejectedValueOnce(new Error('lost response'))
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(undefined);
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    await tick();
    owner.change(desired);
    await tick();
    expect(owner.getSnapshot().status).toBe('ready');
    expect(save).toHaveBeenCalledTimes(1);
    owner.change(value(['year', 'doi']));
    await tick();
    expect(owner.getSnapshot().status).toBe('write-error');
    expect(owner.getSnapshot().preferences.visible).toEqual(['year', 'doi']);
    expect(save).toHaveBeenCalledTimes(2);
    expect(load).toHaveBeenCalledTimes(3);
    owner.retry();
    await tick();
    expect(load).toHaveBeenCalledTimes(4);
    expect(save).toHaveBeenCalledTimes(3);
    expect(owner.getSnapshot().status).toBe('ready');
    owner.dispose();
  });

  it('cancels owned requests and fences late account results and queued sends', async () => {
    let current = true;
    const pending = deferred<unknown>();
    const load = vi.fn((_signal: AbortSignal) => pending.promise);
    const save = vi.fn(async () => {});
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => current);
    owner.start();
    owner.change(value([]));
    current = false;
    owner.dispose();
    expect(load.mock.calls[0][0].aborted).toBe(true);
    pending.resolve(defaults);
    await tick();
    expect(save).not.toHaveBeenCalled();
    expect(owner.getSnapshot().status).toBe('loading');
    const write = deferred<void>();
    const live = new LibraryColumnPreferenceOwner(
      { load: async () => defaults, save: () => write.promise },
      () => current
    );
    current = true;
    live.start();
    await tick();
    live.change(value(['year']));
    live.change(value(['doi']));
    current = false;
    live.dispose();
    write.resolve();
    await tick();
    expect(live.getSnapshot().status).toBe('saving');
  });
});

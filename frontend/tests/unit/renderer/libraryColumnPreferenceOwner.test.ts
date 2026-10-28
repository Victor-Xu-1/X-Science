import { describe, expect, it, vi } from 'vitest';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES as defaults,
  type LibraryColumnPreferences,
  type LibraryColumnPreferencesWire,
} from '@/common/adapter/libraryColumnPreferences';
import { LibraryColumnPreferenceOwner } from '@/renderer/services/runtime/libraryColumnPreferenceOwner';

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
const withNotes = {
  version: 2,
  order: ['readingStatus', 'notes', 'doi', 'year', 'authors', 'attachments'],
  visible: ['readingStatus', 'notes', 'year'],
} as const;

describe('Library column preference view owner', () => {
  it('V2 reader preserves the complete Notes layout through five-column changes and reset', async () => {
    const save = vi.fn(async (_value: unknown, _signal: AbortSignal) => {});
    const owner = new LibraryColumnPreferenceOwner({ load: async () => withNotes, save }, () => true);
    owner.start();
    await tick();
    expect(owner.getSnapshot()).toEqual({
      status: 'ready',
      preferences: {
        version: 1,
        order: ['readingStatus', 'doi', 'year', 'authors', 'attachments'],
        visible: ['readingStatus', 'year'],
      },
    });
    expect(save).not.toHaveBeenCalled();
    owner.change({ ...owner.getSnapshot().preferences, visible: ['readingStatus', 'doi', 'year'] });
    await tick();
    expect(save.mock.calls[0][0]).toEqual({ ...withNotes, visible: ['readingStatus', 'notes', 'doi', 'year'] });
    owner.change({
      ...owner.getSnapshot().preferences,
      order: ['authors', 'readingStatus', 'doi', 'year', 'attachments'],
    });
    await tick();
    expect(save.mock.calls[1][0]).toEqual({
      version: 2,
      order: ['authors', 'notes', 'readingStatus', 'doi', 'year', 'attachments'],
      visible: ['notes', 'readingStatus', 'doi', 'year'],
    });
    owner.reset();
    await tick();
    expect(save.mock.calls[2][0]).toEqual({
      version: 2,
      order: ['readingStatus', 'notes', 'year', 'authors', 'doi', 'attachments'],
      visible: ['readingStatus', 'notes', 'year', 'authors', 'attachments'],
    });
    expect(owner.getSnapshot().preferences).toEqual(defaults);
    expect(save).toHaveBeenCalledTimes(3);
    expect(withNotes.order[1]).toBe('notes');
    owner.dispose();
  });

  it('confirms a V2 order intent by projected readback while retaining a remote Notes change', async () => {
    const remote = {
      version: 2,
      order: ['authors', 'notes', 'readingStatus', 'doi', 'year', 'attachments'],
      visible: ['readingStatus', 'year'],
    };
    const load = vi.fn().mockResolvedValueOnce(withNotes).mockResolvedValueOnce(remote);
    const save = vi.fn().mockRejectedValueOnce(new Error('lost PUT response')).mockResolvedValueOnce(undefined);
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    await tick();
    owner.change({
      ...owner.getSnapshot().preferences,
      order: ['authors', 'readingStatus', 'doi', 'year', 'attachments'],
    });
    await tick();
    expect(owner.getSnapshot().status).toBe('ready');
    expect(load).toHaveBeenCalledTimes(2);
    expect(save).toHaveBeenCalledTimes(1);
    expect(save.mock.calls[0][0]).toEqual({ ...remote, visible: ['notes', 'readingStatus', 'year'] });
    owner.change({ ...owner.getSnapshot().preferences, visible: ['readingStatus', 'doi', 'year'] });
    await tick();
    expect(save.mock.calls[1][0]).toEqual({ ...remote, visible: ['readingStatus', 'doi', 'year'] });
    owner.dispose();
  });

  it('adopts a V2 readback after an unknown V1 PUT without promoting earlier V1 writes', async () => {
    const v2 = { ...withNotes, visible: ['readingStatus', 'notes', 'doi', 'year', 'authors', 'attachments'] };
    const load = vi.fn().mockResolvedValueOnce(defaults).mockResolvedValueOnce(v2);
    const save = vi.fn().mockRejectedValueOnce(new Error('unknown response')).mockResolvedValueOnce(undefined);
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    await tick();
    owner.change(value([...defaults.visible, 'doi']));
    await tick();
    expect(save.mock.calls[0][0].version).toBe(1);
    expect(owner.getSnapshot().status).toBe('ready');
    expect(save).toHaveBeenCalledTimes(1);
    owner.change({ ...owner.getSnapshot().preferences, visible: ['readingStatus', 'doi', 'authors', 'attachments'] });
    await tick();
    expect(save.mock.calls[1][0]).toEqual({
      ...v2,
      visible: ['readingStatus', 'notes', 'doi', 'authors', 'attachments'],
    });
    owner.dispose();
  });

  it('does not silently reset an invalid V2 or resend a mismatching unknown V2 PUT', async () => {
    const load = vi
      .fn()
      .mockResolvedValueOnce({ ...withNotes, order: ['notes'] })
      .mockResolvedValueOnce(withNotes)
      .mockResolvedValueOnce(withNotes)
      .mockResolvedValueOnce(withNotes);
    const save = vi.fn().mockRejectedValueOnce(new Error('lost')).mockResolvedValueOnce(undefined);
    const owner = new LibraryColumnPreferenceOwner({ load, save }, () => true);
    owner.start();
    await tick();
    expect(owner.getSnapshot().status).toBe('invalid');
    owner.change(value([]));
    expect(save).not.toHaveBeenCalled();
    owner.retry();
    await tick();
    owner.change({ ...owner.getSnapshot().preferences, visible: ['doi'] });
    await tick();
    expect(owner.getSnapshot()).toMatchObject({ status: 'write-error', preferences: { visible: ['doi'] } });
    expect(save).toHaveBeenCalledTimes(1);
    expect(load).toHaveBeenCalledTimes(3);
    owner.retry();
    await tick();
    expect(save).toHaveBeenCalledTimes(2);
    expect(load).toHaveBeenCalledTimes(4);
    expect(save.mock.calls[1][0]).toEqual({ ...withNotes, visible: ['notes', 'doi'] });
    expect(owner.getSnapshot().status).toBe('ready');
    owner.dispose();
  });

  it('publishes ready only for the confirmed remote layout across queued edits', async () => {
    const writes = [deferred<void>(), deferred<void>()];
    let writeIndex = 0;
    let remote = defaults;
    const save = vi.fn((_value: LibraryColumnPreferencesWire, _signal: AbortSignal) => writes[writeIndex++].promise);
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
    const save = vi.fn((_value: LibraryColumnPreferencesWire, _signal: AbortSignal) => writes[writeIndex++].promise);
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
    const save = vi.fn(async (_value: LibraryColumnPreferencesWire, _signal: AbortSignal) => {});
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
    const save = vi.fn(async (_value: LibraryColumnPreferencesWire, _signal: AbortSignal) => {});
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

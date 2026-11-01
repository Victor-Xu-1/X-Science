import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createRendererMutationSlot,
  RendererMutationSlotConflict,
  withRendererMutationLock,
} from '@/renderer/services/runtime/rendererMutationSlot';
import { resetRendererAccountScopeForTest } from '@/renderer/services/rendererAccountScope';
import { installLiteratureLocks, literaturePendingStorage } from './literatureMutationLockFixtures';

type Intent = { id: string; text: string };
const first: Intent = { id: 'first', text: 'Original request' };
const next: Intent = { id: 'next', text: 'Next request' };
const key = 'owned-slot';
const codec = (storage: ReturnType<typeof literaturePendingStorage>) =>
  createRendererMutationSlot<Intent>({
    key,
    storage: () => storage,
    decode: (value) => {
      if (!value || typeof value !== 'object' || !('owner' in value) || value.owner !== 'owner' || !('intent' in value))
        throw new Error('Invalid original owner');
      return value.intent as Intent;
    },
    encode: (intent) => JSON.stringify({ owner: 'owner', intent }),
    same: (left, right) => left.id === right.id && left.text === right.text,
  });

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  resetRendererAccountScopeForTest();
});

describe('renderer pending-slot authority', () => {
  it('keeps the original envelope across a second owner view and rejects overwrite', async () => {
    installLiteratureLocks();
    const storage = literaturePendingStorage();
    const a = codec(storage);
    const b = codec(storage);
    await withRendererMutationLock(
      key,
      () => true,
      () => a.replace(null, first)
    );
    expect(b.read()).toEqual(first);
    await expect(
      withRendererMutationLock(
        key,
        () => true,
        () => b.replace(null, next)
      )
    ).rejects.toBeInstanceOf(RendererMutationSlotConflict);
    expect(JSON.parse(storage.getItem(key)!)).toEqual({ owner: 'owner', intent: first });
  });

  it('does not let an old ACK clear a newer intent', async () => {
    installLiteratureLocks();
    const storage = literaturePendingStorage();
    const slot = codec(storage);
    await withRendererMutationLock(
      key,
      () => true,
      () => slot.replace(null, first)
    );
    await withRendererMutationLock(
      key,
      () => true,
      () => slot.settle(first, true)
    );
    await withRendererMutationLock(
      key,
      () => true,
      () => slot.replace(null, next)
    );
    const stale = await withRendererMutationLock(
      key,
      () => true,
      () => slot.settle(first, true)
    );
    expect(stale).toEqual({ remove: false, pending: next });
    expect(slot.read()).toEqual(next);
  });

  it('retains unreadable pending bytes instead of silently erasing them', () => {
    const storage = literaturePendingStorage();
    storage.setItem(key, '{interrupted-original-record');
    expect(() => codec(storage).read()).toThrow();
    expect(storage.getItem(key)).toBe('{interrupted-original-record');
  });

  it('propagates a storage failure without claiming the intent was persisted', () => {
    const storage = literaturePendingStorage();
    vi.spyOn(storage, 'setItem').mockImplementation(() => {
      throw new Error('storage unavailable');
    });
    expect(() => codec(storage).replace(null, first)).toThrow('storage unavailable');
    expect(storage.getItem(key)).toBeNull();
  });

  it('fails closed without a native lock and never enters the critical section', async () => {
    vi.stubGlobal('navigator', {});
    const critical = vi.fn();
    await expect(withRendererMutationLock(key, () => true, critical)).rejects.toThrow('lock unavailable');
    expect(critical).not.toHaveBeenCalled();
  });
});

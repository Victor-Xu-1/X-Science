import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryCategoryDraftOwner } from '@/renderer/services/runtime/memoryCategoryDraft';

function deferred() {
  let complete!: (value: unknown) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<unknown>((yes, no) => {
    complete = yes;
    reject = no;
  });
  return { promise, resolve: () => complete(undefined), reject };
}

const owners = new Set<MemoryCategoryDraftOwner>();
afterEach(() => {
  for (const owner of owners) owner.dispose();
  owners.clear();
});

function fixture() {
  let account = { id: 'owner-a', scope: 'generation-1:owner-a' };
  let reset = () => {};
  const write = vi.fn((_value: unknown, _signal: AbortSignal) => Promise.resolve<unknown>(undefined));
  const owner = new MemoryCategoryDraftOwner({
    account: () => account,
    registerReset: (next) => {
      reset = next;
      return () => {};
    },
    write,
  });
  owners.add(owner);
  const switchAccount = (id: string, scope: string) => {
    account = { id, scope };
    reset();
  };
  const open = () => {
    const view = owner.registerView()!;
    owner.open(view);
    owner.update(owner.actionLease(view), { name: 'Methods', guidance: 'Use only for methods.' });
    return view;
  };
  return { owner, write, switchAccount, open };
}

describe('Memory category document/account owner', () => {
  it('retains values after view disposal and restores one visible editor lease', () => {
    const f = fixture();
    const a = f.open();
    const before = f.owner.getSnapshot().draft;
    f.owner.unregisterView(a);
    expect(f.owner.getSnapshot().draft).toBe(before);
    const b = f.owner.registerView()!;
    const c = f.owner.registerView()!;
    expect(f.owner.getSnapshot().visibleView).toBe(c.id);
    expect(f.owner.actionLease(b)).toBeNull();
    expect(f.owner.open(b)).toBe(false);
    expect(f.owner.getSnapshot().draft?.value).toEqual({
      name: 'Methods',
      guidance: 'Use only for methods.',
      autoRecall: true,
    });
  });

  it('singleflights one write and clears only its episode on a matching ACK', async () => {
    const f = fixture();
    const ack = deferred();
    f.write.mockImplementation(() => ack.promise);
    const view = f.open();
    const action = f.owner.actionLease(view);
    const p1 = f.owner.save(action);
    const p2 = f.owner.save(action);
    expect(p2).toBe(p1);
    expect(f.owner.update(action, { name: 'Unsent edit' })).toBe(false);
    expect(f.owner.discard(action)).toBe(false);
    await Promise.resolve();
    expect(f.write).toHaveBeenCalledTimes(1);
    ack.resolve();
    expect(await p1).toEqual({ kind: 'committed' });
    expect(f.owner.getSnapshot().draft).toBeNull();
    f.owner.open(view);
    expect(f.owner.discard(action)).toBe(false); // Old episode cannot clear the next operation.
  });

  it('keeps failed values and requires an explicit retry', async () => {
    const f = fixture();
    const first = deferred();
    const second = deferred();
    f.write.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise);
    const view = f.open();
    const failed = f.owner.save(f.owner.actionLease(view));
    await Promise.resolve();
    first.reject(new Error('unavailable'));
    expect(await failed).toEqual({ kind: 'failed' });
    expect(f.owner.getSnapshot().draft?.phase).toBe('failed');
    expect(f.owner.getSnapshot().draft?.value.name).toBe('Methods');
    expect(f.write).toHaveBeenCalledTimes(1);
    const retry = f.owner.save(f.owner.actionLease(view));
    await Promise.resolve();
    expect(f.write).toHaveBeenCalledTimes(2);
    second.resolve();
    expect(await retry).toEqual({ kind: 'committed' });
  });

  it('rejects old-account ACK and pre-dispatch work after generation reset', async () => {
    const f = fixture();
    const ack = deferred();
    f.write.mockImplementation(() => ack.promise);
    const a = f.open();
    const request = f.owner.save(f.owner.actionLease(a));
    await Promise.resolve();
    f.switchAccount('owner-b', 'generation-2:owner-b');
    const b = f.open();
    f.owner.update(f.owner.actionLease(b), { name: 'Owner B' });
    ack.resolve();
    expect(await request).toEqual({ kind: 'stale' });
    expect(f.owner.getSnapshot().draft?.value.name).toBe('Owner B');
    expect(f.owner.update(f.owner.actionLease(a), { name: 'Owner A late edit' })).toBe(false);
    f.switchAccount('', 'generation-3:anonymous');
    expect(f.owner.registerView()).toBeNull();
    expect(f.owner.getSnapshot().draft).toBeNull();
    const pending = fixture();
    const oldView = pending.open();
    const beforeDispatch = pending.owner.save(pending.owner.actionLease(oldView));
    pending.switchAccount('owner-b', 'generation-2:owner-b');
    expect(await beforeDispatch).toEqual({ kind: 'stale' });
    expect(pending.write).not.toHaveBeenCalled();
  });

  it('fences old view/action/refresh leases and refreshes the current view once per commit', async () => {
    const f = fixture();
    const a = f.open();
    const oldAction = f.owner.actionLease(a);
    const b = f.owner.registerView('modal')!;
    f.owner.open(b); // Transfer the existing draft, never create a second one.
    expect(f.owner.update(oldAction, { name: 'Stale A' })).toBe(false);
    expect(await f.owner.save(oldAction)).toEqual({ kind: 'denied' });
    expect(f.write).not.toHaveBeenCalled();
    await f.owner.save(f.owner.actionLease(b));
    const oldRefresh = f.owner.captureRefresh(b)!;
    f.owner.unregisterView(b);
    expect(f.owner.isRefreshCurrent(oldRefresh)).toBe(false);
    const current = f.owner.captureRefresh(a)!;
    expect(f.owner.claimChanged(current)).toBe(true);
    expect(f.owner.claimChanged(current)).toBe(false);
    expect(f.owner.claimSaved(current)).toBe(true);
    expect(f.owner.acknowledgeRefresh(current)).toBe(true);
    expect(f.owner.captureRefresh(a)).toBeNull();
  });
});

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { isValidElement, type ReactNode } from 'react';
import DelegatedStopActions from '@/renderer/pages/conversation/components/DelegatedStopActions';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { DelegatedStopController } from '@/renderer/services/runtime/delegatedStopController';
import { acquireDelegatedStopController } from '@/renderer/services/runtime/delegatedStopRegistry';
import type { DelegatedStopReceipt, DelegatedStopSnapshot } from '@/renderer/services/runtime/delegatedStopTypes';
import { deferred, identity, memoryStorage, receipt, snapshot } from './delegatedStopFixtures';

const mocks = vi.hoisted(() => ({ read: vi.fn(), post: vi.fn(), lookup: vi.fn() }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/renderer/services/runtime/delegatedStopTransport', async (original) => {
  const actual = await original<typeof import('@/renderer/services/runtime/delegatedStopTransport')>();
  return {
    ...actual,
    readDelegatedStopSnapshot: mocks.read,
    submitDelegatedStop: mocks.post,
    readDelegatedStopReceipt: mocks.lookup,
  };
});
describe('delegated Stop scoped controller', () => {
  beforeEach(() => {
    setRendererAccountOwner(null);
    vi.stubGlobal('sessionStorage', memoryStorage());
    setRendererAccountOwner(identity.ownerId);
    mocks.read.mockReset();
    mocks.post.mockReset();
    mocks.lookup.mockReset();
    mocks.read.mockResolvedValue(structuredClone(snapshot));
    mocks.post.mockImplementation(async (request) => receipt(request));
    mocks.lookup.mockImplementation(async (request) => receipt(request));
  });
  afterEach(() => {
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
  });
  it('rejects a stale rendered Stop handler instead of retargeting a replacement child or branch', async () => {
    for (const childFrameId of ['child', undefined]) {
      setRendererAccountOwner(null);
      vi.stubGlobal('sessionStorage', memoryStorage());
      setRendererAccountOwner(identity.ownerId);
      mocks.post.mockClear();
      mocks.read.mockResolvedValue(structuredClone(snapshot));
      const controller = new DelegatedStopController(identity);
      await controller.refresh();
      const label = `conversation.delegatedStop.${childFrameId ? 'child' : 'branch'}`;
      const findClick = (node: ReactNode): (() => void) | null => {
        if (Array.isArray(node)) {
          for (const child of node) {
            const click = findClick(child);
            if (click) return click;
          }
          return null;
        }
        if (!isValidElement<{ children?: ReactNode; onClick?: () => void; 'aria-label'?: string }>(node)) return null;
        if (node.type === 'button' && node.props['aria-label'] === label) return node.props.onClick ?? null;
        return findClick(node.props.children);
      };
      const oldHandler = findClick(
        DelegatedStopActions({
          stop: {
            controller,
            view: controller.getSnapshot(),
            scopeKey: 'rendered',
            retry: () => {},
            unavailable: false,
          },
          childFrameId,
        })
      );
      expect(oldHandler).not.toBeNull();
      const replacement = structuredClone(snapshot);
      if (childFrameId) {
        replacement.targets[0].target = {
          ...replacement.targets[0].target!,
          input_message_id: 'new-input',
          input_revision: 3,
          runner_attempt: 2,
        };
      } else {
        replacement.branch.generation += 1;
        replacement.parent_input_revision += 1;
        replacement.fence_generation += 1;
      }
      mocks.read.mockResolvedValue(replacement);
      await controller.refresh();
      oldHandler!();
      expect.soft(mocks.post, `${label} must not stop the replacement`).not.toHaveBeenCalled();
      expect.soft(controller.getSnapshot().issue, label).toBe('stale');
      controller.dispose();
    }
  });
  it('shares one controller and reserves the full child request synchronously before duplicate clicks', async () => {
    const controller = acquireDelegatedStopController(identity)!;
    expect(acquireDelegatedStopController(identity)).toBe(controller);
    const pending = deferred<DelegatedStopReceipt>();
    const source = structuredClone(snapshot);
    mocks.read.mockResolvedValue(source);
    mocks.post.mockReturnValue(pending.promise);
    await controller.refresh();
    controller.start(controller.getSnapshot().snapshot, 'child');
    controller.start(controller.getSnapshot().snapshot, 'child');
    expect(mocks.post).toHaveBeenCalledTimes(1);
    const sent = mocks.post.mock.calls[0][0];
    expect(sent).toMatchObject({
      mode: 'child',
      scope: snapshot.scope,
      branch: snapshot.branch,
      parent_input_revision: 4,
      expected_fence_generation: 0,
      target: snapshot.targets[0].target,
    });
    source.targets[0].target!.input_revision = 99;
    expect(sent.target.input_revision).toBe(2);
    expect(sessionStorage.length).toBe(1);
    pending.resolve(receipt(sent));
    await vi.waitFor(() => expect(controller.getSnapshot().operation?.phase).toBe('stopping'));
  });
  it('drops a late branch read instead of retargeting the selected branch', async () => {
    const controller = new DelegatedStopController(identity);
    const oldRead = deferred<DelegatedStopSnapshot>();
    mocks.read
      .mockReturnValueOnce(oldRead.promise)
      .mockResolvedValueOnce({ ...snapshot, branch: { branch_id: 'br_22222222', generation: 4 } });
    controller.selectBranch('br_11111111', 1);
    const old = controller.refresh();
    controller.selectBranch('br_22222222', 2);
    await controller.refresh();
    oldRead.resolve(snapshot);
    await old;
    expect(controller.getSnapshot().snapshot?.branch.branch_id).toBe('br_22222222');
    expect(mocks.post).not.toHaveBeenCalled();
  });
  it('does not let an old account acknowledgement write recovery or presentation state', async () => {
    const controller = new DelegatedStopController(identity);
    const pending = deferred<DelegatedStopReceipt>();
    mocks.post.mockReturnValue(pending.promise);
    await controller.refresh();
    controller.start(controller.getSnapshot().snapshot);
    const before = sessionStorage.getItem(sessionStorage.key(0)!);
    const sent = mocks.post.mock.calls[0][0];
    setRendererAccountOwner('account-b');
    pending.resolve(receipt(sent));
    await pending.promise;
    await Promise.resolve();
    expect(controller.getSnapshot().operation?.phase).toBe('sending');
    expect(sessionStorage.getItem(sessionStorage.key(0)!)).toBe(before);
    const next = new DelegatedStopController({ ...identity, ownerId: 'account-b' });
    expect(next.getSnapshot().operation).toBeNull();
  });
  it('keeps lost-ACK and subsequent stale retry unresolved with the original frozen request', async () => {
    const controller = new DelegatedStopController(identity);
    mocks.post.mockRejectedValueOnce(new Error('lost ACK'));
    await controller.refresh();
    controller.start(controller.getSnapshot().snapshot);
    await vi.waitFor(() => expect(controller.getSnapshot().operation?.phase).toBe('uncertain'));
    const sent = mocks.post.mock.calls[0][0];
    mocks.lookup.mockRejectedValueOnce(
      new BackendHttpError({
        method: 'GET',
        path: '/receipt',
        status: 404,
        body: { code: 'DELEGATED_STOP_UNAVAILABLE' },
      })
    );
    await controller.check(sent.request_id);
    expect(controller.getSnapshot().operation?.phase).toBe('uncertain');
    mocks.post.mockRejectedValueOnce(
      new BackendHttpError({ method: 'POST', path: '/stop', status: 409, body: { code: 'DELEGATED_STOP_SCOPE_STALE' } })
    );
    controller.retry(sent.request_id);
    await vi.waitFor(() => expect(controller.getSnapshot().operation?.phase).toBe('uncertain'));
    expect(mocks.post.mock.calls.map((call) => JSON.stringify(call[0]))).toEqual([
      JSON.stringify(sent),
      JSON.stringify(sent),
    ]);
    expect(sessionStorage.length).toBe(1);
  });
  it('restores only original request hints and authenticates through GET without automatic POST', async () => {
    const first = new DelegatedStopController(identity);
    mocks.post.mockRejectedValueOnce(new Error('lost ACK'));
    await first.refresh();
    first.start(first.getSnapshot().snapshot);
    await vi.waitFor(() => expect(first.getSnapshot().operation?.phase).toBe('uncertain'));
    const sent = mocks.post.mock.calls[0][0];
    first.dispose();
    const restored = new DelegatedStopController(identity);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    await restored.observe();
    expect(mocks.lookup).toHaveBeenCalledWith(sent);
    expect(mocks.post).toHaveBeenCalledTimes(1);
    expect(restored.getSnapshot().operation?.receipt?.request).toEqual(sent);
  });
  it('rejects foreign incarnation and never derives a Stop target from legacy display rows', async () => {
    const controller = new DelegatedStopController(identity);
    mocks.read.mockResolvedValueOnce({
      ...snapshot,
      scope: { ...snapshot.scope, parent_frame_incarnation_id: 'replacement' },
    });
    await controller.refresh();
    controller.start(controller.getSnapshot().snapshot);
    expect(controller.getSnapshot().issue).toBe('stale');
    expect(mocks.post).not.toHaveBeenCalled();
    mocks.read.mockResolvedValueOnce({
      ...snapshot,
      can_stop_branch: false,
      targets: [{ ...snapshot.targets[0], origin: 'legacy-unavailable', target: null, can_stop: false }],
    });
    await controller.refresh();
    controller.start(controller.getSnapshot().snapshot, 'child');
    controller.start(controller.getSnapshot().snapshot);
    expect(mocks.post).not.toHaveBeenCalled();
  });
  it('surfaces unavailable restore storage without inventing durable recovery', async () => {
    vi.stubGlobal('sessionStorage', {
      getItem: () => {
        throw new Error('storage denied');
      },
    });
    const controller = new DelegatedStopController(identity);
    await controller.refresh();
    expect(controller.getSnapshot().recoveryLimited).toBe(true);
    controller.start(controller.getSnapshot().snapshot);
    await vi.waitFor(() => expect(mocks.post).toHaveBeenCalledTimes(1));
    expect(controller.getSnapshot().recoveryLimited).toBe(true);
  });
  it('coalesces a wake during a read into one follow-up authority read', async () => {
    const controller = new DelegatedStopController(identity);
    const pending = deferred<DelegatedStopSnapshot>();
    mocks.read.mockReturnValueOnce(pending.promise).mockResolvedValueOnce(snapshot);
    const first = controller.refresh();
    void controller.refresh();
    void controller.refresh();
    pending.resolve(snapshot);
    await first;
    await vi.waitFor(() => expect(mocks.read).toHaveBeenCalledTimes(2));
    expect(mocks.post).not.toHaveBeenCalled();
  });
  it('reasserts an accepted stopping request only on explicit retry with the original body', async () => {
    const controller = new DelegatedStopController(identity);
    mocks.post.mockRejectedValueOnce(new Error('lost postcommit acknowledgement'));
    await controller.refresh();
    controller.start(controller.getSnapshot().snapshot, 'child');
    await vi.waitFor(() => expect(controller.getSnapshot().operation?.phase).toBe('uncertain'));
    const original = mocks.post.mock.calls[0][0];
    const originalBody = JSON.stringify(original);
    const accepted = receipt(original);
    accepted.targets[0].execution_observation = 'unavailable';
    mocks.lookup.mockResolvedValueOnce(accepted);
    await controller.check(original.request_id);
    expect(controller.getSnapshot().operation?.phase).toBe('stopping');
    expect(controller.getSnapshot().operation?.receipt?.targets[0].execution_observation).toBe('unavailable');
    expect(mocks.post).toHaveBeenCalledTimes(1);

    const reassertion = deferred<DelegatedStopReceipt>();
    mocks.post.mockReturnValueOnce(reassertion.promise);
    controller.retry(original.request_id);
    controller.retry(original.request_id);
    expect(mocks.post).toHaveBeenCalledTimes(2);
    expect(controller.getSnapshot().operation?.phase).toBe('sending');
    expect(mocks.post.mock.calls.map((call) => JSON.stringify(call[0]))).toEqual([originalBody, originalBody]);
    reassertion.resolve(receipt(original));
    await vi.waitFor(() => expect(controller.getSnapshot().operation?.phase).toBe('stopping'));
    expect(controller.getSnapshot().operation?.receipt?.targets[0].execution_observation).toBe('running');
    expect(mocks.post).toHaveBeenCalledTimes(2);
  });
});

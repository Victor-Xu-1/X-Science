import { beforeEach, describe, expect, it, vi } from 'vitest';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const transport = vi.hoisted(() => ({
  create: vi.fn(),
  lookup: vi.fn(),
  close: vi.fn(),
  cancel: vi.fn(),
  messageCancel: vi.fn(),
  followup: vi.fn(),
  branches: vi.fn(),
  session: vi.fn(),
  resume: vi.fn(),
}));
vi.mock('@/renderer/services/xScienceAside', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceAside')>()),
  createXScienceAside: transport.create,
  lookupXScienceAside: transport.lookup,
  closeXScienceAside: transport.close,
  cancelXScienceAsideInput: transport.cancel,
  cancelXScienceAsideMessageIntent: transport.messageCancel,
  sendXScienceAsideFollowup: transport.followup,
  loadXScienceAsideBranches: transport.branches,
}));
vi.mock('@/renderer/services/xScienceSessionOptions', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceSessionOptions')>()),
  loadXScienceSessionOptions: transport.session,
}));
vi.mock('@/renderer/services/xScienceRuntimeOperations', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceRuntimeOperations')>()),
  resumeXScienceFrame: transport.resume,
}));

import { XScienceAsideOperation } from '@/renderer/services/xScienceAsideOperation';
import { readAsidePresentation } from '@/renderer/services/xScienceAsidePresentation';

const intent = {
  parentFrameId: 'parent-1',
  intentId: '61234567-89ab-4cde-8f01-234567890abc',
  request: 'Independent question',
  model: 'selected-model',
  asSession: false,
  sourceBranchId: 'br_12345678',
  expectedGeneration: 3,
};
const receipt = {
  frameId: 'child-1',
  intentId: intent.intentId,
  inputMessageId: intent.intentId,
  sourceBranchId: intent.sourceBranchId,
  sourceGeneration: intent.expectedGeneration,
  throughPublicationSequence: 12,
  asSession: false,
  replayed: false,
};
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};

describe('aside action ownership', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner-1');
    transport.create.mockResolvedValue(receipt);
    transport.lookup.mockResolvedValue({ status: 'absent' });
    transport.close.mockResolvedValue({ status: 'cancelled-before-acceptance', intentId: intent.intentId });
    transport.cancel.mockResolvedValue(undefined);
    transport.branches.mockResolvedValue({ activeBranchId: 'br_12345678', generation: 3 });
    transport.session.mockResolvedValue({ delegation: false, autoReview: false, memory: false, targetAgent: 'OPERON' });
    transport.followup.mockImplementation(async (_frame, command) => ({
      msg_id: 'branch-continue:' + command.loadingId,
    }));
  });

  it('reconciles a lost acceptance without minting another identity or rebinding controls', async () => {
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    transport.lookup.mockResolvedValueOnce({ status: 'accepted', receipt: { ...receipt, replayed: true } });
    const operation = new XScienceAsideOperation(intent);
    expect(await operation.start()).toMatchObject({ frameId: 'child-1', intentId: intent.intentId });
    expect(transport.create).toHaveBeenCalledOnce();
    expect(transport.create.mock.calls[0][0]).toEqual(intent);
    expect(operation.intent).toEqual(intent);
    expect(readAsidePresentation('parent-1', false)?.receipt?.frameId).toBe('child-1');
  });

  it('retains unresolved admission and retries only the same frozen request', async () => {
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    const operation = new XScienceAsideOperation(intent);
    await expect(operation.start()).rejects.toThrow();
    expect(readAsidePresentation('parent-1', false)?.intent).toEqual(intent);
    await operation.start();
    expect(transport.create.mock.calls.map(([request]) => request)).toEqual([intent, intent]);
    expect(transport.lookup).toHaveBeenCalledTimes(2);
  });

  it('uses atomic cancellation for Close before acceptance and rejects a late create', async () => {
    const creating = deferred<typeof receipt>();
    transport.create.mockReturnValueOnce(creating.promise);
    const operation = new XScienceAsideOperation(intent);
    const started = operation.start();
    const observedStart = started.catch(() => null);
    await Promise.resolve();
    expect(await operation.close()).toEqual({ status: 'cancelled-before-acceptance', intentId: intent.intentId });
    expect(transport.close).toHaveBeenCalledWith(intent, undefined, expect.any(Object));
    creating.reject(new Error('ASIDE_INTENT_CANCELLED'));
    transport.lookup.mockResolvedValue({ status: 'cancelled-before-acceptance', intentId: intent.intentId });
    await observedStart;
    expect(operation.closed).toBe(true);
    expect(operation.receipt).toBeNull();
  });

  it('never treats an absent acceptance snapshot or aborted request as cancelled', async () => {
    transport.close.mockRejectedValueOnce(new DOMException('Aborted', 'AbortError'));
    const operation = new XScienceAsideOperation(intent);
    await expect(operation.close()).rejects.toThrow();
    expect(operation.closed).toBe(false);
    expect(readAsidePresentation('parent-1', false)?.closeRequested).toBe(true);
    expect(transport.cancel).not.toHaveBeenCalled();
  });

  it('cannot execute or write presentation state after an account ABA transition', async () => {
    const operation = new XScienceAsideOperation(intent);
    setRendererAccountOwner('owner-2');
    setRendererAccountOwner('owner-1');
    await expect(operation.start()).rejects.toThrow('aside_action_owner_changed');
    expect(transport.create).not.toHaveBeenCalled();
  });

  it('does not hydrate a persisted close request into new execution authority', async () => {
    const operation = new XScienceAsideOperation(intent);
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    await expect(operation.close()).rejects.toThrow();
    const presentation = readAsidePresentation('parent-1', false)!;
    const restored = new XScienceAsideOperation(presentation.intent, presentation);
    await restored.reconcile();
    expect(restored.closeRequested).toBe(true);
    expect(transport.create).not.toHaveBeenCalled();
    expect(transport.close).toHaveBeenCalledTimes(1);
  });

  it('uses the actual canonical accepted message ID for later exact cancellation', async () => {
    const operation = new XScienceAsideOperation(intent);
    await operation.start();
    expect(await operation.send('Followup question')).toBe(true);
    const command = transport.followup.mock.calls[0][1];
    expect(operation.inputMessageId).toBe('branch-continue:' + command.loadingId);
    await operation.cancel();
    expect(transport.cancel).toHaveBeenCalledWith('child-1', operation.inputMessageId, expect.any(Object));
    expect(transport.messageCancel).not.toHaveBeenCalled();
    expect(operation.presentation.pendingFollowup).toBeNull();
  });

  it.each(['cancelled-before-acceptance', 'cancelled', 'already-terminal'] as const)(
    'Close settles only its frozen pending mutation with %s proof and never cancels another input',
    async (status) => {
      transport.followup.mockRejectedValueOnce(new TypeError('response lost'));
      const operation = new XScienceAsideOperation(intent);
      await operation.start();
      await expect(operation.send('Followup question')).rejects.toThrow();
      const pending = operation.presentation.pendingFollowup!;
      const proof = {
        status,
        frameId: 'child-1',
        loadingId: pending.loadingId,
        requestSha256: 'a'.repeat(64),
        replayed: false,
        inputMessageId: status === 'cancelled-before-acceptance' ? null : 'branch-continue:' + pending.loadingId,
      };
      transport.messageCancel.mockResolvedValue(proof);
      expect(await operation.close()).toEqual(proof);
      expect(await operation.close()).toEqual(proof);
      expect(transport.messageCancel).toHaveBeenCalledOnce();
      expect(transport.messageCancel).toHaveBeenCalledWith('child-1', pending, expect.any(Object));
      expect(transport.followup).toHaveBeenCalledOnce();
      expect(transport.close).not.toHaveBeenCalled();
      expect(transport.cancel).not.toHaveBeenCalled();
      expect(operation.closed).toBe(true);
    }
  );

  it('retains pending body after lost DELETE and restores it without replaying POST', async () => {
    transport.followup.mockRejectedValueOnce(new TypeError('response lost'));
    transport.messageCancel.mockRejectedValueOnce(new TypeError('delete response lost'));
    const operation = new XScienceAsideOperation(intent);
    await operation.start();
    await expect(operation.send('Followup question')).rejects.toThrow();
    const pending = operation.presentation.pendingFollowup!;
    await expect(operation.close()).rejects.toThrow();
    expect(operation.closed).toBe(false);
    const hint = readAsidePresentation('parent-1', false)!;
    expect(hint.pendingFollowup).toEqual(pending);
    expect(hint.closeRequested).toBe(true);
    transport.lookup.mockResolvedValue({
      status: 'accepted',
      receipt,
      cancelledInputMessageId: 'branch-continue:' + pending.loadingId,
    });
    const restored = new XScienceAsideOperation(hint.intent, hint);
    await restored.reconcile();
    expect(restored.closed).toBe(false);
    transport.messageCancel.mockResolvedValue({
      status: 'cancelled',
      frameId: 'child-1',
      loadingId: pending.loadingId,
      requestSha256: 'a'.repeat(64),
      replayed: true,
      inputMessageId: 'branch-continue:' + pending.loadingId,
    });
    await restored.close();
    expect(transport.messageCancel.mock.calls.map(([, body]) => body)).toEqual([pending, pending]);
    expect(transport.followup).toHaveBeenCalledOnce();
    expect(transport.cancel).not.toHaveBeenCalled();
    expect(transport.close).not.toHaveBeenCalled();
  });

  it('does not let a delayed accepted POST response resurrect a closed panel or recovery hint', async () => {
    const delayed = deferred<{ msg_id: string }>();
    transport.followup.mockReturnValueOnce(delayed.promise);
    const operation = new XScienceAsideOperation(intent);
    await operation.start();
    const sent = operation.send('Followup question');
    await vi.waitFor(() => expect(transport.followup).toHaveBeenCalledOnce());
    const pending = operation.presentation.pendingFollowup!;
    expect(pending).not.toBeNull();
    const canonical = 'branch-continue:' + pending.loadingId;
    transport.messageCancel.mockResolvedValue({
      status: 'cancelled',
      frameId: 'child-1',
      loadingId: pending.loadingId,
      requestSha256: 'a'.repeat(64),
      replayed: false,
      inputMessageId: canonical,
    });
    await operation.close();
    delayed.resolve({ msg_id: canonical });
    expect(await sent).toBe(false);
    expect(operation.closed).toBe(true);
    expect(readAsidePresentation('parent-1', false)).toBeNull();
    expect(transport.close).not.toHaveBeenCalled();
  });

  it('rejects a changed immutable publication cutoff on replay instead of replacing the accepted prefix', async () => {
    const operation = new XScienceAsideOperation(intent);
    await operation.start();
    transport.lookup.mockResolvedValue({
      status: 'accepted',
      receipt: { ...receipt, throughPublicationSequence: 13, replayed: true },
    });
    await expect(operation.reconcile()).rejects.toThrow('aside_receipt_changed');
    expect(operation.receipt?.throughPublicationSequence).toBe(12);
  });
});

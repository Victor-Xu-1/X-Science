import { act, cleanup, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import React, { useRef } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { XScienceAsideIntent } from '@/renderer/services/xScienceAside';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { readAsidePresentation, readAsidePresentations } from '@/renderer/services/runtime/asidePresentation';
import type { ConversationCommandQueueItem } from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import { asideSourceSpecialist, asideChildSpecialist } from '../xscience/asideSpecialistFixtures';
import { Message } from '@arco-design/web-react';

const transport = vi.hoisted(() => ({
  capture: vi.fn(),
  create: vi.fn(),
  lookup: vi.fn(),
  close: vi.fn(),
  cancel: vi.fn(),
  followup: vi.fn(),
  messageCancel: vi.fn(),
  branches: vi.fn(),
  session: vi.fn(),
}));
const observer = vi.hoisted(() => ({
  entries: [],
  snapshot: null as unknown,
  loading: false,
  error: false,
  truncated: false,
  refresh: vi.fn(),
}));
vi.mock('@/renderer/services/xScienceAside', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceAside')>()),
  captureXScienceAsideIntent: transport.capture,
  createXScienceAside: transport.create,
  lookupXScienceAside: transport.lookup,
  closeXScienceAside: transport.close,
  cancelXScienceAsideInput: transport.cancel,
  sendXScienceAsideFollowup: transport.followup,
  cancelXScienceAsideMessageIntent: transport.messageCancel,
  loadXScienceAsideBranches: transport.branches,
}));
vi.mock('@/renderer/services/xScienceSessionOptions', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/xScienceSessionOptions')>()),
  loadXScienceSessionOptions: transport.session,
}));
vi.mock('@/renderer/components/chat/BtwOverlay/useAsideRuntimeView', () => ({ useAsideRuntimeView: () => observer }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: string }) => <p>{children}</p>,
}));

import { useBtwCommand } from '@/renderer/components/chat/BtwOverlay/useBtwCommand';
import BtwOverlay from '@/renderer/components/chat/BtwOverlay';
import {
  useAcpAsideController,
  type AsideQueueBridge,
} from '@/renderer/pages/conversation/platforms/acp/useAcpAsideController';

const receiptFor = (intent: XScienceAsideIntent) => ({
  frameId: intent.asSession ? 'branch-child' : 'aside-child',
  intentId: intent.intentId,
  inputMessageId: intent.intentId,
  sourceBranchId: intent.sourceBranchId,
  sourceGeneration: intent.expectedGeneration,
  throughPublicationSequence: 12,
  asSession: intent.asSession,
  replayed: false,
  ...(intent.sourceSpecialist
    ? {
        sourceSpecialist: intent.sourceSpecialist,
        specialistBinding: asideChildSpecialist(intent.asSession ? 'branch-child' : 'aside-child'),
      }
    : {}),
});
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
};
const item: ConversationCommandQueueItem = {
  id: 'queue-one',
  input: 'Independent queued question',
  files: [],
  contextItems: [],
  created_at: 1,
};

describe('Aside lifecycle and exact composer consumption', () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner-1');
    observer.snapshot = normalizeXScienceRuntimeSnapshot({ id: 'aside-child', status: 'completed' });
    transport.capture.mockImplementation(async (parentFrameId: string, request: string, options) =>
      Object.freeze({
        parentFrameId,
        request,
        intentId: options.intentId,
        model: options.model,
        asSession: options.asSession,
        sourceBranchId: 'br_12345678',
        expectedGeneration: 3,
        ...(options.sourceSpecialist ? { sourceSpecialist: options.sourceSpecialist } : {}),
      })
    );
    transport.create.mockImplementation(async (intent: XScienceAsideIntent) => receiptFor(intent));
    transport.lookup.mockResolvedValue({ status: 'absent' });
    transport.close.mockImplementation(async (intent: XScienceAsideIntent) => ({
      status: 'cancelled-before-acceptance',
      intentId: intent.intentId,
    }));
    transport.cancel.mockResolvedValue(undefined);
    transport.branches.mockResolvedValue({ activeBranchId: 'br_12345678', generation: 3 });
    transport.session.mockResolvedValue({ delegation: false, autoReview: false, memory: false, targetAgent: 'OPERON' });
  });
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    resetRendererAccountScopeForTest();
  });

  it('C404 atomic retains one typed source and nonce across lost ACK, reload and explicit original retry', async () => {
    const source = { reference: asideSourceSpecialist, targetAgent: 'FROZEN_AGENT' };
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      expect(await hook.result.current.ask('Frozen question', null, source)).toBe(false);
    });
    const frozen = readAsidePresentation('parent-1', false)!.intent;
    expect(frozen.sourceSpecialist).toEqual(source.reference);
    expect(transport.capture.mock.calls[0][2]).toMatchObject({ sourceSpecialist: source.reference });
    await act(async () => {
      expect(
        await hook.result.current.ask('Frozen question', null, { reference: { ...source.reference, revision: 5 } })
      ).toBe(false);
    });
    expect(transport.create).toHaveBeenCalledOnce();
    hook.unmount();
    const restored = renderHook(() => useBtwCommand('parent-1'));
    await waitFor(() => expect(restored.result.current.starting).toBe(false));
    await act(async () => {
      expect(await restored.result.current.retry()).toBe(true);
    });
    expect(transport.capture).toHaveBeenCalledOnce();
    expect(transport.create.mock.calls.map(([request]) => request)).toEqual([frozen, frozen]);
  });

  it('C404 atomic does not consume an identical queued input whose source changed during acceptance', async () => {
    const original = { ...item, specialistAuthority: { reference: asideSourceSpecialist } };
    let current = original;
    const creating = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(creating.promise);
    const remove = vi.fn(() => true);
    const bridge: AsideQueueBridge = {
      get items() {
        return [current];
      },
      remove,
      getQueuedItem: () => current,
    };
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge | null>(bridge);
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged: vi.fn(),
        navigate: vi.fn(),
      });
    });
    let sent!: Promise<boolean>;
    act(() => {
      sent = hook.result.current.openQueued(original);
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const frozen = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    expect(frozen.sourceSpecialist).toEqual(asideSourceSpecialist);
    current = { ...original, specialistAuthority: { reference: { ...asideSourceSpecialist, revision: 5 } } };
    await act(async () => {
      creating.resolve(receiptFor(frozen));
      expect(await sent).toBe(true);
    });
    expect(remove).not.toHaveBeenCalled();
    expect(hook.result.current.isQueueItemBlocked(current)).toBe(false);
  });

  it('C404 atomic keeps a same-text newer draft authority when its old source is accepted late', async () => {
    let authority = { reference: asideSourceSpecialist };
    const creating = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(creating.promise);
    const clear = vi.fn();
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge | null>(null);
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged: clear,
        captureDraftSpecialist: () => authority,
        navigate: vi.fn(),
      });
    });
    let sent!: Promise<boolean>;
    act(() => {
      sent = hook.result.current.sideDraft('Same draft', authority);
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const frozen = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    authority = { reference: { ...asideSourceSpecialist, revision: 5 } };
    await act(async () => {
      creating.resolve(receiptFor(frozen));
      expect(await sent).toBe(true);
    });
    expect(clear).not.toHaveBeenCalled();
  });

  it('C404 atomic captures the real two-argument btw facade without losing its raw consumer or hiding an unconfirmed source', async () => {
    const warning = vi.spyOn(Message, 'warning').mockImplementation(() => undefined as never);
    const capture = vi.fn(() => ({ reference: asideSourceSpecialist }));
    const creating = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(creating.promise);
    const clear = vi.fn();
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge | null>(null);
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged: clear,
        captureDraftSpecialist: capture,
        navigate: vi.fn(),
      });
    });
    let sent!: Promise<boolean>;
    act(() => {
      sent = hook.result.current.sideQuestion.ask('Question', {
        kind: 'draft',
        id: 'parent-1',
        content: '/btw Question',
      });
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const frozen = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    expect(frozen.sourceSpecialist).toEqual(asideSourceSpecialist);
    expect(readAsidePresentation('parent-1', false)?.consumer).toMatchObject({
      content: '/btw Question',
      sourceSpecialist: asideSourceSpecialist,
    });
    capture.mockImplementation(() => {
      throw new Error('unconfirmed selection');
    });
    await act(async () => {
      creating.resolve(receiptFor(frozen));
      expect(await sent).toBe(true);
    });
    expect(clear).not.toHaveBeenCalled();
    await act(async () => {
      expect(
        await hook.result.current.sideQuestion.ask('Next question', {
          kind: 'draft',
          id: 'parent-1',
          content: '/btw Next question',
        })
      ).toBe(false);
    });
    expect(warning).toHaveBeenCalledWith('conversation.xscienceRuntime.sessionOptions.specialistState.prepareFailed');
    expect(transport.create).toHaveBeenCalledOnce();
    warning.mockRestore();
  });

  it('binds source/model/request once, retains its child beyond 120 seconds and uses real Close', async () => {
    const hook = renderHook(({ model }) => useBtwCommand('parent-1', true, { ownerId: 'owner-1', modelId: model }), {
      initialProps: { model: 'model-one' },
    });
    await act(async () => {
      expect(await hook.result.current.ask('Independent question')).toBe(true);
    });
    const intent = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    hook.rerender({ model: 'model-two' });
    vi.useFakeTimers();
    await act(async () => {
      vi.advanceTimersByTime(120_001);
    });
    expect(hook.result.current.frameId).toBe('aside-child');
    expect(hook.result.current.receipt?.intentId).toBe(intent.intentId);
    expect(readAsidePresentation('parent-1', false)?.intent.model).toBe('model-one');
    transport.close.mockResolvedValue({
      status: 'cancelled',
      receipt: receiptFor(intent),
      inputMessageId: intent.intentId,
    });
    await act(async () => {
      await hook.result.current.close();
    });
    expect(transport.close).toHaveBeenCalledWith(intent, intent.intentId, expect.any(Object));
    expect(hook.result.current.isOpen).toBe(false);
    expect(hook.result.current.operation?.closed).toBe(true);
    expect(transport.create).toHaveBeenCalledOnce();
  });

  it('announces a rejected side-chat recovery write without admitting the task or clearing the draft', async () => {
    const clearDraftIfUnchanged = vi.fn();
    function Scenario() {
      const queue = useRef<AsideQueueBridge | null>(null);
      const controller = useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged,
        navigate: vi.fn(),
      });
      return (
        <>
          <button type='button' onClick={() => void controller.sideDraft('Original draft')}>
            Start side chat
          </button>
          <BtwOverlay controller={controller.sideQuestion} />
        </>
      );
    }
    render(<Scenario />);
    const originalSetItem = Storage.prototype.setItem;
    const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (
      this: Storage,
      key: string,
      value: string
    ) {
      if (key.startsWith('x-science-aside/v1/')) throw new DOMException('No storage left', 'QuotaExceededError');
      originalSetItem.call(this, key, value);
    });
    try {
      fireEvent.click(screen.getByRole('button', { name: 'Start side chat' }));
      await waitFor(() => expect(transport.capture).toHaveBeenCalledOnce());
      expect(await screen.findByRole('alert')).toBeVisible();
      expect(transport.create).not.toHaveBeenCalled();
      expect(transport.close).not.toHaveBeenCalled();
      expect(clearDraftIfUnchanged).not.toHaveBeenCalled();
      expect(readAsidePresentation('parent-1', false)).toBeNull();
    } finally {
      write.mockRestore();
    }
  });

  it('retains an unresolved request and retries the original UUID and body', async () => {
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      expect(await hook.result.current.ask('Independent question')).toBe(false);
    });
    expect(hook.result.current.errorKey).toBe('acceptanceUnknown');
    const frozen = readAsidePresentation('parent-1', false)!.intent;
    await act(async () => {
      expect(await hook.result.current.retry()).toBe(true);
    });
    expect(transport.capture).toHaveBeenCalledOnce();
    expect(transport.create.mock.calls.map(([intent]) => intent)).toEqual([frozen, frozen]);
    expect(hook.result.current.frameId).toBe('aside-child');
  });

  it('preserves pending Close through absence/offline instead of aborting or declaring cancellation', async () => {
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      await hook.result.current.ask('Independent question');
    });
    await act(async () => {
      await hook.result.current.close();
    });
    expect(hook.result.current.isOpen).toBe(false);
    expect(hook.result.current.closeRequested).toBe(true);
    expect(hook.result.current.errorKey).toBe('closeFailed');
    await act(async () => {
      await hook.result.current.reconnect();
    });
    expect(hook.result.current.errorKey).toBe('closePending');
    expect(hook.result.current.operation?.closed).toBe(false);
    expect(transport.create).toHaveBeenCalledOnce();
    expect(transport.cancel).not.toHaveBeenCalled();
  });

  it('keeps the panel dismissed and retries the exact old stop through a separate notice', async () => {
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    function Scenario() {
      const controller = useBtwCommand('parent-1');
      return (
        <>
          <button type='button' onClick={() => void controller.ask('Original question')}>
            Start side chat
          </button>
          <BtwOverlay controller={controller} />
        </>
      );
    }
    const original = render(<Scenario />);
    fireEvent.click(screen.getByRole('button', { name: 'Start side chat' }));
    await screen.findByRole('textbox');
    const intent = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    const receipt = receiptFor(intent);
    fireEvent.click(screen.getByRole('button', { name: 'conversation.sideQuestion.close' }));
    await screen.findByText('conversation.sideQuestion.closeFailed');
    expect(screen.queryByTestId('side-chat-panel')).not.toBeInTheDocument();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getByTestId('side-chat-close-recovery')).toBeVisible();
    original.unmount();

    const refreshing = deferred<{ status: 'accepted'; receipt: typeof receipt }>();
    transport.lookup.mockReturnValueOnce(refreshing.promise);
    render(<Scenario />);
    expect(screen.queryByTestId('side-chat-panel')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'conversation.sideQuestion.retryClose' })).toBeDisabled();
    await act(async () => refreshing.resolve({ status: 'accepted', receipt }));
    expect(screen.queryByTestId('side-chat-panel')).not.toBeInTheDocument();
    expect(transport.close).toHaveBeenCalledOnce();
    expect(transport.create).toHaveBeenCalledOnce();

    transport.close.mockResolvedValueOnce({ status: 'cancelled', receipt, inputMessageId: intent.intentId });
    fireEvent.click(screen.getByRole('button', { name: 'conversation.sideQuestion.retryClose' }));
    await waitFor(() => expect(screen.queryByTestId('side-chat-close-recovery')).not.toBeInTheDocument());
    expect(transport.close.mock.calls).toEqual([
      [intent, intent.intentId, expect.any(Object)],
      [intent, intent.intentId, expect.any(Object)],
    ]);
    expect(transport.create).toHaveBeenCalledOnce();
  });

  it('closes an in-progress capture once and never starts the dismissed intent', async () => {
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      await hook.result.current.ask('Previous question');
      await hook.result.current.close();
    });
    const capturing = deferred<XScienceAsideIntent>();
    transport.capture.mockReturnValueOnce(capturing.promise);
    let asking!: Promise<boolean>;
    let closing!: Promise<void>;
    act(() => {
      asking = hook.result.current.ask('Dismissed before capture');
      closing = hook.result.current.close();
    });
    const [parentFrameId, request, options] = transport.capture.mock.calls[1];
    const intent: XScienceAsideIntent = {
      parentFrameId,
      request,
      intentId: options.intentId,
      asSession: false,
      model: null,
      sourceBranchId: 'br_12345678',
      expectedGeneration: 3,
    };
    await act(async () => {
      capturing.resolve(intent);
      expect(await asking).toBe(false);
      await closing;
    });
    expect(hook.result.current.isOpen).toBe(false);
    expect(hook.result.current.operation?.closed).toBe(true);
    expect(transport.create).toHaveBeenCalledOnce();
    expect(transport.close).toHaveBeenCalledTimes(2);
    expect(transport.close).toHaveBeenLastCalledWith(intent, undefined, expect.any(Object));
  });

  it('starts a fresh same-parent side chat while the old exact close is in flight', async () => {
    transport.create.mockImplementation(async (intent: XScienceAsideIntent) => ({
      ...receiptFor(intent),
      frameId: 'aside-' + intent.intentId,
    }));
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => expect(await hook.result.current.ask('Original question')).toBe(true));
    const original = hook.result.current.getReceipt()!;
    const oldClose = deferred<{ status: 'cancelled'; receipt: typeof original; inputMessageId: string }>();
    transport.close.mockReturnValueOnce(oldClose.promise);
    let closing!: Promise<void>;
    act(() => {
      closing = hook.result.current.close();
    });
    expect(hook.result.current.isOpen).toBe(false);
    await act(async () => expect(await hook.result.current.ask('New question')).toBe(true));
    const next = hook.result.current.getReceipt()!;
    expect(next.intentId).not.toBe(original.intentId);
    expect(next.frameId).not.toBe(original.frameId);
    expect(hook.result.current.isOpen).toBe(true);
    await act(async () => hook.result.current.retryClose(original.intentId));
    expect(transport.close).toHaveBeenCalledOnce();
    await act(async () => {
      oldClose.resolve({ status: 'cancelled', receipt: original, inputMessageId: original.inputMessageId });
      await closing;
    });
    expect(hook.result.current.getReceipt()).toBe(next);
    expect(hook.result.current.isOpen).toBe(true);
    expect(transport.close).toHaveBeenCalledOnce();
    expect(transport.close.mock.calls[0][0].intentId).toBe(original.intentId);
    expect(readAsidePresentation('parent-1', false)?.intent.intentId).toBe(next.intentId);
  });

  it('upgrades a v1 close hint and restores current plus retained intent using GET only', async () => {
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    const first = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => expect(await first.result.current.ask('Original question')).toBe(true));
    await act(async () => first.result.current.close());
    const oldHint = readAsidePresentation('parent-1', false)!;
    first.unmount();
    localStorage.setItem('x-science-aside/v1/owner-1/parent-1/side-chat', JSON.stringify(oldHint));
    const second = renderHook(() => useBtwCommand('parent-1'));
    await waitFor(() => expect(second.result.current.starting).toBe(false));
    await act(async () => expect(await second.result.current.ask('New question')).toBe(true));
    const next = second.result.current.getReceipt()!;
    const archived = readAsidePresentations('parent-1', false);
    expect(archived.current?.intent.intentId).toBe(next.intentId);
    expect(archived.closing.map(({ intent }) => intent.intentId)).toEqual([oldHint.intent.intentId]);
    second.unmount();
    transport.lookup.mockImplementation(async (intent: XScienceAsideIntent) => ({
      status: 'accepted',
      receipt: receiptFor(intent),
    }));
    transport.lookup.mockClear();
    const third = renderHook(() => useBtwCommand('parent-1'));
    await waitFor(() => expect(third.result.current.starting).toBe(false));
    expect(third.result.current.getReceipt()?.intentId).toBe(next.intentId);
    expect(third.result.current.isOpen).toBe(true);
    expect(third.result.current.closingRecoveries.map(({ intentId }) => intentId)).toEqual([oldHint.intent.intentId]);
    expect(transport.lookup.mock.calls.map(([intent]) => intent.intentId).toSorted()).toEqual(
      [oldHint.intent.intentId, next.intentId].toSorted()
    );
    expect(transport.create).toHaveBeenCalledTimes(2);
    expect(transport.close).toHaveBeenCalledOnce();
    transport.close.mockResolvedValueOnce({
      status: 'cancelled',
      receipt: receiptFor(oldHint.intent),
      inputMessageId: oldHint.intent.intentId,
    });
    await act(async () => third.result.current.retryClose(oldHint.intent.intentId));
    expect(transport.close.mock.calls.map(([intent]) => intent.intentId)).toEqual([
      oldHint.intent.intentId,
      oldHint.intent.intentId,
    ]);
    expect(third.result.current.getReceipt()?.intentId).toBe(next.intentId);
    expect(readAsidePresentations('parent-1', false).closing).toEqual([]);
  });

  it('refuses a new admission at capacity without evicting any exact cleanup hints', async () => {
    transport.close.mockRejectedValue(new TypeError('offline'));
    const hook = renderHook(() => useBtwCommand('parent-1'));
    for (let index = 1; index <= 5; index += 1) {
      await act(async () => expect(await hook.result.current.ask('Question ' + index)).toBe(true));
      await act(async () => hook.result.current.close());
    }
    const previous = readAsidePresentations('parent-1', false);
    expect(previous.closing).toHaveLength(4);
    expect(previous.current?.intent.request).toBe('Question 5');
    await act(async () => expect(await hook.result.current.ask('No room')).toBe(false));
    expect(readAsidePresentations('parent-1', false)).toEqual(previous);
    expect(transport.create).toHaveBeenCalledTimes(5);
    expect(hook.result.current.closingRecoveries).toHaveLength(5);
    const oldest = previous.closing[0];
    transport.close.mockResolvedValueOnce({
      status: 'cancelled',
      receipt: receiptFor(oldest.intent),
      inputMessageId: oldest.intent.intentId,
    });
    await act(async () => hook.result.current.retryClose(oldest.intent.intentId));
    await act(async () => expect(await hook.result.current.ask('Now has room')).toBe(true));
    expect(transport.create).toHaveBeenCalledTimes(6);
    expect(readAsidePresentations('parent-1', false).closing).toHaveLength(4);
  });

  it('retains the old transferred queue reservation and consumes only each exact accepted item', async () => {
    const firstAck = deferred<ReturnType<typeof receiptFor>>();
    const closeAck = deferred<{
      status: 'cancelled';
      receipt: ReturnType<typeof receiptFor>;
      inputMessageId: string;
    }>();
    transport.create.mockReturnValueOnce(firstAck.promise);
    transport.close.mockReturnValueOnce(closeAck.promise);
    const nextItem = { ...item, id: 'queue-two', input: 'New independent question' };
    const items = new Map([item, nextItem].map((value) => [value.id, value]));
    const remove = vi.fn((id: string) => items.delete(id));
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge>({
        items: [item, nextItem],
        getQueuedItem: (id) => items.get(id) ?? null,
        remove,
      });
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged: vi.fn(),
        navigate: vi.fn(),
      });
    });
    let accepting!: Promise<boolean>;
    act(() => {
      accepting = hook.result.current.openQueued(item);
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const oldIntent = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    let closing!: Promise<void>;
    act(() => {
      closing = hook.result.current.sideQuestion.close();
    });
    await act(async () => expect(await hook.result.current.openQueued(nextItem)).toBe(true));
    expect(remove.mock.calls).toEqual([[nextItem.id]]);
    expect(hook.result.current.isQueueItemBlocked(item)).toBe(true);
    await act(async () => expect(await hook.result.current.openQueued(item)).toBe(false));
    await act(async () => {
      firstAck.resolve(receiptFor(oldIntent));
      expect(await accepting).toBe(false);
    });
    expect(remove.mock.calls).toEqual([[nextItem.id], [item.id]]);
    await act(async () => {
      closeAck.resolve({ status: 'cancelled', receipt: receiptFor(oldIntent), inputMessageId: oldIntent.intentId });
      await closing;
    });
    expect(remove.mock.calls).toEqual([[nextItem.id], [item.id]]);
    expect(hook.result.current.sideQuestion.isOpen).toBe(true);
    expect(hook.result.current.sideQuestion.getReceipt()?.intentId).not.toBe(oldIntent.intentId);
  });

  it('does not clear an identical current draft on late acceptance of a dismissed side chat', async () => {
    const firstAck = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(firstAck.promise);
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    const clearDraftIfUnchanged = vi.fn();
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge | null>(null);
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged,
        navigate: vi.fn(),
      });
    });
    let first!: Promise<boolean>;
    act(() => {
      first = hook.result.current.sideDraft('Same text');
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const old = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    await act(async () => hook.result.current.sideQuestion.close());
    await act(async () => expect(await hook.result.current.sideQuestion.ask('Same text')).toBe(true));
    await act(async () => {
      firstAck.resolve(receiptFor(old));
      expect(await first).toBe(false);
    });
    expect(clearDraftIfUnchanged).not.toHaveBeenCalled();
    expect(readAsidePresentations('parent-1', false).closing[0].consumer).toBeNull();
    expect(hook.result.current.sideQuestion.getReceipt()?.intentId).not.toBe(old.intentId);
  });

  it('allows a fresh exact queue transfer before the confirmed cancelled POST settles', async () => {
    const firstAck = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(firstAck.promise);
    let queued = true;
    const remove = vi.fn(() => {
      queued = false;
      return true;
    });
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge>({
        items: [item],
        getQueuedItem: (id) => (queued && id === item.id ? item : null),
        remove,
      });
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged: vi.fn(),
        navigate: vi.fn(),
      });
    });
    let oldPost!: Promise<boolean>;
    act(() => {
      oldPost = hook.result.current.openQueued(item);
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const old = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    await act(async () => hook.result.current.sideQuestion.close());
    await act(async () => expect(await hook.result.current.openQueued(item)).toBe(true));
    const next = hook.result.current.sideQuestion.getReceipt()!;
    expect(next.intentId).not.toBe(old.intentId);
    expect(remove).toHaveBeenCalledExactlyOnceWith(item.id);
    await act(async () => {
      firstAck.resolve(receiptFor(old));
      expect(await oldPost).toBe(false);
    });
    expect(transport.lookup).not.toHaveBeenCalled();
    expect(remove).toHaveBeenCalledExactlyOnceWith(item.id);
    expect(hook.result.current.sideQuestion.getReceipt()).toBe(next);
    expect(hook.result.current.sideQuestion.isOpen).toBe(true);
    expect(readAsidePresentation('parent-1', false)?.intent.intentId).toBe(next.intentId);
  });

  it('releases the Main queue reservation after exact pre-admission cancellation before old POST returns', async () => {
    const firstAck = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(firstAck.promise);
    const remove = vi.fn();
    const hook = renderHook(() => {
      const queue = useRef<AsideQueueBridge>({
        items: [item],
        getQueuedItem: (id) => (id === item.id ? item : null),
        remove,
      });
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: null,
        queue,
        clearDraftIfUnchanged: vi.fn(),
        navigate: vi.fn(),
      });
    });
    let oldPost!: Promise<boolean>;
    act(() => {
      oldPost = hook.result.current.openQueued(item);
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    expect(hook.result.current.isQueueItemBlocked(item)).toBe(true);
    try {
      await act(async () => hook.result.current.sideQuestion.close());
      expect(hook.result.current.sideQuestion.operation?.closed).toBe(true);
      expect(readAsidePresentation('parent-1', false)).toBeNull();
      expect(transport.close).toHaveBeenCalledOnce();
      expect(remove).not.toHaveBeenCalled();
      expect(hook.result.current.isQueueItemBlocked(item)).toBe(false);
    } finally {
      await act(async () => {
        firstAck.reject(new TypeError('old POST transport settled'));
        await oldPost;
      });
    }
  });

  it('rejects a late old retry action after a new side chat starts', async () => {
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      await hook.result.current.ask('Original question');
      await hook.result.current.close();
    });
    const original = hook.result.current.getReceipt()!;
    const oldRetry = hook.result.current.retryClose;
    transport.close.mockResolvedValueOnce({
      status: 'cancelled',
      receipt: original,
      inputMessageId: original.inputMessageId,
    });
    await act(async () => await oldRetry());
    await act(async () => expect(await hook.result.current.ask('New question')).toBe(true));
    const next = hook.result.current.getReceipt();
    expect(next?.intentId).not.toBe(original.intentId);
    await act(async () => await oldRetry());
    expect(hook.result.current.getReceipt()).toBe(next);
    expect(hook.result.current.isOpen).toBe(true);
    expect(transport.close).toHaveBeenCalledTimes(2);
  });

  it('recovers two exact closing intents after account switch without admitting or stopping on reload', async () => {
    type Proof = { status: 'cancelled'; receipt: ReturnType<typeof receiptFor>; inputMessageId: string };
    const firstClose = deferred<Proof>();
    const secondClose = deferred<Proof>();
    transport.close.mockReturnValueOnce(firstClose.promise).mockReturnValueOnce(secondClose.promise);
    const hook = renderHook(({ ownerId }) => useBtwCommand('parent-1', true, { ownerId }), {
      initialProps: { ownerId: 'owner-1' },
    });
    await act(async () => expect(await hook.result.current.ask('First question')).toBe(true));
    const first = hook.result.current.getReceipt()!;
    let firstDone!: Promise<void>;
    act(() => {
      firstDone = hook.result.current.close();
    });
    await act(async () => expect(await hook.result.current.ask('Second question')).toBe(true));
    const second = hook.result.current.getReceipt()!;
    let secondDone!: Promise<void>;
    act(() => {
      secondDone = hook.result.current.close();
    });
    await act(async () => expect(await hook.result.current.ask('Third question')).toBe(true));
    const third = hook.result.current.getReceipt()!;
    expect(readAsidePresentations('parent-1', false).closing.map(({ intent }) => intent.intentId)).toEqual([
      first.intentId,
      second.intentId,
    ]);
    act(() => {
      setRendererAccountOwner('owner-2');
      hook.rerender({ ownerId: 'owner-2' });
    });
    expect(hook.result.current.closingRecoveries).toEqual([]);
    await act(async () => expect(await hook.result.current.ask('Other account')).toBe(true));
    const other = hook.result.current.getReceipt()!;
    await act(async () => {
      secondClose.resolve({ status: 'cancelled', receipt: second, inputMessageId: second.inputMessageId });
      firstClose.resolve({ status: 'cancelled', receipt: first, inputMessageId: first.inputMessageId });
      await Promise.all([firstDone, secondDone]);
    });
    expect(hook.result.current.getReceipt()).toBe(other);
    expect(readAsidePresentations('parent-1', false).closing).toEqual([]);
    const cancelled = new Set([first.intentId, second.intentId]);
    transport.lookup.mockImplementation(async (intent: XScienceAsideIntent) => ({
      status: 'accepted',
      receipt: receiptFor(intent),
      cancelledInputMessageId: cancelled.has(intent.intentId) ? intent.intentId : null,
    }));
    act(() => {
      setRendererAccountOwner('owner-1');
      hook.rerender({ ownerId: 'owner-1' });
    });
    await waitFor(() => expect(hook.result.current.getReceipt()?.intentId).toBe(third.intentId));
    await waitFor(() => expect(hook.result.current.closingRecoveries).toEqual([]));
    expect(hook.result.current.isOpen).toBe(true);
    expect(readAsidePresentations('parent-1', false).closing).toEqual([]);
    expect(transport.create).toHaveBeenCalledTimes(4);
    expect(transport.close).toHaveBeenCalledTimes(2);
    expect(transport.lookup.mock.calls.map(([intent]) => intent.intentId).toSorted()).toEqual(
      [first.intentId, second.intentId, third.intentId].toSorted()
    );
  });

  it('does not apply a late accepted result to another conversation or consume its draft', async () => {
    const creating = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(creating.promise);
    const hook = renderHook(({ parent }) => useBtwCommand(parent), { initialProps: { parent: 'parent-1' } });
    let started!: Promise<boolean>;
    act(() => {
      started = hook.result.current.ask('Original question');
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    const intent = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    hook.rerender({ parent: 'parent-2' });
    await act(async () => {
      creating.resolve(receiptFor(intent));
      expect(await started).toBe(false);
    });
    expect(hook.result.current.frameId).toBeNull();
    expect(hook.result.current.isOpen).toBe(false);
    expect(readAsidePresentation('parent-1', false)?.intent.intentId).toBe(intent.intentId);
  });

  it('restores close hints with GET only, without silently restarting or cancelling a task', async () => {
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    transport.close.mockRejectedValueOnce(new TypeError('offline'));
    const first = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      await first.result.current.ask('Original question');
      await first.result.current.close();
    });
    first.unmount();
    const second = renderHook(() => useBtwCommand('parent-1'));
    await waitFor(() => expect(second.result.current.starting).toBe(false));
    expect(second.result.current.closeRequested).toBe(true);
    expect(second.result.current.isOpen).toBe(false);
    expect(second.result.current.frameId).toBeNull();
    expect(transport.create).toHaveBeenCalledOnce();
    expect(transport.close).toHaveBeenCalledOnce();
    expect(transport.lookup).toHaveBeenCalledTimes(2);
  });

  it.each(['cancelled-before-acceptance', 'cancelled', 'already-terminal'] as const)(
    'native panel Close uses only the frozen pending mutation with %s proof',
    async (status) => {
      transport.followup.mockRejectedValueOnce(new TypeError('response lost'));
      transport.messageCancel.mockImplementation(async (frameId: string, command) => ({
        frameId,
        loadingId: command.loadingId,
        requestSha256: 'a'.repeat(64),
        replayed: false,
        status,
        inputMessageId: status === 'cancelled-before-acceptance' ? null : 'branch-continue:' + command.loadingId,
      }));
      function Scenario() {
        const controller = useBtwCommand('parent-1');
        return (
          <>
            <button type='button' onClick={() => void controller.ask('Original question')}>
              Start side chat
            </button>
            <BtwOverlay controller={controller} />
          </>
        );
      }
      render(<Scenario />);
      fireEvent.click(screen.getByRole('button', { name: 'Start side chat' }));
      await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'conversation.sideQuestion.sendFollowup' })).toBeInTheDocument()
      );
      fireEvent.change(screen.getByRole('textbox'), { target: { value: 'Followup question' } });
      fireEvent.click(screen.getByRole('button', { name: 'conversation.sideQuestion.sendFollowup' }));
      await waitFor(() =>
        expect(screen.getByRole('button', { name: 'conversation.sideQuestion.retryMessage' })).toBeInTheDocument()
      );
      expect(screen.getByRole('button', { name: 'conversation.sideQuestion.cancelPendingMessage' })).toBeVisible();
      fireEvent.click(screen.getByRole('button', { name: 'conversation.sideQuestion.close' }));
      await waitFor(() => expect(screen.queryByTestId('side-chat-panel')).not.toBeInTheDocument());
      await waitFor(() => expect(readAsidePresentation('parent-1', false)).toBeNull());
      expect(transport.messageCancel).toHaveBeenCalledOnce();
      expect(transport.messageCancel.mock.calls[0][1]).toEqual(transport.followup.mock.calls[0][1]);
      expect(transport.close).not.toHaveBeenCalled();
      expect(transport.cancel).not.toHaveBeenCalled();
      expect(transport.followup).toHaveBeenCalledOnce();
    }
  );

  it('does not let a retired operation reconnect failure overwrite a reopened same-parent panel', async () => {
    const hook = renderHook(() => useBtwCommand('parent-1'));
    await act(async () => {
      await hook.result.current.ask('Original question');
    });
    const original = hook.result.current.getReceipt()!;
    const lookup = deferred<{ status: 'absent' }>();
    transport.lookup.mockReturnValueOnce(lookup.promise);
    let reconnecting!: Promise<void>;
    act(() => {
      reconnecting = hook.result.current.reconnect();
    });
    await waitFor(() => expect(transport.lookup).toHaveBeenCalledOnce());
    transport.close.mockResolvedValueOnce({
      status: 'cancelled',
      receipt: original,
      inputMessageId: original.inputMessageId,
    });
    await act(async () => {
      await hook.result.current.close();
    });
    transport.create.mockImplementationOnce(async (intent: XScienceAsideIntent) => ({
      ...receiptFor(intent),
      frameId: 'second-child',
    }));
    await act(async () => {
      expect(await hook.result.current.ask('A new independent question')).toBe(true);
    });
    await act(async () => {
      lookup.reject(new TypeError('retired read failed'));
      await reconnecting;
    });
    expect(hook.result.current.frameId).toBe('second-child');
    expect(hook.result.current.errorKey).toBeNull();
    expect(hook.result.current.isOpen).toBe(true);
  });

  const composition = (bridge: AsideQueueBridge, clear: (text: string) => void, navigate = vi.fn()) =>
    renderHook(() => {
      const queue = useRef<AsideQueueBridge | null>(bridge);
      return useAcpAsideController({
        conversationId: 'parent-1',
        ownerId: 'owner-1',
        enabled: true,
        modelId: 'model-one',
        queue,
        clearDraftIfUnchanged: clear,
        navigate,
      });
    });

  it('reserves only the original queue item while acceptance is unknown and consumes it once on proof', async () => {
    let items = [item, { ...item, id: 'queue-two', input: 'Other queued question' }];
    const remove = vi.fn((id: string) => {
      items = items.filter((queued) => queued.id !== id);
      return true;
    });
    const bridge = {
      get items() {
        return items;
      },
      remove,
      getQueuedItem: (id: string) => items.find((queued) => queued.id === id) ?? null,
    };
    transport.create.mockRejectedValueOnce(new TypeError('response lost'));
    const hook = composition(bridge, vi.fn());
    await act(async () => {
      expect(await hook.result.current.openQueued(item)).toBe(false);
    });
    expect(remove).not.toHaveBeenCalled();
    expect(items).toHaveLength(2);
    expect(hook.result.current.isQueueItemBlocked(item)).toBe(true);
    expect(hook.result.current.isQueueItemBlocked(items[1])).toBe(false);
    await act(async () => {
      expect(await hook.result.current.sideQuestion.retry()).toBe(true);
    });
    expect(remove).toHaveBeenCalledOnce();
    expect(remove).toHaveBeenCalledWith(item.id);
    expect(items).toEqual([expect.objectContaining({ id: 'queue-two' })]);
    expect(hook.result.current.isQueueItemBlocked(item)).toBe(false);
    await act(async () => {
      await hook.result.current.sideQuestion.reconnect();
    });
    expect(remove).toHaveBeenCalledOnce();
  });

  it('cannot transfer an already-dequeued Main item using stale menu props', async () => {
    const bridge: AsideQueueBridge = { items: [], remove: vi.fn(() => true), getQueuedItem: () => null };
    const hook = composition(bridge, vi.fn());
    await act(async () => {
      expect(await hook.result.current.openQueued(item)).toBe(false);
    });
    expect(transport.create).not.toHaveBeenCalled();
    expect(bridge.remove).not.toHaveBeenCalled();
  });

  it('preserves a changed queue item and a newer draft instead of blindly deleting them on acceptance', async () => {
    const creating = deferred<ReturnType<typeof receiptFor>>();
    transport.create.mockReturnValueOnce(creating.promise);
    let draft = 'Original draft';
    const clear = vi.fn((original: string) => {
      if (draft === original) draft = '';
    });
    const bridge: AsideQueueBridge = { items: [], remove: vi.fn(() => true), getQueuedItem: () => null };
    const hook = composition(bridge, clear);
    let sent!: Promise<boolean>;
    act(() => {
      sent = hook.result.current.sideDraft(draft);
    });
    await waitFor(() => expect(transport.create).toHaveBeenCalledOnce());
    draft = 'A new draft typed while admission was pending';
    const intent = transport.create.mock.calls[0][0] as XScienceAsideIntent;
    await act(async () => {
      creating.resolve(receiptFor(intent));
      expect(await sent).toBe(true);
    });
    expect(clear).toHaveBeenCalledOnce();
    expect(clear).toHaveBeenCalledWith('Original draft');
    expect(draft).toBe('A new draft typed while admission was pending');
  });
});

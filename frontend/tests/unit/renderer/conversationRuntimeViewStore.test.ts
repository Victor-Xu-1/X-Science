import { beforeEach, describe, expect, it } from 'vitest';
import type { TConversationRuntimeSummary } from '@/common/config/storage';
import {
  getConversationRuntimeViewSnapshot,
  hydrateSucceeded,
  localSendAccepted,
  localSendStarted,
  isLocalSendPreparing,
  localSendDispatched,
  cancelLocalSendPreparation,
  localStopAcknowledged,
  localStopRequested,
  resetConversationRuntimeViewStoreForTest,
  resetLocalGate,
  turnCompleted,
} from '@/renderer/pages/conversation/runtime/conversationRuntimeViewStore';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

const idleRuntime = (): TConversationRuntimeSummary => ({
  state: 'idle',
  can_send_message: true,
  has_task: true,
  task_status: 'finished',
  is_processing: false,
  pending_confirmations: 0,
  turn_id: null,
});

const runningRuntime = (turn_id: string): TConversationRuntimeSummary => ({
  state: 'running',
  can_send_message: false,
  has_task: true,
  task_status: 'running',
  is_processing: true,
  pending_confirmations: 0,
  turn_id,
});

const cancellingRuntime = (turn_id: string): TConversationRuntimeSummary => ({
  state: 'cancelling',
  can_send_message: true,
  has_task: true,
  task_status: 'running',
  is_processing: false,
  pending_confirmations: 0,
  turn_id,
});

describe('conversationRuntimeViewStore turn id contract', () => {
  beforeEach(() => {
    resetConversationRuntimeViewStoreForTest();
    resetRendererAccountScopeForTest();
  });

  it('pre-dispatch Stop cancels one exact ticket idempotently and cannot withdraw a newer or dispatched ticket', () => {
    hydrateSucceeded('preparation-frame', idleRuntime());
    const old = {};
    localSendStarted('preparation-frame', old, 'preparing');
    expect(isLocalSendPreparing('preparation-frame', old)).toBe(true);
    expect(cancelLocalSendPreparation('preparation-frame', old).cancelled).toBe(true);
    expect(cancelLocalSendPreparation('preparation-frame', old)).toEqual({ cancelled: true, logs: [] });
    expect(localSendDispatched('preparation-frame', old)).toBe(false);
    expect(
      localSendAccepted('preparation-frame', 'old-turn', runningRuntime('old-turn'), undefined, { ticket: old })
    ).toEqual([]);
    hydrateSucceeded('preparation-frame', runningRuntime('admitted-other-client'));
    expect(cancelLocalSendPreparation('preparation-frame').cancelled).toBe(false);
    expect(
      localSendAccepted('preparation-frame', 'old-turn', runningRuntime('old-turn'), undefined, { ticket: old })
    ).toEqual([]);
    const next = {};
    localSendStarted('preparation-frame', next, 'preparing');
    expect(cancelLocalSendPreparation('preparation-frame', old).cancelled).toBe(false);
    expect(isLocalSendPreparing('preparation-frame', next)).toBe(true);
    expect(localSendDispatched('preparation-frame', next)).toBe(true);
    expect(localSendDispatched('preparation-frame', next)).toBe(false);
    expect(cancelLocalSendPreparation('preparation-frame', next).cancelled).toBe(false);
    expect(
      localSendAccepted('preparation-frame', 'next-turn', runningRuntime('next-turn'), undefined, { ticket: next })
    ).not.toEqual([]);
    expect(getConversationRuntimeViewSnapshot('preparation-frame')).toMatchObject({
      state: 'running',
      activeTurnId: 'next-turn',
    });
    const legacy = {};
    localSendStarted('legacy-transport-frame', legacy);
    expect(cancelLocalSendPreparation('legacy-transport-frame', legacy).cancelled).toBe(false);
  });

  it('pre-dispatch Stop yields a cancelled marker to canonical waiting approval without accepting old callbacks', () => {
    const ticket = {};
    localSendStarted('approval-frame', ticket, 'preparing');
    expect(cancelLocalSendPreparation('approval-frame', ticket).cancelled).toBe(true);
    // Same projection emitted by webConversationRuntimeWithActions for an admitted plan approval.
    hydrateSucceeded('approval-frame', {
      state: 'waiting_approval',
      can_send_message: true,
      has_task: true,
      task_status: 'pending',
      is_processing: false,
      pending_confirmations: 1,
      turn_id: 'approval-frame',
    });
    expect(cancelLocalSendPreparation('approval-frame').cancelled).toBe(false);
    expect(localSendAccepted('approval-frame', 'old-turn', runningRuntime('old-turn'), undefined, { ticket })).toEqual(
      []
    );
    expect(getConversationRuntimeViewSnapshot('approval-frame')).toMatchObject({
      state: 'waiting_approval',
      activeTurnId: 'approval-frame',
    });
  });

  it('clears runtime projection state when the authenticated owner changes', () => {
    setRendererAccountOwner('owner-a');
    hydrateSucceeded('conv-shared', runningRuntime('turn-owner-a'));
    expect(getConversationRuntimeViewSnapshot('conv-shared')).toMatchObject({ state: 'running' });

    setRendererAccountOwner('owner-b');
    expect(getConversationRuntimeViewSnapshot('conv-shared')).toMatchObject({
      state: 'idle',
      hydrated: false,
      activeTurnId: null,
    });
  });

  it('keeps idle when turn.completed arrives before local send accepted', () => {
    localSendStarted('conv-1');
    turnCompleted('conv-1', 'turn-1', idleRuntime());
    localSendAccepted('conv-1', 'turn-1', runningRuntime('turn-1'), 'msg-1');

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.state).toBe('idle');
    expect(view.isProcessing).toBe(false);
    expect(view.canSendMessage).toBe(true);
    expect(view.localSubmitting).toBe(false);
    expect(view.activeTurnId).toBeNull();
  });

  it('keeps idle when a stale running hydrate arrives after turn.completed', () => {
    localSendStarted('conv-1');
    localSendAccepted('conv-1', 'turn-1', runningRuntime('turn-1'), 'msg-1');
    turnCompleted('conv-1', 'turn-1', idleRuntime());
    const logs = hydrateSucceeded('conv-1', runningRuntime('turn-1'));

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.state).toBe('idle');
    expect(view.isProcessing).toBe(false);
    expect(view.canSendMessage).toBe(true);
    expect(view.activeTurnId).toBeNull();
    expect(logs[0]).toMatchObject({
      event: 'runtime_hydrated',
      data: {
        stale_after_completed: true,
        source: 'hydrate',
      },
    });
  });

  it('keeps a late send acknowledgement stale after a terminal runtime invalidation and permits the next send', () => {
    localSendStarted('conv-1');
    hydrateSucceeded('conv-1', idleRuntime(), {
      releasePendingLocalSend: true,
      completedTurnId: 'conv-1',
    });
    localSendAccepted('conv-1', 'conv-1', runningRuntime('conv-1'), 'msg-stale');

    expect(getConversationRuntimeViewSnapshot('conv-1')).toMatchObject({
      state: 'idle',
      isProcessing: false,
      canSendMessage: true,
      activeTurnId: null,
    });

    localSendStarted('conv-1');
    localSendAccepted('conv-1', 'conv-1', runningRuntime('conv-1'), 'msg-next');
    expect(getConversationRuntimeViewSnapshot('conv-1')).toMatchObject({
      state: 'running',
      isProcessing: true,
      canSendMessage: false,
      activeTurnId: 'conv-1',
    });
  });

  it('keeps idle when a stale stop acknowledgement arrives after turn.completed', () => {
    hydrateSucceeded('conv-1', runningRuntime('turn-1'));
    localStopRequested('conv-1', 'turn-1');
    turnCompleted('conv-1', 'turn-1', idleRuntime());
    const logs = localStopAcknowledged('conv-1', 'turn-1', runningRuntime('turn-1'));

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.state).toBe('idle');
    expect(view.isProcessing).toBe(false);
    expect(view.canSendMessage).toBe(true);
    expect(view.localStopping).toBe(false);
    expect(view.activeTurnId).toBeNull();
    expect(logs[0]).toMatchObject({
      event: 'local_stop_acknowledged',
      data: {
        stale_after_completed: true,
        source: 'stop_response',
      },
    });
  });

  it('uses send response runtime summary as authoritative active turn', () => {
    localSendStarted('conv-1');
    localSendAccepted('conv-1', 'turn-1', runningRuntime('turn-1'), 'msg-1');

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.state).toBe('running');
    expect(view.isProcessing).toBe(true);
    expect(view.canSendMessage).toBe(false);
    expect(view.localSubmitting).toBe(false);
    expect(view.activeTurnId).toBe('turn-1');
  });

  it('clears failed local stop metadata while preserving the authoritative running state', () => {
    hydrateSucceeded('conv-1', runningRuntime('turn-1'));
    localStopRequested('conv-1', 'turn-1');
    resetLocalGate('conv-1', 'stop_failed');
    hydrateSucceeded('conv-1', runningRuntime('turn-1'));

    expect(getConversationRuntimeViewSnapshot('conv-1')).toMatchObject({
      state: 'running',
      isProcessing: true,
      canSendMessage: false,
      localStopping: false,
      activeTurnId: 'turn-1',
    });
  });

  it('keeps cancelling runtime gated from new sends', () => {
    hydrateSucceeded('conv-1', cancellingRuntime('turn-cancel'));

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.state).toBe('cancelling');
    expect(view.isProcessing).toBe(true);
    expect(view.canSendMessage).toBe(false);
    expect(view.activeTurnId).toBe('turn-cancel');
  });

  it('ignores stale stop ack for an older turn', () => {
    hydrateSucceeded('conv-1', runningRuntime('turn-2'));
    localStopRequested('conv-1', 'turn-1');
    localStopAcknowledged('conv-1', 'turn-1', runningRuntime('turn-2'));

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.activeTurnId).toBe('turn-2');
    expect(view.isProcessing).toBe(true);
    expect(view.localStopping).toBe(false);
  });

  it('uses cancel response runtime summary as authoritative state', () => {
    hydrateSucceeded('conv-1', runningRuntime('turn-1'));
    localStopRequested('conv-1', 'turn-1');
    localStopAcknowledged('conv-1', 'turn-1', runningRuntime('turn-2'));

    const view = getConversationRuntimeViewSnapshot('conv-1');
    expect(view.activeTurnId).toBe('turn-2');
    expect(view.isProcessing).toBe(true);
    expect(view.localStopping).toBe(false);
  });

  it('bounds retained conversation runtime state and evicts the least recently used owner', () => {
    for (let index = 0; index < 64; index += 1) {
      hydrateSucceeded(`conv-${index}`, runningRuntime(`turn-${index}`));
    }
    expect(getConversationRuntimeViewSnapshot('conv-0').hydrated).toBe(true);

    hydrateSucceeded('conv-64', runningRuntime('turn-64'));

    expect(getConversationRuntimeViewSnapshot('conv-0')).toMatchObject({ hydrated: true, activeTurnId: 'turn-0' });
    expect(getConversationRuntimeViewSnapshot('conv-1')).toMatchObject({ hydrated: false, activeTurnId: null });
    expect(getConversationRuntimeViewSnapshot('conv-64')).toMatchObject({ hydrated: true, activeTurnId: 'turn-64' });
  });
});

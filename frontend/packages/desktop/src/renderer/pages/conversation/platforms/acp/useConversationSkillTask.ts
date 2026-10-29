import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import { uuid } from '@/common/utils/utils';
import type { TMessage } from '@/common/chat/chatLib';
import { ConversationPreparationCancelledError } from '@/common/chat/conversationSubmission';

export function isActiveConversationSkillTask(messages: readonly TMessage[], activeInputMessageId?: string | null) {
  return Boolean(
    activeInputMessageId &&
    messages.some(
      (message) =>
        (message.msg_id ?? message.id) === activeInputMessageId &&
        message.hidden === true &&
        message.turn_intent === 'save-as-skill'
    )
  );
}

export type ConversationSkillTaskState =
  | { status: 'idle' }
  | { status: 'starting' | 'running' | 'error' }
  | { status: 'finished'; refresh: 'pending' | 'ready' | 'error' };

type Scope = {
  authority: object;
  ownerId: string;
  state: ConversationSkillTaskState;
  pending: boolean;
  sawRunning: boolean;
  commandId: string;
};

type Options = {
  authority: object;
  ownerId: string;
  mountedRef: RefObject<boolean>;
  disabled: boolean;
  isProcessing: boolean;
  activeControlTask?: boolean;
  startTask: (id: string) => Promise<void>;
  refreshCatalogs: (isCurrent: () => boolean) => Promise<unknown>;
};

// This hook owns click admission and catalog refresh only. The canonical chat
// runner owns execution, persistence, interruption, recovery and its final result.
export function useConversationSkillTask({
  authority,
  ownerId,
  mountedRef,
  disabled,
  isProcessing,
  activeControlTask = false,
  startTask,
  refreshCatalogs,
}: Options) {
  const scopeRef = useRef<Scope>({
    authority,
    ownerId,
    state: { status: 'idle' },
    pending: false,
    sawRunning: false,
    commandId: '',
  });
  if (scopeRef.current.authority !== authority || scopeRef.current.ownerId !== ownerId) {
    scopeRef.current = {
      authority,
      ownerId,
      state: { status: 'idle' },
      pending: false,
      sawRunning: false,
      commandId: '',
    };
  }
  const scope = scopeRef.current;
  const [snapshot, setSnapshot] = useState({ scope, state: scope.state });
  const state = snapshot.scope === scope ? snapshot.state : scope.state;
  const isCurrent = useCallback(
    (request: Scope) => mountedRef.current === true && scopeRef.current === request,
    [mountedRef]
  );
  const publish = useCallback((request: Scope, next: ConversationSkillTaskState) => {
    request.state = next;
    setSnapshot({ scope: request, state: next });
  }, []);
  const refresh = useCallback(async () => {
    if (!isCurrent(scope) || scope.pending) return;
    scope.pending = true;
    publish(scope, { status: 'finished', refresh: 'pending' });
    try {
      await refreshCatalogs(() => isCurrent(scope));
      if (isCurrent(scope)) publish(scope, { status: 'finished', refresh: 'ready' });
    } catch {
      if (isCurrent(scope)) publish(scope, { status: 'finished', refresh: 'error' });
    } finally {
      scope.pending = false;
    }
  }, [isCurrent, publish, refreshCatalogs, scope]);
  useEffect(() => {
    // Reloaded controls are identified by the canonical active input, never by
    // the text or recency of an earlier Save request.
    if (activeControlTask) {
      scope.sawRunning = true;
      if (scope.state.status === 'idle' || scope.state.status === 'error') publish(scope, { status: 'running' });
      return;
    }
    if (scope.state.status !== 'starting' && scope.state.status !== 'running') return;
    if (isProcessing) scope.sawRunning = true;
    if (scope.state.status === 'running' && scope.sawRunning && !isProcessing) void refresh();
  }, [activeControlTask, isProcessing, publish, refresh, scope, state.status]);
  const isBlocking = useCallback(
    () =>
      isCurrent(scope) && (activeControlTask || scope.state.status === 'starting' || scope.state.status === 'running'),
    [activeControlTask, isCurrent, scope]
  );
  const save = useCallback(async () => {
    if (!isCurrent(scope) || disabled || activeControlTask || scope.pending || scope.state.status === 'running') return;
    // An explicit retry of an uncertain dispatch retains its mutation identity.
    // A completed control run gets a new identity only on the next manual click.
    if (scope.state.status !== 'error') scope.commandId = uuid();
    scope.pending = true;
    scope.sawRunning = false;
    publish(scope, { status: 'starting' });
    try {
      await startTask(scope.commandId);
      scope.sawRunning = true;
      if (isCurrent(scope)) publish(scope, { status: 'running' });
    } catch (error) {
      if (isCurrent(scope)) {
        if (error instanceof ConversationPreparationCancelledError) {
          scope.commandId = '';
          scope.sawRunning = false;
          publish(scope, { status: 'idle' });
        } else publish(scope, { status: 'error' });
      }
    } finally {
      scope.pending = false;
    }
  }, [activeControlTask, disabled, isCurrent, publish, scope, startTask]);
  return {
    state,
    save,
    retryRefresh: refresh,
    isBlocking,
    inFlight: activeControlTask || state.status === 'starting' || state.status === 'running',
    saveDisabled: disabled || activeControlTask || scope.pending || state.status === 'running',
  };
}

import { captureXScienceAsideIntent } from '@/renderer/services/xScienceAside';
import { XScienceAsideOperation } from '@/renderer/services/runtime/asideOperation';
import { readAsidePresentations, type AsideConsumer } from '@/renderer/services/runtime/asidePresentation';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { toXScienceRuntimeSummary } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsideRuntimeView } from './useAsideRuntimeView';
import { decodeSpecialistSendAuthority, type SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import { sameAsideSpecialist } from '@/renderer/services/runtime/asideSpecialist';

type Scope = {
  key: string;
  account: string;
  generation: number;
  active: boolean;
  operation: XScienceAsideOperation | null;
  preparing: Promise<boolean> | null;
  closeRequested: boolean;
  preparingOwner: { operation: XScienceAsideOperation | null; consumer: AsideConsumer | null } | null;
  closing: Map<string, ClosingOperation>;
};
type ClosingOperation = {
  operation: XScienceAsideOperation;
  action: 'close' | 'reconnect' | null;
  errorKey: 'closePending' | 'closeFailed' | 'restoreFailed' | null;
};
type AcceptedConsumer = { intentId: string; consumer: AsideConsumer; ownsDraft: boolean };
type State = {
  owner: string;
  generation: number;
  operation: XScienceAsideOperation | null;
  isOpen: boolean;
  starting: boolean;
  question: string;
  draft: string;
  action: 'close' | 'cancel' | 'send' | 'resume' | 'reconnect' | null;
  errorKey: string | null;
  closingRecoveries: { intentId: string; question: string; waiting: boolean; errorKey: ClosingOperation['errorKey'] }[];
  acceptedConsumers: AcceptedConsumer[];
};
const emptyState = (owner = '', generation = 0): State => ({
  owner,
  generation,
  operation: null,
  isOpen: false,
  starting: false,
  question: '',
  draft: '',
  action: null,
  errorKey: null,
  closingRecoveries: [],
  acceptedConsumers: [],
});

function acceptedConsumers(scope: Scope): AcceptedConsumer[] {
  const current = scope.operation;
  const operations = [...scope.closing.values()].map((entry) => entry.operation);
  if (current && !scope.closing.has(current.intent.intentId)) operations.push(current);
  return operations.flatMap((operation) =>
    operation.receipt && operation.presentation.consumer
      ? [
          {
            intentId: operation.intent.intentId,
            consumer: operation.presentation.consumer,
            ownsDraft: operation === current && !operation.closeRequested,
          },
        ]
      : []
  );
}

function releaseClosedRecovery(scope: Scope, operation: XScienceAsideOperation) {
  if (!operation.closed || (operation.receipt && operation.presentation.consumer)) return;
  scope.closing.delete(operation.intent.intentId);
  if (scope.operation !== operation) operation.retire();
}

export function useBtwCommand(
  conversationId?: string,
  enabled = true,
  options: { ownerId?: string; modelId?: string | null; asSession?: boolean } = {}
) {
  const { t } = useTranslation();
  const account = getRendererAccountScopeToken();
  const ownerId = getRendererAccountOwnerId();
  const eligible = Boolean(enabled && conversationId && ownerId && (!options.ownerId || options.ownerId === ownerId));
  const asSession = options.asSession === true;
  const owner = JSON.stringify([account, conversationId, eligible, asSession]);
  const currentOwner = useRef(owner);
  currentOwner.current = owner;
  const model = useRef(options.modelId ?? null);
  model.current = options.modelId ?? null;
  const scopeRef = useRef<Scope | null>(null);
  const sequence = useRef(0);
  const [state, setState] = useState<State>(() => emptyState());
  const isCurrent = useCallback(
    (scope: Scope) =>
      scope.active &&
      scopeRef.current === scope &&
      currentOwner.current === scope.key &&
      getRendererAccountScopeToken() === scope.account,
    []
  );
  const sync = useCallback(
    (scope: Scope, patch: Partial<State> = {}) => {
      if (!isCurrent(scope)) return;
      const operation = scope.operation;
      setState((current) =>
        current.owner === scope.key && current.generation === scope.generation
          ? {
              ...current,
              operation,
              draft: operation?.presentation.draft ?? current.draft,
              acceptedConsumers: acceptedConsumers(scope),
              closingRecoveries: [...scope.closing.values()].flatMap(({ operation: closing, action, errorKey }) =>
                closing.closed
                  ? []
                  : [
                      {
                        intentId: closing.intent.intentId,
                        question: closing.intent.request,
                        waiting: action !== null,
                        errorKey,
                      },
                    ]
              ),
              ...patch,
              // A late Start, Send, or GET cannot reopen an explicitly dismissed side chat.
              ...(operation?.closed || (!asSession && scope.closeRequested) ? { isOpen: false } : {}),
            }
          : current
      );
    },
    [asSession, isCurrent]
  );

  const recoverClose = useCallback(
    async (scope: Scope, operation: XScienceAsideOperation, action: 'close' | 'reconnect') => {
      if (!isCurrent(scope) || operation.closed) return;
      const id = operation.intent.intentId;
      const previous = scope.closing.get(id);
      if (previous && (previous.operation !== operation || previous.action)) return;
      if (!previous && scope.operation !== operation) return;
      const entry: ClosingOperation = { operation, action, errorKey: null };
      scope.closing.set(id, entry);
      sync(scope);
      try {
        if (action === 'close') await operation.close();
        else await operation.reconcile();
        entry.errorKey = operation.closed ? null : 'closePending';
      } catch {
        entry.errorKey = action === 'close' ? 'closeFailed' : 'restoreFailed';
      } finally {
        if (isCurrent(scope) && scope.closing.get(id) === entry) {
          entry.action = null;
          releaseClosedRecovery(scope, operation);
          sync(
            scope,
            scope.operation === operation
              ? { action: null, starting: false, isOpen: false, errorKey: entry.errorKey }
              : {}
          );
        }
      }
    },
    [isCurrent, sync]
  );

  useLayoutEffect(() => {
    const scope: Scope = {
      key: owner,
      account,
      generation: ++sequence.current,
      active: true,
      operation: null,
      preparing: null,
      preparingOwner: null,
      closeRequested: false,
      closing: new Map(),
    };
    scopeRef.current = scope;
    setState(emptyState(owner, scope.generation));
    if (eligible && conversationId) {
      const { current: hint, closing } = readAsidePresentations(conversationId, asSession);
      if (hint) {
        const operation = new XScienceAsideOperation(hint.intent, hint, { timeoutMs: 15_000 });
        scope.operation = operation;
        scope.closeRequested = hint.closeRequested;
        setState({
          ...emptyState(owner, scope.generation),
          operation,
          isOpen: asSession || !hint.closeRequested,
          question: hint.intent.request,
          draft: hint.draft,
          starting: true,
        });
        // Hydration is GET-only. A persisted flag cannot authorize new execution or cancellation.
        if (!asSession && hint.closeRequested) void recoverClose(scope, operation, 'reconnect');
        else
          void operation
            .reconcile()
            .then((result) => {
              if (!isCurrent(scope) || scope.operation !== operation) return;
              sync(scope, {
                starting: false,
                isOpen: !operation.closed,
                errorKey:
                  operation.closeRequested && !operation.closed
                    ? 'closePending'
                    : result.status === 'absent'
                      ? 'acceptanceUnknown'
                      : null,
              });
            })
            .catch(() => {
              if (scope.operation === operation) sync(scope, { starting: false, errorKey: 'restoreFailed' });
            });
      }
      for (const retained of closing) {
        const operation = new XScienceAsideOperation(retained.intent, retained, { timeoutMs: 15_000 });
        scope.closing.set(retained.intent.intentId, { operation, action: null, errorKey: 'closePending' });
        void recoverClose(scope, operation, 'reconnect');
      }
    }
    return () => {
      scope.active = false;
      scope.operation?.retire();
      for (const { operation } of scope.closing.values()) operation.retire();
      if (scopeRef.current === scope) scopeRef.current = null;
    };
  }, [account, asSession, conversationId, eligible, isCurrent, owner, recoverClose, sync]);

  const current = state.owner === owner ? state : emptyState(owner);
  const operation = current.operation;
  const receipt = operation?.receipt ?? null;
  const runtime = useAsideRuntimeView(receipt, current.isOpen && !asSession, current.generation);
  let summary: ReturnType<typeof toXScienceRuntimeSummary> | null = null;
  if (runtime.snapshot) {
    try {
      summary = toXScienceRuntimeSummary(runtime.snapshot);
    } catch {
      /* Unsupported canonical state stays visible but cannot authorize sending. */
    }
  }
  const summaryRef = useRef(summary);
  summaryRef.current = summary;

  const ask = useCallback(
    (rawText: string, consumer: AsideConsumer | null = null, authority?: SpecialistSendAuthority): Promise<boolean> => {
      const scope = scopeRef.current;
      const text = rawText.trim();
      if (!scope || !isCurrent(scope) || !eligible || !conversationId || !text) return Promise.resolve(false);
      let sourceSpecialist: SpecialistSendAuthority['reference'];
      try {
        sourceSpecialist = decodeSpecialistSendAuthority(authority ?? {}).reference;
      } catch {
        return Promise.resolve(false);
      }
      let existing = scope.operation;
      const dismissed = !asSession && existing && (existing.closeRequested || existing.closed);
      if (scope.preparing && !dismissed) return Promise.resolve(false);
      if (dismissed) {
        scope.operation = null;
        releaseClosedRecovery(scope, existing);
        existing = null;
      }
      if (
        existing &&
        !existing.closed &&
        (existing.receipt ||
          existing.intent.request !== text ||
          existing.closeRequested ||
          !sameAsideSpecialist(existing.intent.sourceSpecialist, sourceSpecialist))
      ) {
        sync(scope, { isOpen: true, errorKey: existing.closeRequested ? 'closePending' : 'alreadyRunning' });
        return Promise.resolve(false);
      }
      if (!existing || existing.closed) scope.closeRequested = false;
      const preparationOwner = { operation: existing && !existing.closed ? existing : null, consumer };
      scope.preparingOwner = preparationOwner;
      sync(scope, {
        isOpen: !asSession,
        starting: true,
        question: text,
        draft: existing?.presentation.draft ?? '',
        action: null,
        errorKey: null,
      });
      const run = async () => {
        let owned = existing && !existing.closed ? existing : null;
        let intentCaptured = false;
        try {
          if (!owned) {
            const intentId = crypto.randomUUID();
            const intent = await captureXScienceAsideIntent(conversationId, text, {
              intentId,
              model: model.current,
              asSession,
              ...(sourceSpecialist ? { sourceSpecialist } : {}),
            });
            if (!isCurrent(scope)) return false;
            intentCaptured = true;
            owned = new XScienceAsideOperation(intent, undefined, { timeoutMs: 15_000 }, consumer);
            preparationOwner.operation = owned;
            scope.operation?.retire();
            scope.operation = owned;
            sync(scope);
          }
          // Close owns cancellation once the in-progress capture hands off its exact intent.
          if (scope.closeRequested) return false;
          await owned.start();
          if (!isCurrent(scope)) return false;
          if (scope.operation !== owned) {
            sync(scope);
            return false;
          }
          sync(scope, { starting: false, isOpen: !owned.closed && !asSession, errorKey: null });
          return true;
        } catch {
          if (isCurrent(scope) && scope.preparingOwner === preparationOwner && !scope.closeRequested)
            sync(scope, {
              starting: false,
              isOpen: !asSession && !owned?.closed && (!intentCaptured || Boolean(owned)),
              errorKey: owned?.closed ? null : intentCaptured && !owned ? 'persistenceFailed' : 'acceptanceUnknown',
            });
          return false;
        }
      };
      const promise = run().finally(() => {
        if (scope.preparing === promise) scope.preparing = null;
        if (scope.preparingOwner === preparationOwner) scope.preparingOwner = null;
      });
      scope.preparing = promise;
      return promise;
    },
    [asSession, conversationId, eligible, isCurrent, sync]
  );

  const close = useCallback(
    async (expected?: XScienceAsideOperation) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return;
      if (expected && (scope.operation !== expected || !expected.closeRequested || expected.closed)) return;
      const preparing = scope.preparing;
      const preparingOwner = scope.preparingOwner;
      const previous = scope.operation;
      let owned = preparingOwner ? preparingOwner.operation : previous;
      scope.closeRequested = true;
      sync(scope, { isOpen: false, action: 'close', errorKey: null });
      try {
        if (!owned && preparing) {
          await preparing;
          owned = preparingOwner?.operation ?? null;
        }
        if (!isCurrent(scope) || scope.operation !== (owned ?? previous)) return;
        if (owned && !owned.closed) {
          if (!asSession) {
            await recoverClose(scope, owned, 'close');
            return;
          }
          await owned.close();
        }
        if (scope.operation !== (owned ?? previous)) return;
        sync(scope, { isOpen: false, action: null, starting: false });
      } catch {
        if (scope.operation === owned)
          sync(scope, { isOpen: asSession, action: null, starting: false, errorKey: 'closeFailed' });
      }
    },
    [asSession, isCurrent, recoverClose, sync]
  );
  const retryClose = useCallback(
    (intentId?: string) => {
      if (asSession) return operation ? close(operation) : Promise.resolve();
      const scope = scopeRef.current;
      const target = intentId ? scope?.closing.get(intentId)?.operation : operation;
      return scope && target?.closeRequested ? recoverClose(scope, target, 'close') : Promise.resolve();
    },
    [asSession, close, operation, recoverClose]
  );
  const reconnectClose = useCallback(
    (intentId: string) => {
      const scope = scopeRef.current;
      const target = scope?.closing.get(intentId)?.operation;
      return scope && target ? recoverClose(scope, target, 'reconnect') : Promise.resolve();
    },
    [recoverClose]
  );

  const setDraft = useCallback(
    (value: string) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return;
      try {
        scope.operation?.setDraft(value);
      } catch {
        sync(scope, { errorKey: 'persistenceFailed' });
      }
      sync(scope, { draft: value });
    },
    [isCurrent, sync]
  );

  const reconnect = useCallback(async () => {
    const scope = scopeRef.current;
    if (!scope || !isCurrent(scope) || !scope.operation) return;
    const owned = scope.operation;
    if (!asSession && owned.closeRequested) {
      await recoverClose(scope, owned, 'reconnect');
      return;
    }
    sync(scope, { action: 'reconnect', errorKey: null });
    try {
      const result = await owned.reconcile();
      if (scope.operation !== owned) return;
      sync(scope, {
        action: null,
        isOpen: !owned.closed && !asSession,
        errorKey:
          owned.closeRequested && !owned.closed
            ? 'closePending'
            : result.status === 'absent'
              ? 'acceptanceUnknown'
              : null,
      });
      if (isCurrent(scope)) runtime.refresh();
    } catch {
      if (scope.operation === owned) sync(scope, { action: null, errorKey: 'restoreFailed' });
    }
  }, [asSession, isCurrent, recoverClose, runtime.refresh, sync]);

  const cancel = useCallback(async () => {
    const scope = scopeRef.current;
    if (
      !scope ||
      !isCurrent(scope) ||
      !scope.operation ||
      !receipt ||
      (!scope.operation.presentation.pendingFollowup && !runtime.snapshot?.canCancel)
    )
      return;
    sync(scope, { action: 'cancel', errorKey: null });
    const owned = scope.operation;
    try {
      await owned.cancel();
      if (scope.operation !== owned) return;
      if (isCurrent(scope)) runtime.refresh();
      sync(scope, { action: null });
    } catch {
      if (scope.operation === owned) sync(scope, { action: null, errorKey: 'cancelFailed' });
    }
  }, [isCurrent, receipt, runtime.refresh, runtime.snapshot, sync]);

  const send = useCallback(
    async (rawText: string) => {
      const scope = scopeRef.current;
      const pending = scope?.operation?.presentation.pendingFollowup;
      const exactReplay = pending?.text === rawText.trim();
      if (
        !scope ||
        !isCurrent(scope) ||
        !scope.operation ||
        current.action ||
        (!exactReplay &&
          (!runtime.snapshot ||
            !summaryRef.current?.can_send_message ||
            runtime.snapshot.pendingInputRequests.length > 0))
      )
        return false;
      sync(scope, { action: 'send', errorKey: null });
      const owned = scope.operation;
      try {
        const accepted = await owned.send(rawText);
        if (!isCurrent(scope) || scope.operation !== owned) return false;
        if (accepted && owned.presentation.draft === rawText) owned.setDraft('');
        sync(scope, { action: null, isOpen: !owned.closed });
        runtime.refresh();
        return accepted;
      } catch {
        if (scope.operation === owned) sync(scope, { action: null, errorKey: 'followupFailed' });
        return false;
      }
    },
    [current.action, isCurrent, runtime.refresh, runtime.snapshot, sync]
  );

  const resume = useCallback(async () => {
    const scope = scopeRef.current;
    if (!scope || !isCurrent(scope) || !scope.operation || !runtime.snapshot) return;
    const owned = scope.operation;
    sync(scope, { action: 'resume', errorKey: null });
    try {
      await owned.resume(runtime.snapshot);
      if (scope.operation !== owned) return;
      sync(scope, { action: null });
      if (isCurrent(scope)) runtime.refresh();
    } catch {
      if (scope.operation === owned) sync(scope, { action: null, errorKey: 'resumeFailed' });
    }
  }, [isCurrent, runtime.refresh, runtime.snapshot, sync]);

  const getReceipt = useCallback(() => {
    const scope = scopeRef.current;
    return scope && isCurrent(scope) ? (scope.operation?.receipt ?? null) : null;
  }, [isCurrent]);
  const getAcceptedConsumers = useCallback(() => {
    const scope = scopeRef.current;
    return scope && isCurrent(scope) ? acceptedConsumers(scope) : [];
  }, [isCurrent]);
  const getUnfrozenConsumer = useCallback(() => {
    const scope = scopeRef.current;
    if (!scope || !isCurrent(scope)) return null;
    const preparing = scope.preparingOwner;
    return preparing && !preparing.operation ? preparing.consumer : null;
  }, [isCurrent]);
  const acknowledgeExactConsumer = useCallback(
    (intentId: string) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return;
      const owned =
        scope.closing.get(intentId)?.operation ??
        (scope.operation?.intent.intentId === intentId ? scope.operation : null);
      if (!owned?.receipt || !owned.presentation.consumer) return;
      owned.acknowledgeConsumer();
      releaseClosedRecovery(scope, owned);
      sync(scope);
    },
    [isCurrent, sync]
  );
  const acknowledgeBranch = useCallback(() => {
    const scope = scopeRef.current;
    if (scope && isCurrent(scope)) scope.operation?.acknowledgeBranch();
  }, [isCurrent]);
  const retry = useCallback(() => {
    const scope = scopeRef.current;
    const original = scope?.operation;
    return original?.intent.sourceSpecialist
      ? ask(original.intent.request, original.presentation.consumer, { reference: original.intent.sourceSpecialist })
      : ask(original?.intent.request ?? current.question);
  }, [ask, current.question]);

  const running = Boolean(
    current.starting || runtime.loading || runtime.snapshot?.canCancel || runtime.snapshot?.runtimeActive
  );
  const canSend = Boolean(
    receipt &&
    runtime.snapshot &&
    !current.action &&
    !operation?.closed &&
    !operation?.closeRequested &&
    !operation?.presentation.pendingFollowup &&
    summary?.can_send_message &&
    runtime.snapshot.pendingInputRequests.length === 0
  );
  return {
    ...current,
    entries: runtime.entries,
    snapshot: runtime.snapshot,
    observationError: runtime.error || Boolean(runtime.snapshot && !summary),
    truncated: runtime.truncated,
    receipt,
    frameId: receipt?.frameId ?? null,
    isLoading: running,
    error: current.errorKey ? t(`conversation.sideQuestion.${current.errorKey}` as Parameters<typeof t>[0]) : null,
    persistenceError: operation?.persistenceError === true,
    closeRequested: operation?.closeRequested === true,
    pendingFollowupText: operation?.presentation.pendingFollowup?.text ?? null,
    canRetryFollowup: Boolean(
      receipt &&
      operation?.presentation.pendingFollowup &&
      !current.action &&
      !operation.closed &&
      !operation.closeRequested
    ),
    canSend,
    canCancel: Boolean(
      receipt &&
      (runtime.snapshot?.canCancel || operation?.presentation.pendingFollowup) &&
      !current.action &&
      !operation?.closed &&
      !operation?.closeRequested
    ),
    canResume: Boolean(
      receipt &&
      runtime.snapshot &&
      (runtime.snapshot.canResume || operation?.presentation.pendingResume) &&
      !current.action &&
      !operation?.closed &&
      !operation?.closeRequested
    ),
    ask,
    retry,
    close,
    retryClose,
    reconnectClose,
    cancel,
    send,
    setDraft,
    reconnect,
    resume,
    getReceipt,
    getAcceptedConsumers,
    getUnfrozenConsumer,
    acknowledgeExactConsumer,
    acknowledgeBranch,
  };
}

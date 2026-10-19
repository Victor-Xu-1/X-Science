import { captureXScienceAsideIntent } from '@/renderer/services/xScienceAside';
import { XScienceAsideOperation } from '@/renderer/services/runtime/asideOperation';
import { readAsidePresentation, type AsideConsumer } from '@/renderer/services/runtime/asidePresentation';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { toXScienceRuntimeSummary } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useAsideRuntimeView } from './useAsideRuntimeView';
import { decodeSpecialistSendAuthority, type SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import { sameAsideSpecialist } from '@/renderer/services/asideSpecialist';

type Scope = {
  key: string;
  account: string;
  generation: number;
  active: boolean;
  operation: XScienceAsideOperation | null;
  preparing: Promise<boolean> | null;
  closeRequested: boolean;
  preparingOwner: { operation: XScienceAsideOperation | null } | null;
};
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
});

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
        current.owner === scope.key
          ? {
              ...current,
              operation,
              draft: operation?.presentation.draft ?? current.draft,
              ...(operation?.closed ? { isOpen: false } : {}),
              ...patch,
            }
          : current
      );
    },
    [isCurrent]
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
    };
    scopeRef.current = scope;
    setState(emptyState(owner, scope.generation));
    if (eligible && conversationId) {
      const hint = readAsidePresentation(conversationId, asSession);
      if (hint) {
        const operation = new XScienceAsideOperation(hint.intent, hint, { timeoutMs: 15_000 });
        scope.operation = operation;
        scope.closeRequested = hint.closeRequested;
        setState({
          ...emptyState(owner, scope.generation),
          operation,
          isOpen: true,
          question: hint.intent.request,
          draft: hint.draft,
          starting: true,
        });
        // Hydration is GET-only. A persisted flag cannot authorize new execution or cancellation.
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
    }
    return () => {
      scope.active = false;
      scope.operation?.retire();
      if (scopeRef.current === scope) scopeRef.current = null;
    };
  }, [account, asSession, conversationId, eligible, isCurrent, owner, sync]);

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
      if (scope.preparing) return Promise.resolve(false);
      let sourceSpecialist: SpecialistSendAuthority['reference'];
      try {
        sourceSpecialist = decodeSpecialistSendAuthority(authority ?? {}).reference;
      } catch {
        return Promise.resolve(false);
      }
      const existing = scope.operation;
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
      const preparationOwner = { operation: existing && !existing.closed ? existing : null };
      scope.preparingOwner = preparationOwner;
      sync(scope, { isOpen: !asSession, starting: true, question: text, errorKey: null });
      const run = async () => {
        let owned = existing && !existing.closed ? existing : null;
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
            owned = new XScienceAsideOperation(intent, undefined, { timeoutMs: 15_000 }, consumer);
            preparationOwner.operation = owned;
            scope.operation?.retire();
            scope.operation = owned;
            sync(scope);
          }
          if (scope.closeRequested) {
            await owned.close();
            if (!isCurrent(scope) || scope.operation !== owned) return false;
            sync(scope, { starting: false, isOpen: false, action: null });
            return false;
          }
          await owned.start();
          if (!isCurrent(scope) || scope.operation !== owned) return false;
          sync(scope, { starting: false, isOpen: !owned.closed && !asSession, errorKey: null });
          return true;
        } catch {
          if (isCurrent(scope) && scope.preparingOwner === preparationOwner)
            sync(scope, {
              starting: false,
              isOpen: !owned?.closed && !asSession,
              errorKey: owned?.closed ? null : 'acceptanceUnknown',
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

  const close = useCallback(async () => {
    const scope = scopeRef.current;
    if (!scope || !isCurrent(scope)) return;
    const preparing = scope.preparing;
    const preparingOwner = scope.preparingOwner;
    let owned = scope.operation;
    scope.closeRequested = true;
    sync(scope, { isOpen: false, action: 'close', errorKey: null });
    try {
      if (!owned && preparing) {
        await preparing;
        owned = preparingOwner?.operation ?? null;
      }
      if (!isCurrent(scope)) return;
      if (owned && !owned.closed) await owned.close();
      if (scope.operation !== owned) return;
      sync(scope, { isOpen: false, action: null, starting: false });
    } catch {
      if (scope.operation === owned) sync(scope, { isOpen: true, action: null, errorKey: 'closeFailed' });
    }
  }, [isCurrent, sync]);

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
  }, [asSession, isCurrent, runtime.refresh, sync]);

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
  const getAcceptedConsumer = useCallback(() => {
    const scope = scopeRef.current;
    return scope && isCurrent(scope) && scope.operation?.receipt ? scope.operation.presentation.consumer : null;
  }, [isCurrent]);
  const acknowledgeConsumer = useCallback(() => {
    const scope = scopeRef.current;
    if (!scope || !isCurrent(scope)) return;
    scope.operation?.acknowledgeConsumer();
    sync(scope);
  }, [isCurrent, sync]);
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
    acceptedConsumer: receipt ? (operation?.presentation.consumer ?? null) : null,
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
    cancel,
    send,
    setDraft,
    reconnect,
    resume,
    getReceipt,
    getAcceptedConsumer,
    acknowledgeConsumer,
    acknowledgeBranch,
  };
}

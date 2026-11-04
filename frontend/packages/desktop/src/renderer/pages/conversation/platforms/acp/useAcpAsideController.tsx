import React, { useCallback, useLayoutEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useBtwCommand } from '@/renderer/components/chat/BtwOverlay/useBtwCommand';
import { readAsidePresentations, type AsideConsumer } from '@/renderer/services/runtime/asidePresentation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { ConversationCommandQueueItem } from '../useConversationCommandQueue';
import { decodeSpecialistSendAuthority, type SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import { asideSourceMatches } from '@/renderer/services/runtime/asideSpecialist';
import { Message } from '@arco-design/web-react';

export type AsideQueueBridge = {
  items: ConversationCommandQueueItem[];
  remove: (id: string) => boolean;
  getQueuedItem: (id: string) => ConversationCommandQueueItem | null;
};
type Props = {
  conversationId: string;
  ownerId: string;
  enabled: boolean;
  modelId: string | null;
  queue: React.MutableRefObject<AsideQueueBridge | null>;
  clearDraftIfUnchanged: (text: string) => void;
  captureDraftSpecialist?: () => SpecialistSendAuthority;
  draftSpecialistInvalid?: boolean;
  navigate: (path: string) => void;
};
type Scope = { key: string; active: boolean };
const matchesQueueConsumer = (item: ConversationCommandQueueItem, consumer: AsideConsumer | null | undefined) =>
  consumer?.kind === 'queue' &&
  consumer.id === item.id &&
  consumer.content === item.input &&
  item.files.length === 0 &&
  item.contextItems.length === 0 &&
  !item.specialistAuthorityInvalid &&
  asideSourceMatches(item.specialistAuthority, consumer.sourceSpecialist);

/** ACP composition only; admission, execution and cancellation have one shared owner chain. */
export function useAcpAsideController(props: Props) {
  const { t } = useTranslation();
  const latest = useRef(props);
  latest.current = props;
  const account = getRendererAccountScopeToken();
  const key = JSON.stringify([account, props.ownerId, props.conversationId]);
  const currentKey = useRef(key);
  currentKey.current = key;
  const scopeRef = useRef<Scope | null>(null);
  useLayoutEffect(() => {
    const scope = { key, active: true };
    scopeRef.current = scope;
    return () => {
      scope.active = false;
    };
  }, [key]);
  const isCurrent = useCallback(
    (scope: Scope) => scope.active && scopeRef.current === scope && currentKey.current === scope.key,
    []
  );
  const sideQuestion = useBtwCommand(props.conversationId, props.enabled, {
    ownerId: props.ownerId,
    modelId: props.modelId,
  });
  const branch = useBtwCommand(props.conversationId, props.enabled, {
    ownerId: props.ownerId,
    modelId: props.modelId,
    asSession: true,
  });

  const consume = useCallback(
    (controller: ReturnType<typeof useBtwCommand>, scope: Scope) => {
      if (!isCurrent(scope)) return;
      const context = latest.current;
      for (const { intentId, consumer, ownsDraft } of controller.getAcceptedConsumers()) {
        if (consumer.kind === 'queue') {
          const item = context.queue.current?.getQueuedItem(consumer.id);
          if (
            item &&
            matchesQueueConsumer(item, consumer) &&
            !item.readingContext &&
            !item.readingContextInvalid &&
            context.queue.current?.remove(item.id) !== true
          )
            continue;
        } else if (ownsDraft && consumer.id === context.conversationId) {
          try {
            if (
              !context.draftSpecialistInvalid &&
              asideSourceMatches(context.captureDraftSpecialist?.(), consumer.sourceSpecialist)
            )
              context.clearDraftIfUnchanged(consumer.content);
          } catch {
            /* An unconfirmed new selection cannot be consumed by the old receipt. */
          }
        }
        controller.acknowledgeExactConsumer(intentId);
      }
    },
    [isCurrent]
  );

  useLayoutEffect(() => {
    const scope = scopeRef.current;
    if (scope) {
      consume(sideQuestion, scope);
      consume(branch, scope);
    }
  }, [branch.acceptedConsumers, consume, sideQuestion.acceptedConsumers]);

  const sideDraft = useCallback(
    async (text: string, authority?: SpecialistSendAuthority) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return false;
      const consumer: AsideConsumer = {
        kind: 'draft',
        id: latest.current.conversationId,
        content: text,
        ...(authority?.reference ? { sourceSpecialist: authority.reference } : {}),
      };
      const accepted = authority?.reference
        ? await sideQuestion.ask(text, consumer, authority)
        : await sideQuestion.ask(text, consumer);
      if (accepted) consume(sideQuestion, scope);
      return accepted;
    },
    [consume, isCurrent, sideQuestion.ask, sideQuestion.getAcceptedConsumers, sideQuestion.acknowledgeExactConsumer]
  );

  const branchDraft = useCallback(
    async (text: string, authority?: SpecialistSendAuthority) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return false;
      const consumer: AsideConsumer = {
        kind: 'branch',
        id: latest.current.conversationId,
        content: text,
        ...(authority?.reference ? { sourceSpecialist: authority.reference } : {}),
      };
      const accepted = authority?.reference
        ? await branch.ask(text, consumer, authority)
        : await branch.ask(text, consumer);
      if (!accepted || !isCurrent(scope)) return false;
      const receipt = branch.getReceipt();
      if (!receipt) return false;
      consume(branch, scope);
      latest.current.navigate(`/conversation/${encodeURIComponent(receipt.frameId)}`);
      branch.acknowledgeBranch();
      return true;
    },
    [
      branch.ask,
      branch.getReceipt,
      branch.acknowledgeBranch,
      branch.getAcceptedConsumers,
      branch.acknowledgeExactConsumer,
      consume,
      isCurrent,
    ]
  );

  const openQueued = useCallback(
    async (item: ConversationCommandQueueItem) => {
      const scope = scopeRef.current;
      const actual = latest.current.queue.current?.getQueuedItem(item.id);
      if (
        !scope ||
        !isCurrent(scope) ||
        !actual ||
        actual.input !== item.input ||
        actual.files.length ||
        actual.contextItems.length ||
        actual.readingContext ||
        actual.readingContextInvalid ||
        actual.specialistAuthorityInvalid ||
        !asideSourceMatches(item.specialistAuthority, actual.specialistAuthority?.reference) ||
        matchesQueueConsumer(actual, sideQuestion.getUnfrozenConsumer())
      )
        return false;
      const hints = readAsidePresentations(latest.current.conversationId, false);
      if (
        hints.closing.some((hint) => matchesQueueConsumer(actual, hint.consumer)) ||
        (hints.current?.closeRequested && matchesQueueConsumer(actual, hints.current.consumer))
      )
        return false;
      let authority: SpecialistSendAuthority;
      try {
        authority = decodeSpecialistSendAuthority(actual.specialistAuthority ?? {});
      } catch {
        return false;
      }
      const consumer: AsideConsumer = {
        kind: 'queue',
        id: actual.id,
        content: actual.input,
        ...(authority.reference ? { sourceSpecialist: authority.reference } : {}),
      };
      const accepted = authority.reference
        ? await sideQuestion.ask(actual.input, consumer, authority)
        : await sideQuestion.ask(actual.input, consumer);
      if (accepted) consume(sideQuestion, scope);
      return accepted;
    },
    [
      consume,
      isCurrent,
      sideQuestion.ask,
      sideQuestion.getAcceptedConsumers,
      sideQuestion.acknowledgeExactConsumer,
      sideQuestion.getUnfrozenConsumer,
    ]
  );

  const isQueueItemBlocked = useCallback(
    (item: ConversationCommandQueueItem) => {
      const context = latest.current;
      if (!context.enabled) return false;
      if (matchesQueueConsumer(item, sideQuestion.getUnfrozenConsumer())) return true;
      const { current, closing } = readAsidePresentations(context.conversationId, false);
      return Boolean(
        matchesQueueConsumer(item, current?.consumer) ||
        closing.some((hint) => matchesQueueConsumer(item, hint.consumer))
      );
    },
    [
      sideQuestion.operation,
      sideQuestion.starting,
      sideQuestion.receipt,
      sideQuestion.closeRequested,
      sideQuestion.errorKey,
      sideQuestion.acceptedConsumers,
      sideQuestion.closingRecoveries,
      sideQuestion.getUnfrozenConsumer,
    ]
  );

  const openBranch = useCallback(() => {
    const scope = scopeRef.current;
    const receipt = branch.getReceipt();
    if (!scope || !isCurrent(scope) || !receipt) return;
    consume(branch, scope);
    latest.current.navigate(`/conversation/${encodeURIComponent(receipt.frameId)}`);
    branch.acknowledgeBranch();
  }, [
    branch.getReceipt,
    branch.acknowledgeBranch,
    branch.getAcceptedConsumers,
    branch.acknowledgeExactConsumer,
    consume,
    isCurrent,
  ]);

  const retryBranch = useCallback(async () => {
    const scope = scopeRef.current;
    if (!scope || !isCurrent(scope)) return;
    if (branch.getReceipt()) {
      openBranch();
      return;
    }
    if (await branch.retry()) openBranch();
  }, [branch.retry, branch.getReceipt, isCurrent, openBranch]);

  // The shared /btw composer calls this facade with its original raw consumer
  // text. Internal queue/draft paths keep using the same core owner directly.
  const askDraft = useCallback(
    (text: string, consumer: AsideConsumer | null = null) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return Promise.resolve(false);
      try {
        const context = latest.current;
        if (context.draftSpecialistInvalid) throw new Error('aside_source_unavailable');
        const authority = decodeSpecialistSendAuthority(context.captureDraftSpecialist?.() ?? {});
        const frozenConsumer = consumer
          ? { ...consumer, ...(authority.reference ? { sourceSpecialist: authority.reference } : {}) }
          : null;
        return authority.reference
          ? sideQuestion.ask(text, frozenConsumer, authority)
          : sideQuestion.ask(text, frozenConsumer);
      } catch {
        Message.warning(t('conversation.xscienceRuntime.sessionOptions.specialistState.prepareFailed'));
        return Promise.resolve(false);
      }
    },
    [isCurrent, sideQuestion.ask, t]
  );

  return {
    sideQuestion: { ...sideQuestion, ask: askDraft },
    sideDraft,
    branchDraft,
    openQueued,
    isQueueItemBlocked,
    branchStatus: <AsideBranchStatus controller={branch} onOpen={openBranch} onRetry={retryBranch} />,
  };
}

function AsideBranchStatus({
  controller,
  onOpen,
  onRetry,
}: {
  controller: ReturnType<typeof useBtwCommand>;
  onOpen: () => void;
  onRetry: () => Promise<void>;
}) {
  const { t } = useTranslation();
  if (controller.operation?.closed || (!controller.starting && !controller.error && !controller.receipt)) return null;
  return (
    <div
      className='mb-8px flex flex-wrap items-center gap-8px rd-8px px-10px py-8px text-12px text-t-secondary'
      data-testid='aside-branch-status'
    >
      <span role={controller.error ? 'alert' : 'status'}>
        {controller.error ??
          t(controller.receipt ? 'conversation.sideQuestion.branchReady' : 'conversation.sideQuestion.branchStarting')}
      </span>
      {controller.receipt ? (
        <button type='button' onClick={onOpen}>
          {t('conversation.sideQuestion.openBranch')}
        </button>
      ) : null}
      {controller.error && !controller.receipt ? (
        <>
          <button type='button' disabled={controller.starting} onClick={() => void onRetry()}>
            {t('conversation.sideQuestion.retryOriginal')}
          </button>
          <button type='button' disabled={Boolean(controller.action)} onClick={() => void controller.reconnect()}>
            {t('conversation.sideQuestion.reconnect')}
          </button>
          <button type='button' disabled={Boolean(controller.action)} onClick={() => void controller.close()}>
            {t('conversation.sideQuestion.cancelBranch')}
          </button>
        </>
      ) : null}
    </div>
  );
}

import React, { useCallback, useLayoutEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { useBtwCommand } from '@/renderer/components/chat/BtwOverlay/useBtwCommand';
import { readAsidePresentation, type AsideConsumer } from '@/renderer/services/runtime/asidePresentation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import type { ConversationCommandQueueItem } from '../useConversationCommandQueue';

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
  navigate: (path: string) => void;
};
type Scope = { key: string; active: boolean };

/** ACP composition only; admission, execution and cancellation have one shared owner chain. */
export function useAcpAsideController(props: Props) {
  const latest = useRef(props);
  latest.current = props;
  const account = getRendererAccountScopeToken();
  const key = JSON.stringify([account, props.ownerId, props.conversationId]);
  const currentKey = useRef(key);
  currentKey.current = key;
  const scopeRef = useRef<Scope | null>(null);
  const provisional = useRef<{ scope: Scope; consumer: AsideConsumer } | null>(null);
  useLayoutEffect(() => {
    const scope = { key, active: true };
    scopeRef.current = scope;
    provisional.current = null;
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
      const consumer = controller.getAcceptedConsumer();
      if (!consumer) return;
      const context = latest.current;
      if (consumer.kind === 'queue') {
        const item = context.queue.current?.getQueuedItem(consumer.id);
        if (item && item.input === consumer.content && item.files.length === 0 && item.contextItems.length === 0) {
          if (context.queue.current?.remove(item.id) !== true) return;
        }
      } else if (consumer.id === context.conversationId) {
        context.clearDraftIfUnchanged(consumer.content);
      }
      controller.acknowledgeConsumer();
    },
    [isCurrent]
  );

  useLayoutEffect(() => {
    const scope = scopeRef.current;
    if (scope) {
      consume(sideQuestion, scope);
      consume(branch, scope);
    }
  }, [branch.acceptedConsumer, branch.receipt, consume, sideQuestion.acceptedConsumer, sideQuestion.receipt]);

  const sideDraft = useCallback(
    async (text: string) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return false;
      const accepted = await sideQuestion.ask(text, {
        kind: 'draft',
        id: latest.current.conversationId,
        content: text,
      });
      if (accepted) consume(sideQuestion, scope);
      return accepted;
    },
    [consume, isCurrent, sideQuestion.ask, sideQuestion.getAcceptedConsumer, sideQuestion.acknowledgeConsumer]
  );

  const branchDraft = useCallback(
    async (text: string) => {
      const scope = scopeRef.current;
      if (!scope || !isCurrent(scope)) return false;
      const accepted = await branch.ask(text, { kind: 'branch', id: latest.current.conversationId, content: text });
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
      branch.getAcceptedConsumer,
      branch.acknowledgeConsumer,
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
        provisional.current
      )
        return false;
      const consumer: AsideConsumer = { kind: 'queue', id: item.id, content: item.input };
      const pending = { scope, consumer };
      provisional.current = pending;
      try {
        const accepted = await sideQuestion.ask(item.input, consumer);
        if (accepted) consume(sideQuestion, scope);
        return accepted;
      } finally {
        if (provisional.current === pending) provisional.current = null;
      }
    },
    [consume, isCurrent, sideQuestion.ask, sideQuestion.getAcceptedConsumer, sideQuestion.acknowledgeConsumer]
  );

  const isQueueItemBlocked = useCallback(
    (item: ConversationCommandQueueItem) => {
      const context = latest.current;
      const active = provisional.current;
      const consumer =
        active && isCurrent(active.scope) && !sideQuestion.operation?.closed
          ? active.consumer
          : readAsidePresentation(context.conversationId, false)?.consumer;
      return Boolean(
        context.enabled &&
        consumer?.kind === 'queue' &&
        consumer.id === item.id &&
        consumer.content === item.input &&
        item.files.length === 0 &&
        item.contextItems.length === 0
      );
    },
    [
      isCurrent,
      sideQuestion.operation,
      sideQuestion.starting,
      sideQuestion.receipt,
      sideQuestion.closeRequested,
      sideQuestion.errorKey,
      sideQuestion.acceptedConsumer,
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
    branch.getAcceptedConsumer,
    branch.acknowledgeConsumer,
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

  return {
    sideQuestion,
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

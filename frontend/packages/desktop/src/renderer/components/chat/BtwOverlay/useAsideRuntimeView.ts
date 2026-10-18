import { ipcBridge } from '@/common';
import type { TMessage } from '@/common/chat/chatLib';
import { requestConversationMessages } from '@/common/adapter/ipcDatabaseClient';
import { normalizeDbMessagePage } from '@/renderer/pages/conversation/Messages/hooks';
import { mergeLoadedPageWithCurrent } from '@/renderer/pages/conversation/Messages/messageWindowReconciliation';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { invalidateXScienceFrameReads } from '@/renderer/services/xScienceFrameReads';
import {
  loadXScienceRuntimeSnapshot,
  subscribeXScienceRuntimeInvalidation,
} from '@/renderer/services/xScienceRuntimeOperations';
import type { XScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import type { XScienceAsideReceipt } from '@/renderer/services/xScienceAside';
import { useCallback, useEffect, useRef, useState } from 'react';

export type AsideEntry =
  | { id: string; kind: 'message'; role: 'user' | 'assistant'; text: string }
  | { id: string; kind: 'thinking' | 'status'; text: string }
  | { id: string; kind: 'tool'; title: string; status: string };
type View = {
  owner: string;
  entries: AsideEntry[];
  snapshot: XScienceRuntimeSnapshot | null;
  loading: boolean;
  error: boolean;
  truncated: boolean;
};

const record = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
const text = (value: unknown) => (typeof value === 'string' ? value : '');
const isInput = (message: TMessage, id: string) =>
  message.position === 'right' && (message.msg_id === id || message.id === id);

/** Canonical input identity separates the inherited parent snapshot from actual side-chat activity. */
export function projectAsideEntries(messages: readonly TMessage[], frame: string, input: string): AsideEntry[] | null {
  const own = messages.filter((message) => message.conversation_id === frame && message.hidden !== true);
  const boundary = own.findIndex((message) => isInput(message, input));
  if (boundary < 0) return null;
  return projectMessages(own.slice(boundary));
}

function projectMessages(messages: readonly TMessage[]): AsideEntry[] {
  return messages.flatMap((message): AsideEntry[] => {
    if (message.type === 'text')
      return [
        {
          id: message.id,
          kind: 'message',
          role: message.position === 'right' ? 'user' : 'assistant',
          text: message.content.content,
        },
      ];
    if (message.type === 'thinking') return [{ id: message.id, kind: 'thinking', text: message.content.content }];
    if (message.type === 'tool_group')
      return message.content.map((tool) => ({
        id: tool.call_id,
        kind: 'tool',
        title: tool.description || tool.name,
        status: tool.status,
      }));
    if (message.type === 'tool_call' || message.type === 'acp_tool_call') {
      const content = record(message.content);
      const update = record(content.update);
      return [
        {
          id: message.id,
          kind: 'tool',
          title: text(content.description) || text(content.name) || text(update.title),
          status: text(content.status) || text(update.status),
        },
      ];
    }
    if (message.type === 'tips') return [{ id: message.id, kind: 'status', text: message.content.content }];
    return [];
  });
}

/** Read-only observer. No model dispatch, final-output polling or execution timeout lives here. */
export function useAsideRuntimeView(receipt: XScienceAsideReceipt | null, enabled: boolean, generation: number) {
  const account = getRendererAccountScopeToken();
  const owner = JSON.stringify([account, generation, receipt?.frameId, receipt?.intentId]);
  const currentOwner = useRef(owner);
  currentOwner.current = owner;
  const [view, setView] = useState<View>({
    owner: '',
    entries: [],
    snapshot: null,
    loading: false,
    error: false,
    truncated: false,
  });
  const refreshRef = useRef<() => void>(() => {});

  useEffect(() => {
    if (!enabled || !receipt) {
      refreshRef.current = () => {};
      return;
    }
    let active = true;
    let inFlight = false;
    let trailing = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | null = null;
    let readDeadline: ReturnType<typeof setTimeout> | undefined;
    let messages: TMessage[] = [];
    let snapshot: XScienceRuntimeSnapshot | null = null;
    let branch: string | undefined;
    let boundaryProved = false;
    let truncated = false;
    const isCurrent = () => active && currentOwner.current === owner && getRendererAccountScopeToken() === account;
    const publish = (loading: boolean, error: boolean) => {
      if (isCurrent()) setView({ owner, entries: projectMessages(messages), snapshot, loading, error, truncated });
    };
    const schedule = () => {
      if (!isCurrent() || timer !== undefined) return;
      timer = setTimeout(() => {
        timer = undefined;
        void read();
      }, 750);
    };
    const read = async () => {
      if (!isCurrent()) return;
      if (inFlight) {
        trailing = true;
        return;
      }
      inFlight = true;
      controller = new AbortController();
      const readController = controller;
      // A disconnected observation may time out; execution and its owned
      // identity do not. Reconnect remains possible after this read settles.
      readDeadline = setTimeout(() => readController.abort(), 15_000);
      if (!snapshot) publish(true, false);
      try {
        invalidateXScienceFrameReads(receipt.frameId);
        const [page, nextSnapshot] = await Promise.all([
          requestConversationMessages(
            { conversation_id: receipt.frameId, limit: 200, content_mode: 'compact' },
            controller.signal
          ),
          loadXScienceRuntimeSnapshot(receipt.frameId, { signal: controller.signal }),
        ]);
        if (!isCurrent()) return;
        if (branch && page.branch_id && page.branch_id !== branch) {
          boundaryProved = false;
          messages = [];
        }
        branch = page.branch_id;
        const normalized = normalizeDbMessagePage(page.items, page.through_publication_sequence).filter(
          (message) => message.conversation_id === receipt.frameId && message.hidden !== true
        );
        if (!boundaryProved) {
          let anchored = normalized;
          let boundary = anchored.findIndex((message) => isInput(message, receipt.inputMessageId));
          if (boundary < 0) {
            const anchor = await requestConversationMessages(
              {
                conversation_id: receipt.frameId,
                anchor_message_id: receipt.inputMessageId,
                limit: 200,
                content_mode: 'compact',
                ...(branch ? { branch_id: branch } : {}),
              },
              controller.signal
            );
            if (!isCurrent()) return;
            if (branch && anchor.branch_id !== branch) throw new Error('aside_history_owner_changed');
            anchored = normalizeDbMessagePage(anchor.items, anchor.through_publication_sequence).filter(
              (message) => message.conversation_id === receipt.frameId && message.hidden !== true
            );
            boundary = anchored.findIndex((message) => isInput(message, receipt.inputMessageId));
          }
          if (boundary < 0) throw new Error('aside_input_history_unavailable');
          messages = anchored.slice(boundary);
          boundaryProved = true;
        }
        messages = mergeLoadedPageWithCurrent(receipt.frameId, normalized, messages, true, true);
        const boundary = messages.findIndex((message) => isInput(message, receipt.inputMessageId));
        if (boundary >= 0) messages = messages.slice(boundary);
        if (messages.length > 1000) {
          messages = messages.slice(-1000);
          truncated = true;
        }
        snapshot = nextSnapshot;
        publish(false, false);
      } catch {
        if (isCurrent()) publish(false, true);
      } finally {
        if (readDeadline !== undefined) clearTimeout(readDeadline);
        readDeadline = undefined;
        inFlight = false;
        controller = null;
        if (
          isCurrent() &&
          (trailing ||
            snapshot?.canCancel ||
            snapshot?.runtimeActive === true ||
            (snapshot?.status === 'processing' && !snapshot.canResume))
        )
          schedule();
        trailing = false;
      }
    };
    refreshRef.current = () => {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      void read();
    };
    const release = subscribeXScienceRuntimeInvalidation(receipt.frameId, schedule);
    const stream = ipcBridge.acpConversation.responseStream.on((event) => {
      if (event.conversation_id === receipt.frameId) schedule();
    });
    const users = ipcBridge.acpConversation.userCreated.on((event) => {
      if (event.conversation_id === receipt.frameId) schedule();
    });
    void read();
    return () => {
      active = false;
      controller?.abort();
      if (readDeadline !== undefined) clearTimeout(readDeadline);
      if (timer !== undefined) clearTimeout(timer);
      refreshRef.current = () => {};
      users();
      stream();
      release();
    };
  }, [account, enabled, generation, owner, receipt]);

  const refresh = useCallback(() => refreshRef.current(), []);
  return {
    ...(view.owner === owner && enabled
      ? view
      : { entries: [], snapshot: null, loading: Boolean(enabled && receipt), error: false, truncated: false }),
    refresh,
  };
}

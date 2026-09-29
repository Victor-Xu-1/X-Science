import '@arco-design/web-react/es/_util/react-19-adapter';
import '@arco-design/web-react/dist/css/arco.css';
import 'uno.css';
import '@/renderer/styles/themes/index.css';
import '@/renderer/styles/workspace-theme.css';
import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Virtuoso, type VirtuosoHandle } from 'react-virtuoso';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import zhConversation from '@/renderer/services/i18n/locales/zh-CN/conversation.json';
import ConversationTurnRail from '@/renderer/pages/conversation/components/ConversationTitleMinimap/ConversationTurnRail';
import { buildTurnPreview } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/minimapUtils';
import type { TMessage } from '@/common/chat/chatLib';
import {
  isNearMessageBoundary,
  navigateVirtualMessage,
} from '@/renderer/pages/conversation/Messages/virtualMessageNavigation';
import { useAnchorViewport } from '@/renderer/pages/conversation/Messages/useAnchorViewport';
import { useConversationScrollController } from '@/renderer/pages/conversation/Messages/useConversationScrollController';
import { MessageVirtualItem } from '@/renderer/pages/conversation/Messages/MessageVirtualItem';

await i18n.use(initReactI18next).init({
  lng: 'zh-CN',
  resources: { 'zh-CN': { translation: { conversation: zhConversation } } },
  interpolation: { escapeValue: false },
});
const { count, windowed } = await fetch('/__turn_rail_data__').then((response) => response.json());
function Fixture() {
  const allMessages = useMemo<TMessage[]>(
    () =>
      Array.from({ length: count }, (_, index) => [
        {
          id: `q${index}`,
          conversation_id: 'fixture',
          type: 'text' as const,
          position: 'right' as const,
          content: { content: `Question ${index + 1} — browser regression fixture, not a scientific result` },
        },
        {
          id: `a${index}`,
          conversation_id: 'fixture',
          type: 'text' as const,
          position: 'left' as const,
          content: {
            content: `Answer ${index + 1}. A two-line preview remains associated with its question while virtual rows enter and leave the viewport.`,
          },
        },
      ]).flat(),
    []
  );
  const [range, setRange] = useState([windowed ? Math.max(0, count * 2 - 8) : 0, count * 2]);
  const [afterLoads, setAfterLoads] = useState(0);
  const [hydrated, setHydrated] = useState(true);
  useEffect(() => {
    if (hydrated) return;
    const timer = setTimeout(() => setHydrated(true), 1500);
    return () => clearTimeout(timer);
  }, [hydrated]);
  const afterLoading = useRef(false);
  useLayoutEffect(() => {
    afterLoading.current = false;
  }, [range]);
  const messages = allMessages.slice(range[0], range[1]);
  const turns = useMemo(() => buildTurnPreview(allMessages), [allMessages]);
  const anchorViewport = useAnchorViewport(
    'fixture',
    messages.map((message) => `text:${message.id}`)
  );
  const list = useRef<VirtuosoHandle>(null);
  const [viewport, setViewport] = useState<HTMLDivElement | null>(null);
  const lastUser = messages.findLast((message) => message.position === 'right');
  const controller = useConversationScrollController({
    conversationId: 'fixture',
    messages,
    itemCount: messages.length,
    lastUserMessageId: lastUser?.id ?? null,
    lastUserRowIndex: messages.findIndex((message) => message.id === lastUser?.id),
    scrollToBottomItem: (behavior) => {
      if (!list.current) return false;
      list.current.scrollTo({ top: Number.MAX_SAFE_INTEGER, behavior });
      return true;
    },
    scrollMessageIntoView: (id, options) => {
      const index = messages.findIndex((message) => message.id === id);
      if (!list.current || index < 0) return false;
      navigateVirtualMessage(list.current, viewport, id, index, options);
      return true;
    },
  });
  const loadAfter = () => {
    if (
      windowed &&
      range[1] < allMessages.length &&
      !afterLoading.current &&
      controller.canLoadNextPage() &&
      isNearMessageBoundary(viewport, 'end')
    ) {
      afterLoading.current = true;
      setAfterLoads((value) => value + 1);
      setRange((current) => [current[0], Math.min(allMessages.length, current[1] + 80)]);
    }
  };
  return (
    <main style={{ height: '80vh', width: 'calc(100% - 160px)', margin: '50px 80px' }}>
      <button data-testid='before-navigation'>Before navigation</button>
      <output data-testid='automatic-after-loads'>{afterLoads}</output>
      <Virtuoso
        components={{ Item: MessageVirtualItem }}
        key={anchorViewport.revision}
        ref={list}
        firstItemIndex={1000000}
        increaseViewportBy={{ top: 600, bottom: 900 }}
        initialTopMostItemIndex={
          windowed && anchorViewport.revision === 0
            ? { index: messages.length - 1, align: 'end' }
            : anchorViewport.initial
        }
        scrollerRef={(node) => setViewport(node instanceof HTMLDivElement ? node : null)}
        data={messages}
        followOutput={controller.followOutput}
        atBottomStateChange={controller.handleAtBottomStateChange}
        totalListHeightChanged={controller.handleTotalListHeightChanged}
        style={{ height: '100%' }}
        endReached={loadAfter}
        onScroll={loadAfter}
        onWheel={(event) => controller.handleUserScrollIntent(event.deltaY < 0 ? 'away-from-tail' : 'toward-tail')}
        itemContent={(_, message) => (
          <article
            data-source-message-id={message.id}
            style={{
              marginTop: 10,
              height: message.position === 'right' ? 60 : windowed && hydrated ? 760 : 260,
              padding: 16,
              boxSizing: 'border-box',
            }}
          >
            {message.type === 'text' ? message.content.content : ''}
          </article>
        )}
      />
      <ConversationTurnRail
        items={turns}
        viewport={viewport}
        onJump={(id) => {
          controller.retainMessageAnchor(id);
          if (!messages.some((message) => message.id === id)) {
            const start = Math.max(0, allMessages.findIndex((message) => message.id === id) - 4);
            setRange([start, Math.min(allMessages.length, start + 8)]);
            setHydrated(false);
            anchorViewport.reset(`text:${id}`);
            return;
          }
          if (list.current)
            navigateVirtualMessage(
              list.current,
              viewport,
              id,
              messages.findIndex((message) => message.id === id),
              {
                block: 'start',
                behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth',
              }
            );
        }}
      />
    </main>
  );
}
createRoot(document.getElementById('root')!).render(<Fixture />);

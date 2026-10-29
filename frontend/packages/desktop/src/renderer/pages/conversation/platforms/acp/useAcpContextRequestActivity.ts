import { useCallback, useRef, useState } from 'react';
import type { TChatConversation } from '@/common/config/storage';
import type { ContextRequestActivity } from '@/renderer/components/xScience/runtime/contextRequestActivity';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

/** Local presentation bridge for the existing context reader; never a second request owner. */
export function useAcpContextRequestActivity(conversationId: string, conversation?: TChatConversation) {
  const frameIncarnationId =
    conversation?.type === 'acp' && conversation.id === conversationId
      ? conversation.extra.frame_incarnation_id
      : undefined;
  const scope = JSON.stringify([getRendererAccountScopeToken(), conversationId, frameIncarnationId]);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const [activity, setActivity] = useState<{ scope: string; value: ContextRequestActivity | null }>();
  const onRequestActivityChange = useCallback(
    (value: ContextRequestActivity | null) => {
      if (currentScope.current !== scope) return;
      setActivity((previous) =>
        previous?.scope === scope && JSON.stringify(previous.value) === JSON.stringify(value)
          ? previous
          : { scope, value }
      );
    },
    [scope]
  );
  return {
    frameIncarnationId,
    panelProps: { conversationId, onRequestActivityChange },
    runtimeProps: {
      contextActivity: activity?.scope === scope ? activity.value : null,
      contextFrameIncarnationId: frameIncarnationId,
    },
  };
}

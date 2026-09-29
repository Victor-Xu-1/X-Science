import { useEffect, useRef, type TouchEventHandler } from 'react';
import type { ConversationScrollIntent } from './useConversationScrollController';

/** Finger movement is opposite to viewport scrolling. Ignore taps and pinches. */
export function useTouchScrollIntent(owner: string, onIntent: (intent: ConversationScrollIntent) => void) {
  const previous = useRef<{ id: number; y: number } | null>(null);
  useEffect(() => {
    previous.current = null;
  }, [owner]);
  const reset = () => {
    previous.current = null;
  };
  const onTouchStart: TouchEventHandler<HTMLElement> = (event) => {
    const touch = event.touches.length === 1 ? event.touches[0] : undefined;
    previous.current = touch ? { id: touch.identifier, y: touch.clientY } : null;
  };
  const onTouchMove: TouchEventHandler<HTMLElement> = (event) => {
    const touch = event.touches.length === 1 ? event.touches[0] : undefined;
    const last = previous.current;
    if (!touch || !last || touch.identifier !== last.id) {
      reset();
      return;
    }
    previous.current = { id: touch.identifier, y: touch.clientY };
    if (touch.clientY < last.y) onIntent('toward-tail');
    else if (touch.clientY > last.y) onIntent('away-from-tail');
  };
  return { onTouchStart, onTouchMove, onTouchEnd: reset, onTouchCancel: reset };
}

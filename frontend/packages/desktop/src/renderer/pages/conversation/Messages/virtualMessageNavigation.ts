import type { VirtuosoHandle } from 'react-virtuoso';

/** Render overscan can reach a page edge far before the user's viewport. */
export function isNearMessageBoundary(viewport: HTMLElement | null, edge: 'start' | 'end'): boolean {
  if (!viewport || viewport.clientHeight <= 0 || viewport.scrollHeight <= 0) return false;
  const distance =
    edge === 'start' ? viewport.scrollTop : viewport.scrollHeight - viewport.clientHeight - viewport.scrollTop;
  return distance <= 128;
}

/** Virtuoso remains the geometry owner; unseen variable-height rows need its
 * measured seek path, not a smooth animation to an estimated pixel offset. */
export function navigateVirtualMessage(
  list: VirtuosoHandle,
  viewport: HTMLElement | null,
  messageId: string,
  index: number,
  options?: { behavior?: ScrollBehavior; block?: ScrollLogicalPosition }
): void {
  const target =
    viewport &&
    Array.from(viewport.querySelectorAll<HTMLElement>('[data-source-message-id]')).find(
      (element) => element.dataset.sourceMessageId === messageId
    );
  const nearby =
    viewport &&
    target &&
    Math.abs(target.getBoundingClientRect().top - viewport.getBoundingClientRect().top) <= viewport.clientHeight * 2;
  list.scrollToIndex({
    index,
    align: options?.block === 'center' ? 'center' : options?.block === 'end' ? 'end' : 'start',
    behavior: options?.behavior === 'smooth' && nearby ? 'smooth' : 'auto',
  });
}

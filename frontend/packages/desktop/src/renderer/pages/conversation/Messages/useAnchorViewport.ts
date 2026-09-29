import { useCallback, useMemo, useState } from 'react';
import type { IndexLocationWithAlign } from 'react-virtuoso';

/** A fetched anchor window is a new viewport, unlike ordinary prepend/append.
 * Seed Virtuoso at its target instead of reusing the previous window's tail
 * measurements and racing an imperative jump against automatic pagination. */
export function useAnchorViewport(conversationId: string, rowKeys: readonly string[]) {
  const [seed, setSeed] = useState<{
    owner: string;
    revision: number;
    key: string;
    align: 'start' | 'center' | 'end';
  }>();
  const reset = useCallback(
    (key: string, align: ScrollLogicalPosition = 'start') => {
      setSeed((previous) => ({
        owner: conversationId,
        revision: (previous?.revision ?? 0) + 1,
        key,
        align: align === 'center' || align === 'end' ? align : 'start',
      }));
    },
    [conversationId]
  );
  const current = seed?.owner === conversationId ? seed : undefined;
  const index = current ? rowKeys.indexOf(current.key) : -1;
  const hasSeed = Boolean(current);
  const initial = useMemo<IndexLocationWithAlign | number>(
    () =>
      hasSeed
        ? {
            index: Math.max(0, index),
            align: current?.align ?? 'start',
            behavior: 'auto',
          }
        : 0,
    [hasSeed, index, current?.align]
  );
  return { reset, revision: current?.revision ?? 0, initial };
}

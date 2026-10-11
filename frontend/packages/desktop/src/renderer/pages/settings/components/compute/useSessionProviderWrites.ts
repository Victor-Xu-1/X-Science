import { useLayoutEffect, useMemo, useRef, useState } from 'react';
import { setSynonBiomedSessionComputeProvider } from '@/renderer/services/synonBiomedCompute';
import type { useComputeJobReads } from './useComputeJobReads';

type Providers = ReturnType<typeof useComputeJobReads>['providers'];
type View = { owner: object; rootFrameId: string | null; providers: Providers; onFailure: (error: unknown) => void };

/** A write belongs to a session root even while its job dialog closes or changes. */
export function useSessionProviderWrites(
  jobId: string | null,
  rootFrameId: string | null,
  providers: Providers,
  onFailure: (error: unknown) => void
) {
  const owner = useMemo(() => ({}), [jobId]);
  const current = useRef<View | null>(null);
  const writes = useRef(new Map<string, string>());
  const [pending, setPending] = useState(new Map<string, string>());
  useLayoutEffect(() => {
    current.current = { owner, rootFrameId, providers, onFailure };
  });
  useLayoutEffect(
    () => () => {
      current.current = null;
    },
    []
  );

  const toggle = async (providerName: string, checked: boolean) => {
    const view = current.current;
    const root = view?.rootFrameId;
    if (!view || !root || view.providers.status !== 'ready' || writes.current.has(root)) return;
    writes.current.set(root, providerName);
    setPending(new Map(writes.current));
    const previous = view.providers.data ?? [];
    view.providers.replace(
      checked ? Array.from(new Set([...previous, providerName])) : previous.filter((name) => name !== providerName)
    );
    try {
      await setSynonBiomedSessionComputeProvider(root, providerName, checked);
      // A reopened job can already have read the pre-write value. Replace that
      // read with a fresh server read, never project an old optimistic snapshot.
      if (current.current?.rootFrameId === root) await current.current.providers.refresh(true);
    } catch (error) {
      if (current.current?.owner === view.owner) {
        view.providers.replace(previous);
        current.current.onFailure(error);
      } else if (current.current?.rootFrameId === root) {
        await current.current.providers.refresh(true);
      }
    } finally {
      writes.current.delete(root);
      if (current.current) setPending(new Map(writes.current));
    }
  };

  return { pendingProvider: rootFrameId ? (pending.get(rootFrameId) ?? null) : null, toggle };
}

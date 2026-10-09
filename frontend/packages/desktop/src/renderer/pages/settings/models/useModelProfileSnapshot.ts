import { useCallback, useEffect, useRef, useState } from 'react';
import { loadSynonBiomedLlmProviders, type SynonBiomedLlmProvidersSnapshot } from '@/renderer/services/synonBiomedLlm';

type ReadState = {
  snapshot: SynonBiomedLlmProvidersSnapshot | null;
  loading: boolean;
  failed: boolean;
};

export function useModelProfileSnapshot() {
  const [state, setState] = useState<ReadState>({ snapshot: null, loading: true, failed: false });
  const activeView = useRef(false);
  const epoch = useRef(0);
  const request = useRef<AbortController | null>(null);
  const isActive = useCallback(() => activeView.current, []);

  const load = useCallback(async () => {
    if (!activeView.current) return false;
    const owner = ++epoch.current;
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    const ownsRead = () => activeView.current && epoch.current === owner && !controller.signal.aborted;
    setState((current) => ({ ...current, loading: true, failed: false }));
    try {
      const snapshot = await loadSynonBiomedLlmProviders((input, init) =>
        fetch(input, { ...init, signal: controller.signal })
      );
      if (!ownsRead()) return false;
      setState({ snapshot, loading: false, failed: false });
      return true;
    } catch {
      if (!ownsRead()) return false;
      // Provider error bodies can contain sensitive diagnostics; presentation
      // is deliberately generic and the last successful snapshot is retained.
      setState((current) => ({ ...current, loading: false, failed: true }));
      return false;
    } finally {
      if (request.current === controller) request.current = null;
    }
  }, []);

  useEffect(() => {
    activeView.current = true;
    void load();
    return () => {
      activeView.current = false;
      ++epoch.current;
      request.current?.abort();
      request.current = null;
    };
  }, [load]);

  return { ...state, load, isActive };
}

import { useCallback, useLayoutEffect, useRef, useState } from 'react';

type ReadState<T> = {
  key: string | null;
  data: T | null;
  status: 'idle' | 'loading' | 'ready' | 'failed';
};

/** Independent, owner-bound reads. Retrying a failed section never replays a write. */
export function useSettingsReadResource<T>(key: string | null, load: () => Promise<T>) {
  const [state, setState] = useState<ReadState<T>>({ key, data: null, status: key ? 'loading' : 'idle' });
  const owner = useRef<string | null>(null);
  const generation = useRef(0);
  const inFlight = useRef<Promise<void> | null>(null);

  const refresh = useCallback(
    (afterWrite = false): Promise<void> => {
      if (!key || owner.current !== key) return Promise.resolve();
      if (inFlight.current && !afterWrite) return inFlight.current;
      const current = ++generation.current;
      setState((previous) => ({ key, data: previous.key === key ? previous.data : null, status: 'loading' }));
      const request = Promise.resolve()
        .then(load)
        .then(
          (data) => {
            if (owner.current === key && generation.current === current) setState({ key, data, status: 'ready' });
          },
          () => {
            if (owner.current === key && generation.current === current) {
              setState((previous) => ({ ...previous, status: 'failed' }));
            }
          }
        )
        .finally(() => {
          if (generation.current === current) inFlight.current = null;
        });
      inFlight.current = request;
      return request;
    },
    [key, load]
  );

  const replace = useCallback(
    (data: T) => {
      if (!key || owner.current !== key) return;
      generation.current++;
      inFlight.current = null;
      setState({ key, data, status: 'ready' });
    },
    [key]
  );

  useLayoutEffect(() => {
    owner.current = key;
    setState({ key, data: null, status: key ? 'loading' : 'idle' });
    void refresh();
    return () => {
      owner.current = null;
      generation.current++;
      inFlight.current = null;
    };
  }, [key, refresh]);

  const visible =
    state.key === key ? state : { key, data: null, status: key ? ('loading' as const) : ('idle' as const) };
  return { ...visible, refresh, replace };
}

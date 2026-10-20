/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useId, useMemo, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import {
  createXScienceSessionDefaults,
  loadXScienceSessionOptions,
  loadXScienceSessionMemoryPreference,
  updateXScienceSessionConfig,
  type XScienceSessionMemoryControl,
  type XScienceSessionOptions,
} from '@/renderer/services/xScienceSessionOptions';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';

const SESSION_OPTIONS_RETRY_DELAYS_MS = [1_000, 2_000, 5_000, 10_000, 30_000] as const;

export type XScienceSessionOptionsLoadState = {
  conversationId: string;
  status: 'loading' | 'ready' | 'retrying';
};

type XScienceSessionOptionsState = {
  options: XScienceSessionOptions;
  setOptions: Dispatch<SetStateAction<XScienceSessionOptions>>;
  loadState: XScienceSessionOptionsLoadState;
  memory: XScienceSessionMemoryControl;
};

type MemoryIdentity = {
  ownerId: string;
  frameIncarnationId?: string;
  expectedRootFrameId?: string;
  projectId?: string;
  onMutationStarted?: () => void;
};

/**
 * Session options are an optional control-plane projection. They retry for as
 * long as the route remains mounted, but never own the core composer gate.
 */
export function useXScienceSessionOptionsState(
  conversationId: string,
  agentName?: string,
  identity?: MemoryIdentity
): XScienceSessionOptionsState {
  const [options, setOptions] = useState<XScienceSessionOptions>(() => createXScienceSessionDefaults(agentName));
  const [loadState, setLoadState] = useState<XScienceSessionOptionsLoadState>({
    conversationId,
    status: 'loading',
  });
  const account = getRendererAccountScopeToken();
  const scope = useMemo(
    () => ({}),
    [
      account,
      conversationId,
      identity?.ownerId,
      identity?.frameIncarnationId,
      identity?.expectedRootFrameId,
      identity?.projectId,
    ]
  );
  const owner = useRef(scope);
  owner.current = scope;
  const mounted = useRef(false);
  const resetId = useId();
  const operation = useRef<{ scope: object; controller: AbortController } | null>(null);
  const generation = useRef(0);
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const onMutationStarted = useRef(identity?.onMutationStarted);
  onMutationStarted.current = identity?.onMutationStarted;
  type Observation = {
    scope: object;
    status: XScienceSessionMemoryControl['status'];
    rootFrameId?: string;
    error?: unknown;
  };
  const initial: Observation = { scope, status: 'loading' };
  const latest = useRef(initial);
  const [observation, setObservation] = useState(initial);
  const current = useCallback(
    () => mounted.current && owner.current === scope && getRendererAccountScopeToken() === account,
    [account, scope]
  );
  const publish = useCallback(
    (value: Observation) => {
      if (!current()) return;
      latest.current = value;
      setObservation(value);
    },
    [current]
  );
  const observe = useCallback(
    async (request: { scope: object; controller: AbortController }, sequence: number) => {
      const snapshot = await loadXScienceSessionMemoryPreference(conversationId, {
        expectedRootFrameId: identity?.expectedRootFrameId,
        projectId: identity?.projectId,
        signal: request.controller.signal,
      });
      if (!current() || operation.current !== request || generation.current !== sequence) return;
      optionsRef.current = { ...optionsRef.current, memory: snapshot.memory };
      setOptions((value) => ({ ...value, memory: snapshot.memory }));
      publish({ scope, status: 'ready', rootFrameId: snapshot.rootFrameId });
    },
    [conversationId, current, identity?.expectedRootFrameId, identity?.projectId, publish, scope]
  );
  const refresh = useCallback(() => {
    if (!current() || (latest.current.scope === scope && ['writing', 'readback'].includes(latest.current.status)))
      return;
    operation.current?.controller.abort();
    const request = { scope, controller: new AbortController() };
    operation.current = request;
    const sequence = ++generation.current;
    publish({
      scope,
      status: 'loading',
      rootFrameId: latest.current.scope === scope ? latest.current.rootFrameId : undefined,
    });
    void observe(request, sequence)
      .catch((error: unknown) => {
        if (current() && operation.current === request && generation.current === sequence)
          publish({ scope, status: 'unknown', error });
      })
      .finally(() => {
        if (operation.current === request) operation.current = null;
      });
  }, [current, observe, publish, scope]);
  const update = useCallback(
    async (value: boolean) => {
      if (!current() || latest.current.scope !== scope || latest.current.status !== 'ready') return;
      operation.current?.controller.abort();
      const request = { scope, controller: new AbortController() };
      operation.current = request;
      const sequence = ++generation.current;
      const rootFrameId = latest.current.rootFrameId;
      publish({ scope, status: 'writing', rootFrameId });
      try {
        onMutationStarted.current?.();
        await updateXScienceSessionConfig(conversationId, { memory: value }, fetch, {
          rootFrameId,
          signal: request.controller.signal,
        });
        if (!current() || operation.current !== request || generation.current !== sequence) return;
        publish({ scope, status: 'readback', rootFrameId });
        await observe(request, sequence);
      } catch (error) {
        if (!current() || operation.current !== request || generation.current !== sequence) return;
        // Keep the last snapshot, but it is not current authority after any uncertain write.
        publish({ scope, status: 'unknown', rootFrameId, error });
        throw error;
      } finally {
        if (operation.current === request) operation.current = null;
      }
    },
    [conversationId, current, observe, publish, scope]
  );

  useEffect(() => {
    mounted.current = true;
    refresh();
    const unregister = registerRendererAccountReset(`session-memory:${resetId}`, () => {
      operation.current?.controller.abort();
      ++generation.current;
      setObservation({ scope, status: 'loading' });
    });
    return () => {
      mounted.current = false;
      if (operation.current?.scope === scope) {
        operation.current.controller.abort();
        operation.current = null;
      }
      unregister();
    };
  }, [refresh, resetId, scope]);

  useEffect(() => {
    let active = true;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;

    setOptions((value) => ({ ...createXScienceSessionDefaults(agentName), memory: value.memory }));
    setLoadState({ conversationId, status: 'loading' });

    const load = () => {
      void loadXScienceSessionOptions(conversationId)
        .then((loaded) => {
          if (!active) return;
          setOptions((value) => ({ ...loaded, memory: value.memory }));
          setLoadState({ conversationId, status: 'ready' });
        })
        .catch(() => {
          if (!active) return;
          const delay = SESSION_OPTIONS_RETRY_DELAYS_MS[Math.min(attempt, SESSION_OPTIONS_RETRY_DELAYS_MS.length - 1)];
          attempt += 1;
          setLoadState({ conversationId, status: 'retrying' });
          retryTimer = setTimeout(() => {
            retryTimer = null;
            if (!active) return;
            load();
          }, delay);
        });
    };

    load();
    return () => {
      active = false;
      if (retryTimer !== null) clearTimeout(retryTimer);
    };
  }, [agentName, conversationId]);

  const status = observation.scope === scope ? observation.status : 'loading';
  return {
    options,
    setOptions,
    loadState,
    memory: {
      status,
      pending: status === 'writing' || status === 'readback',
      error: observation.scope === scope ? observation.error : undefined,
      current,
      refresh,
      update,
      capture: () =>
        current() && latest.current.scope === scope && latest.current.status === 'ready'
          ? optionsRef.current.memory
          : null,
    },
  };
}

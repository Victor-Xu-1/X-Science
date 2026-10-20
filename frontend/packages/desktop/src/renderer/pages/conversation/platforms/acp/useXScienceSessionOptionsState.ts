/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
  type RefObject,
} from 'react';
import {
  createXScienceSessionDefaults,
  loadXScienceSessionOptions,
  loadXScienceSessionMemoryPreference,
  loadXScienceSessionAutoReviewPreference,
  updateXScienceSessionConfig,
  type XScienceSessionPreferenceControl,
  type XScienceSessionOptions,
} from '@/renderer/services/xScienceSessionOptions';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';

const SESSION_OPTIONS_RETRY_DELAYS_MS = [1_000, 2_000, 5_000, 10_000, 30_000] as const;
type PreferenceKey = 'memory' | 'autoReview';
type PreferenceIdentity = {
  ownerId: string;
  frameIncarnationId?: string;
  expectedRootFrameId?: string;
  projectId?: string;
  onMutationStarted?: () => void;
  onReviewMutationStarted?: () => void;
};
export type XScienceSessionOptionsLoadState = {
  conversationId: string;
  status: 'loading' | 'ready' | 'retrying';
};
type OptionsSetter = Dispatch<SetStateAction<XScienceSessionOptions>>;

/** One session value/identity owner; observation metadata never becomes a second preference store. */
export function useXScienceSessionOptionsState(
  conversationId: string,
  agentName?: string,
  identity?: PreferenceIdentity
) {
  const [options, setOptions] = useState<XScienceSessionOptions>(() => createXScienceSessionDefaults(agentName));
  const optionsRef = useRef(options);
  optionsRef.current = options;
  const [loadState, setLoadState] = useState<XScienceSessionOptionsLoadState>({ conversationId, status: 'loading' });
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
  const current = useCallback(
    () => owner.current === scope && getRendererAccountScopeToken() === account,
    [account, scope]
  );
  const memory = usePreferenceObservation('memory', conversationId, scope, current, optionsRef, setOptions, identity);
  const autoReview = usePreferenceObservation(
    'autoReview',
    conversationId,
    scope,
    current,
    optionsRef,
    setOptions,
    identity
  );

  useEffect(() => {
    let active = true;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;
    setOptions((value) => ({
      ...createXScienceSessionDefaults(agentName),
      memory: value.memory,
      autoReview: value.autoReview,
    }));
    setLoadState({ conversationId, status: 'loading' });
    const load = () => {
      void loadXScienceSessionOptions(conversationId)
        .then((loaded) => {
          if (!active) return;
          // Fresh preference owners, including unknown snapshots, cannot be overwritten by this legacy load.
          setOptions((value) => ({ ...loaded, memory: value.memory, autoReview: value.autoReview }));
          setLoadState({ conversationId, status: 'ready' });
        })
        .catch(() => {
          if (!active) return;
          const delay =
            SESSION_OPTIONS_RETRY_DELAYS_MS[Math.min(attempt++, SESSION_OPTIONS_RETRY_DELAYS_MS.length - 1)];
          setLoadState({ conversationId, status: 'retrying' });
          retryTimer = setTimeout(() => {
            retryTimer = null;
            if (active) load();
          }, delay);
        });
    };
    load();
    return () => {
      active = false;
      if (retryTimer !== null) clearTimeout(retryTimer);
    };
  }, [agentName, conversationId]);
  return { options, setOptions, loadState, memory, autoReview };
}

type Observation = {
  scope: object;
  status: XScienceSessionPreferenceControl['status'];
  rootFrameId?: string;
  error?: unknown;
};
type Operation = { scope: object; controller: AbortController };

/** Shared lifecycle machinery, with independent operations under the same captured session identity. */
function usePreferenceObservation(
  key: PreferenceKey,
  conversationId: string,
  scope: object,
  identityCurrent: () => boolean,
  options: RefObject<XScienceSessionOptions>,
  setOptions: OptionsSetter,
  identity?: PreferenceIdentity
): XScienceSessionPreferenceControl {
  const mounted = useRef(false);
  const resetId = useId();
  const operation = useRef<Operation | null>(null);
  const generation = useRef(0);
  const onMutationStarted = useRef<(() => void) | undefined>(undefined);
  onMutationStarted.current = key === 'memory' ? identity?.onMutationStarted : identity?.onReviewMutationStarted;
  const [observation, setObservation] = useState<Observation>({ scope, status: 'loading' });
  const latest = useRef(observation);
  const current = useCallback(() => mounted.current && identityCurrent(), [identityCurrent]);
  const publish = useCallback(
    (value: Observation) => {
      if (!current()) return;
      latest.current = value;
      setObservation(value);
    },
    [current]
  );
  const observe = useCallback(
    async (request: Operation, sequence: number) => {
      const readOptions = {
        expectedRootFrameId: identity?.expectedRootFrameId,
        projectId: identity?.projectId,
        signal: request.controller.signal,
      };
      const snapshot =
        key === 'memory'
          ? await loadXScienceSessionMemoryPreference(conversationId, readOptions).then((value) => ({
              rootFrameId: value.rootFrameId,
              value: value.memory,
            }))
          : await loadXScienceSessionAutoReviewPreference(conversationId, readOptions).then((value) => ({
              rootFrameId: value.rootFrameId,
              value: value.autoReview,
            }));
      if (!current() || operation.current !== request || generation.current !== sequence) return;
      options.current = { ...options.current, [key]: snapshot.value };
      setOptions((value) => ({ ...value, [key]: snapshot.value }));
      publish({ scope, status: 'ready', rootFrameId: snapshot.rootFrameId });
    },
    [
      conversationId,
      current,
      identity?.expectedRootFrameId,
      identity?.projectId,
      key,
      options,
      publish,
      scope,
      setOptions,
    ]
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
        await updateXScienceSessionConfig(conversationId, { [key]: value }, fetch, {
          rootFrameId,
          signal: request.controller.signal,
        });
        if (!current() || operation.current !== request || generation.current !== sequence) return;
        publish({ scope, status: 'readback', rootFrameId });
        await observe(request, sequence);
      } catch (error) {
        if (!current() || operation.current !== request || generation.current !== sequence) return;
        // A GET can observe current state, but cannot settle or cancel a possibly late server write.
        publish({ scope, status: 'unknown', rootFrameId, error });
        throw error;
      } finally {
        if (operation.current === request) operation.current = null;
      }
    },
    [conversationId, current, key, observe, publish, scope]
  );
  useEffect(() => {
    mounted.current = true;
    refresh();
    const unregister = registerRendererAccountReset(`session-${key}:${resetId}`, () => {
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
  }, [key, refresh, resetId, scope]);
  const status = observation.scope === scope ? observation.status : 'loading';
  return {
    status,
    pending: status === 'writing' || status === 'readback',
    error: observation.scope === scope ? observation.error : undefined,
    current,
    refresh,
    update,
    capture: () =>
      current() && latest.current.scope === scope && latest.current.status === 'ready' ? options.current[key] : null,
  };
}

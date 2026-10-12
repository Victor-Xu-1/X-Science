/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useState, type Dispatch, type SetStateAction } from 'react';
import {
  createXScienceSessionDefaults,
  loadXScienceSessionOptions,
  type XScienceSessionOptions,
} from '@/renderer/services/xScienceSessionOptions';

const SESSION_OPTIONS_RETRY_DELAYS_MS = [1_000, 2_000, 5_000, 10_000, 30_000] as const;

export type XScienceSessionOptionsLoadState = {
  conversationId: string;
  status: 'loading' | 'ready' | 'retrying';
};

type XScienceSessionOptionsState = {
  options: XScienceSessionOptions;
  setOptions: Dispatch<SetStateAction<XScienceSessionOptions>>;
  loadState: XScienceSessionOptionsLoadState;
};

/**
 * Session options are an optional control-plane projection. They retry for as
 * long as the route remains mounted, but never own the core composer gate.
 */
export function useXScienceSessionOptionsState(
  conversationId: string,
  agentName?: string
): XScienceSessionOptionsState {
  const [options, setOptions] = useState<XScienceSessionOptions>(() => createXScienceSessionDefaults(agentName));
  const [loadState, setLoadState] = useState<XScienceSessionOptionsLoadState>({
    conversationId,
    status: 'loading',
  });

  useEffect(() => {
    let active = true;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;

    setOptions(createXScienceSessionDefaults(agentName));
    setLoadState({ conversationId, status: 'loading' });

    const load = () => {
      void loadXScienceSessionOptions(conversationId)
        .then((loaded) => {
          if (!active) return;
          setOptions(loaded);
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

  return { options, setOptions, loadState };
}

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useLayoutEffect, useRef, type RefObject } from 'react';
import type { ArtifactReadState } from './useArtifactMetadataReads';

/** A manual read may replace its own control, but cannot steal another view's focus. */
export function useArtifactRecoveryFocus(
  owner: string,
  status: ArtifactReadState<unknown>['status'],
  target: RefObject<HTMLElement | null>
) {
  const intent = useRef<string | null>(null);
  useLayoutEffect(() => {
    intent.current = null;
    return () => {
      intent.current = null;
    };
  }, [owner]);
  useLayoutEffect(() => {
    if (status === 'loading' || intent.current !== owner) return;
    intent.current = null;
    const destination = target.current;
    if (
      document.activeElement !== document.body ||
      !destination?.isConnected ||
      destination.closest('[hidden],[aria-hidden="true"],[inert]')
    )
      return;
    destination.focus({ preventScroll: true });
  }, [owner, status, target]);
  return useCallback(() => {
    intent.current = owner;
  }, [owner]);
}

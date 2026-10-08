/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useLayoutEffect, useRef } from 'react';

/** Fence one editor's requests without taking over its data or the server operation. */
export function useArtifactRequestScope(artifactId: string, versionId: string, anchorKey: string, instruction = '') {
  const epoch = useRef(0);
  const owner = useRef<readonly string[] | null>(null);
  const writing = useRef(false);
  useLayoutEffect(() => {
    epoch.current += 1;
    owner.current = [artifactId, versionId, anchorKey, instruction];
    writing.current = false;
    return () => {
      owner.current = null;
      epoch.current += 1;
    };
  }, [artifactId, versionId, anchorKey, instruction]);
  const begin = useCallback(() => {
    const current = owner.current;
    if (
      !current ||
      writing.current ||
      current[0] !== artifactId ||
      current[1] !== versionId ||
      current[2] !== anchorKey ||
      current[3] !== instruction
    )
      return null;
    writing.current = true;
    return epoch.current;
  }, [artifactId, versionId, anchorKey, instruction]);
  const isCurrent = useCallback((token: number) => owner.current !== null && epoch.current === token, []);
  const finish = useCallback(
    (token: number) => {
      if (isCurrent(token)) writing.current = false;
    },
    [isCurrent]
  );
  const close = useCallback(() => {
    owner.current = null;
    epoch.current += 1;
    writing.current = false;
  }, []);
  return { begin, isCurrent, finish, close };
}

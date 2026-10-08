/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useLayoutEffect, useRef, useState } from 'react';

type MediaKind = 'audio' | 'video';
type MediaStatus = 'loading' | 'ready' | 'failed';
type MediaOwner = { source: string; kind: MediaKind; attempt: number };

export function useNativeMediaRead(source: string, kind: MediaKind) {
  const elementRef = useRef<HTMLMediaElement | null>(null);
  const [attempt, setAttempt] = useState(0);
  const owner = { source, kind, attempt };
  const currentOwner = useRef(owner);
  const mounted = useRef(false);
  const [read, setRead] = useState<MediaOwner & { status: MediaStatus }>({
    ...owner,
    status: source ? 'loading' : 'failed',
  });

  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useLayoutEffect(() => {
    currentOwner.current = owner;
    const media = elementRef.current;
    const current = media?.getAttribute('src') === source;
    const status =
      !source || (current && media?.error) ? 'failed' : current && media!.readyState >= 1 ? 'ready' : 'loading';
    setRead({ ...owner, status });
  }, [source, kind, attempt]);

  useEffect(() => {
    const media = elementRef.current;
    if (!media) return;
    const pauseWhenHidden = () => {
      if (document.visibilityState === 'hidden') media.pause();
    };
    document.addEventListener('visibilitychange', pauseWhenHidden);
    return () => {
      document.removeEventListener('visibilitychange', pauseWhenHidden);
      if (!media.paused) media.pause();
      // Strict replay/retained DOM must not lose its source. Release only this
      // detached node after the commit; never reset a replacement player.
      queueMicrotask(() => {
        if (media.isConnected) return;
        media.removeAttribute('src');
        media.load();
      });
    };
  }, [source, kind, attempt]);

  const isCurrent = (media: HTMLMediaElement) =>
    mounted.current &&
    elementRef.current === media &&
    currentOwner.current.source === source &&
    currentOwner.current.kind === kind &&
    currentOwner.current.attempt === attempt &&
    media.getAttribute('src') === source;
  const ready = (media: HTMLMediaElement) => {
    if (isCurrent(media)) setRead({ ...owner, status: media.error ? 'failed' : 'ready' });
  };
  const failed = (media: HTMLMediaElement) => {
    if (isCurrent(media)) setRead({ ...owner, status: 'failed' });
  };
  const owned = read.source === source && read.kind === kind && read.attempt === attempt;
  return {
    elementRef,
    attempt,
    ready,
    failed,
    status: owned ? read.status : source ? ('loading' as const) : ('failed' as const),
    retry: () => {
      if (source) setAttempt((value) => value + 1);
    },
  };
}

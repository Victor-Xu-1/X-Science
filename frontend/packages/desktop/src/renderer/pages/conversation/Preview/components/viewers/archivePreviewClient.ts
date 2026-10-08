/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

export type ArchiveEntry = {
  path: string;
  name: string;
  size: number;
  modified_at?: string;
  directory: boolean;
  archive: boolean;
};

export type ArchiveListing = {
  filename: string;
  containers: string[];
  entries: ArchiveEntry[];
};

export type ArchivePreviewFailure = 'entryFailed' | 'invalid' | 'limit' | 'remoteOnly' | 'unavailable';

export class ArchivePreviewError extends Error {
  readonly reason: ArchivePreviewFailure;
  readonly status?: number;

  constructor(reason: ArchivePreviewFailure, status?: number) {
    super(`archive preview failed: ${reason}${status ? ` (${status})` : ''}`);
    this.name = 'ArchivePreviewError';
    this.reason = reason;
    this.status = status;
  }
}

const DEFAULT_RETRY_DELAYS = [250, 750];

/** Archive traversal is served by the authenticated same-origin content host. */
export function archivePreviewEndpoint(
  contentUrl: string,
  containers: readonly string[],
  entry?: string
): string | null {
  try {
    const source = new URL(contentUrl, window.location.origin);
    if (
      source.origin !== window.location.origin ||
      source.username ||
      source.password ||
      !/^https?:$/.test(source.protocol)
    )
      return null;
    source.hash = '';
    source.search = '';
    source.pathname = `${source.pathname.replace(/\/$/, '')}/archive${entry ? '/content' : ''}`;
    containers.forEach((container) => source.searchParams.append('container', container));
    if (entry) source.searchParams.set('entry', entry);
    return source.href;
  } catch {
    return null;
  }
}

function waitForRetry(delay: number, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  if (delay <= 0) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const onAbort = () => {
      window.clearTimeout(timer);
      reject(new DOMException('Aborted', 'AbortError'));
    };
    const timer = window.setTimeout(() => {
      signal?.removeEventListener('abort', onAbort);
      resolve();
    }, delay);
    signal?.addEventListener('abort', onAbort, { once: true });
  });
}

function isRetryableStatus(status: number): boolean {
  return status === 404 || status === 408 || status === 425 || status === 429 || status >= 500;
}

function classifyFailure(status: number): ArchivePreviewFailure {
  if (status === 413) return 'limit';
  if (status === 400 || status === 422) return 'invalid';
  return 'unavailable';
}

function parseArchiveListing(value: unknown): ArchiveListing | null {
  if (!value || typeof value !== 'object') return null;
  const listing = value as Partial<ArchiveListing>;
  const containers = listing.containers == null ? [] : listing.containers;
  const valid =
    typeof listing.filename === 'string' &&
    Array.isArray(containers) &&
    containers.every((container) => typeof container === 'string') &&
    Array.isArray(listing.entries) &&
    listing.entries.every(
      (entry) =>
        entry &&
        typeof entry.path === 'string' &&
        typeof entry.name === 'string' &&
        typeof entry.size === 'number' &&
        typeof entry.directory === 'boolean' &&
        typeof entry.archive === 'boolean'
    );
  if (!valid) return null;
  return { filename: listing.filename, containers, entries: listing.entries };
}

export async function fetchArchiveListing(
  endpoint: string,
  options: { signal?: AbortSignal; retryDelays?: number[] } = {}
): Promise<ArchiveListing> {
  const retryDelays = options.retryDelays ?? DEFAULT_RETRY_DELAYS;
  const runAttempt = async (attempt: number): Promise<ArchiveListing> => {
    options.signal?.throwIfAborted();
    try {
      const response = await fetch(endpoint, {
        credentials: 'same-origin',
        headers: { accept: 'application/json' },
        signal: options.signal,
      });
      options.signal?.throwIfAborted();
      if (response.ok) {
        const payload: unknown = await response.json();
        options.signal?.throwIfAborted();
        const listing = parseArchiveListing(payload);
        if (!listing) throw new ArchivePreviewError('unavailable');
        return listing;
      }
      if (!isRetryableStatus(response.status) || attempt >= retryDelays.length) {
        throw new ArchivePreviewError(classifyFailure(response.status), response.status);
      }
    } catch (error) {
      if (options.signal?.aborted) throw error;
      if (error instanceof ArchivePreviewError || (error instanceof DOMException && error.name === 'AbortError')) {
        throw error;
      }
      if (attempt >= retryDelays.length) throw new ArchivePreviewError('unavailable');
    }
    await waitForRetry(retryDelays[attempt], options.signal);
    return runAttempt(attempt + 1);
  };
  return runAttempt(0);
}

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import {
  archivePreviewEndpoint,
  fetchArchiveListing,
} from '@/renderer/pages/conversation/Preview/components/viewers/archivePreviewClient';
import type { ArchivePreviewError } from '@/renderer/pages/conversation/Preview/components/viewers/archivePreviewClient';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const listing = {
  filename: 'binding_modes.zip',
  containers: [],
  entries: [
    { path: 'images', name: 'images', size: 0, directory: true, archive: false },
    { path: 'report.md', name: 'report.md', size: 42, directory: false, archive: false },
  ],
};

describe('fetchArchiveListing', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('recovers from a transient route restart without misreporting archive corruption', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response('route unavailable', { status: 404 }))
      .mockResolvedValueOnce(Response.json(listing));
    vi.stubGlobal('fetch', fetchMock);

    await expect(fetchArchiveListing('/artifact/archive', { retryDelays: [0] })).resolves.toEqual(listing);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('normalizes an empty container path from an older backend during rolling activation', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ ...listing, containers: null })));

    await expect(fetchArchiveListing('/artifact/archive', { retryDelays: [] })).resolves.toEqual(listing);
  });

  it.each([
    [422, 'invalid'],
    [413, 'limit'],
    [403, 'unavailable'],
  ] as const)('maps HTTP %i to %s', async (status, reason) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('failed', { status })));

    await expect(fetchArchiveListing('/artifact/archive', { retryDelays: [] })).rejects.toMatchObject<
      Partial<ArchivePreviewError>
    >({ reason, status });
  });

  it('rejects malformed successful responses as unavailable service data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ entries: 'not-an-array' })));

    await expect(fetchArchiveListing('/artifact/archive', { retryDelays: [] })).rejects.toMatchObject<
      Partial<ArchivePreviewError>
    >({ reason: 'unavailable' });
  });

  it('does not send a read for an already aborted request', async () => {
    const controller = new AbortController();
    controller.abort();
    const request = vi.fn().mockResolvedValue(Response.json(listing));
    vi.stubGlobal('fetch', request);
    await expect(fetchArchiveListing('/artifact/archive', { signal: controller.signal })).rejects.toMatchObject({
      name: 'AbortError',
    });
    expect(request).not.toHaveBeenCalled();
  });

  it('does not retry when a transport ignores cancellation and returns a retryable failure', async () => {
    const controller = new AbortController();
    const request = vi.fn(async () => {
      controller.abort();
      return new Response('late', { status: 503 });
    });
    vi.stubGlobal('fetch', request);
    await expect(
      fetchArchiveListing('/artifact/archive', { signal: controller.signal, retryDelays: [0] })
    ).rejects.toMatchObject({ name: 'AbortError' });
    expect(request).toHaveBeenCalledTimes(1);
  });
});

describe('archive content host boundary', () => {
  beforeEach(() => vi.stubGlobal('window', { location: { origin: 'http://localhost:3000' } }));
  afterEach(() => vi.unstubAllGlobals());
  it('retains the authenticated source and safely encodes nested/entry paths', () => {
    const endpoint = archivePreviewEndpoint(
      '/api/artifacts/a/versions/v?old=1#stale',
      ['inner package.zip'],
      'folder/a & b.md'
    );
    const url = new URL(endpoint!);
    expect(url.origin).toBe(window.location.origin);
    expect(url.pathname).toBe('/api/artifacts/a/versions/v/archive/content');
    expect(url.searchParams.getAll('container')).toEqual(['inner package.zip']);
    expect(url.searchParams.get('entry')).toBe('folder/a & b.md');
    expect(url.searchParams.has('old')).toBe(false);
    expect(url.hash).toBe('');
  });

  it.each([
    'https://external.example/archive.zip',
    'data:application/zip;base64,AA==',
    'blob:https://external.example/archive',
    'http://user:not-a-secret@localhost:3000/archive.zip',
  ])('does not traverse an unsupported source %s', (source) => {
    expect(archivePreviewEndpoint(source, [])).toBeNull();
  });
});

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { resolveSynonBiomedArtifactPreviewPlan } from '@/renderer/services/synonBiomedArtifactPreview';
import type { PreviewContextValue } from '../../context/PreviewContext';
import {
  ArchivePreviewError,
  archivePreviewEndpoint,
  fetchArchiveListing,
  type ArchiveEntry,
  type ArchiveListing,
  type ArchivePreviewFailure,
} from './archivePreviewClient';

type ListingRead = {
  owner: string | null;
  value: ArchiveListing | null;
  loading: boolean;
  error: ArchivePreviewFailure | '';
};
type EntryRead = 'pending' | 'failed';

export function useArchivePreviewRead({
  contentUrl,
  containers,
  directory,
  onOpen,
}: {
  contentUrl?: string;
  containers: readonly string[];
  directory: string;
  onOpen: PreviewContextValue['openPreview'];
}) {
  const endpoint = useMemo(
    () => (contentUrl ? archivePreviewEndpoint(contentUrl, containers) : null),
    [contentUrl, containers]
  );
  const context = JSON.stringify([contentUrl, containers, directory]);
  const mounted = useRef(false);
  const currentContext = useRef(context);
  const open = useRef(onOpen);
  const listingRequest = useRef<AbortController | null>(null);
  const entryRequests = useRef(new Map<string, AbortController>());
  const [read, setRead] = useState<ListingRead>({
    owner: endpoint,
    value: null,
    loading: !!endpoint,
    error: endpoint ? '' : 'remoteOnly',
  });
  const [entries, setEntries] = useState({ owner: context, values: new Map<string, EntryRead>() });

  useLayoutEffect(() => {
    open.current = onOpen;
  }, [onOpen]);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      listingRequest.current?.abort();
      for (const request of entryRequests.current.values()) request.abort();
      entryRequests.current.clear();
    };
  }, []);
  useLayoutEffect(() => {
    currentContext.current = context;
    setEntries({ owner: context, values: new Map() });
    return () => {
      for (const request of entryRequests.current.values()) request.abort();
      entryRequests.current.clear();
    };
  }, [context]);

  const reload = useCallback(async () => {
    if (!mounted.current || listingRequest.current) return;
    if (!endpoint) {
      setRead({ owner: null, value: null, loading: false, error: 'remoteOnly' });
      return;
    }
    const request = new AbortController();
    listingRequest.current = request;
    setRead({ owner: endpoint, value: null, loading: true, error: '' });
    const isCurrent = () => mounted.current && !request.signal.aborted && listingRequest.current === request;
    try {
      const value = await fetchArchiveListing(endpoint, { signal: request.signal });
      if (isCurrent()) setRead({ owner: endpoint, value, loading: false, error: '' });
    } catch (reason) {
      if (isCurrent())
        setRead({
          owner: endpoint,
          value: null,
          loading: false,
          error: reason instanceof ArchivePreviewError ? reason.reason : 'unavailable',
        });
    } finally {
      if (listingRequest.current === request) listingRequest.current = null;
    }
  }, [endpoint]);

  useEffect(() => {
    void reload();
    return () => {
      listingRequest.current?.abort();
      listingRequest.current = null;
    };
  }, [reload]);

  const openEntry = async (entry: ArchiveEntry) => {
    if (!contentUrl || !mounted.current || currentContext.current !== context || entryRequests.current.has(entry.path))
      return;
    const url = archivePreviewEndpoint(contentUrl, containers, entry.path);
    if (!url) return;
    const request = new AbortController();
    entryRequests.current.set(entry.path, request);
    const isCurrent = () =>
      mounted.current &&
      currentContext.current === context &&
      !request.signal.aborted &&
      entryRequests.current.get(entry.path) === request;
    const update = (status?: EntryRead) =>
      setEntries((previous) => {
        const values = new Map(previous.owner === context ? previous.values : []);
        if (status) values.set(entry.path, status);
        else values.delete(entry.path);
        return { owner: context, values };
      });
    const plan = resolveSynonBiomedArtifactPreviewPlan({ filename: entry.name });
    const metadata = {
      file_name: entry.name,
      title: entry.name,
      contentUrl: url,
      editable: false,
      language: plan.language,
    };
    try {
      let content = plan.type === 'image' || plan.type === 'pdf' ? url : '';
      if (plan.fetchText) {
        update('pending');
        const response = await fetch(url, { credentials: 'same-origin', signal: request.signal });
        if (!isCurrent()) return;
        if (!response.ok) throw new ArchivePreviewError('entryFailed', response.status);
        content = await response.text();
      }
      if (!isCurrent()) return;
      open.current(content, plan.type, metadata, { presentation: 'board' });
      update();
    } catch {
      if (isCurrent()) update('failed');
    } finally {
      if (entryRequests.current.get(entry.path) === request) entryRequests.current.delete(entry.path);
    }
  };

  const current: ListingRead =
    read.owner === endpoint
      ? read
      : { owner: endpoint, value: null, loading: !!endpoint, error: endpoint ? '' : ('remoteOnly' as const) };
  return {
    listing: current.value,
    loading: current.loading,
    error: current.error,
    canReload: !!endpoint,
    entries: entries.owner === context ? entries.values : new Map<string, EntryRead>(),
    reload,
    openEntry,
  };
}

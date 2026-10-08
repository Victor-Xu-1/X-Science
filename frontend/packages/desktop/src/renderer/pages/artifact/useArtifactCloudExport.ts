/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import {
  loadSynonBiomedCloudBuckets,
  loadSynonBiomedCloudCredentials,
  type SynonBiomedCloudCredential,
} from '@/renderer/services/synonBiomedWorkspaceSettings';

/** Read ownership is separate from the file mutation: a newer choice supersedes an older read. */
export function useArtifactCloudExport(enabled: boolean) {
  const mounted = useRef(false);
  const catalogueRevision = useRef(0);
  const bucketRevision = useRef(0);
  const [status, setStatus] = useState<'idle' | 'loading' | 'ready' | 'failed'>('idle');
  const [credentials, setCredentials] = useState<SynonBiomedCloudCredential[]>([]);
  const [credentialId, setCredentialId] = useState('');
  const [buckets, setBuckets] = useState<string[]>([]);
  const [bucket, setBucket] = useState('');
  const [bucketLoading, setBucketLoading] = useState(false);
  const [bucketFailed, setBucketFailed] = useState(false);

  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      catalogueRevision.current += 1;
      bucketRevision.current += 1;
    };
  }, []);

  const selectCredential = async (id: string, catalogue = credentials) => {
    const revision = ++bucketRevision.current;
    setCredentialId(id);
    setBucket('');
    setBuckets([]);
    setBucketFailed(false);
    setBucketLoading(Boolean(id));
    if (!id) return;
    try {
      const nextBuckets = await loadSynonBiomedCloudBuckets(id);
      if (!mounted.current || revision !== bucketRevision.current) return;
      const defaultBucket = catalogue.find((item) => item.id === id)?.defaultBucket;
      setBuckets(nextBuckets);
      setBucket(defaultBucket && nextBuckets.includes(defaultBucket) ? defaultBucket : nextBuckets[0] || '');
    } catch {
      if (mounted.current && revision === bucketRevision.current) setBucketFailed(true);
    } finally {
      if (mounted.current && revision === bucketRevision.current) setBucketLoading(false);
    }
  };

  const loadCatalogue = async () => {
    const revision = ++catalogueRevision.current;
    bucketRevision.current += 1;
    setStatus('loading');
    setCredentials([]);
    setCredentialId('');
    setBuckets([]);
    setBucket('');
    setBucketLoading(false);
    setBucketFailed(false);
    try {
      const catalogue = await loadSynonBiomedCloudCredentials();
      if (!mounted.current || revision !== catalogueRevision.current) return;
      const connected = catalogue.filter((item) => item.connected);
      setCredentials(connected);
      setStatus('ready');
      if (connected[0]) void selectCredential(connected[0].id, connected);
    } catch {
      if (mounted.current && revision === catalogueRevision.current) setStatus('failed');
    }
  };

  useEffect(() => {
    if (enabled) void loadCatalogue();
    // The dialog is keyed by file and action; reopening mounts a fresh read owner.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled]);

  return {
    status,
    credentials,
    credentialId,
    buckets,
    bucket,
    setBucket,
    bucketLoading,
    bucketFailed,
    selectCredential,
    retryCatalogue: loadCatalogue,
    retryBuckets: () => selectCredential(credentialId),
    ready: status === 'ready' && !bucketLoading && !bucketFailed && Boolean(credentialId && bucket),
  };
}

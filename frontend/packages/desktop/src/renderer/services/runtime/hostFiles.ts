import { getBaseUrl, httpRequest } from '@/common/adapter/httpBridge';
import { revokeXScienceHostGrant, type XScienceHostGrant } from '@/renderer/services/xScienceWorkspaceSettings';

export type HostFolderEntry = { name: string; isDirectory: boolean };
export type HostFolderMode = 'ro' | 'rw';
export type { XScienceHostGrant };

export async function loadHostFolders(signal: AbortSignal): Promise<XScienceHostGrant[]> {
  const result = await httpRequest<{ grants?: unknown }>('GET', '/api/preferences/host-grants', undefined, {
    signal,
    timeoutMs: 15_000,
  });
  if (!Array.isArray(result?.grants)) throw new Error('Invalid granted folder list');
  return result.grants.map((value: unknown) => {
    if (!value || typeof value !== 'object') throw new Error('Invalid granted folder');
    const item = value as Record<string, unknown>;
    if (
      typeof item.id !== 'string' ||
      !item.id ||
      typeof item.hostPath !== 'string' ||
      !item.hostPath ||
      typeof item.mode !== 'string'
    )
      throw new Error('Invalid granted folder');
    return {
      id: item.id,
      hostPath: item.hostPath,
      mode: item.mode,
      mountName: typeof item.mountName === 'string' ? item.mountName : item.hostPath,
      guestPath: typeof item.guestPath === 'string' ? item.guestPath : '',
    };
  });
}

export async function readHostFolder(
  grant: XScienceHostGrant,
  relative: string,
  signal: AbortSignal
): Promise<HostFolderEntry[]> {
  const path = relative ? `${grant.hostPath.replace(/[\\/]+$/, '')}/${relative}` : grant.hostPath;
  const result = await httpRequest<unknown>(
    'GET',
    `/api/preferences/host-browse?path=${encodeURIComponent(path)}`,
    undefined,
    { signal, timeoutMs: 15_000 }
  );
  if (!Array.isArray(result)) throw new Error('Invalid granted folder listing');
  return result.map((item: unknown) => {
    if (!item || typeof item !== 'object') throw new Error('Invalid granted folder entry');
    const { name, isDirectory } = item as Record<string, unknown>;
    if (
      typeof name !== 'string' ||
      !name ||
      name === '.' ||
      name === '..' ||
      name.includes('/') ||
      typeof isDirectory !== 'boolean'
    ) {
      throw new Error('Invalid granted folder entry');
    }
    return { name, isDirectory };
  });
}

/** Granting/changing access uses the configured operator authority, never a UI fallback. */
export async function grantHostFolder(path: string, mode: HostFolderMode, change = false): Promise<void> {
  const result = await httpRequest<{ ok?: boolean; grant?: unknown }>(
    change ? 'PATCH' : 'POST',
    '/api/go/preferences/host-grants',
    {
      path,
      mode: mode === 'rw' ? 'read_write' : 'read',
    }
  );
  if (result?.ok !== true || !validCanonicalGrant(result.grant)) throw new Error('Invalid folder access receipt');
}

export async function chooseAndGrantHostFolder(mode: HostFolderMode): Promise<boolean> {
  const result = await httpRequest<{ ok?: boolean; cancelled?: boolean; grant?: unknown }>(
    'POST',
    '/api/go/preferences/host-grants/picker',
    {
      mode: mode === 'rw' ? 'read_write' : 'read',
    }
  );
  if (result?.ok !== true) throw new Error('Invalid folder picker receipt');
  if (result.cancelled === true) return false;
  if (!validCanonicalGrant(result.grant)) throw new Error('Invalid folder picker receipt');
  return true;
}

function validCanonicalGrant(value: unknown): boolean {
  if (!value || typeof value !== 'object') return false;
  const grant = value as Record<string, unknown>;
  return (
    typeof grant.id === 'string' &&
    Boolean(grant.id) &&
    typeof grant.path === 'string' &&
    Boolean(grant.path) &&
    (grant.mode === 'read' || grant.mode === 'read_write')
  );
}

export function revokeHostFolder(path: string): Promise<void> {
  return revokeXScienceHostGrant(path, { baseUrl: getBaseUrl() });
}

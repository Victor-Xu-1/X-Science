import { readXScienceFileImportResponse } from './xScienceFileImportError';

export type XScienceSettingsOptions = {
  baseUrl?: string;
  fetchImpl?: typeof fetch;
  signal?: AbortSignal;
};

export class XScienceSettingsRequestError extends Error {
  constructor(
    message: string,
    public readonly status: number
  ) {
    super(message);
    this.name = 'XScienceSettingsRequestError';
  }
}

export type XScienceAllowlistGroup = {
  id: string;
  label: string;
  description: string;
  locked: boolean;
  domains: string[];
};

export type XScienceNetworkSettings = {
  groups: XScienceAllowlistGroup[];
  disabledGroups: string[];
  activeKernelCount: number;
  hasSeenOnboarding: boolean;
  domains: string[];
  configDomains: string[];
  deniedDomains: string[];
};

export type XScienceHostGrant = {
  id: string;
  hostPath: string;
  mountName: string;
  guestPath: string;
  mode: 'ro' | 'rw' | string;
};

export type XScienceSecret = {
  id: string;
  provider: string;
  name: string;
  description: string;
  credentialType: string;
  buckets: string[];
  region: string;
  maskedPreview: string;
  maskedFields: string[];
  valueConfigured: boolean;
  credentialsConfigured: boolean;
  credentialFields: string[];
  createdAt: string;
  updatedAt: string;
};

export type XScienceSecretInput = {
  provider: string;
  name: string;
  value?: string;
  credentials?: Record<string, string>;
  description?: string;
  credentialType?: string;
  buckets?: string[];
  region?: string;
};

export type XScienceDataDirectoryMove = {
  id: string;
  source: string;
  target: string;
  migrate: boolean;
  copied: boolean;
  stagedAt: string;
  completedAt: string | null;
};

export type XScienceDataDirectory = {
  current: string;
  resolved: string | null;
  defaultPath: string;
  source: string;
  usageBytes: number | null;
  freeBytes: number | null;
  activeFrames: number | null;
  configPath: string;
  pendingMove: XScienceDataDirectoryMove | null;
  lastMove: XScienceDataDirectoryMove | null;
};

export type XScienceStorageRuleValues = {
  taskArtifacts: string;
  logs: string;
  toolResults: string;
  temp: string;
};

export type XScienceStorageRules = {
  root: string;
  rules: XScienceStorageRuleValues;
  paths: XScienceStorageRuleValues;
  systemPaths: {
    taskRuns: string;
    workspace: string;
  };
};

export type XScienceDiskUsage = {
  artifactsBytes: number | null;
  workspaceBytes: number | null;
  toolResultsBytes: number | null;
  condaBytes: number | null;
  logsBytes: number | null;
  tempBytes: number | null;
  availableBytes: number | null;
  scannedAt: string | null;
  accounting: string;
  warnings: string[];
};

export type XScienceEnvironmentUsage = {
  environments: Array<{ name: string; bytes: number | null }>;
  packageCacheBytes: number | null;
  scannedAt: string | null;
  truncated: boolean;
  warnings: string[];
};

export type XScienceCloudCredential = {
  id: string;
  provider: string;
  name: string;
  credentialType: string;
  connected: boolean;
  defaultBucket: string | null;
  region: string;
};

export type XScienceCloudObject = {
  key: string;
  size: number;
  lastModified: string;
};

export type XScienceCloudFolder = {
  folders: string[];
  files: XScienceCloudObject[];
};

export type XScienceCloudExportItem = {
  artifactId: string;
  key: string;
};

export type XScienceCloudExportFailure = XScienceCloudExportItem & {
  error: string;
};

export type XScienceCloudBatchExportResult = {
  completed: XScienceCloudExportItem[];
  failed: XScienceCloudExportFailure[];
};

export type XScienceCloudBatchExportProgress = {
  completed: number;
  total: number;
  current: XScienceCloudExportItem;
  error?: string;
};

export type XScienceUseIntent = {
  intent: 'commercial' | 'noncommercial';
  declared: boolean;
};

export type XScienceContactEmail = {
  decision: 'allowed' | 'declined' | 'revoked' | null;
  email: string | null;
  noticeText: string;
  noticeVersion: string;
  noticeStale: boolean;
};

type RecordValue = Record<string, unknown>;

export async function loadXScienceNetworkSettings(
  options: XScienceSettingsOptions = {}
): Promise<XScienceNetworkSettings> {
  const [builtinPayload, domainsPayload] = await Promise.all([
    requestJson('/api/preferences/builtin-allowlist', options),
    requestJson('/api/preferences/allowed-domains', options),
  ]);
  const builtin = asRecord(builtinPayload);
  const domains = asRecord(domainsPayload);
  return {
    groups: arrayValue(builtin?.groups).map(toAllowlistGroup).filter(isPresent),
    disabledGroups: stringArray(builtin?.disabledGroups),
    activeKernelCount: numberValue(builtin?.activeKernelCount),
    hasSeenOnboarding: builtin?.hasSeenOnboarding === true,
    domains: stringArray(domains?.domains),
    configDomains: stringArray(domains?.configDomains),
    deniedDomains: stringArray(domains?.deniedDomains),
  };
}

export async function updateXScienceAllowlistGroups(
  disabledGroups: string[],
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson('/api/preferences/builtin-allowlist/disabled-groups', options, {
    method: 'PUT',
    body: JSON.stringify({ disabledGroups }),
  });
}

export async function addXScienceAllowedDomain(
  domain: string,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson('/api/preferences/allowed-domains', options, {
    method: 'POST',
    body: JSON.stringify({ domain }),
  });
}

export async function replaceXScienceAllowedDomains(
  domains: string[],
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson('/api/preferences/allowed-domains', options, {
    method: 'PUT',
    body: JSON.stringify({ domains }),
  });
}

export async function removeXScienceAllowedDomain(
  domain: string,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson(`/api/preferences/allowed-domains/${encodeURIComponent(domain)}`, options, { method: 'DELETE' });
}

export async function loadXScienceHostGrants(
  options: XScienceSettingsOptions = {}
): Promise<XScienceHostGrant[]> {
  const payload = asRecord(await requestJson('/api/preferences/host-grants', options));
  return arrayValue(payload?.grants).map(toHostGrant).filter(isPresent);
}

export async function updateXScienceHostGrant(
  hostPath: string,
  mode: 'ro' | 'rw',
  options: XScienceSettingsOptions = {}
): Promise<XScienceHostGrant> {
  return requireHostGrant(
    await requestJson('/api/preferences/host-grants', options, {
      method: 'PATCH',
      body: JSON.stringify({ path: hostPath, mode }),
    })
  );
}

export async function revokeXScienceHostGrant(
  hostPath: string,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson('/api/preferences/host-grants', options, {
    method: 'DELETE',
    body: JSON.stringify({ path: hostPath }),
  });
}

export async function loadXScienceSecrets(options: XScienceSettingsOptions = {}): Promise<XScienceSecret[]> {
  const payload = await requestJson('/api/secrets', options);
  const values = Array.isArray(payload) ? payload : arrayValue(asRecord(payload)?.secrets);
  return values.map(toSecret).filter(isPresent);
}

export async function createXScienceSecret(
  input: XScienceSecretInput,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson('/api/secrets', options, { method: 'POST', body: JSON.stringify(input) });
}

export async function updateXScienceSecret(
  id: string,
  input: Partial<XScienceSecretInput>,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson(`/api/secrets/${encodeURIComponent(id)}`, options, {
    method: 'PATCH',
    body: JSON.stringify(input),
  });
}

export async function deleteXScienceSecret(id: string, options: XScienceSettingsOptions = {}): Promise<void> {
  await requestJson(`/api/secrets/${encodeURIComponent(id)}`, options, { method: 'DELETE' });
}

export async function loadXScienceStorageSettings(options: XScienceSettingsOptions = {}): Promise<{
  dataDirectory: XScienceDataDirectory;
  diskUsage: XScienceDiskUsage;
  cloudCredentials: XScienceCloudCredential[];
}> {
  const [dataDirectory, diskUsage, cloudCredentials] = await Promise.all([
    loadXScienceDataDirectory(options),
    loadXScienceDiskUsage(options),
    loadXScienceCloudCredentials(options),
  ]);
  return { dataDirectory, diskUsage, cloudCredentials };
}

export async function loadXScienceDataDirectory(
  options: XScienceSettingsOptions & { includeUsage?: boolean } = {}
): Promise<XScienceDataDirectory> {
  const directoryPayload = await requestJson(
    `/api/settings/data-dir${options.includeUsage === false ? '?includeUsage=false' : ''}`,
    options
  );
  const directory = asRecord(directoryPayload);
  return {
    current: stringValue(directory?.current),
    resolved: nullableString(directory?.resolved),
    defaultPath: stringValue(directory?.default),
    source: stringValue(directory?.source),
    usageBytes: storageNumber(directory?.usageBytes),
    freeBytes: storageNumber(directory?.freeBytes),
    activeFrames: storageNumber(directory?.activeFrames),
    configPath: stringValue(directory?.configPath),
    pendingMove: toDataDirectoryMove(directory?.pendingMove),
    lastMove: toDataDirectoryMove(directory?.lastMove),
  };
}

export async function loadXScienceStorageRules(
  options: XScienceSettingsOptions = {}
): Promise<XScienceStorageRules> {
  return toStorageRules(await requestJson('/api/settings/storage-rules', options));
}

export async function saveXScienceStorageRules(
  rules: XScienceStorageRuleValues,
  options: XScienceSettingsOptions = {}
): Promise<XScienceStorageRules> {
  return toStorageRules(
    await requestJson('/api/settings/storage-rules', options, {
      method: 'PUT',
      body: JSON.stringify({ rules }),
    })
  );
}

export async function loadXScienceDiskUsage(
  options: XScienceSettingsOptions & { refresh?: boolean } = {}
): Promise<XScienceDiskUsage> {
  const usage = asRecord(
    await requestJson(`/api/preferences/disk-usage${options.refresh ? '?refresh=true' : ''}`, options)
  );
  return {
    artifactsBytes: storageNumber(asRecord(usage?.artifacts)?.totalBytes),
    workspaceBytes: storageNumber(asRecord(usage?.workspace)?.totalBytes),
    toolResultsBytes: storageNumber(asRecord(usage?.toolResults)?.totalBytes),
    condaBytes: storageNumber(asRecord(usage?.conda)?.totalBytes),
    logsBytes: storageNumber(asRecord(usage?.logs)?.totalBytes),
    tempBytes: storageNumber(asRecord(usage?.temp)?.totalBytes),
    availableBytes: storageNumber(usage?.availableBytes),
    scannedAt: nullableString(usage?.scannedAt),
    accounting: stringValue(usage?.accounting),
    warnings: stringArray(usage?.warnings),
  };
}

export async function loadXScienceEnvironmentUsage(
  options: XScienceSettingsOptions & { refresh?: boolean } = {}
): Promise<XScienceEnvironmentUsage> {
  const usage = asRecord(
    await requestJson(`/api/preferences/disk-usage/conda${options.refresh ? '?refresh=true' : ''}`, options)
  );
  return {
    environments: arrayValue(usage?.envs).flatMap((value) => {
      const entry = asRecord(value);
      const name = stringValue(entry?.name);
      return name ? [{ name, bytes: storageNumber(entry?.bytes) }] : [];
    }),
    packageCacheBytes: storageNumber(usage?.pkgsBytes),
    scannedAt: nullableString(usage?.scannedAt),
    truncated: usage?.truncated === true,
    warnings: stringArray(usage?.warnings),
  };
}

function storageNumber(value: unknown): number | null {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
}

export async function loadXScienceCloudCredentials(
  options: XScienceSettingsOptions = {}
): Promise<XScienceCloudCredential[]> {
  const payload = await requestJson('/api/cloud-credentials', options);
  const values = Array.isArray(payload) ? payload : arrayValue(asRecord(payload)?.credentials);
  return values.map(toCloudCredential).filter(isPresent);
}

export async function changeXScienceDataDirectory(
  input: { path: string; migrate: boolean; noRestart?: boolean },
  options: XScienceSettingsOptions = {}
): Promise<{ restarting: boolean; restartRequired: boolean; move: XScienceDataDirectoryMove | null }> {
  const payload = asRecord(
    await requestJson('/api/settings/data-dir', options, {
      method: 'POST',
      body: JSON.stringify(input),
    })
  );
  return {
    restarting: payload?.restarting === true,
    restartRequired: payload?.restartRequired === true,
    move: toDataDirectoryMove(payload?.move),
  };
}

export async function clearXScienceLastDataDirectoryMove(
  deleteSource: boolean,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson(`/api/settings/data-dir/last-move?deleteSource=${deleteSource ? '1' : '0'}`, options, {
    method: 'DELETE',
  });
}

export async function testXScienceCloudCredential(
  id: string,
  options: XScienceSettingsOptions = {}
): Promise<{ success: boolean; message: string }> {
  const payload = asRecord(
    await requestJson(`/api/cloud-credentials/${encodeURIComponent(id)}/test`, options, { method: 'POST' })
  );
  return { success: payload?.success === true, message: stringValue(payload?.message) };
}

export async function loadXScienceCloudBuckets(
  id: string,
  options: XScienceSettingsOptions = {}
): Promise<string[]> {
  return stringArray(await requestJson(`/api/cloud-credentials/${encodeURIComponent(id)}/buckets`, options));
}

export async function loadXScienceCloudFolder(
  id: string,
  bucket: string,
  prefix: string,
  options: XScienceSettingsOptions = {}
): Promise<XScienceCloudFolder> {
  const query = new URLSearchParams({ bucket, prefix, page_limit: '5000' });
  const payload = asRecord(
    await requestJson(`/api/cloud-credentials/${encodeURIComponent(id)}/folder?${query.toString()}`, options)
  );
  return {
    folders: stringArray(payload?.folders),
    files: arrayValue(payload?.files).map(toCloudObject).filter(isPresent),
  };
}

export function getXScienceCloudDownloadUrl(id: string, bucket: string, key: string, baseUrl = ''): string {
  const query = new URLSearchParams({ bucket, key });
  return `${baseUrl.replace(/\/+$/, '')}/api/cloud-credentials/${encodeURIComponent(id)}/download?${query.toString()}`;
}

export async function importXScienceCloudObject(
  id: string,
  input: { bucket: string; key: string; projectId: string },
  options: XScienceSettingsOptions = {}
): Promise<RecordValue> {
  const path = `/api/cloud-credentials/${encodeURIComponent(id)}/import`;
  const response = await (options.fetchImpl ?? fetch)(`${(options.baseUrl ?? '').replace(/\/+$/, '')}${path}`, {
    signal: options.signal,
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ bucket: input.bucket, key: input.key, project_id: input.projectId }),
  });
  const payload = asRecord(await readXScienceFileImportResponse(response));
  return payload ?? {};
}

export async function exportXScienceArtifactToCloud(
  id: string,
  input: { artifactId: string; bucket: string; key: string },
  options: XScienceSettingsOptions = {}
): Promise<RecordValue> {
  const payload = asRecord(
    await requestJson(`/api/cloud-credentials/${encodeURIComponent(id)}/export`, options, {
      method: 'POST',
      body: JSON.stringify({ artifact_id: input.artifactId, bucket: input.bucket, key: input.key }),
    })
  );
  return payload ?? {};
}

export async function exportXScienceArtifactsToCloud(
  id: string,
  input: { bucket: string; items: XScienceCloudExportItem[] },
  onProgress?: (progress: XScienceCloudBatchExportProgress) => void,
  options: XScienceSettingsOptions = {}
): Promise<XScienceCloudBatchExportResult> {
  const result: XScienceCloudBatchExportResult = { completed: [], failed: [] };
  for (const item of input.items) {
    let error: string | undefined;
    try {
      // oxlint-disable-next-line no-await-in-loop -- preserve progress order and bound remote cloud concurrency.
      await exportXScienceArtifactToCloud(
        id,
        { artifactId: item.artifactId, bucket: input.bucket, key: item.key },
        options
      );
      result.completed.push(item);
    } catch (reason) {
      error = reason instanceof Error ? reason.message : String(reason);
      result.failed.push({ ...item, error });
    }
    onProgress?.({
      completed: result.completed.length + result.failed.length,
      total: input.items.length,
      current: item,
      ...(error ? { error } : {}),
    });
  }
  return result;
}

export async function loadXScienceUseIntent(
  options: XScienceSettingsOptions = {}
): Promise<XScienceUseIntent> {
  const payload = asRecord(await requestJson('/api/preferences/use-intent', options));
  return {
    intent: payload?.intent === 'noncommercial' ? 'noncommercial' : 'commercial',
    declared: payload?.declared === true,
  };
}

export async function setXScienceUseIntent(
  intent: XScienceUseIntent['intent'],
  options: XScienceSettingsOptions = {}
): Promise<XScienceUseIntent> {
  const payload = asRecord(
    await requestJson('/api/preferences/use-intent', options, { method: 'PUT', body: JSON.stringify({ intent }) })
  );
  return { intent: payload?.intent === 'noncommercial' ? 'noncommercial' : 'commercial', declared: true };
}

export async function loadXScienceContactEmail(
  options: XScienceSettingsOptions = {}
): Promise<XScienceContactEmail> {
  const payload = asRecord(await requestJson('/api/contact-email', options));
  return {
    decision:
      payload?.decision === 'allowed' || payload?.decision === 'declined' || payload?.decision === 'revoked'
        ? payload.decision
        : null,
    email: nullableString(payload?.email),
    noticeText: stringValue(payload?.notice_text),
    noticeVersion: stringValue(payload?.notice_version),
    noticeStale: payload?.notice_stale === true,
  };
}

export async function setXScienceContactEmail(
  email: string,
  noticeVersion: string,
  options: XScienceSettingsOptions = {}
): Promise<void> {
  await requestJson('/api/contact-email', options, {
    method: 'PUT',
    body: JSON.stringify({ email, notice_version: noticeVersion }),
  });
}

export async function removeXScienceContactEmail(options: XScienceSettingsOptions = {}): Promise<void> {
  await requestJson('/api/contact-email', options, { method: 'DELETE' });
}

async function requestJson(
  path: string,
  options: XScienceSettingsOptions,
  init: RequestInit = {}
): Promise<unknown> {
  const response = await (options.fetchImpl ?? fetch)(`${(options.baseUrl ?? '').replace(/\/+$/, '')}${path}`, {
    ...init,
    signal: options.signal,
    headers: {
      Accept: 'application/json',
      ...(init.body ? { 'Content-Type': 'application/json' } : {}),
      ...init.headers,
    },
  });
  if (!response.ok) {
    const payload = asRecord(await response.json().catch((): null => null));
    throw new XScienceSettingsRequestError(
      stringValue(payload?.detail ?? payload?.error ?? payload?.message) || `Request failed: ${response.status}`,
      response.status
    );
  }
  if (response.status === 204) return null;
  const text = await response.text();
  return text ? JSON.parse(text) : null;
}

function toAllowlistGroup(value: unknown): XScienceAllowlistGroup | null {
  const item = asRecord(value);
  const id = stringValue(item?.id);
  if (!id) return null;
  return {
    id,
    label: stringValue(item?.label),
    description: stringValue(item?.description),
    locked: item?.locked === true,
    domains: stringArray(item?.domains),
  };
}

function toHostGrant(value: unknown): XScienceHostGrant | null {
  const item = asRecord(value);
  const hostPath = stringValue(item?.hostPath);
  if (!hostPath) return null;
  const mode = stringValue(item?.mode);
  return {
    id: stringValue(item?.id) || hostPath,
    hostPath,
    mountName: stringValue(item?.mountName),
    guestPath: stringValue(item?.guestPath),
    mode,
  };
}

function requireHostGrant(value: unknown): XScienceHostGrant {
  const grant = toHostGrant(value);
  if (!grant) throw new Error('X-Science returned an invalid host grant response.');
  return grant;
}

function toSecret(value: unknown): XScienceSecret | null {
  const item = asRecord(value);
  const id = stringValue(item?.id);
  if (!id) return null;
  return {
    id,
    provider: stringValue(item?.provider),
    name: stringValue(item?.name),
    description: stringValue(item?.description),
    credentialType: stringValue(item?.credential_type ?? item?.credentialType),
    buckets: stringArray(item?.buckets),
    region: stringValue(item?.region),
    maskedPreview: stringValue(item?.masked_preview),
    maskedFields: stringArray(item?.masked_fields),
    valueConfigured: item?.valueConfigured === true,
    credentialsConfigured: item?.credentialsConfigured === true,
    credentialFields: stringArray(item?.credentialFields),
    createdAt: stringValue(item?.created_at ?? item?.createdAt),
    updatedAt: stringValue(item?.updated_at ?? item?.updatedAt),
  };
}

function toDataDirectoryMove(value: unknown): XScienceDataDirectoryMove | null {
  const item = asRecord(value);
  const id = stringValue(item?.id);
  if (!id) return null;
  return {
    id,
    source: stringValue(item?.source),
    target: stringValue(item?.target),
    migrate: item?.migrate === true,
    copied: item?.copied === true,
    stagedAt: stringValue(item?.stagedAt),
    completedAt: nullableString(item?.completedAt),
  };
}

function toStorageRules(value: unknown): XScienceStorageRules {
  const payload = asRecord(value);
  const rules = storageRuleValues(payload?.rules);
  return {
    root: stringValue(payload?.root),
    rules,
    paths: storageRuleValues(payload?.paths),
    systemPaths: {
      taskRuns: stringValue(asRecord(payload?.systemPaths)?.taskRuns),
      workspace: stringValue(asRecord(payload?.systemPaths)?.workspace),
    },
  };
}

function storageRuleValues(value: unknown): XScienceStorageRuleValues {
  const item = asRecord(value);
  return {
    taskArtifacts: stringValue(item?.taskArtifacts),
    logs: stringValue(item?.logs),
    toolResults: stringValue(item?.toolResults),
    temp: stringValue(item?.temp),
  };
}

function toCloudCredential(value: unknown): XScienceCloudCredential | null {
  const item = asRecord(value);
  const id = stringValue(item?.id);
  if (!id) return null;
  return {
    id,
    provider: stringValue(item?.provider),
    name: stringValue(item?.name),
    credentialType: stringValue(item?.credential_type),
    connected: item?.is_connected === true,
    defaultBucket: nullableString(item?.default_bucket),
    region: stringValue(item?.region),
  };
}

function toCloudObject(value: unknown): XScienceCloudObject | null {
  const item = asRecord(value);
  const key = stringValue(item?.key);
  if (!key) return null;
  return { key, size: numberValue(item?.size), lastModified: stringValue(item?.last_modified ?? item?.lastModified) };
}

function asRecord(value: unknown): RecordValue | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordValue) : null;
}
function arrayValue(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}
function stringArray(value: unknown): string[] {
  return arrayValue(value).filter((item): item is string => typeof item === 'string');
}
function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}
function nullableString(value: unknown): string | null {
  const result = stringValue(value);
  return result || null;
}
function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}
function isPresent<T>(value: T | null): value is T {
  return value !== null;
}

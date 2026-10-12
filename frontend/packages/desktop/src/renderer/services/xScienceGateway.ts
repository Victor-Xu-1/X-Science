import { isXScienceHttpError, XScienceHttpError } from './xScienceHttp';
import { clearXScienceFrameReads } from './xScienceFrameReads';
import type { IConversationMcpStatus, IConversationMcpStatusKind } from '@/common/config/storage';
import { registerRendererAccountReset, rendererAccountScopedKey } from './rendererAccountScope';

export type XScienceProject = {
  projectId: string;
  latestConversationId: string | null;
  name: string;
  description: string | null;
  context: string | null;
  conversationCount: number;
  artifactCount: number;
  createdAt: string | null;
  updatedAt: string | null;
  lastActiveAt: string | null;
};

export type XScienceComposerCapabilities = {
  skills: string[];
  mcpStatuses: IConversationMcpStatus[];
};

export type XScienceProjectArtifact = {
  artifactId: string;
  versionId: string | null;
  versionNumber: number;
  projectId: string | null;
  rootFrameId: string | null;
  frameId: string | null;
  creatingFrameId: string | null;
  filename: string;
  contentType: string | null;
  sizeBytes: number;
  createdAt: string | null;
  updatedAt: string | null;
  checksum: string | null;
  filePath: string | null;
  folderId: string | null;
  priority: string | null;
  isUserUpload: boolean;
  agentName: string | null;
  isIntermediate: boolean;
};

export type XScienceProjectBench = {
  frameId: string;
  rootFrameId: string;
  parentFrameId: string | null;
  projectId: string | null;
  name: string;
  taskSummary: string | null;
  agentName: string | null;
  status: string | null;
  statusDescription: string | null;
  createdAt: string | null;
  updatedAt: string | null;
  completedAt: string | null;
  lastActivityAt: string | null;
  hasImageOutput: boolean;
};

export type XScienceLinkedTask = Pick<XScienceProjectBench, 'frameId' | 'projectId' | 'name'>;

export type XScienceProjectFolder = {
  folderId: string;
  projectId: string | null;
  parentId: string | null;
  rootFrameId: string | null;
  name: string;
  sortOrder: number;
  artifactCount: number;
  isConversationFolder: boolean;
  isUserUploadsFolder: boolean;
};

export type XScienceProjectWorkbench = {
  project: XScienceProject;
  benches: XScienceProjectBench[];
  artifacts: XScienceProjectArtifact[];
  folders: XScienceProjectFolder[];
};

export type XScienceGatewayOptions = {
  baseUrl?: string;
  fetchImpl?: typeof fetch;
  /** Cancels owned reads; write requests retain their existing server lifecycle. */
  signal?: AbortSignal;
};

export type XScienceProjectInput = {
  name: string;
  description: string | null;
  context: string | null;
};

type ProjectPayload = {
  projects?: unknown;
};

type RecordValue = Record<string, unknown>;

const DEFAULT_GATEWAY_BASE_URL = '';
const sharedGatewayReads = new Map<string, Promise<unknown>>();

type SharedReadKind = 'projects' | 'project-artifacts' | 'project-folders' | 'project-benches';

/**
 * Coalesces concurrent identical read-only gateway requests into one HTTP
 * call. Only requests in flight at the same moment are shared: settled reads
 * are always evicted, so a later caller still receives a fresh authoritative
 * response instead of stale data. Custom transports and explicit origins are
 * isolation boundaries and never share response state with the shared map.
 */
function coalesceSharedGatewayRead<T>(
  kind: SharedReadKind,
  resourceId: string,
  options: XScienceGatewayOptions,
  load: () => Promise<T>
): Promise<T> {
  // A caller-owned abort must not cancel another consumer's shared read.
  if (options.fetchImpl || options.baseUrl || options.signal) return load();
  const key = rendererAccountScopedKey(`${kind}:${resourceId}`);
  const active = sharedGatewayReads.get(key);
  if (active) return active as Promise<T>;
  const request = load().finally(() => {
    if (sharedGatewayReads.get(key) === request) sharedGatewayReads.delete(key);
  });
  sharedGatewayReads.set(key, request);
  return request as Promise<T>;
}

export function clearSharedGatewayReads(): void {
  sharedGatewayReads.clear();
}

registerRendererAccountReset('x-science-gateway-reads', clearSharedGatewayReads);

export async function loadXScienceProjects(options: XScienceGatewayOptions = {}): Promise<XScienceProject[]> {
  return coalesceSharedGatewayRead('projects', '', options, async () => {
    const payload = await getJson<ProjectPayload>('/api/projects', options);
    const projects = Array.isArray(payload.projects) ? payload.projects : [];

    return projects.map(toProject).filter((project): project is XScienceProject => project !== null);
  });
}

export async function loadXScienceProject(
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProject> {
  const project = toProject(await getJson<unknown>(`/api/projects/${encodeURIComponent(projectId)}`, options));
  if (!project || project.projectId !== projectId) throw new Error('X-Science project metadata identity is invalid');
  return project;
}

/** Reads the exact conversation record, not a capped list or its message history. */
export async function loadXScienceLinkedTask(
  frameId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceLinkedTask> {
  const record = asRecord(await getJson<unknown>(`/api/conversations/${encodeURIComponent(frameId)}`, options));
  const name = stringValue(record?.name);
  if (stringValue(record?.id) !== frameId || !name) throw new Error('X-Science linked task identity is invalid');
  return { frameId, name, projectId: nullableStringValue(asRecord(record?.extra)?.project_id) };
}

export async function loadXScienceComposerCapabilities(
  conversationId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceComposerCapabilities> {
  const normalizedConversationId = conversationId.trim();
  if (!normalizedConversationId) throw new Error('X-Science conversation id is required');
  return decodeXScienceComposerCapabilities(
    await getJson<unknown>(
      `/api/conversations/${encodeURIComponent(normalizedConversationId)}/composer-capabilities`,
      options
    )
  );
}

export async function loadXScienceAssistantComposerCapabilities(
  assistantId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceComposerCapabilities> {
  const normalizedAssistantId = assistantId.trim();
  if (!normalizedAssistantId) throw new Error('X-Science assistant id is required');
  return decodeXScienceComposerCapabilities(
    await getJson<unknown>(
      `/api/assistants/${encodeURIComponent(normalizedAssistantId)}/composer-capabilities`,
      options
    )
  );
}

function decodeXScienceComposerCapabilities(payloadValue: unknown): XScienceComposerCapabilities {
  const payload = asRecord(payloadValue);
  if (!payload) throw new Error('X-Science composer capabilities response is invalid');

  const skills = uniqueNonEmptyStrings(payload.skills);
  const statuses = Array.isArray(payload.mcp_statuses)
    ? payload.mcp_statuses
        .map(toConversationMcpStatus)
        .filter((status): status is IConversationMcpStatus => status !== null)
    : [];
  const seenMcp = new Set<string>();
  return {
    skills,
    mcpStatuses: statuses.filter((status) => {
      const key = status.id.toLocaleLowerCase();
      if (seenMcp.has(key)) return false;
      seenMcp.add(key);
      return true;
    }),
  };
}

export async function createXScienceProject(
  input: XScienceProjectInput,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProject> {
  const payload = await requestJson<unknown>('/api/projects', options, {
    method: 'POST',
    body: input,
  });
  return requireProjectPayload(payload, 'create');
}

export async function updateXScienceProject(
  projectId: string,
  input: XScienceProjectInput,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProject> {
  const payload = await requestJson<unknown>(`/api/projects/${encodeURIComponent(projectId)}`, options, {
    method: 'PATCH',
    body: input,
  });
  return requireProjectPayload(payload, 'update');
}

export async function deleteXScienceProject(
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  try {
    await requestJson<unknown>(`/api/projects/${encodeURIComponent(projectId)}`, options, {
      method: 'DELETE',
    });
  } catch (error) {
    // Project deletion is also idempotent when another tab already removed it.
    if (!isXScienceHttpError(error) || error.status !== 404) throw error;
  } finally {
    // A project owns multiple frames and artifact reads, so invalidate the
    // bounded browser cache after both success and failure.
    clearXScienceFrameReads();
  }
}

export async function loadXScienceProjectArtifacts(
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProjectArtifact[]> {
  const normalizedProjectId = projectId.trim();
  return coalesceSharedGatewayRead('project-artifacts', normalizedProjectId, options, async () => {
    const artifacts = await getJson<unknown[]>(
      `/api/projects/${encodeURIComponent(normalizedProjectId)}/artifacts`,
      options
    );
    return artifacts
      .map(toProjectArtifact)
      .filter((artifact): artifact is XScienceProjectArtifact => artifact !== null);
  });
}

export async function loadXScienceProjectBenches(
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProjectBench[]> {
  const normalizedProjectId = projectId.trim();
  const load = async () => {
    const benches = await getJson<unknown[]>(
      `/api/projects/${encodeURIComponent(normalizedProjectId)}/benches`,
      options
    );
    return benches.map(toProjectBench).filter((bench): bench is XScienceProjectBench => bench !== null);
  };
  // An empty project id is a caller error; keep those requests unshared.
  if (!normalizedProjectId) return load();
  return coalesceSharedGatewayRead('project-benches', normalizedProjectId, options, load);
}

export async function loadXScienceProjectFolders(
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProjectFolder[]> {
  const normalizedProjectId = projectId.trim();
  return coalesceSharedGatewayRead('project-folders', normalizedProjectId, options, async () => {
    const folders = await getJson<unknown[]>(
      `/api/projects/${encodeURIComponent(normalizedProjectId)}/folders`,
      options
    );
    return folders.map(toProjectFolder).filter((folder): folder is XScienceProjectFolder => folder !== null);
  });
}

export async function loadXScienceArtifact(
  artifactId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProjectArtifact> {
  const artifact = toProjectArtifact(
    await getJson<unknown>(`/api/artifacts/${encodeURIComponent(artifactId)}/metadata`, options)
  );
  if (!artifact) {
    throw new Error(`X-Science artifact response is invalid: ${artifactId}`);
  }
  return artifact;
}

export function getXScienceArtifactContentUrl(artifactId: string, baseUrl = DEFAULT_GATEWAY_BASE_URL): string {
  return toGatewayUrl(`/api/artifacts/${encodeURIComponent(artifactId)}`, baseUrl);
}

export async function loadXScienceProjectWorkbench(
  projectId: string,
  options: XScienceGatewayOptions = {}
): Promise<XScienceProjectWorkbench> {
  const [project, benches, artifacts, folders] = await Promise.all([
    loadXScienceProject(projectId, options),
    loadXScienceProjectBenches(projectId, options),
    loadXScienceProjectArtifacts(projectId, options),
    loadXScienceProjectFolders(projectId, options),
  ]);
  return { project, benches, artifacts, folders };
}

async function getJson<T>(path: string, options: XScienceGatewayOptions): Promise<T> {
  const fetchImpl = options.fetchImpl ?? fetch;
  const response = await fetchImpl(toGatewayUrl(path, options.baseUrl), {
    headers: { Accept: 'application/json' },
    ...(options.signal ? { signal: options.signal } : {}),
  });

  if (!response.ok) {
    throw new Error(`X-Science gateway request failed: ${response.status} ${path}`);
  }

  return (await response.json()) as T;
}

async function requestJson<T>(
  path: string,
  options: XScienceGatewayOptions,
  request: { method: 'POST' | 'PATCH' | 'DELETE'; body?: unknown }
): Promise<T> {
  const accountScope = rendererAccountScopedKey('');
  const fetchImpl = options.fetchImpl ?? fetch;
  const response = await fetchImpl(toGatewayUrl(path, options.baseUrl), {
    method: request.method,
    credentials: 'include',
    headers: {
      Accept: 'application/json',
      ...(request.body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    ...(request.body === undefined ? {} : { body: JSON.stringify(request.body) }),
  });

  // A successful write ends the pre-mutation read generation before callers
  // emit refresh events. An idempotent DELETE 404 confirms absence as well.
  // Custom origins/transports and a previous authenticated owner cannot clear
  // the current browser-origin generation. Old completions retain their own
  // callers but the identity-checked eviction cannot remove a newer request.
  if (
    (response.ok || (request.method === 'DELETE' && response.status === 404)) &&
    !options.fetchImpl &&
    !options.baseUrl &&
    accountScope === rendererAccountScopedKey('')
  ) {
    clearSharedGatewayReads();
  }
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    let body: unknown = detail;
    if (detail) {
      try {
        body = JSON.parse(detail) as unknown;
      } catch {
        // Keep the bounded text body on the typed error for diagnostics.
      }
    }
    throw new XScienceHttpError({ method: request.method, path, status: response.status, body });
  }
  if (response.status === 204) return undefined as T;
  const text = await response.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

function requireProjectPayload(value: unknown, operation: string): XScienceProject {
  const record = asRecord(value);
  const project = toProject(record?.project ?? value);
  if (!project) {
    throw new Error(`X-Science project ${operation} response is invalid`);
  }
  return project;
}

function toGatewayUrl(path: string, baseUrl = DEFAULT_GATEWAY_BASE_URL): string {
  const normalizedBaseUrl = baseUrl.replace(/\/+$/, '');
  return `${normalizedBaseUrl}${path}`;
}

function toProject(value: unknown): XScienceProject | null {
  const record = asRecord(value);
  const projectId = stringValue(record?.project_id);
  const name = stringValue(record?.name);
  if (!projectId || !name) return null;

  return {
    projectId,
    latestConversationId: nullableStringValue(record.latest_conversation_id),
    name,
    description: nullableStringValue(record.description),
    context: nullableStringValue(record.context),
    conversationCount: numberValue(record.conversation_count),
    artifactCount: numberValue(record.artifact_count),
    createdAt: nullableStringValue(record.created_at),
    updatedAt: nullableStringValue(record.updated_at),
    lastActiveAt: nullableStringValue(record.last_active_at),
  };
}

function toProjectArtifact(value: unknown): XScienceProjectArtifact | null {
  const record = asRecord(value);
  const artifactId = stringValue(record?.id) || stringValue(record?.artifact_id);
  const filename = stringValue(record?.filename);
  if (!artifactId || !filename) return null;

  return {
    artifactId,
    versionId: nullableStringValue(record.version_id),
    versionNumber: numberValue(record.version_number),
    projectId: nullableStringValue(record.project_id),
    rootFrameId: nullableStringValue(record.root_frame_id),
    frameId: nullableStringValue(record.frame_id),
    creatingFrameId: nullableStringValue(record.creating_frame_id),
    filename,
    contentType: nullableStringValue(record.content_type),
    sizeBytes: numberValue(record.size_bytes),
    createdAt: nullableStringValue(record.created_at),
    updatedAt: nullableStringValue(record.updated_at),
    checksum: nullableStringValue(record.checksum),
    filePath: nullableStringValue(record.file_path),
    folderId: nullableStringValue(record.folder_id),
    priority: nullableStringValue(record.priority),
    isUserUpload: booleanValue(record.is_user_upload),
    agentName: nullableStringValue(record.agent_name),
    isIntermediate: booleanValue(record.is_intermediate),
  };
}

function toProjectBench(value: unknown): XScienceProjectBench | null {
  const record = asRecord(value);
  const frameId = stringValue(record?.id);
  const rootFrameId = stringValue(record?.root_frame_id) || frameId;
  const name = stringValue(record?.name);
  if (!frameId || !name) return null;

  return {
    frameId,
    rootFrameId,
    parentFrameId: nullableStringValue(record.parent_frame_id),
    projectId: nullableStringValue(record.project_id),
    name,
    taskSummary: nullableStringValue(record.task_summary),
    agentName: nullableStringValue(record.agent_name),
    status: nullableStringValue(record.status),
    statusDescription: nullableStringValue(record.status_description),
    createdAt: nullableStringValue(record.created_at),
    updatedAt: nullableStringValue(record.updated_at),
    completedAt: nullableStringValue(record.completed_at),
    lastActivityAt: nullableStringValue(record.last_activity_at),
    hasImageOutput: booleanValue(record.has_image_output),
  };
}

function toProjectFolder(value: unknown): XScienceProjectFolder | null {
  const record = asRecord(value);
  const folderId = stringValue(record?.id);
  const name = stringValue(record?.name);
  if (!folderId || !name) return null;

  return {
    folderId,
    projectId: nullableStringValue(record.project_id),
    parentId: nullableStringValue(record.parent_id),
    rootFrameId: nullableStringValue(record.root_frame_id),
    name,
    sortOrder: numberValue(record.sort_order),
    artifactCount: numberValue(record.artifact_count),
    isConversationFolder: booleanValue(record.is_conversation_folder),
    isUserUploadsFolder: booleanValue(record.is_user_uploads_folder),
  };
}

function asRecord(value: unknown): RecordValue | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordValue) : null;
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function nullableStringValue(value: unknown): string | null {
  return typeof value === 'string' ? value : null;
}

function numberValue(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function booleanValue(value: unknown): boolean {
  return value === true;
}

function uniqueNonEmptyStrings(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const result: string[] = [];
  const seen = new Set<string>();
  for (const candidate of value) {
    if (typeof candidate !== 'string') continue;
    const normalized = candidate.trim();
    const key = normalized.toLocaleLowerCase();
    if (!normalized || seen.has(key)) continue;
    seen.add(key);
    result.push(normalized);
  }
  return result;
}

function toConversationMcpStatus(value: unknown): IConversationMcpStatus | null {
  const record = asRecord(value);
  const id = stringValue(record?.id).trim();
  const name = stringValue(record?.name).trim();
  const status = stringValue(record?.status).trim() as IConversationMcpStatusKind;
  if (!id || !name || !['loaded', 'failed', 'unsupported'].includes(status)) return null;
  const reason = stringValue(record?.reason).trim();
  return { id, name, status, ...(reason ? { reason } : {}) };
}

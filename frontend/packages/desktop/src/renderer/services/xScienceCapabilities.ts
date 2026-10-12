import type { Assistant } from '@/common/types/agent/assistantTypes';
import { resolveXScienceBuiltinExpertText } from './agents/xScienceExpertLocalization';
import { isXScienceHttpError, requestXScienceJson } from './xScienceHttp';

export type XScienceCapabilityOptions = {
  baseUrl?: string;
  fetchImpl?: typeof fetch;
};

export type XScienceAgent = {
  name: string;
  displayName: string;
  description: string;
  healthy: boolean;
  enabled: boolean;
  source: string;
  skillsLocked: boolean;
  unrestricted: boolean;
  supportsPlanMode: boolean;
  userHidden: boolean;
  skillNames: string[];
};

export type XScienceSkill = {
  name: string;
  displayName: string;
  name_i18n?: Record<string, string>;
  description: string;
  description_i18n?: Record<string, string>;
  source: string;
  category: string | null;
  license: string | null;
  skillId: string;
  updatedAt: string | null;
  attachedAgents: string[];
  enabled: boolean;
};

export type XScienceMcpServer = {
  id: string;
  name: string;
  displayName: string;
  description: string;
  description_i18n?: Record<string, string>;
  source: string;
  authRequired: boolean;
  oauthSupported: boolean;
  authState: string;
  authHint: string;
  apiKeyConfigurable: boolean;
  apiKeyConfigured: boolean;
  apiKeyLabel: string;
  transport: string;
  upstreams: unknown[];
  hostedByXScience: boolean;
  health: { ok: boolean };
  attachedAgents: string[];
  enabled: boolean;
  connectionStatus: string;
  usage?: XScienceMcpUsage;
};

export type XScienceMcpUsage = {
  invocationCount: number;
  lastUsedAt: string | null;
};

export type XScienceMcpDirectoryHealth = {
  ok: boolean;
  detail: string;
};

export type XScienceOptionalMcp = {
  id: string;
  name: string;
  displayName: string;
  description: string;
  category: string;
  license: string;
  repositoryUrl: string;
  installKind: string;
  installLabel: string;
  sizeLabel: string;
  requirements: string[];
  sourceRef: string;
  status: 'not-installed' | 'installing' | 'installed' | 'failed' | 'broken' | string;
  installed: boolean;
  installPath: string;
  error: string;
};

export type XScienceMcpAuthorization = {
  authorizationUrl: string | null;
};

export type XScienceMcpToolPermissionState = 'allow' | 'ask' | 'deny';

export type XScienceMcpToolPermission = {
  toolName: string;
  title: string | null;
  description: string;
  readOnlyHint: boolean;
  state: XScienceMcpToolPermissionState;
};

export type XScienceMcpToolPermissions = {
  tools: XScienceMcpToolPermission[];
  skipApprovalsActive: boolean;
};

export type XScienceCustomMcpServer = {
  id: string;
  name: string;
  description: string | null;
  url: string;
  transport: 'sse' | 'streamable_http';
  oauthServerUrl: string | null;
  clientId: string | null;
  scopes: string | null;
  headersHelper: string | null;
  connected: boolean;
  attachedAgents: string[];
  createdAt: string | number | null;
  updatedAt: string | number | null;
};

export type XScienceCustomMcpServerInput = {
  name: string;
  description?: string | null;
  url: string;
  transport: 'sse' | 'streamable_http';
  oauthServerUrl?: string | null;
  clientId?: string | null;
  scopes?: string | null;
  headersHelper?: string | null;
};

export class XScienceCapabilityError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'XScienceCapabilityError';
    this.status = status;
  }
}

type RecordValue = Record<string, unknown>;

type SkillsPayload = {
  skills?: unknown;
};

const CAPABILITY_REQUEST_TIMEOUT_MS = 15_000;
const X_SCIENCE_ASSISTANT_SORT_BASE = 10000;

export async function loadXScienceAgents(options: XScienceCapabilityOptions = {}): Promise<XScienceAgent[]> {
  const payload = await getJson<unknown>('/api/x-science/expert-profiles', options);
  const payloadRecord = asRecord(payload);
  const agents = Array.isArray(payload) ? payload : Array.isArray(payloadRecord?.data) ? payloadRecord.data : [];

  return agents.map(toAgent).filter((agent): agent is XScienceAgent => agent !== null && agent.userHidden === false);
}

export async function loadXScienceSkills(options: XScienceCapabilityOptions = {}): Promise<XScienceSkill[]> {
  const payload = await getJson<SkillsPayload>('/api/skills/catalog', options);
  const skills = Array.isArray(payload.skills) ? payload.skills : [];

  const normalizedSkills: XScienceSkill[] = [];
  for (const value of skills) {
    const skill = toSkill(value);
    if (!skill) continue;
    normalizedSkills.push(skill);
  }
  return normalizedSkills;
}

export async function setXScienceSkillEnabled(
  name: string,
  enabled: boolean,
  options: XScienceCapabilityOptions = {}
): Promise<void> {
  await requestJson(`/api/skills/catalog/${encodeURIComponent(name)}/enabled`, options, {
    method: 'PUT',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
}

export async function loadXScienceMcpServers(
  options: XScienceCapabilityOptions = {}
): Promise<XScienceMcpServer[]> {
  const payload = await getJson<unknown>('/api/mcp-servers/connectors', options);
  const servers = Array.isArray(payload) ? payload : [];

  return servers.map(toMcpServer).filter((server): server is XScienceMcpServer => server !== null);
}

export async function loadXScienceOptionalMcps(
  options: XScienceCapabilityOptions = {}
): Promise<XScienceOptionalMcp[]> {
  const payload = await getJson<unknown>('/api/mcp-servers/optional', options);
  const items = Array.isArray(payload) ? payload : [];
  return items.map(toOptionalMcp).filter((item): item is XScienceOptionalMcp => item !== null);
}

export async function startXScienceOptionalMcpInstall(
  id: string,
  options: XScienceCapabilityOptions = {}
): Promise<XScienceOptionalMcp> {
  return requireOptionalMcp(
    await requestJson<unknown>(`/api/mcp-servers/optional/${encodeURIComponent(id)}/install`, options, {
      method: 'POST',
      headers: { Accept: 'application/json' },
    })
  );
}

export async function loadXScienceCustomMcpServers(
  options: XScienceCapabilityOptions = {}
): Promise<XScienceCustomMcpServer[]> {
  const payload = await getJson<unknown>('/api/mcp-servers', options);
  const servers = Array.isArray(payload) ? payload : [];
  return servers.map(toCustomMcpServer).filter((server): server is XScienceCustomMcpServer => server !== null);
}

export async function createXScienceCustomMcpServer(
  input: XScienceCustomMcpServerInput,
  options: XScienceCapabilityOptions = {}
): Promise<XScienceCustomMcpServer> {
  return requireCustomMcpServer(
    await requestJson<unknown>('/api/mcp-servers', options, jsonRequest('POST', toCustomMcpPayload(input)))
  );
}

export async function updateXScienceCustomMcpServer(
  serverId: string,
  input: XScienceCustomMcpServerInput,
  options: XScienceCapabilityOptions = {}
): Promise<XScienceCustomMcpServer> {
  return requireCustomMcpServer(
    await requestJson<unknown>(
      `/api/mcp-servers/${encodeURIComponent(serverId)}`,
      options,
      jsonRequest('PATCH', toCustomMcpPayload(input))
    )
  );
}

export async function deleteXScienceCustomMcpServer(
  serverId: string,
  options: XScienceCapabilityOptions = {}
): Promise<void> {
  await requestJson(`/api/mcp-servers/${encodeURIComponent(serverId)}`, options, { method: 'DELETE' });
}

export async function attachXScienceMcpServerToAllAgents(
  serverId: string,
  options: XScienceCapabilityOptions = {}
): Promise<unknown> {
  return requestJson(`/api/mcp-servers/${encodeURIComponent(serverId)}/attach-all`, options, { method: 'POST' });
}

export async function detachXScienceMcpServerFromAllAgents(
  serverId: string,
  options: XScienceCapabilityOptions = {}
): Promise<unknown> {
  return requestJson(`/api/mcp-servers/${encodeURIComponent(serverId)}/detach-all`, options, { method: 'DELETE' });
}

export async function loadXScienceMcpDirectoryHealth(
  options: XScienceCapabilityOptions = {}
): Promise<XScienceMcpDirectoryHealth> {
  const payload = asRecord(await getJson<unknown>('/api/mcp-servers/directory-health', options));
  const health = asRecord(payload?.directoryHealth);
  return {
    ok: booleanValue(health?.ok),
    detail: stringValue(health?.detail),
  };
}

export async function setXScienceMcpConnectorEnabled(
  serverId: string,
  enabled: boolean,
  options: XScienceCapabilityOptions = {}
): Promise<unknown> {
  return requestJson(`/api/mcp-servers/connectors/${encodeURIComponent(serverId)}/enabled`, options, {
    method: 'PUT',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ enabled }),
  });
}

export async function reconcileXScienceMcpServers(
  options: XScienceCapabilityOptions = {}
): Promise<Record<string, unknown>> {
  const payload = await requestJson<unknown>('/api/mcp-servers/reconcile', options, {
    method: 'POST',
    headers: { Accept: 'application/json' },
  });
  return asRecord(payload) ?? {};
}

export async function authorizeXScienceMcpConnector(
  serverId: string,
  options: XScienceCapabilityOptions = {}
): Promise<XScienceMcpAuthorization> {
  const payload = asRecord(
    await requestJson<unknown>(`/api/mcp-servers/connectors/${encodeURIComponent(serverId)}/authorize`, options, {
      method: 'POST',
      headers: { Accept: 'application/json' },
    })
  );
  return {
    authorizationUrl: nullableStringValue(payload?.authorizationUrl ?? payload?.authorization_url),
  };
}

export async function configureXScienceMcpConnectorAPIKey(
  serverId: string,
  apiKey: string,
  options: XScienceCapabilityOptions = {}
): Promise<unknown> {
  return requestJson(`/api/mcp-servers/connectors/${encodeURIComponent(serverId)}/credential`, options, {
    method: 'PUT',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ apiKey }),
  });
}

export async function disconnectXScienceMcpConnector(
  serverId: string,
  options: XScienceCapabilityOptions = {}
): Promise<unknown> {
  return requestJson(`/api/mcp-servers/connectors/${encodeURIComponent(serverId)}/disconnect`, options, {
    method: 'POST',
    headers: { Accept: 'application/json' },
  });
}

export async function loadXScienceMcpToolPermissions(
  serverId: string,
  options: XScienceCapabilityOptions = {}
): Promise<XScienceMcpToolPermissions> {
  const payload = asRecord(
    await getJson<unknown>(`/api/mcp-servers/${encodeURIComponent(serverId)}/tool-permissions`, options)
  );
  const tools = Array.isArray(payload?.tools) ? payload.tools : [];
  return {
    tools: tools.map(toMcpToolPermission).filter((tool): tool is XScienceMcpToolPermission => tool !== null),
    skipApprovalsActive: booleanValue(payload?.skipApprovalsActive),
  };
}

export async function updateXScienceMcpToolPermission(
  serverId: string,
  toolName: string,
  state: XScienceMcpToolPermissionState,
  options: XScienceCapabilityOptions = {}
): Promise<unknown> {
  return requestJson(`/api/mcp-servers/${encodeURIComponent(serverId)}/tool-grants`, options, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({ toolName, decision: state === 'ask' ? null : state }),
  });
}

export function toXScienceAssistant(agent: XScienceAgent, index: number): Assistant {
  const status = agent.healthy ? 'online' : 'offline';
  const source = agent.source === 'user' ? 'user' : 'builtin';
  const fallbackDisplayName = resolveAgentDisplayName(agent.name, agent.displayName);
  const builtinText = source === 'builtin' ? resolveXScienceBuiltinExpertText(agent.name) : null;
  const localizedName = {
    'en-US': builtinText?.['en-US']?.displayName ?? fallbackDisplayName,
    'zh-CN': builtinText?.['zh-CN']?.displayName ?? fallbackDisplayName,
  };
  const localizedDescription = {
    'en-US': builtinText?.['en-US']?.description ?? (agent.description || localizedName['en-US']),
    'zh-CN': builtinText?.['zh-CN']?.description ?? (agent.description || localizedName['zh-CN']),
  };

  return {
    id: `xscience:${toAssistantSlug(agent.name)}`,
    source,
    name: localizedName['en-US'],
    name_i18n: localizedName,
    description: localizedDescription['en-US'],
    description_i18n: localizedDescription,
    enabled: agent.enabled,
    sort_order: X_SCIENCE_ASSISTANT_SORT_BASE + index,
    agent_id: agent.name,
    agent: {
      type: 'xscience',
      source: source === 'user' ? 'custom' : 'builtin',
      acp_backend: 'xscience',
    },
    enabled_skills: agent.skillNames,
    custom_skill_names: [],
    disabled_builtin_skills: [],
    context: localizedDescription['en-US'],
    context_i18n: localizedDescription,
    prompts: [],
    prompts_i18n: {},
    models: [],
    agent_status: status,
    agent_status_message: agent.healthy ? undefined : 'X-Science expert is not healthy.',
    deletable: source === 'user',
  };
}

async function getJson<T>(path: string, options: XScienceCapabilityOptions): Promise<T> {
  return requestJson<T>(path, options, { headers: { Accept: 'application/json' } });
}

async function requestJson<T>(path: string, options: XScienceCapabilityOptions, init: RequestInit): Promise<T> {
  try {
    return await requestXScienceJson<T>(path, init, {
      baseUrl: options.baseUrl,
      fetchImpl: options.fetchImpl,
      timeoutMs: CAPABILITY_REQUEST_TIMEOUT_MS,
    });
  } catch (error) {
    if (isXScienceHttpError(error)) {
      throw new XScienceCapabilityError(
        error.status,
        error.backendMessage || `X-Science capability request failed with HTTP ${error.status}`
      );
    }
    throw error;
  }
}

function toAgent(value: unknown): XScienceAgent | null {
  const record = asRecord(value);
  const managedAgentId = stringValue(record?.id);
  const name = stringValue(record?.name) || managedAgentId;
  if (!name) return null;

  const status = stringValue(record?.status);
  const rawDisplayName = stringValue(record?.displayName) || stringValue(record?.name) || name;

  return {
    name,
    displayName: resolveAgentDisplayName(name, rawDisplayName),
    description: stringValue(record?.description),
    healthy: booleanValue(record?.healthy) || status === 'online' || status === 'healthy',
    enabled: record?.enabled !== false,
    source: stringValue(record?.source) || stringValue(record?.agent_source) || 'unknown',
    skillsLocked: booleanValue(record?.skillsLocked),
    unrestricted: booleanValue(record?.unrestricted),
    supportsPlanMode: booleanValue(record?.supportsPlanMode),
    userHidden: booleanValue(record?.userHidden),
    skillNames: stringArray(record?.skillNames),
  };
}

function resolveAgentDisplayName(name: string, value: string): string {
  if (name.trim().toUpperCase() === 'OPERON') return 'General Research Assistant';
  return value;
}

function toSkill(value: unknown): XScienceSkill | null {
  const record = asRecord(value);
  const name = stringValue(record?.name);
  const skillId = stringValue(record?.skillId);
  if (!name || !skillId) return null;

  return {
    name,
    displayName: stringValue(record?.displayName) || name,
    name_i18n: stringRecord(record?.name_i18n),
    description: stringValue(record?.description),
    description_i18n: stringRecord(record?.description_i18n ?? record?.descriptionI18n),
    source: stringValue(record?.source) || 'unknown',
    category: nullableStringValue(record?.category),
    license: nullableStringValue(record?.license),
    skillId,
    updatedAt: nullableStringValue(record?.updatedAt),
    attachedAgents: stringArray(record?.attachedAgents),
    enabled: record?.enabled !== false,
  };
}

function toMcpServer(value: unknown): XScienceMcpServer | null {
  const record = asRecord(value);
  const id = stringValue(record?.id);
  const name = stringValue(record?.name);
  if (!id || !name) return null;

  return {
    id,
    name,
    displayName: stringValue(record?.displayName) || name,
    description: stringValue(record?.description),
    description_i18n: stringRecord(record?.description_i18n ?? record?.descriptionI18n),
    source: stringValue(record?.source) || 'unknown',
    authRequired: booleanValue(record?.authRequired),
    oauthSupported: booleanValue(record?.oauthSupported),
    authState: stringValue(record?.authState) || 'unknown',
    authHint: stringValue(record?.authHint),
    apiKeyConfigurable: booleanValue(record?.apiKeyConfigurable),
    apiKeyConfigured: booleanValue(record?.apiKeyConfigured),
    apiKeyLabel: stringValue(record?.apiKeyLabel) || 'API Key',
    transport: stringValue(record?.transport) || 'unknown',
    upstreams: Array.isArray(record?.upstreams) ? record.upstreams : [],
    hostedByXScience: booleanValue(record?.hostedByXScience),
    health: {
      ok: booleanValue(asRecord(record?.health)?.ok),
    },
    attachedAgents: stringArray(record?.attachedAgents),
    enabled: record?.enabled !== false,
    connectionStatus: stringValue(record?.connectionStatus) || 'unknown',
    usage: toMcpUsage(record?.usage ?? record),
  };
}

function toOptionalMcp(value: unknown): XScienceOptionalMcp | null {
  const record = asRecord(value);
  const id = stringValue(record?.id);
  const name = stringValue(record?.name);
  if (!id || !name) return null;
  return {
    id,
    name,
    displayName: stringValue(record?.displayName) || name,
    description: stringValue(record?.description),
    category: stringValue(record?.category),
    license: stringValue(record?.license),
    repositoryUrl: stringValue(record?.repositoryUrl),
    installKind: stringValue(record?.installKind),
    installLabel: stringValue(record?.installLabel),
    sizeLabel: stringValue(record?.sizeLabel),
    requirements: stringArray(record?.requirements),
    sourceRef: stringValue(record?.sourceRef),
    status: stringValue(record?.status) || 'not-installed',
    installed: booleanValue(record?.installed),
    installPath: stringValue(record?.installPath),
    error: stringValue(record?.error),
  };
}

function requireOptionalMcp(value: unknown): XScienceOptionalMcp {
  const item = toOptionalMcp(value);
  if (!item) throw new Error('X-Science returned an invalid optional MCP response.');
  return item;
}

function toMcpUsage(value: unknown): XScienceMcpUsage | undefined {
  const record = asRecord(value);
  if (!record) return undefined;
  const rawCount = record.invocationCount ?? record.callCount ?? record.totalCalls;
  const invocationCount =
    typeof rawCount === 'number' && Number.isSafeInteger(rawCount) && rawCount >= 0 ? rawCount : undefined;
  const rawLastUsedAt = record.lastUsedAt ?? record.lastCallAt ?? record.last_invoked_at;
  const lastUsedAt =
    typeof rawLastUsedAt === 'string' && rawLastUsedAt.trim() && Number.isFinite(Date.parse(rawLastUsedAt))
      ? rawLastUsedAt
      : null;
  if (invocationCount === undefined && lastUsedAt === null) return undefined;
  return { invocationCount: invocationCount ?? 0, lastUsedAt };
}

function toMcpToolPermission(value: unknown): XScienceMcpToolPermission | null {
  const record = asRecord(value);
  const toolName = stringValue(record?.toolName);
  const rawState = record?.state;
  const state = rawState === null || rawState === undefined ? 'ask' : stringValue(rawState);
  if (!toolName || !isMcpToolPermissionState(state)) return null;

  return {
    toolName,
    title: nullableStringValue(record?.title),
    description: stringValue(record?.description),
    readOnlyHint: booleanValue(record?.readOnlyHint),
    state,
  };
}

function toCustomMcpServer(value: unknown): XScienceCustomMcpServer | null {
  const record = asRecord(value);
  const id = stringValue(record?.id);
  const name = stringValue(record?.name);
  const transport = stringValue(record?.transport);
  if (!id || !name || (transport !== 'sse' && transport !== 'streamable_http')) return null;
  return {
    id,
    name,
    description: nullableStringValue(record?.description),
    url: stringValue(record?.url),
    transport,
    oauthServerUrl: nullableStringValue(record?.oauth_server_url),
    clientId: nullableStringValue(record?.client_id),
    scopes: nullableStringValue(record?.scopes),
    headersHelper: nullableStringValue(record?.headers_helper),
    connected: booleanValue(record?.is_connected),
    attachedAgents: stringArray(record?.attached_agents),
    createdAt: scalarValue(record?.created_at),
    updatedAt: scalarValue(record?.updated_at),
  };
}

function requireCustomMcpServer(value: unknown): XScienceCustomMcpServer {
  const server = toCustomMcpServer(value);
  if (!server) throw new Error('X-Science returned an invalid MCP server response.');
  return server;
}

function toCustomMcpPayload(input: XScienceCustomMcpServerInput): Record<string, unknown> {
  return {
    name: input.name,
    description: input.description || null,
    url: input.url,
    transport: input.transport,
    oauth_server_url: input.oauthServerUrl || null,
    client_id: input.clientId || null,
    scopes: input.scopes || null,
    headers_helper: input.headersHelper || null,
  };
}

function jsonRequest(method: string, payload: unknown): RequestInit {
  return {
    method,
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
  };
}

function isMcpToolPermissionState(value: string): value is XScienceMcpToolPermissionState {
  return value === 'allow' || value === 'ask' || value === 'deny';
}

function toAssistantSlug(agentName: string): string {
  return agentName
    .trim()
    .toLowerCase()
    .replace(/[_\s]+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

function asRecord(value: unknown): RecordValue | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordValue) : null;
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function stringRecord(value: unknown): Record<string, string> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined;
  const entries = Object.entries(value).filter((entry): entry is [string, string] => typeof entry[1] === 'string');
  return entries.length > 0 ? Object.fromEntries(entries) : undefined;
}

function nullableStringValue(value: unknown): string | null {
  const string = stringValue(value);
  return string || null;
}

function booleanValue(value: unknown): boolean {
  return value === true;
}

function scalarValue(value: unknown): string | number | null {
  return typeof value === 'string' || typeof value === 'number' ? value : null;
}

function stringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string') : [];
}

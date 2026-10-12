/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { Assistant } from '@/common/types/agent/assistantTypes';
import type { IMcpServer } from '@/common/config/storage';
import { loadXScienceAgents, toXScienceAssistant } from './xScienceCapabilities';

export const X_SCIENCE_ASSISTANTS_CACHE_KEY = 'xscience.assistants.catalog.v1';

export type XScienceSkillInfo = {
  name: string;
  description: string;
  location: string;
  relative_location?: string;
  is_auto_inject: boolean;
  is_custom: boolean;
  source: 'builtin';
};

type CountedNames = {
  count: number;
  names: string[];
};

export type XScienceSeedProject = {
  slug: string;
  name: string;
  manifestPath?: string;
  rootFrameId: string;
  artifactCount: number;
  childFrameCount: number;
  folderCount: number;
};

export type XScienceSeedProjectCatalog = {
  count: number;
  projects: XScienceSeedProject[];
};

export type XScienceBackendProject = {
  projectId: string;
  name: string;
  description: string | null;
  conversationCount: number;
  artifactCount: number;
  createdAt: string | null;
  updatedAt: string | null;
  lastActiveAt: string | null;
};

export type XScienceBackendProjectCatalog = {
  count: number;
  projects: XScienceBackendProject[];
};

export type XScienceCatalog = {
  product: 'X-Science' | string;
  runtime: {
    runtimeAssetsDir: string;
    agents: CountedNames;
    skills: CountedNames;
    mcpServers: CountedNames;
    thirdPartyAssets: CountedNames;
    seedProjects: XScienceSeedProjectCatalog;
  };
  backend: {
    baseUrl: string;
    health: {
      status: string;
      service: string;
      agentsRegistered?: number;
    };
    agents: CountedNames;
    projects: XScienceBackendProjectCatalog;
  };
};

export type XScienceCatalogSummary = {
  product: string;
  backendStatus: string;
  runtimeAssetsDir: string;
  backendBaseUrl: string;
  counts: {
    agents: number;
    skills: number;
    mcpServers: number;
    thirdPartyAssets: number;
    backendAgents: number;
    seedProjects: number;
    backendProjects: number;
  };
  featuredAgents: string[];
  featuredSkills: string[];
  seedProjects: XScienceSeedProject[];
  backendProjects: XScienceBackendProject[];
  mcpServers: string[];
  thirdPartyAssets: string[];
  integrationSurfaces: XScienceIntegrationSurface[];
};

export type XScienceWorkspaceOption = {
  id: string;
  name: string;
  source: 'backend-project' | 'seed-project';
  description: string | null;
  artifactCount: number;
  conversationCount?: number;
  workspaceUri?: string;
  projectId?: string;
  slug?: string;
  manifestPath?: string;
  rootFrameId?: string;
};

export type XScienceIntegrationSurfaceId =
  | 'launch'
  | 'conversation'
  | 'workspace'
  | 'preview'
  | 'capabilities'
  | 'governance'
  | 'runtime';

export type XScienceIntegrationSurface = {
  id: XScienceIntegrationSurfaceId;
  xscienceAiSurface:
    | 'guid'
    | 'conversation'
    | 'workspace'
    | 'preview'
    | 'settings-capabilities'
    | 'settings-governance'
    | 'web-host';
};

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export type XScienceCreateProjectInput = {
  name: string;
  description?: string;
  context?: string;
};

const CATALOG_ENDPOINT = '/api/x-science/catalog';
const PROJECTS_ENDPOINT = '/api/x-science/projects';
const FEATURED_LIMIT = 8;
const X_SCIENCE_ASSISTANT_SORT_BASE = 900_000;
const X_SCIENCE_AGENT_BACKEND = 'xscience';

const SURFACE_DEFINITIONS: XScienceIntegrationSurface[] = [
  {
    id: 'launch',
    xscienceAiSurface: 'guid',
  },
  {
    id: 'conversation',
    xscienceAiSurface: 'conversation',
  },
  {
    id: 'workspace',
    xscienceAiSurface: 'workspace',
  },
  {
    id: 'preview',
    xscienceAiSurface: 'preview',
  },
  {
    id: 'capabilities',
    xscienceAiSurface: 'settings-capabilities',
  },
  {
    id: 'governance',
    xscienceAiSurface: 'settings-governance',
  },
  {
    id: 'runtime',
    xscienceAiSurface: 'web-host',
  },
];

export class XScienceCatalogError extends Error {
  readonly status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = 'XScienceCatalogError';
    this.status = status;
  }
}

export class XScienceAssistantCatalogError extends Error {
  readonly code = 'ASSISTANT_CATALOG_LOAD_FAILED' as const;

  constructor(cause: unknown) {
    super('X-Science assistant catalog is unavailable', { cause });
    this.name = 'XScienceAssistantCatalogError';
  }
}

export async function fetchXScienceCatalog(fetchImpl: FetchLike = fetch): Promise<XScienceCatalog> {
  const response = await fetchImpl(CATALOG_ENDPOINT, {
    method: 'GET',
    headers: { Accept: 'application/json' },
  });

  if (!response.ok) {
    throw new XScienceCatalogError(response.status, await readErrorMessage(response));
  }

  return (await response.json()) as XScienceCatalog;
}

export function summarizeXScienceCatalog(catalog: XScienceCatalog): XScienceCatalogSummary {
  return {
    product: catalog.product,
    backendStatus: catalog.backend.health.status,
    runtimeAssetsDir: catalog.runtime.runtimeAssetsDir,
    backendBaseUrl: catalog.backend.baseUrl,
    counts: {
      agents: catalog.runtime.agents.count,
      skills: catalog.runtime.skills.count,
      mcpServers: catalog.runtime.mcpServers.count,
      thirdPartyAssets: catalog.runtime.thirdPartyAssets.count,
      backendAgents: catalog.backend.health.agentsRegistered ?? catalog.backend.agents.count,
      seedProjects: catalog.runtime.seedProjects.count,
      backendProjects: catalog.backend.projects.count,
    },
    featuredAgents: catalog.runtime.agents.names.slice(0, FEATURED_LIMIT),
    featuredSkills: catalog.runtime.skills.names.slice(0, FEATURED_LIMIT),
    seedProjects: catalog.runtime.seedProjects.projects.slice(0, FEATURED_LIMIT),
    backendProjects: catalog.backend.projects.projects.slice(0, FEATURED_LIMIT),
    mcpServers: catalog.runtime.mcpServers.names,
    thirdPartyAssets: catalog.runtime.thirdPartyAssets.names,
    integrationSurfaces: buildXScienceIntegrationSurfaces(),
  };
}

export async function loadXScienceCatalogSummary(fetchImpl: FetchLike = fetch): Promise<XScienceCatalogSummary> {
  return summarizeXScienceCatalog(await fetchXScienceCatalog(fetchImpl));
}

function buildXScienceProjectWorkspaceUri(project: XScienceBackendProject): string {
  const params = new URLSearchParams();
  if (project.name) {
    params.set('name', project.name);
  }
  params.set('artifacts', String(project.artifactCount));
  params.set('conversations', String(project.conversationCount));

  return `x-science://project/${encodeURIComponent(project.projectId)}?${params.toString()}`;
}

export async function createXScienceProject(
  input: XScienceCreateProjectInput,
  fetchImpl: FetchLike = fetch
): Promise<XScienceWorkspaceOption> {
  const response = await fetchImpl(PROJECTS_ENDPOINT, {
    method: 'POST',
    headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name: input.name,
      description: input.description ?? '',
      context: input.context ?? '',
    }),
  });

  if (!response.ok) {
    throw new XScienceCatalogError(response.status, await readErrorMessage(response));
  }

  const payload = (await response.json()) as { project?: XScienceBackendProject };
  if (!payload.project?.projectId || !payload.project.name) {
    throw new XScienceCatalogError(response.status, 'X-Science project create response is invalid');
  }

  return toXScienceBackendProjectWorkspaceOption(payload.project);
}

function toXScienceBackendProjectWorkspaceOption(project: XScienceBackendProject): XScienceWorkspaceOption {
  return {
    id: `xscience-project:${project.projectId}`,
    name: project.name,
    source: 'backend-project' as const,
    description: project.description,
    artifactCount: project.artifactCount,
    conversationCount: project.conversationCount,
    workspaceUri: buildXScienceProjectWorkspaceUri(project),
    projectId: project.projectId,
  };
}

export function buildXScienceWorkspaceOptions(summary: XScienceCatalogSummary): XScienceWorkspaceOption[] {
  const backendProjects: XScienceWorkspaceOption[] = summary.backendProjects.map(
    toXScienceBackendProjectWorkspaceOption
  );
  const seedProjects: XScienceWorkspaceOption[] = summary.seedProjects.map((project) => ({
    id: `xscience-seed:${project.slug}`,
    name: project.name,
    source: 'seed-project' as const,
    description: null as string | null,
    artifactCount: project.artifactCount,
    slug: project.slug,
    manifestPath: project.manifestPath,
    rootFrameId: project.rootFrameId,
  }));

  return [...backendProjects, ...seedProjects];
}

export function buildXScienceIntegrationSurfaces(): XScienceIntegrationSurface[] {
  return SURFACE_DEFINITIONS.map((surface) => ({
    id: surface.id,
    xscienceAiSurface: surface.xscienceAiSurface,
  }));
}

export function buildXScienceSkills(catalog: XScienceCatalog): XScienceSkillInfo[] {
  return catalog.runtime.skills.names.map((skillName) => ({
    name: skillName,
    description: `X-Science skill from runtime assets: ${skillName}.`,
    location: `${catalog.runtime.runtimeAssetsDir}/skills/${skillName}/SKILL.md`,
    relative_location: `xscience/skills/${skillName}/SKILL.md`,
    is_auto_inject: false,
    is_custom: false,
    source: 'builtin',
  }));
}

export function mergeXScienceSkills<T extends { name: string; source?: string; is_custom?: boolean }>(
  _baseSkills: T[],
  xScienceSkills: XScienceSkillInfo[]
): Array<T | XScienceSkillInfo> {
  return xScienceSkills;
}

function normalizeCatalogName(name: string): string {
  return name.trim().toLowerCase();
}

export async function loadXScienceSkills(fetchImpl: FetchLike = fetch): Promise<XScienceSkillInfo[]> {
  try {
    return buildXScienceSkills(await fetchXScienceCatalog(fetchImpl));
  } catch {
    return [];
  }
}

export function buildXScienceMcpServers(catalog: XScienceCatalog): IMcpServer[] {
  return catalog.runtime.mcpServers.names.flatMap((serverName) => {
    const server = buildXScienceMcpServer(catalog.runtime.runtimeAssetsDir, serverName);
    return server ? [server] : [];
  });
}

export function mergeXScienceMcpServers(baseServers: IMcpServer[], xScienceServers: IMcpServer[]): IMcpServer[] {
  if (xScienceServers.length === 0) return baseServers;

  const existingXScienceServers = new Map(
    baseServers
      .filter((server) => server.builtin === true && isXScienceMcpServer(server))
      .map((server) => [server.id, server])
  );
  const xScienceIds = new Set(xScienceServers.map((server) => server.id));
  const xScienceNames = new Set(xScienceServers.map((server) => normalizeCatalogName(server.name)));
  const userServers = baseServers.filter(
    (server) =>
      server.builtin !== true &&
      !xScienceIds.has(server.id) &&
      !xScienceNames.has(normalizeCatalogName(server.name))
  );

  return [...xScienceServers.map((server) => existingXScienceServers.get(server.id) ?? server), ...userServers];
}

function isXScienceMcpServer(server: Pick<IMcpServer, 'id' | 'name' | 'original_json'>): boolean {
  return server.id.startsWith('xscience:mcp:') || server.original_json.includes('"xscience"');
}
export async function loadXScienceMcpServers(fetchImpl: FetchLike = fetch): Promise<IMcpServer[]> {
  try {
    return buildXScienceMcpServers(await fetchXScienceCatalog(fetchImpl));
  } catch {
    return [];
  }
}

export function buildXScienceAssistants(catalog: XScienceCatalog): Assistant[] {
  const agentNames =
    catalog.backend.agents.names.length > 0 ? catalog.backend.agents.names : catalog.runtime.agents.names;
  const agentStatus = catalog.backend.health.status === 'healthy' ? 'online' : 'offline';

  return agentNames.map((agentName, index) => {
    const id = `xscience:${toXScienceAgentSlug(agentName)}`;
    const displayName = toXScienceAgentDisplayName(agentName);
    const description = `X-Science expert agent connected through ${catalog.backend.baseUrl}.`;
    const zhDescription = `通过 ${catalog.backend.baseUrl} 接入的 X-Science 专家 Agent。`;

    return {
      id,
      source: 'builtin',
      name: displayName,
      name_i18n: {
        'en-US': displayName,
        'zh-CN': displayName,
      },
      description,
      description_i18n: {
        'en-US': description,
        'zh-CN': zhDescription,
      },
      enabled: true,
      sort_order: X_SCIENCE_ASSISTANT_SORT_BASE + index,
      agent_id: agentName,
      agent: {
        type: 'xscience',
        source: 'builtin',
        acp_backend: X_SCIENCE_AGENT_BACKEND,
      },
      enabled_skills: [] as string[],
      custom_skill_names: [] as string[],
      disabled_builtin_skills: [] as string[],
      context: `Use the X-Science backend agent ${agentName} for biomedical research workflows.`,
      context_i18n: {
        'en-US': `Use the X-Science backend agent ${agentName} for biomedical research workflows.`,
        'zh-CN': `使用 X-Science 后端专家 ${agentName} 处理生物医药研究任务。`,
      },
      prompts: [] as string[],
      prompts_i18n: {},
      models: [] as string[],
      agent_status: agentStatus,
      agent_status_message:
        agentStatus === 'online'
          ? undefined
          : `X-Science backend status is ${catalog.backend.health.status || 'unknown'}.`,
      deletable: false,
    } satisfies Assistant;
  });
}

export function mergeXScienceAssistants(
  baseAssistants: Assistant[],
  xScienceAssistants: Assistant[]
): Assistant[] {
  if (xScienceAssistants.length === 0) return [];

  const existingXScienceAssistants = new Map(
    baseAssistants
      .filter((assistant) => isXScienceAssistant(assistant))
      .map((assistant) => [assistant.id, assistant])
  );

  return xScienceAssistants.map((assistant) => existingXScienceAssistants.get(assistant.id) ?? assistant);
}

function isXScienceAssistant(assistant: Assistant): boolean {
  return assistant.id.startsWith('xscience:') || assistant.agent?.type === 'xscience';
}
export async function loadXScienceAssistants(fetchImpl: FetchLike = fetch): Promise<Assistant[]> {
  try {
    const agents = await loadXScienceAgents({ fetchImpl: fetchImpl as typeof fetch });
    return await Promise.all(agents.map(toXScienceAssistant));
  } catch (error) {
    throw new XScienceAssistantCatalogError(error);
  }
}

async function readErrorMessage(response: Response): Promise<string> {
  const text = await response.text();
  if (!text.trim()) {
    return `X-Science catalog request failed with HTTP ${response.status}`;
  }

  try {
    const payload = JSON.parse(text) as { message?: unknown; error?: unknown };
    if (typeof payload.message === 'string') return payload.message;
    if (typeof payload.error === 'string') return payload.error;
  } catch {
    // Non-JSON failures from proxies should still surface their response body.
  }

  return text;
}

function buildXScienceMcpServer(runtimeAssetsDir: string, serverName: string): IMcpServer | null {
  const base = `${runtimeAssetsDir}/mcp-servers`;
  const transport =
    serverName === 'ketcher-chemistry'
      ? {
          type: 'stdio' as const,
          command: 'x-science',
          args: ['mcp-ketcher'],
        }
      : serverName.startsWith('mcp_')
        ? {
            type: 'stdio' as const,
            command: 'python',
            args: [`${base}/bio-tools/run_server.py`, serverName],
          }
        : null;

  if (!transport) {
    return null;
  }

  const originalJson = JSON.stringify(
    {
      source: 'xscience',
      name: serverName,
      transport,
    },
    null,
    2
  );

  return {
    id: `xscience:mcp:${serverName}`,
    name: serverName,
    description: `X-Science MCP server from runtime assets: ${serverName}.`,
    enabled: true,
    transport,
    created_at: 0,
    updated_at: 0,
    original_json: originalJson,
    builtin: true,
  };
}

function toXScienceAgentSlug(agentName: string): string {
  return agentName
    .trim()
    .toLowerCase()
    .replace(/[_\s]+/g, '-')
    .replace(/[^a-z0-9-]/g, '')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');
}

function toXScienceAgentDisplayName(agentName: string): string {
  return agentName
    .trim()
    .split(/[_\s-]+/)
    .filter(Boolean)
    .map((token) => (token.length <= 4 ? token.toUpperCase() : token[0].toUpperCase() + token.slice(1).toLowerCase()))
    .join(' ');
}

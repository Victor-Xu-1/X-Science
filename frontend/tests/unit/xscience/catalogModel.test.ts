/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { describe, expect, it, vi } from 'vitest';
import {
  buildXScienceMcpServers,
  buildXScienceSkills,
  mergeXScienceMcpServers,
  mergeXScienceSkills,
  fetchXScienceCatalog,
  createXScienceProject,
  loadXScienceAssistants,
  summarizeXScienceCatalog,
  type XScienceCatalogError,
  type XScienceCatalog,
} from '@/renderer/services/xScienceCatalog';

it('loads Guid assistants from the user-visible expert profiles instead of the runtime asset catalog', async () => {
  const fetchImpl = vi.fn(async (input: string) => {
    expect(input).toBe('/api/x-science/expert-profiles');
    return new Response(
      JSON.stringify([
        {
          name: 'AIDD_EXPERT',
          displayName: 'AI药物研发专家',
          description: '药物研发',
          source: 'bundled',
          healthy: true,
          enabled: true,
          userHidden: false,
        },
        {
          name: 'BOOKMARKER',
          displayName: '会话重点标记专家',
          source: 'bundled',
          healthy: true,
          enabled: true,
          userHidden: true,
        },
        {
          name: 'USER_EXPERT',
          displayName: '用户专家',
          description: '用户创建',
          source: 'user',
          healthy: true,
          enabled: true,
          userHidden: false,
        },
      ]),
      { status: 200 }
    );
  });

  const assistants = await loadXScienceAssistants(fetchImpl);

  expect(assistants.map((assistant) => assistant.agent_id)).toEqual(['AIDD_EXPERT', 'USER_EXPERT']);
  expect(assistants[0]).toMatchObject({
    name: 'AI Drug Discovery Expert',
    name_i18n: {
      'en-US': 'AI Drug Discovery Expert',
      'zh-CN': 'AI 药物研发专家',
    },
    source: 'builtin',
  });
  expect(assistants[1]).toMatchObject({ name: '用户专家', source: 'user', deletable: true });
});

const catalog: XScienceCatalog = {
  product: 'X-Science',
  runtime: {
    runtimeAssetsDir: '/home/victor_1/xscience-workbench/xscience/runtime/assets',
    agents: { count: 2, names: ['Gene Analysis', 'Variant Review'] },
    skills: { count: 3, names: ['PubMed Search', 'Variant Lookup', 'Trial Match'] },
    mcpServers: { count: 1, names: ['Biomed Tools'] },
    thirdPartyAssets: { count: 2, names: ['NCBI BLAST', 'PubChem'] },
    seedProjects: {
      count: 2,
      projects: [
        {
          slug: 'immunotherapy',
          name: 'Immunotherapy',
          rootFrameId: 'seed-root-1',
          artifactCount: 28,
          childFrameCount: 4,
          folderCount: 3,
        },
        {
          slug: 'crispr_screen',
          name: 'CRISPR Screen',
          rootFrameId: 'seed-root-2',
          artifactCount: 31,
          childFrameCount: 5,
          folderCount: 4,
        },
      ],
    },
  },
  backend: {
    baseUrl: 'http://127.0.0.1:8892',
    health: {
      status: 'healthy',
      service: 'xscience',
      agentsRegistered: 14,
    },
    agents: { count: 14, names: ['Gene Analysis', 'Variant Review'] },
    projects: {
      count: 2,
      projects: [
        {
          projectId: 'proj_stat6',
          name: 'STAT6',
          description: 'STAT6 SBDD PPI workflow',
          conversationCount: 2,
          artifactCount: 23,
          createdAt: '2026-07-02T03:59:09.453Z',
          updatedAt: '2026-07-05T09:24:10.591Z',
          lastActiveAt: '2026-07-05T09:24:10.591Z',
        },
        {
          projectId: 'proj_example',
          name: 'Example project',
          description: 'Example biomedical workflow project',
          conversationCount: 3,
          artifactCount: 83,
          createdAt: '2026-07-02T03:59:09.453Z',
          updatedAt: '2026-07-02T03:59:09.453Z',
          lastActiveAt: '2026-07-02T03:59:09.453Z',
        },
      ],
    },
  },
};

describe('X-Science catalog model', () => {
  it('summarizes runtime capability counts for the settings UI', () => {
    const summary = summarizeXScienceCatalog(catalog);

    expect(summary.product).toBe('X-Science');
    expect(summary.backendStatus).toBe('healthy');
    expect(summary.counts).toEqual({
      agents: 2,
      skills: 3,
      mcpServers: 1,
      thirdPartyAssets: 2,
      backendAgents: 14,
      seedProjects: 2,
      backendProjects: 2,
    });
    expect(summary.seedProjects.map((project) => project.slug)).toEqual(['immunotherapy', 'crispr_screen']);
    expect(summary.backendProjects.map((project) => project.name)).toEqual(['STAT6', 'Example project']);
    expect(summary.featuredAgents).toEqual(['Gene Analysis', 'Variant Review']);
    expect(summary.featuredSkills).toEqual(['PubMed Search', 'Variant Lookup', 'Trial Match']);
    expect(summary.integrationSurfaces.map((surface) => surface.id)).toEqual([
      'launch',
      'conversation',
      'workspace',
      'preview',
      'capabilities',
      'governance',
      'runtime',
    ]);
    expect(summary.integrationSurfaces.every((surface) => !('legacyModules' in surface))).toBe(true);
  });

  it('maps runtime skills into XScience skill catalog entries', () => {
    const skills = buildXScienceSkills({
      ...catalog,
      runtime: {
        ...catalog.runtime,
        skills: { count: 2, names: ['alphafold2', 'cheminfo-render'] },
      },
    });

    expect(skills).toEqual([
      {
        name: 'alphafold2',
        description: 'X-Science skill from runtime assets: alphafold2.',
        location: `${catalog.runtime.runtimeAssetsDir}/skills/alphafold2/SKILL.md`,
        relative_location: 'xscience/skills/alphafold2/SKILL.md',
        is_auto_inject: false,
        is_custom: false,
        source: 'builtin',
      },
      {
        name: 'cheminfo-render',
        description: 'X-Science skill from runtime assets: cheminfo-render.',
        location: `${catalog.runtime.runtimeAssetsDir}/skills/cheminfo-render/SKILL.md`,
        relative_location: 'xscience/skills/cheminfo-render/SKILL.md',
        is_auto_inject: false,
        is_custom: false,
        source: 'builtin',
      },
    ]);
  });

  it('maps launchable runtime MCP servers into XScience tool catalog entries without rewriting third-party code', () => {
    const servers = buildXScienceMcpServers({
      ...catalog,
      runtime: {
        ...catalog.runtime,
        mcpServers: { count: 2, names: ['mcp_pubmed', 'ketcher-chemistry'] },
      },
    });

    expect(servers.map((server) => server.id)).toEqual([
      'xscience:mcp:mcp_pubmed',
      'xscience:mcp:ketcher-chemistry',
    ]);
    expect(servers[0]).toMatchObject({
      name: 'mcp_pubmed',
      builtin: true,
      enabled: true,
      transport: {
        type: 'stdio',
        command: 'python',
        args: [`${catalog.runtime.runtimeAssetsDir}/mcp-servers/bio-tools/run_server.py`, 'mcp_pubmed'],
      },
    });
    expect(servers[1]).toMatchObject({
      name: 'ketcher-chemistry',
      builtin: true,
      enabled: true,
      transport: {
        type: 'stdio',
        command: 'x-science',
        args: ['mcp-ketcher'],
      },
    });
  });

  it('uses X-Science runtime skills as the native catalog and removes unrelated XScience builtin skills', () => {
    const baseSkills = [
      { name: 'cron', description: 'Scheduled tasks', source: 'builtin', is_custom: false },
      { name: 'xiaohongshu-post', description: 'Social media copywriting', source: 'builtin', is_custom: false },
      { name: 'lab-note-template', description: 'User lab note', source: 'custom', is_custom: true },
    ];
    const xScienceSkills = buildXScienceSkills({
      ...catalog,
      runtime: {
        ...catalog.runtime,
        skills: { count: 2, names: ['alphafold2', 'pubmed-search'] },
      },
    });

    expect(mergeXScienceSkills(baseSkills, xScienceSkills).map((skill) => skill.name)).toEqual([
      'alphafold2',
      'pubmed-search',
    ]);
  });

  it('does not fall back to the original skill catalog when X-Science skills are unavailable', () => {
    const baseSkills = [{ name: 'cron', description: 'Scheduled tasks', source: 'builtin', is_custom: false }];

    expect(mergeXScienceSkills(baseSkills, [])).toEqual([]);
  });

  it('uses X-Science MCP servers as the native builtin tool catalog and removes unrelated builtin tools', () => {
    const baseServers = [
      {
        id: 'builtin-browser',
        name: 'browser-tools',
        enabled: true,
        transport: { type: 'stdio' as const, command: 'node', args: ['browser.js'] },
        created_at: 1,
        updated_at: 1,
        original_json: '{}',
        builtin: true,
      },
      {
        id: 'user-lims',
        name: 'LIMS Connector',
        enabled: true,
        transport: { type: 'stdio' as const, command: 'python', args: ['lims.py'] },
        created_at: 2,
        updated_at: 2,
        original_json: '{}',
        builtin: false,
      },
    ];
    const xScienceServers = buildXScienceMcpServers({
      ...catalog,
      runtime: {
        ...catalog.runtime,
        mcpServers: { count: 2, names: ['mcp_pubmed', 'ketcher-chemistry'] },
      },
    });

    expect(mergeXScienceMcpServers(baseServers, xScienceServers).map((server) => server.id)).toEqual([
      'xscience:mcp:mcp_pubmed',
      'xscience:mcp:ketcher-chemistry',
      'user-lims',
    ]);
  });

  it('keeps the original MCP catalog when X-Science MCP servers are unavailable', () => {
    const baseServers = [
      {
        id: 'builtin-browser',
        name: 'browser-tools',
        enabled: true,
        transport: { type: 'stdio' as const, command: 'node', args: ['browser.js'] },
        created_at: 1,
        updated_at: 1,
        original_json: '{}',
        builtin: true,
      },
    ];

    expect(mergeXScienceMcpServers(baseServers, [])).toEqual(baseServers);
  });
  it('fetches the X-Science catalog from the web host API', async () => {
    const fetchImpl = vi.fn(async () => new Response(JSON.stringify(catalog), { status: 200 }));

    await expect(fetchXScienceCatalog(fetchImpl)).resolves.toEqual(catalog);
    expect(fetchImpl).toHaveBeenCalledWith('/api/x-science/catalog', {
      method: 'GET',
      headers: { Accept: 'application/json' },
    });
  });

  it('creates a X-Science backend project through the XScience web host and returns a selectable workspace', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            project: {
              projectId: 'proj_created',
              name: 'XScience Oncology Review',
              description: 'Created from XScience',
              conversationCount: 0,
              artifactCount: 0,
              createdAt: '2026-07-10T01:00:00.000Z',
              updatedAt: '2026-07-10T01:00:00.000Z',
              lastActiveAt: '2026-07-10T01:00:00.000Z',
            },
          }),
          { status: 201 }
        )
    );

    await expect(
      createXScienceProject(
        {
          name: 'XScience Oncology Review',
          description: 'Created from XScience',
          context: 'STAT6 workflow',
        },
        fetchImpl
      )
    ).resolves.toEqual({
      id: 'xscience-project:proj_created',
      name: 'XScience Oncology Review',
      source: 'backend-project',
      description: 'Created from XScience',
      artifactCount: 0,
      conversationCount: 0,
      workspaceUri: 'x-science://project/proj_created?name=XScience+Oncology+Review&artifacts=0&conversations=0',
      projectId: 'proj_created',
    });
    expect(fetchImpl).toHaveBeenCalledWith('/api/x-science/projects', {
      method: 'POST',
      headers: { Accept: 'application/json', 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: 'XScience Oncology Review',
        description: 'Created from XScience',
        context: 'STAT6 workflow',
      }),
    });
  });

  it('raises a typed error when the catalog API is unavailable', async () => {
    const fetchImpl = vi.fn(async () => new Response('backend unavailable', { status: 503 }));

    await expect(fetchXScienceCatalog(fetchImpl)).rejects.toMatchObject({
      name: 'XScienceCatalogError',
      status: 503,
      message: 'backend unavailable',
    } satisfies Partial<XScienceCatalogError>);
  });
});

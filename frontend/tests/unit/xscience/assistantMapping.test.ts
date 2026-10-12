/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { describe, expect, it } from 'vitest';
import {
  buildXScienceAssistants,
  buildXScienceSkills,
  mergeXScienceAssistants,
  mergeXScienceSkills,
  type XScienceCatalog,
} from '@/renderer/services/xScienceCatalog';
import type { Assistant } from '@/common/types/agent/assistantTypes';

const baseCatalog: XScienceCatalog = {
  product: 'X-Science',
  runtime: {
    runtimeAssetsDir: '/home/victor_1/xscience-workbench/xscience/runtime/assets',
    agents: {
      count: 2,
      names: ['aidd-expert', 'genomics-bioinfo-expert'],
    },
    skills: { count: 2, names: ['alphafold2', 'literature-review'] },
    mcpServers: { count: 1, names: ['bio-tools'] },
    thirdPartyAssets: { count: 1, names: ['bionemo-agent-toolkit'] },
  },
  backend: {
    baseUrl: 'http://127.0.0.1:8892',
    health: {
      status: 'healthy',
      service: 'gateway',
      agentsRegistered: 2,
    },
    agents: {
      count: 2,
      names: ['AIDD_EXPERT', 'GENOMICS_BIOINFO_EXPERT'],
    },
  },
};

describe('X-Science assistant mapping', () => {
  it('converts backend expert agents into XScience builtin assistants', () => {
    const assistants = buildXScienceAssistants(baseCatalog);

    expect(assistants.map((assistant) => assistant.id)).toEqual([
      'xscience:aidd-expert',
      'xscience:genomics-bioinfo-expert',
    ]);
    expect(assistants[0]).toMatchObject({
      source: 'builtin',
      name: 'AIDD Expert',
      agent_id: 'AIDD_EXPERT',
      agent_status: 'online',
      deletable: false,
      enabled: true,
      agent: {
        type: 'xscience',
        source: 'builtin',
        acp_backend: 'xscience',
      },
    });
    expect(assistants[0].description_i18n['zh-CN']).toContain('X-Science');
    expect(assistants[1].name_i18n['zh-CN']).toBe('Genomics Bioinfo Expert');
  });

  it('marks expert assistants offline when the X-Science backend is unhealthy', () => {
    const assistants = buildXScienceAssistants({
      ...baseCatalog,
      backend: {
        ...baseCatalog.backend,
        health: {
          ...baseCatalog.backend.health,
          status: 'unavailable',
        },
      },
    });

    expect(assistants).toHaveLength(2);
    expect(assistants.every((assistant) => assistant.agent_status === 'offline')).toBe(true);
    expect(assistants[0].agent_status_message).toContain('unavailable');
  });

  it('preserves existing X-Science assistant state without keeping non-XScience agents', () => {
    const existing: Assistant[] = [
      {
        ...buildXScienceAssistants(baseCatalog)[0],
        name: 'Existing AIDD Expert',
      },
      {
        ...buildXScienceAssistants(baseCatalog)[1],
        id: 'writer',
        name: 'Writer',
        source: 'user',
      },
    ];

    const merged = mergeXScienceAssistants(existing, buildXScienceAssistants(baseCatalog));

    expect(merged.map((assistant) => assistant.id)).toEqual([
      'xscience:aidd-expert',
      'xscience:genomics-bioinfo-expert',
    ]);
    expect(merged[0].name).toBe('Existing AIDD Expert');
  });
  it('makes X-Science experts the only native assistant catalog and removes every other agent source', () => {
    const nonXScienceAssistants: Assistant[] = [
      {
        id: 'unsupported:runtime',
        name: 'Unsupported runtime',
        source: 'generated',
        sort_order: -3,
        enabled: true,
        agent: { type: 'unsupportedRuntime', source: 'internal' },
      },
      {
        id: 'bare:claude-code',
        name: 'Claude Code',
        source: 'generated',
        sort_order: -2,
        enabled: true,
        agent: { type: 'acp', source: 'builtin', acp_backend: 'claude' },
      },
      {
        id: 'bare:codex-cli',
        name: 'Codex CLI',
        source: 'generated',
        sort_order: -1,
        enabled: true,
        agent: { type: 'acp', source: 'builtin', acp_backend: 'codex' },
      },
      {
        id: 'my-lab-note-assistant',
        name: 'My Lab Note Assistant',
        source: 'user',
        sort_order: 100,
        enabled: true,
      },
    ];

    const merged = mergeXScienceAssistants(nonXScienceAssistants, buildXScienceAssistants(baseCatalog));

    expect(merged.map((assistant) => assistant.id)).toEqual([
      'xscience:aidd-expert',
      'xscience:genomics-bioinfo-expert',
    ]);
  });

  it('returns an empty catalog when X-Science experts are unavailable', () => {
    const baseAssistants: Assistant[] = [
      { id: 'bare:codex-cli', name: 'Codex CLI', source: 'generated', sort_order: -1, enabled: true },
      { id: 'my-lab-note-assistant', name: 'My Lab Note Assistant', source: 'user', sort_order: 100, enabled: true },
    ];

    expect(mergeXScienceAssistants(baseAssistants, [])).toEqual([]);
  });

  it('uses X-Science runtime skills as the only native skills catalog', () => {
    const baseSkills = [
      { name: 'cron', source: 'builtin', is_custom: false },
      { name: 'xiaohongshu-post', source: 'builtin', is_custom: false },
      { name: 'lab-note-template', source: 'custom', is_custom: true },
    ];
    const xScienceSkills = buildXScienceSkills(baseCatalog);

    expect(mergeXScienceSkills(baseSkills, xScienceSkills).map((skill) => skill.name)).toEqual([
      'alphafold2',
      'literature-review',
    ]);
    expect(mergeXScienceSkills(baseSkills, [])).toEqual([]);
  });
});

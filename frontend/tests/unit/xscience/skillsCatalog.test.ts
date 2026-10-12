/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  listAvailableSkills: vi.fn(),
  loadXScienceSkills: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    fs: {
      listAvailableSkills: {
        invoke: mocks.listAvailableSkills,
      },
    },
  },
}));

vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: mocks.loadXScienceSkills,
}));

import { loadAvailableSkillsWithXScience } from '@/renderer/services/skills/skillsCatalog';

describe('loadAvailableSkillsWithXScience', () => {
  beforeEach(() => {
    mocks.listAvailableSkills.mockReset();
    mocks.loadXScienceSkills.mockReset();
  });

  it('uses the real X-Science skill catalog as the only native XScience skill catalog', async () => {
    mocks.listAvailableSkills.mockResolvedValue([
      { name: 'cron', description: 'Scheduled tasks', source: 'builtin', is_auto_inject: true },
      { name: 'xiaohongshu-post', description: 'Social media copywriting', source: 'builtin', is_auto_inject: false },
    ]);
    mocks.loadXScienceSkills.mockResolvedValue([
      {
        name: 'alphafold2',
        displayName: 'AlphaFold 2',
        description: 'Predict protein structures from an amino acid sequence.',
        source: 'xscience-runtime',
        category: 'structure',
        license: 'Apache-2.0',
        skillId: 'alphafold2',
        updatedAt: '2026-07-10T00:00:00Z',
        attachedAgents: ['OPERON'],
      },
      {
        name: 'pubmed-search',
        displayName: 'PubMed search',
        description: 'Search biomedical literature.',
        source: 'xscience-runtime',
        category: 'literature',
        license: 'MIT',
        skillId: 'pubmed-search',
        updatedAt: null,
        attachedAgents: ['OPERON'],
      },
    ]);

    await expect(loadAvailableSkillsWithXScience()).resolves.toEqual([
      {
        name: 'alphafold2',
        description: 'Predict protein structures from an amino acid sequence.',
        source: 'xscience-runtime',
        is_auto_inject: false,
        is_custom: false,
        skillId: 'alphafold2',
        displayName: 'AlphaFold 2',
        category: 'structure',
        license: 'Apache-2.0',
        attachedAgents: ['OPERON'],
      },
      {
        name: 'pubmed-search',
        description: 'Search biomedical literature.',
        source: 'xscience-runtime',
        is_auto_inject: false,
        is_custom: false,
        skillId: 'pubmed-search',
        displayName: 'PubMed search',
        category: 'literature',
        license: 'MIT',
        attachedAgents: ['OPERON'],
      },
    ]);
    expect(mocks.listAvailableSkills).not.toHaveBeenCalled();
  });

  it('does not fall back to XScience skills when X-Science skills are unavailable', async () => {
    mocks.listAvailableSkills.mockResolvedValue([
      { name: 'cron', description: 'Scheduled tasks', source: 'builtin', is_auto_inject: true },
    ]);
    mocks.loadXScienceSkills.mockResolvedValue([]);

    await expect(loadAvailableSkillsWithXScience()).resolves.toEqual([]);
    expect(mocks.listAvailableSkills).not.toHaveBeenCalled();
  });
});

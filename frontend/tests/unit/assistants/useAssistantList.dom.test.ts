/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
  loadXScienceAgents: vi.fn(),
  toXScienceAssistant: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    assistants: {
      setState: { invoke: vi.fn(), provider: vi.fn() },
    },
  },
}));

vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceAgents: mocks.loadXScienceAgents,
  toXScienceAssistant: mocks.toXScienceAssistant,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => key,
    i18n: { language: 'en', changeLanguage: vi.fn() },
  }),
}));

import { ipcBridge } from '@/common';
import type { Assistant } from '@/common/types/agent/assistantTypes';
import { useAssistantList } from '@/renderer/hooks/assistant/useAssistantList';

describe('useAssistantList', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.loadXScienceAgents.mockResolvedValue([]);
    mocks.toXScienceAssistant.mockImplementation((agent: Assistant) => agent);
  });

  it('loads backend experts on mount and selects the first one', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([
      xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1),
      xscienceAssistant('xscience:genomics-bioinfo-expert', 'Genomics Bioinfo Expert', 2),
    ]);

    const { result } = renderHook(() => useAssistantList());

    await waitFor(() => expect(result.current.assistants).toHaveLength(2));

    expect(result.current.assistants[0].id).toBe('xscience:aidd-expert');
    expect(result.current.activeAssistantId).toBe('xscience:aidd-expert');
    expect(mocks.loadXScienceAgents).toHaveBeenCalledTimes(1);
  });

  it('uses the capability API instead of IPC or the legacy catalog facade', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1)]);

    const { result } = renderHook(() => useAssistantList());

    await waitFor(() => expect(result.current.assistants).toHaveLength(1));

    expect(mocks.loadXScienceAgents).toHaveBeenCalled();
    expect(ipcBridge.assistants.setState.invoke).not.toHaveBeenCalled();
    expect(result.current.assistants.map((assistant) => assistant.id)).toEqual(['xscience:aidd-expert']);
  });

  it('preserves the backend expert order without client-side resorting', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([
      xscienceAssistant('xscience:structural-biology-expert', 'Structural Biology Expert', 2000),
      xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1000),
    ]);

    const { result } = renderHook(() => useAssistantList());

    await waitFor(() => expect(result.current.assistants).toHaveLength(2));
    expect(result.current.assistants.map((assistant) => assistant.id)).toEqual([
      'xscience:structural-biology-expert',
      'xscience:aidd-expert',
    ]);
  });

  it('handles an empty expert registry', async () => {
    const { result } = renderHook(() => useAssistantList());

    await waitFor(() => expect(mocks.loadXScienceAgents).toHaveBeenCalled());
    expect(result.current.assistants).toHaveLength(0);
    expect(result.current.activeAssistantId).toBeNull();
  });

  it('keeps the active expert after a reload when it remains available', async () => {
    const experts = [
      xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1),
      xscienceAssistant('xscience:oncology-expert', 'Oncology Expert', 2),
    ];
    mocks.loadXScienceAgents.mockResolvedValue(experts);

    const { result } = renderHook(() => useAssistantList());
    await waitFor(() => expect(result.current.assistants).toHaveLength(2));

    act(() => result.current.setActiveAssistantId('xscience:oncology-expert'));
    await act(async () => result.current.loadAssistants());

    expect(result.current.activeAssistantId).toBe('xscience:oncology-expert');
  });

  it('falls back to the first expert when the active expert is removed', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([
      xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1),
      xscienceAssistant('xscience:oncology-expert', 'Oncology Expert', 2),
    ]);
    const { result } = renderHook(() => useAssistantList());
    await waitFor(() => expect(result.current.assistants).toHaveLength(2));

    act(() => result.current.setActiveAssistantId('xscience:oncology-expert'));
    mocks.loadXScienceAgents.mockResolvedValue([xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1)]);
    await act(async () => result.current.loadAssistants());

    expect(result.current.activeAssistantId).toBe('xscience:aidd-expert');
  });

  it('logs a capability API failure without crashing the list', async () => {
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
    mocks.loadXScienceAgents.mockRejectedValue(new Error('Backend down'));

    const { result } = renderHook(() => useAssistantList());

    await waitFor(() => expect(consoleErrorSpy).toHaveBeenCalled());
    expect(result.current.assistants).toHaveLength(0);
    expect(result.current.activeAssistantId).toBeNull();
    consoleErrorSpy.mockRestore();
  });

  it('reorders experts and persists sort order updates', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([
      xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1),
      xscienceAssistant('xscience:oncology-expert', 'Oncology Expert', 2),
      xscienceAssistant('xscience:genomics-bioinfo-expert', 'Genomics Bioinfo Expert', 3),
    ]);
    (ipcBridge.assistants.setState.invoke as any).mockResolvedValue(undefined);

    const { result } = renderHook(() => useAssistantList());
    await waitFor(() => expect(result.current.assistants).toHaveLength(3));
    await act(async () => result.current.reorderAssistants('xscience:genomics-bioinfo-expert', 'xscience:aidd-expert'));

    expect(result.current.assistants.map((assistant) => assistant.id)).toEqual([
      'xscience:genomics-bioinfo-expert',
      'xscience:aidd-expert',
      'xscience:oncology-expert',
    ]);
    expect(ipcBridge.assistants.setState.invoke).toHaveBeenCalledTimes(3);
  });

  it('restores the previous order when ordering persistence fails', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([
      xscienceAssistant('xscience:aidd-expert', 'AIDD Expert', 1),
      xscienceAssistant('xscience:oncology-expert', 'Oncology Expert', 2),
    ]);
    (ipcBridge.assistants.setState.invoke as any).mockRejectedValue(new Error('persist failed'));
    const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

    const { result } = renderHook(() => useAssistantList());
    await waitFor(() => expect(result.current.assistants).toHaveLength(2));
    await act(async () => result.current.reorderAssistants('xscience:oncology-expert', 'xscience:aidd-expert'));

    expect(result.current.assistants.map((assistant) => assistant.id)).toEqual([
      'xscience:aidd-expert',
      'xscience:oncology-expert',
    ]);
    expect(consoleErrorSpy).toHaveBeenCalled();
    consoleErrorSpy.mockRestore();
  });
});

function xscienceAssistant(id: string, name: string, sortOrder: number): Assistant {
  return {
    id,
    name,
    sort_order: sortOrder,
    source: 'builtin',
    enabled: true,
    agent_id: name.toUpperCase().replace(/\s+/g, '_'),
    agent_status: 'online',
    deletable: false,
    name_i18n: {},
    description_i18n: {},
    enabled_skills: [],
    custom_skill_names: [],
    disabled_builtin_skills: [],
    context_i18n: {},
    prompts: [],
    prompts_i18n: {},
    models: [],
    agent: { type: 'xscience', source: 'builtin', acp_backend: 'xscience' },
  };
}

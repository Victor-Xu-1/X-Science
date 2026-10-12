import { expect, describe, it, vi } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

const mocks = vi.hoisted(() => ({
  loadXScienceAgents: vi.fn(),
  toXScienceAssistant: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    assistants: {
      setState: { invoke: vi.fn() },
    },
  },
}));

vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceAgents: mocks.loadXScienceAgents,
  toXScienceAssistant: mocks.toXScienceAssistant,
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    i18n: { language: 'en' },
  }),
}));

import { useAssistantList } from '@/renderer/hooks/assistant/useAssistantList';

describe('useAssistantList direct X-Science API', () => {
  it('loads visible backend experts through the capability service instead of the legacy catalog facade', async () => {
    mocks.loadXScienceAgents.mockResolvedValue([
      {
        name: 'OPERON',
        displayName: 'Operon',
        description: 'Coordinate biomedical research workflows.',
        healthy: true,
        enabled: true,
        source: 'managed',
        skillsLocked: true,
        unrestricted: false,
        supportsPlanMode: true,
        userHidden: false,
        skillNames: ['alphafold2'],
      },
    ]);
    mocks.toXScienceAssistant.mockReturnValue({
      id: 'xscience:operon',
      name: 'Operon',
      enabled: true,
      sort_order: 10000,
    });

    const { result } = renderHook(() => useAssistantList());

    await waitFor(() => expect(result.current.assistants).toHaveLength(1));

    expect(mocks.loadXScienceAgents).toHaveBeenCalledTimes(1);
    expect(mocks.toXScienceAssistant).toHaveBeenCalledWith(expect.objectContaining({ name: 'OPERON' }), 0);
    expect(result.current.activeAssistantId).toBe('xscience:operon');
  });
});

import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({
  loadContext: vi.fn(),
  loadEnabled: vi.fn(),
  loadAutoEnabled: vi.fn(),
  setAutoEnabled: vi.fn(),
  loadProjects: vi.fn(),
}));

vi.mock('@/renderer/services/xScienceMemory', () => ({
  clearXScienceMemories: vi.fn(),
  createXScienceMemory: vi.fn(),
  createXScienceMemoryCategory: vi.fn(),
  deleteXScienceMemory: vi.fn(),
  deleteXScienceMemoryCategory: vi.fn(),
  loadXScienceMemoryContext: mocks.loadContext,
  loadXScienceMemoryEnabled: mocks.loadEnabled,
  loadXScienceAutoMemoryEnabled: mocks.loadAutoEnabled,
  loadXScienceSessionMemories: vi.fn().mockResolvedValue([]),
  setXScienceMemoryEnabled: vi.fn(),
  setXScienceAutoMemoryEnabled: mocks.setAutoEnabled,
  setXScienceProjectMemoryEnabled: vi.fn(),
  updateXScienceMemory: vi.fn(),
  updateXScienceMemoryCategory: vi.fn(),
}));

vi.mock('@/renderer/services/xScienceGateway', () => ({
  loadXScienceProjects: mocks.loadProjects,
}));

import XScienceMemoryManager from '@/renderer/pages/settings/components/XScienceMemoryManager';

describe('XScienceMemoryManager automatic memory control', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.loadContext.mockResolvedValue({
      profileMarkdown: '',
      listingMarkdown: '',
      enabled: null,
      totalRows: 0,
      entities: [],
      sessions: [],
      categories: [],
    });
    mocks.loadEnabled.mockResolvedValue(true);
    mocks.loadAutoEnabled.mockResolvedValue(true);
    mocks.setAutoEnabled.mockResolvedValue(undefined);
    mocks.loadProjects.mockResolvedValue([]);
  });

  it('shows one master memory control and one post-completion extraction control wired to its own endpoint', async () => {
    await act(async () => {
      await renderWithI18n(<XScienceMemoryManager />, 'zh-CN');
    });

    const master = await screen.findByTestId('memory-enabled-toggle');
    const automatic = screen.getByTestId('memory-auto-enabled-toggle');
    await waitFor(() => {
      expect(mocks.loadContext).toHaveBeenCalled();
      expect(mocks.loadProjects).toHaveBeenCalled();
    });
    expect(master).toHaveAttribute('aria-label', '专家记忆');
    expect(automatic).toHaveAttribute('aria-label', '自动记忆');
    expect(automatic).not.toBeDisabled();

    await act(async () => {
      fireEvent.click(automatic);
    });
    await waitFor(() => expect(mocks.setAutoEnabled).toHaveBeenCalledWith(false));
    expect(mocks.loadContext).toHaveBeenCalled();
  });
});

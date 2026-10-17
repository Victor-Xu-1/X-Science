/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const capabilityMocks = vi.hoisted(() => ({
  loadSkills: vi.fn(),
  loadMcpServers: vi.fn(),
}));

vi.mock('@arco-design/web-react', () => ({
  Input: ({
    value,
    onChange,
    placeholder,
  }: {
    value: string;
    onChange: (value: string) => void;
    placeholder: string;
  }) => <input aria-label={placeholder} value={value} onChange={(event) => onChange(event.target.value)} />,
  Trigger: ({ children, popup }: { children: React.ReactNode; popup: React.ReactNode | (() => React.ReactNode) }) => (
    <>
      {children}
      {typeof popup === 'function' ? popup() : popup}
    </>
  ),
}));

vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: (...args: unknown[]) => capabilityMocks.loadSkills(...args),
  loadXScienceMcpServers: (...args: unknown[]) => capabilityMocks.loadMcpServers(...args),
}));

import ComposerCapabilityPicker from '@/renderer/components/media/ComposerCapabilityPicker';

describe('ComposerCapabilityPicker', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    capabilityMocks.loadSkills.mockResolvedValue([]);
    capabilityMocks.loadMcpServers.mockResolvedValue([
      {
        id: 'pubmed',
        name: 'pubmed',
        displayName: 'PubMed',
        description: 'PubMed biomedical literature search.',
      },
    ]);
  });

  it('reloads localized catalog descriptions when the active language changes', async () => {
    let view!: Awaited<ReturnType<typeof renderWithI18n>>;
    await act(async () => {
      view = await renderWithI18n(
        <ComposerCapabilityPicker
          skillNames={[]}
          mcpStatuses={[{ id: 'pubmed', name: 'pubmed', status: 'loaded' }]}
          selectedSkillNames={[]}
          selectedMcpServerIds={[]}
          onSelectSkill={vi.fn()}
          onSelectMcpServer={vi.fn()}
          onOpenMcpSettings={vi.fn()}
        />,
        'zh-CN',
        { wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter> }
      );
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    await waitFor(() =>
      expect(screen.getByRole('menuitemcheckbox', { name: 'PubMed' })).toHaveAttribute(
        'title',
        expect.stringContaining('检索')
      )
    );

    await act(async () => {
      await view.i18n.changeLanguage('en-US');
      await Promise.resolve();
      await Promise.resolve();
    });

    await waitFor(() =>
      expect(screen.getByRole('menuitemcheckbox', { name: 'PubMed' })).toHaveAttribute(
        'title',
        expect.stringContaining('search')
      )
    );
    expect(capabilityMocks.loadMcpServers).toHaveBeenCalledTimes(2);
  });

  it('offers an enabled library Skill through an explicit activation action, never ordinary selection', async () => {
    capabilityMocks.loadSkills.mockResolvedValue([
      {
        name: 'new-library-workflow',
        displayName: 'New library workflow',
        description: 'Read existing files.',
        enabled: true,
      },
      {
        name: 'disabled-library-workflow',
        displayName: 'Disabled library workflow',
        description: 'Excluded.',
        enabled: false,
      },
    ]);
    const onSelectSkill = vi.fn();
    const onEnableSkill = vi.fn();
    await renderWithI18n(
      <ComposerCapabilityPicker
        skillNames={['existing-workflow']}
        mcpStatuses={[]}
        selectedSkillNames={[]}
        selectedMcpServerIds={[]}
        onSelectSkill={onSelectSkill}
        onEnableSkill={onEnableSkill}
        onOpenMcpSettings={vi.fn()}
      />,
      'en-US',
      { wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter> }
    );
    const newSkill = await screen.findByRole('menuitemcheckbox', { name: 'New library workflow' });
    expect(newSkill).toHaveAttribute('aria-checked', 'false');
    expect(onEnableSkill).not.toHaveBeenCalled();
    expect(onSelectSkill).not.toHaveBeenCalled();
    expect(screen.queryByRole('menuitemcheckbox', { name: 'Disabled library workflow' })).not.toBeInTheDocument();
    fireEvent.click(newSkill);
    expect(onEnableSkill).toHaveBeenCalledExactlyOnceWith('new-library-workflow');
    expect(onSelectSkill).not.toHaveBeenCalled();
  });

  it('does not expand a restricted picker when no activation authority is supplied', async () => {
    capabilityMocks.loadSkills.mockResolvedValue([
      {
        name: 'new-library-workflow',
        displayName: 'New library workflow',
        description: 'Read existing files.',
        enabled: true,
      },
    ]);
    await renderWithI18n(
      <ComposerCapabilityPicker
        skillNames={['existing-workflow']}
        mcpStatuses={[]}
        selectedSkillNames={[]}
        selectedMcpServerIds={[]}
        onSelectSkill={vi.fn()}
        onOpenMcpSettings={vi.fn()}
      />,
      'en-US',
      { wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter> }
    );
    await screen.findByRole('menuitemcheckbox', { name: 'existing-workflow' });
    expect(screen.queryByRole('menuitemcheckbox', { name: 'New library workflow' })).not.toBeInTheDocument();
  });
});

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { bindRealtimeRuntime } from '@/common/adapter/httpBridge';
import { parseRealtimeMessage } from '@/common/adapter/realtimeProtocol';
import { createRealtimeRuntime, type RealtimeRuntimeClientObservers } from '@/common/adapter/realtimeRuntime';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
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
  Trigger: ({
    children,
    popup,
    popupVisible,
    onVisibleChange,
  }: {
    children: React.ReactNode;
    popup: React.ReactNode | (() => React.ReactNode);
    popupVisible: boolean;
    onVisibleChange: (visible: boolean) => void;
  }) => (
    <div onMouseEnter={() => onVisibleChange(true)} onMouseLeave={() => onVisibleChange(false)}>
      {children}
      {popupVisible ? (typeof popup === 'function' ? popup() : popup) : null}
    </div>
  ),
}));

vi.mock('@/renderer/services/xScienceCapabilities', () => ({
  loadXScienceSkills: (...args: unknown[]) => capabilityMocks.loadSkills(...args),
  loadXScienceMcpServers: (...args: unknown[]) => capabilityMocks.loadMcpServers(...args),
}));

import ComposerCapabilityPicker from '@/renderer/components/media/ComposerCapabilityPicker';

const renderPicker = async (...args: Parameters<typeof renderWithI18n>) => {
  let view!: Awaited<ReturnType<typeof renderWithI18n>>;
  await act(async () => {
    view = await renderWithI18n(...args);
  });
  return view;
};

const createDeferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
};

const personalSkill = (name: string) => ({
  name,
  displayName: name,
  description: 'Read existing files.',
  source: 'personal',
  enabled: true,
});

let releaseCatalogRuntime: (() => void) | undefined;

async function connectCatalogRuntime() {
  let observers!: RealtimeRuntimeClientObservers;
  const runtime = createRealtimeRuntime({
    createClient: (next) => {
      observers = next;
      return { setIdentity: async () => undefined, dispose: () => undefined };
    },
  });
  const unbind = bindRealtimeRuntime(runtime);
  releaseCatalogRuntime = () => {
    unbind();
    runtime.dispose();
    releaseCatalogRuntime = undefined;
  };
  await runtime.setIdentity('catalog-owner');
  let sequence = 0;
  return () => {
    sequence += 1;
    const parsed = parseRealtimeMessage(
      JSON.stringify({
        type: 'skills.catalog.changed',
        _event_id: `catalog-${sequence}`,
        _sequence: sequence,
        _kind: 'invalidate',
      })
    );
    if (!parsed.ok) throw new Error('Controlled catalog event must satisfy the real protocol');
    observers.onMessage(parsed.message);
  };
}

afterEach(() => releaseCatalogRuntime?.());

describe('ComposerCapabilityPicker', () => {
  it('refreshes the already-open submenu after a remote catalog invalidation without selecting', async () => {
    const invalidate = await connectCatalogRuntime();
    const onSelectSkill = vi.fn();
    const onEnableSkill = vi.fn();
    capabilityMocks.loadSkills.mockResolvedValue([personalSkill('existing-personal-workflow')]);
    const view = await renderPicker(
      <ComposerCapabilityPicker
        skillNames={[]}
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
    await waitFor(() => expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(1));
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
    });
    await screen.findByRole('menuitemcheckbox', { name: 'existing-personal-workflow' });
    capabilityMocks.loadSkills.mockResolvedValue([personalSkill('remotely-published-workflow')]);
    await act(async () => invalidate());
    expect(await screen.findByRole('menuitemcheckbox', { name: 'remotely-published-workflow' })).toHaveAttribute(
      'aria-checked',
      'false'
    );
    expect(screen.queryByRole('menuitemcheckbox', { name: 'existing-personal-workflow' })).not.toBeInTheDocument();
    expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(3);
    expect(onEnableSkill).not.toHaveBeenCalled();
    expect(onSelectSkill).not.toHaveBeenCalled();
    view.unmount();
    await act(async () => invalidate());
    expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(3);
  });

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
      view = await renderPicker(
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
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /已加载 MCP/ }));
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
        source: 'personal',
        enabled: true,
      },
      {
        name: 'disabled-library-workflow',
        displayName: 'Disabled library workflow',
        description: 'Excluded.',
        source: 'personal',
        enabled: false,
      },
    ]);
    const onSelectSkill = vi.fn();
    const onEnableSkill = vi.fn();
    await renderPicker(
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
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
    });
    const newSkill = await screen.findByRole('menuitemcheckbox', { name: 'New library workflow' });
    expect(newSkill).toHaveAttribute('aria-checked', 'false');
    expect(onEnableSkill).not.toHaveBeenCalled();
    expect(onSelectSkill).not.toHaveBeenCalled();
    expect(screen.queryByRole('menuitemcheckbox', { name: 'Disabled library workflow' })).not.toBeInTheDocument();
    await act(async () => {
      fireEvent.click(newSkill);
    });
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
    await renderPicker(
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
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Loaded Skills ·/ }));
    });
    await screen.findByRole('menuitemcheckbox', { name: 'existing-workflow' });
    expect(screen.queryByRole('menuitemcheckbox', { name: 'New library workflow' })).not.toBeInTheDocument();
  });

  it('does not offer an unsigned builtin as a personal activation action', async () => {
    capabilityMocks.loadSkills.mockResolvedValue([
      {
        name: 'outside-builtin',
        displayName: 'Outside builtin',
        description: 'Not admitted.',
        source: 'x_science_llm',
        enabled: true,
      },
    ]);
    await renderPicker(
      <ComposerCapabilityPicker
        skillNames={['existing-workflow']}
        mcpStatuses={[]}
        selectedSkillNames={[]}
        selectedMcpServerIds={[]}
        onSelectSkill={vi.fn()}
        onEnableSkill={vi.fn()}
        onOpenMcpSettings={vi.fn()}
      />,
      'en-US',
      { wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter> }
    );
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
    });
    await screen.findByRole('menuitemcheckbox', { name: 'existing-workflow' });
    expect(screen.queryByRole('menuitemcheckbox', { name: 'Outside builtin' })).not.toBeInTheDocument();
  });

  it('refreshes the personal Skill library every time the submenu opens without selecting a Skill', async () => {
    const onSelectSkill = vi.fn();
    const onEnableSkill = vi.fn();
    await renderPicker(
      <ComposerCapabilityPicker
        skillNames={[]}
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
    await waitFor(() => expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole('menu', { name: 'Skills' })).not.toBeInTheDocument();

    capabilityMocks.loadSkills.mockResolvedValue([personalSkill('newly-saved-workflow')]);
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
    });
    expect(await screen.findByRole('menuitemcheckbox', { name: 'newly-saved-workflow' })).toHaveAttribute(
      'aria-checked',
      'false'
    );
    expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(2);

    await act(async () => {
      fireEvent.mouseLeave(screen.getByRole('menuitem', { name: /^Skills ·/ }));
    });
    expect(screen.queryByRole('menu', { name: 'Skills' })).not.toBeInTheDocument();
    capabilityMocks.loadSkills.mockResolvedValue([
      personalSkill('newly-saved-workflow'),
      personalSkill('later-workflow'),
    ]);
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
    });
    expect(await screen.findByRole('menuitemcheckbox', { name: 'later-workflow' })).toHaveAttribute(
      'aria-checked',
      'false'
    );
    expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(3);
    expect(onEnableSkill).not.toHaveBeenCalled();
    expect(onSelectSkill).not.toHaveBeenCalled();
  });

  it('keeps an empty Skill library reachable through loading, refresh failure, and explicit retry', async () => {
    const pendingRefresh = createDeferred<ReturnType<typeof personalSkill>[]>();
    await renderPicker(
      <ComposerCapabilityPicker
        skillNames={[]}
        mcpStatuses={[]}
        selectedSkillNames={[]}
        selectedMcpServerIds={[]}
        onSelectSkill={vi.fn()}
        onEnableSkill={vi.fn()}
        onOpenMcpSettings={vi.fn()}
      />,
      'en-US',
      { wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter> }
    );
    await waitFor(() => expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(1));
    capabilityMocks.loadSkills.mockReturnValueOnce(pendingRefresh.promise);
    await act(async () => {
      fireEvent.mouseEnter(screen.getByRole('menuitem', { name: 'Skills · 0' }));
    });
    expect(screen.getByRole('status')).toHaveTextContent('Refreshing Skills');
    await act(async () => {
      pendingRefresh.reject(new Error('synthetic-private-catalog-failure'));
    });
    expect(screen.getByRole('alert')).toHaveTextContent('The Skill list could not be refreshed');
    expect(screen.getByRole('alert')).not.toHaveTextContent('synthetic-private-catalog-failure');
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(2);

    capabilityMocks.loadSkills.mockResolvedValueOnce([personalSkill('recovered-workflow')]);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Retry refresh' }));
    });
    expect(await screen.findByRole('menuitemcheckbox', { name: 'recovered-workflow' })).toHaveAttribute(
      'aria-checked',
      'false'
    );
    expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(3);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it.each(['resolve', 'reject'] as const)(
    'ignores an obsolete Skill refresh that later %ss after the submenu has reopened',
    async (completion) => {
      const obsolete = createDeferred<ReturnType<typeof personalSkill>[]>();
      const current = createDeferred<ReturnType<typeof personalSkill>[]>();
      await renderPicker(
        <ComposerCapabilityPicker
          skillNames={[]}
          mcpStatuses={[]}
          selectedSkillNames={[]}
          selectedMcpServerIds={[]}
          onSelectSkill={vi.fn()}
          onEnableSkill={vi.fn()}
          onOpenMcpSettings={vi.fn()}
        />,
        'en-US',
        { wrapper: ({ children }) => <MemoryRouter>{children}</MemoryRouter> }
      );
      await waitFor(() => expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(1));
      capabilityMocks.loadSkills.mockReturnValueOnce(obsolete.promise).mockReturnValueOnce(current.promise);
      await act(async () => {
        fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
      });
      expect(screen.getByRole('status')).toHaveTextContent('Refreshing Skills');
      await act(async () => {
        fireEvent.mouseLeave(screen.getByRole('menuitem', { name: /^Skills ·/ }));
      });
      await act(async () => {
        fireEvent.mouseEnter(screen.getByRole('menuitem', { name: /^Skills ·/ }));
      });
      expect(capabilityMocks.loadSkills).toHaveBeenCalledTimes(3);
      await act(async () => {
        current.resolve([personalSkill('current-workflow')]);
      });
      expect(await screen.findByRole('menuitemcheckbox', { name: 'current-workflow' })).toBeInTheDocument();
      await act(async () => {
        if (completion === 'resolve') obsolete.resolve([personalSkill('obsolete-workflow')]);
        else obsolete.reject(new Error('obsolete catalog failure'));
      });
      expect(screen.getByRole('menuitemcheckbox', { name: 'current-workflow' })).toBeInTheDocument();
      expect(screen.queryByRole('menuitemcheckbox', { name: 'obsolete-workflow' })).not.toBeInTheDocument();
      expect(screen.queryByRole('status')).not.toBeInTheDocument();
      expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    }
  );
});

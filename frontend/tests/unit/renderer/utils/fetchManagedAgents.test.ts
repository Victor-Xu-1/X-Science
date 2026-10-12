/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 *
 * Unit tests for renderer/utils/model/agentTypes.ts → fetchManagedAgents.
 * The settings management fetcher must hit the dedicated `getManagedAgents`
 * bridge (`/api/agents/management`) and degrade to [] on failure.
 */

import { describe, it, expect, beforeEach, vi } from 'vitest';

vi.mock('@/common', () => ({
  ipcBridge: {
    xScience: {
      getManagedAgents: { invoke: vi.fn() },
    },
  },
}));

import { fetchManagedAgents } from '@/renderer/utils/xScience/runtime/runtimeTypes';
import { ipcBridge } from '@/common';

describe('fetchManagedAgents', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('returns rows from the include_disabled (managed) bridge', async () => {
    const rows = [
      {
        id: 'AIDD_EXPERT',
        name: 'AIDD Expert',
        agent_type: 'xscience',
        agent_source: 'builtin',
        enabled: true,
        available: true,
      },
    ];
    (ipcBridge.xScience.getManagedAgents.invoke as any).mockResolvedValue(rows);

    await expect(fetchManagedAgents()).resolves.toEqual(rows);
    expect(ipcBridge.xScience.getManagedAgents.invoke).toHaveBeenCalledTimes(1);
    expect(ipcBridge).not.toHaveProperty('acpConversation.getManagedAgents');
  });

  it('returns [] when the bridge rejects', async () => {
    (ipcBridge.xScience.getManagedAgents.invoke as any).mockRejectedValue(new Error('boom'));

    await expect(fetchManagedAgents()).resolves.toEqual([]);
  });

  it('returns [] when the bridge yields a non-array', async () => {
    (ipcBridge.xScience.getManagedAgents.invoke as any).mockResolvedValue(undefined);

    await expect(fetchManagedAgents()).resolves.toEqual([]);
  });
});

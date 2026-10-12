/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { ManagedAgent } from '@/renderer/utils/xScience/runtime/runtimeTypes';
import { MANAGED_AGENTS_SWR_KEY, fetchManagedAgents } from '@/renderer/utils/xScience/runtime/runtimeTypes';
import { X_SCIENCE_ASSISTANTS_CACHE_KEY } from '@/renderer/services/xScienceCatalog';
import useSWR, { useSWRConfig } from 'swr';

export type UseManagedAgentsResult = {
  agents: ManagedAgent[];
  isLoading: boolean;
  isRefreshing: boolean;
  error: unknown;
  revalidate: () => Promise<ManagedAgent[] | undefined>;
  refreshCatalog: () => Promise<ManagedAgent[] | undefined>;
};

const isXScienceManagedAgent = (agent: ManagedAgent): boolean =>
  agent.agent_type === 'xscience' || agent.backend === 'xscience';

const filterXScienceManagedAgents = (agents?: ManagedAgent[]): ManagedAgent[] =>
  (agents ?? []).filter(isXScienceManagedAgent);

type ScopedMutate = ReturnType<typeof useSWRConfig>['mutate'];

export async function refreshManagedAgentCatalogAndAssistants(
  mutate: ScopedMutate
): Promise<ManagedAgent[] | undefined> {
  const [agents] = await Promise.all([
    mutate<ManagedAgent[]>(MANAGED_AGENTS_SWR_KEY),
    mutate(X_SCIENCE_ASSISTANTS_CACHE_KEY),
  ]);
  return agents;
}

/**
 * Hook for X-Science-only diagnostics surfaces. Reads the dedicated
 * `/api/x-science/experts` diagnostics view (`MANAGED_AGENTS_SWR_KEY`) so
 * unavailable X-Science experts stay visible to assistant and runtime views.
 *
 * `revalidate` refreshes only the X-Science management key. It is the right
 * action for diagnostics-only changes that should not invalidate assistant
 * selection.
 *
 * `refreshCatalog` refreshes the management catalog plus assistant list caches
 * after structural or status changes that can affect X-Science assistants.
 * Business assistant pickers must not depend on this hook.
 *
 * Do not use this to reintroduce generic agent connection or marketplace UI.
 */
export const useManagedAgents = (): UseManagedAgentsResult => {
  const { mutate } = useSWRConfig();
  const { data, isLoading, isValidating, error } = useSWR<ManagedAgent[]>(MANAGED_AGENTS_SWR_KEY, fetchManagedAgents);

  const revalidateManaged = () => mutate<ManagedAgent[]>(MANAGED_AGENTS_SWR_KEY);

  return {
    agents: filterXScienceManagedAgents(data),
    isLoading,
    isRefreshing: isValidating && !isLoading,
    error,
    revalidate: revalidateManaged,
    refreshCatalog: () => refreshManagedAgentCatalogAndAssistants(mutate),
  };
};

/**
 * Lightweight runtime catalog read model for assistant-bound X-Science rows.
 * Uses the same `/api/x-science/experts` payload as the diagnostics hook.
 */
export const useManagedAgentRuntimeCatalog = (): ManagedAgent[] => {
  const { data } = useSWR<ManagedAgent[]>(MANAGED_AGENTS_SWR_KEY, fetchManagedAgents);
  return filterXScienceManagedAgents(data);
};

/**
 * Non-hook entry point for settings/tooling surfaces that need the X-Science
 * diagnostics catalog without mutating a renderer identity cache from outside
 * its React scope.
 */
export async function getManagedAgents(): Promise<ManagedAgent[]> {
  return filterXScienceManagedAgents(await fetchManagedAgents());
}

import { createXScienceSessionDefaults, type XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { DelegationPolicy } from '@/renderer/services/runtime/sessionDelegationPolicy';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

type GuidSessionOptionsInput = {
  baseAgentName: string;
  navigationKey: string;
  resetRequested: boolean;
  ownerId?: string;
  pending?: boolean;
};

/**
 * Owns the lifecycle of an unsent conversation draft. New-task navigation
 * resets every optional module to the product default, while changing the base
 * assistant only updates the draft's base agent.
 */
export function useGuidSessionOptions({
  baseAgentName,
  navigationKey,
  resetRequested,
  ownerId = '',
  pending = false,
}: GuidSessionOptionsInput) {
  const account = getRendererAccountScopeToken();
  const [sessionOptions, setSessionOptions] = useState<XScienceSessionOptions>(() =>
    createXScienceSessionDefaults(baseAgentName)
  );
  const [sessionComputeProviders, setSessionComputeProviders] = useState<string[]>([]);
  const [delegationPolicyOverride, setDelegationPolicyOverride] = useState<DelegationPolicy>();
  const lifetime = useRef({ account, ownerId, resetKey: '', generation: 0 });
  if (
    lifetime.current.account !== account ||
    lifetime.current.ownerId !== ownerId ||
    (resetRequested && lifetime.current.resetKey !== navigationKey)
  ) {
    lifetime.current = {
      account,
      ownerId,
      resetKey: resetRequested ? navigationKey : '',
      generation: lifetime.current.generation + 1,
    };
  }
  const draftGeneration = lifetime.current.generation;
  const baseAgent = useRef(baseAgentName);
  baseAgent.current = baseAgentName;
  const draftOwner = useMemo(() => ({ active: true }), [account, ownerId, draftGeneration]);
  const latestOwner = useRef(draftOwner);
  latestOwner.current = draftOwner;
  useLayoutEffect(() => {
    draftOwner.active = true;
    return () => {
      draftOwner.active = false;
    };
  }, [draftOwner]);
  const delegationActions = useMemo(() => {
    const current = () =>
      draftOwner.active &&
      latestOwner.current === draftOwner &&
      Boolean(ownerId) &&
      getRendererAccountScopeToken() === account &&
      getRendererAccountOwnerId() === ownerId;
    return {
      current,
      change: (enabled: boolean) => {
        if (current()) setDelegationPolicyOverride(enabled ? 'allow' : 'deny');
      },
      refresh: () => {},
      retry: () => {},
    };
  }, [account, ownerId, draftOwner]);
  const delegationControl = useMemo<XScienceDelegationControl>(
    () => ({
      ...delegationActions,
      enabled: delegationActions.current() ? delegationPolicyOverride !== 'deny' : undefined,
      pending,
      available: delegationActions.current(),
      hasLiveAttempts: false,
      error: null,
      retryable: false,
    }),
    [delegationActions, delegationPolicyOverride, pending]
  );

  useLayoutEffect(() => {
    if (draftGeneration === 0) return;
    setSessionOptions(createXScienceSessionDefaults(baseAgent.current));
    setSessionComputeProviders([]);
    setDelegationPolicyOverride(undefined);
  }, [draftGeneration]);

  useEffect(() => {
    setSessionOptions((current) =>
      current.targetAgent === baseAgentName ? current : { ...current, targetAgent: baseAgentName }
    );
  }, [baseAgentName]);

  return {
    sessionOptions,
    setSessionOptions,
    sessionComputeProviders,
    setSessionComputeProviders,
    delegationPolicyOverride,
    draftGeneration,
    delegationControl,
  };
}

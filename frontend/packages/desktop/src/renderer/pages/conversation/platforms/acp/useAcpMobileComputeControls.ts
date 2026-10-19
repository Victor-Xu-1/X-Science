import { useCallback, useEffect, useState, type RefObject } from 'react';
import type { TFunction } from 'i18next';
import { Message } from '@arco-design/web-react';
import {
  loadXScienceComputeProviders,
  loadXScienceSessionComputeProviders,
  setXScienceSessionComputeProvider,
} from '@/renderer/services/xScienceCompute';
import { safeErrorDiagnostic } from './acpSendBoxDiagnostics';

/** Existing mobile compute projection; keeps its request lifetime separate from specialist state. */
export function useAcpMobileComputeControls(
  conversationId: string,
  isMobile: boolean,
  locale: string,
  ownerId: string,
  authority: RefObject<object>,
  mounted: RefObject<boolean>,
  t: TFunction
) {
  const [mobileComputeProviders, setProviders] = useState<Array<{ name: string; label: string }>>([]);
  const [mobileEnabledCompute, setEnabled] = useState<string[]>([]);
  useEffect(() => {
    if (!isMobile) return;
    let active = true;
    void Promise.all([loadXScienceComputeProviders(), loadXScienceSessionComputeProviders(conversationId)])
      .then(([providers, enabled]) => {
        if (!active) return;
        setProviders(
          providers
            .filter((provider) => provider.checked)
            .map((provider) => ({ name: provider.name, label: provider.displayName }))
        );
        setEnabled(enabled);
      })
      .catch((error) =>
        console.warn('[AcpSendBox] Failed to load mobile session controls', safeErrorDiagnostic(error))
      );
    return () => {
      active = false;
    };
  }, [conversationId, isMobile, locale, ownerId]);
  const handleSessionComputeChange = useCallback(
    async (providerName: string, checked: boolean) => {
      const requestAuthority = authority.current;
      try {
        await setXScienceSessionComputeProvider(conversationId, providerName, checked);
        if (!mounted.current || authority.current !== requestAuthority) return;
        setEnabled((current) =>
          checked ? Array.from(new Set([...current, providerName])) : current.filter((name) => name !== providerName)
        );
      } catch (error) {
        if (!mounted.current || authority.current !== requestAuthority) return;
        console.error('[AcpSendBox] Failed to update session compute', safeErrorDiagnostic(error));
        Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
      }
    },
    [conversationId, authority, mounted, t]
  );
  return { mobileComputeProviders, mobileEnabledCompute, handleSessionComputeChange };
}

import { useCallback, useEffect, useMemo, useRef, useState, type RefObject } from 'react';
import type { TFunction } from 'i18next';
import type { TChatConversation } from '@/common/config/storage';
import { Message } from '@arco-design/web-react';
import {
  loadXScienceComputeProviders,
  loadXScienceSessionComputeProviders,
  setXScienceSessionComputeProvider,
} from '@/renderer/services/xScienceCompute';
import { safeErrorDiagnostic } from './acpSendBoxDiagnostics';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

/** Existing mobile compute projection; keeps its request lifetime separate from specialist state. */
export function useAcpMobileComputeControls(
  conversationId: string,
  isMobile: boolean,
  locale: string,
  ownerId: string,
  authority: RefObject<object>,
  mounted: RefObject<boolean>,
  t: TFunction,
  conversation?: TChatConversation
) {
  const projected = conversation?.type === 'acp' && conversation.id === conversationId ? conversation.extra : null;
  const incarnation = projected?.frame_incarnation_id;
  const root = projected?.root_frame_id;
  const rootFrameId =
    ownerId &&
    ownerId === getRendererAccountOwnerId() &&
    typeof incarnation === 'string' &&
    incarnation.trim() &&
    typeof root === 'string' &&
    root.trim()
      ? root
      : null;
  const account = getRendererAccountScopeToken();
  const scope = useMemo(() => ({}), [account, ownerId, conversationId, incarnation, rootFrameId]);
  const currentScope = useRef(scope);
  currentScope.current = scope;
  const isCurrent = useCallback(
    () => mounted.current && currentScope.current === scope && getRendererAccountScopeToken() === account,
    [account, mounted, scope]
  );
  const [view, setView] = useState<{
    scope: typeof scope;
    providers: Array<{ name: string; label: string }>;
    enabled: string[];
  } | null>(null);
  useEffect(() => {
    if (!isMobile || !rootFrameId) return;
    let active = true;
    const requestAuthority = authority.current;
    void Promise.all([loadXScienceComputeProviders(), loadXScienceSessionComputeProviders(rootFrameId)])
      .then(([providers, enabled]) => {
        if (!active || !isCurrent() || authority.current !== requestAuthority) return;
        setView({
          scope,
          providers: providers
            .filter((provider) => provider.checked)
            .map((provider) => ({ name: provider.name, label: provider.displayName })),
          enabled,
        });
      })
      .catch((error) => {
        if (active && isCurrent() && authority.current === requestAuthority)
          console.warn('[AcpSendBox] Failed to load mobile session controls', safeErrorDiagnostic(error));
      });
    return () => {
      active = false;
    };
  }, [rootFrameId, scope, isMobile, locale, authority, isCurrent]);
  const handleSessionComputeChange = useCallback(
    async (providerName: string, checked: boolean) => {
      if (!rootFrameId || !isCurrent()) return;
      const requestAuthority = authority.current;
      try {
        await setXScienceSessionComputeProvider(rootFrameId, providerName, checked);
        if (!isCurrent() || authority.current !== requestAuthority) return;
        setView((current) =>
          current?.scope === scope
            ? {
                ...current,
                enabled: checked
                  ? Array.from(new Set([...current.enabled, providerName]))
                  : current.enabled.filter((name) => name !== providerName),
              }
            : current
        );
      } catch (error) {
        if (!isCurrent() || authority.current !== requestAuthority) return;
        console.error('[AcpSendBox] Failed to update session compute', safeErrorDiagnostic(error));
        Message.error(t('conversation.xscienceRuntime.sessionOptions.computeUpdateFailed'));
      }
    },
    [rootFrameId, scope, authority, isCurrent, t]
  );
  return {
    rootFrameId,
    mobileComputeProviders: view?.scope === scope ? view.providers : [],
    mobileEnabledCompute: view?.scope === scope ? view.enabled : [],
    handleSessionComputeChange,
  };
}

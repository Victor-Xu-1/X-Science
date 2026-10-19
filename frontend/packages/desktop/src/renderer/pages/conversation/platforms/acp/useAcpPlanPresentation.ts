import { useCallback, useLayoutEffect, useRef, useState } from 'react';
import type { XSciencePlanReference } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

/** Read-only relay from the existing snapshot owner, not a second plan reader. */
export function useAcpPlanPresentation(ownerId: string, conversationId: string, incarnationId?: string) {
  const account = getRendererAccountScopeToken();
  const key = JSON.stringify([ownerId, conversationId, incarnationId ?? '', account]);
  const authorityRef = useRef({ key, active: false });
  if (authorityRef.current.key !== key) authorityRef.current = { key, active: false };
  const authority = authorityRef.current;
  const [view, setView] = useState<{
    authority: typeof authority;
    reference: XSciencePlanReference | null;
  } | null>(null);
  useLayoutEffect(() => {
    authority.active = true;
    return () => {
      authority.active = false;
    };
  }, [authority]);
  const onPlanReferenceChange = useCallback(
    (owner: string, reference: XSciencePlanReference | null) => {
      if (
        !authority.active ||
        authorityRef.current !== authority ||
        owner !== conversationId ||
        getRendererAccountScopeToken() !== account
      )
        return;
      setView((current) =>
        current?.authority === authority && JSON.stringify(current.reference) === JSON.stringify(reference)
          ? current
          : { authority, reference }
      );
    },
    [authority, conversationId, account]
  );
  return {
    key,
    reference: view?.authority === authority ? view.reference : null,
    onPlanReferenceChange,
  };
}

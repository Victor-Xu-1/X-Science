import { useEffect, useMemo, useSyncExternalStore } from 'react';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { sessionDelegationIdentity } from '@/renderer/services/runtime/sessionDelegationPolicy';
import {
  EMPTY_SESSION_DELEGATION_VIEW,
  SessionDelegationPolicyController,
  type SessionDelegationView,
} from '@/renderer/services/runtime/sessionDelegationPolicyController';

export type XScienceDelegationControl = {
  enabled: boolean | undefined;
  pending: boolean;
  available: boolean;
  hasLiveAttempts: boolean;
  error: SessionDelegationView['error'];
  retryable: boolean;
  change: (enabled: boolean) => void | Promise<void>;
  retry: () => void | Promise<void>;
  refresh: () => void | Promise<void>;
  current: () => boolean;
};
const noSubscribe = () => () => {};
const empty = () => EMPTY_SESSION_DELEGATION_VIEW;
const noChange = () => {};
export function useSessionDelegationPolicy(ownerId: string, frameId: string, extra: unknown) {
  const account = getRendererAccountScopeToken();
  const identity = sessionDelegationIdentity(ownerId, frameId, extra);
  const incarnation = identity?.frameIncarnationId,
    root = identity?.rootFrameId;
  const validIdentity = Boolean(identity);
  const controller = useMemo(
    () =>
      validIdentity && incarnation && root
        ? new SessionDelegationPolicyController({
            ownerId,
            frameId,
            frameIncarnationId: incarnation,
            rootFrameId: root,
          })
        : null,
    [account, ownerId, frameId, incarnation, root, validIdentity]
  );
  useEffect(() => controller?.attach(), [controller]);
  const view = useSyncExternalStore(controller?.subscribe ?? noSubscribe, controller?.getSnapshot ?? empty, empty);
  return useMemo<XScienceDelegationControl>(
    () => ({
      enabled: view.available && controller?.current() && view.snapshot ? view.snapshot.policy === 'allow' : undefined,
      pending: view.loading || view.writing,
      available: view.available && Boolean(controller?.current()),
      hasLiveAttempts: view.snapshot?.has_live_delegated_attempts ?? false,
      error: view.error,
      retryable: view.retryable,
      change: controller?.change ?? noChange,
      retry: controller?.retry ?? noChange,
      refresh: controller?.refresh ?? noChange,
      current: controller?.current ?? (() => false),
    }),
    [controller, view]
  );
}

export function delegationControlPresentation(control?: XScienceDelegationControl) {
  const current = control?.current() === true;
  const checked = current ? control.enabled : undefined;
  const disabled =
    !current || !control?.available || control.pending || checked === undefined || Boolean(control.error);
  const reason =
    control?.error === 'uncertain'
      ? 'uncertain'
      : control?.error === 'conflict'
        ? 'conflict'
        : control?.error
          ? 'failed'
          : control?.pending || checked === undefined
            ? 'loading'
            : control.hasLiveAttempts
              ? 'live'
              : checked
                ? 'allow'
                : 'deny';
  return { checked, disabled, reason };
}

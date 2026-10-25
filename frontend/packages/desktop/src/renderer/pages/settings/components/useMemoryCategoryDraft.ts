import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  useSettingsHostVisible,
  useSettingsViewMode,
} from '@/renderer/components/settings/SettingsModal/settingsViewContext';
import {
  memoryCategoryDraftOwner,
  type CategoryFields,
  type CategorySeed,
  type ViewLease,
} from '@/renderer/services/runtime/memoryCategoryDraft';

export type CategoryCommitDelivery = {
  isCurrent: () => boolean;
  claimChanged: () => boolean;
  claimSaved: () => boolean;
};
type Callbacks = {
  onCommitted: (delivery: CategoryCommitDelivery) => Promise<void>;
  onFailed: (isCurrent: () => boolean) => void;
  onRefreshFailed: (isCurrent: () => boolean) => void;
};

/** View registration is disposable; the owner and its single write are not. */
export function useMemoryCategoryDraft(callbacks: Callbacks) {
  const hostVisible = useSettingsHostVisible();
  const layer = useSettingsViewMode();
  const snapshot = useSyncExternalStore(
    memoryCategoryDraftOwner.subscribe,
    memoryCategoryDraftOwner.getSnapshot,
    memoryCategoryDraftOwner.getSnapshot
  );
  const scope = getRendererAccountScopeToken();
  const [view, setView] = useState<ViewLease | null>(null);
  const callbacksRef = useRef(callbacks);
  const visibleRef = useRef(hostVisible);
  callbacksRef.current = callbacks;
  // The render fence precedes passive cleanup, including cached Arco subtrees.
  visibleRef.current = hostVisible;

  useEffect(() => {
    if (!hostVisible || !getRendererAccountOwnerId()) {
      setView(null);
      return;
    }
    const lease = memoryCategoryDraftOwner.registerView(layer);
    setView(lease);
    return () => memoryCategoryDraftOwner.unregisterView(lease);
  }, [hostVisible, scope, layer]);

  useEffect(() => {
    if (!hostVisible) return;
    const lease = memoryCategoryDraftOwner.captureRefresh(view);
    if (!lease) return;
    let active = true;
    const current = () =>
      active &&
      visibleRef.current &&
      getRendererAccountScopeToken() === lease.view.scope &&
      memoryCategoryDraftOwner.isRefreshCurrent(lease);
    const delivery: CategoryCommitDelivery = {
      isCurrent: current,
      claimChanged: () => current() && memoryCategoryDraftOwner.claimChanged(lease),
      claimSaved: () => current() && memoryCategoryDraftOwner.claimSaved(lease),
    };
    void (async () => {
      try {
        if (current()) await callbacksRef.current.onCommitted(delivery);
      } catch {
        if (current()) callbacksRef.current.onRefreshFailed(current);
      } finally {
        if (current()) memoryCategoryDraftOwner.acknowledgeRefresh(lease);
      }
    })();
    return () => {
      active = false;
    };
  }, [hostVisible, scope, view, snapshot.activation, snapshot.commitSerial]);

  useEffect(() => {
    if (!hostVisible) return;
    const lease = memoryCategoryDraftOwner.captureFailure(view);
    if (!lease) return;
    const current = () =>
      visibleRef.current &&
      getRendererAccountScopeToken() === lease.view.scope &&
      memoryCategoryDraftOwner.getSnapshot().activation === lease.activation;
    if (current() && memoryCategoryDraftOwner.claimFailure(lease)) callbacksRef.current.onFailed(current);
  }, [hostVisible, scope, view, snapshot.activation, snapshot.failedAttempt]);

  const owns =
    hostVisible &&
    Boolean(view && view.scope === scope && snapshot.scope === scope && snapshot.visibleView === view.id);
  const action = owns ? memoryCategoryDraftOwner.actionLease(view) : null;
  const editor = owns ? snapshot.draft : null;
  const draft: CategorySeed | null = editor ? { ...(editor.id ? { id: editor.id } : {}), ...editor.value } : null;
  const canAct = () =>
    visibleRef.current &&
    Boolean(view && view.scope === getRendererAccountScopeToken()) &&
    memoryCategoryDraftOwner.isViewRegistered(view);
  return {
    available: hostVisible && memoryCategoryDraftOwner.isViewRegistered(view),
    draft,
    busy: snapshot.draft?.phase === 'saving',
    openCreate: () => canAct() && memoryCategoryDraftOwner.open(view),
    openEdit: (seed: CategorySeed) => canAct() && memoryCategoryDraftOwner.open(view, seed),
    update: (patch: Partial<CategoryFields>) => canAct() && memoryCategoryDraftOwner.update(action, patch),
    discard: () => canAct() && memoryCategoryDraftOwner.discard(action),
    save: () => (canAct() ? memoryCategoryDraftOwner.save(action) : Promise.resolve({ kind: 'denied' as const })),
  };
}

import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import { useAuth } from '@/renderer/hooks/context/AuthContext';
import { getClientBusinessSetting, setClientBusinessSetting } from '@/renderer/services/clientBusinessSettings';
import { getRendererAccountOwnerId, getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

const preferenceKey = 'system.notificationEnabled';
const invalidationPrefix = 'changed:';

type PreferenceState =
  | { status: 'loading' | 'readFailed' }
  | { status: 'ready'; enabled: boolean; saving: boolean; saveFailed: boolean };

type Preference = {
  state: PreferenceState;
  canDeliver: () => boolean;
  retry: () => void;
  setEnabled: (enabled: boolean) => void;
};

type PreferenceRequest = {
  controller: AbortController;
  scope: string;
  invalidated: boolean;
};

const initialState: PreferenceState = { status: 'loading' };
const BrowserNotificationPreferenceContext = createContext<Preference>({
  state: initialState,
  canDeliver: () => false,
  retry: () => {},
  setEnabled: () => {},
});

export function useBrowserNotificationPreference(): Preference {
  return useContext(BrowserNotificationPreferenceContext);
}

function announcePreferenceChange(key: string) {
  try {
    // Storage is a same-origin wakeup only. The value lives on the server.
    const nonce = window.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random()}`;
    window.localStorage.setItem(key, invalidationPrefix + nonce);
    window.localStorage.removeItem(key);
  } catch {
    // Some browser configurations block storage. The write already settled.
  }
}

function AccountPreference({ owner, scope, children }: React.PropsWithChildren<{ owner?: string; scope: string }>) {
  const changeKey = owner ? `x-science:notification-preference-changed:${encodeURIComponent(owner)}` : undefined;
  const [state, setState] = useState<PreferenceState>(initialState);
  const current = useRef<PreferenceState>(initialState);
  const active = useRef<PreferenceRequest | null>(null);

  const publish = useCallback((next: PreferenceState) => {
    current.current = next;
    setState(next);
  }, []);

  const canDeliver = useCallback(() => {
    const snapshot = current.current;
    return (
      getRendererAccountScopeToken() === scope && snapshot.status === 'ready' && snapshot.enabled && !snapshot.saving
    );
  }, [scope]);

  const isCurrent = useCallback(
    (request: PreferenceRequest) =>
      active.current === request &&
      !request.controller.signal.aborted &&
      getRendererAccountScopeToken() === request.scope,
    []
  );

  const read = useCallback(
    function readCurrent(saveFailed = false) {
      if (!owner || owner !== getRendererAccountOwnerId() || getRendererAccountScopeToken() !== scope || active.current)
        return;
      const request = { controller: new AbortController(), scope, invalidated: false };
      active.current = request;
      publish(initialState);
      void (async () => {
        let next: PreferenceState;
        try {
          const value = await getClientBusinessSetting(preferenceKey, { signal: request.controller.signal });
          next = { status: 'ready', enabled: value !== false, saving: false, saveFailed };
        } catch {
          next = { status: 'readFailed' };
        }
        if (!isCurrent(request)) return;
        active.current = null;
        // The pending response can predate another tab's write. Coalesce
        // invalidations into one fresh read after the old request settles.
        if (request.invalidated) readCurrent(saveFailed);
        else publish(next);
      })();
    },
    [isCurrent, owner, publish, scope]
  );

  useEffect(() => {
    if (!changeKey) return;
    const onStorage = (event: StorageEvent) => {
      if (
        event.key !== changeKey ||
        !event.newValue?.startsWith(invalidationPrefix) ||
        getRendererAccountScopeToken() !== scope
      )
        return;
      if (active.current) active.current.invalidated = true;
      else read();
    };
    window.addEventListener('storage', onStorage);
    read();
    return () => {
      window.removeEventListener('storage', onStorage);
      active.current?.controller.abort();
      active.current = null;
    };
  }, [changeKey, read, scope]);

  function setEnabled(enabled: boolean) {
    const snapshot = current.current;
    if (
      !changeKey ||
      owner !== getRendererAccountOwnerId() ||
      getRendererAccountScopeToken() !== scope ||
      active.current ||
      snapshot.status !== 'ready' ||
      snapshot.enabled === enabled
    )
      return;
    const request = { controller: new AbortController(), scope, invalidated: false };
    active.current = request;
    publish({ ...snapshot, saving: true, saveFailed: false });
    void (async () => {
      let saveFailed = false;
      try {
        await setClientBusinessSetting(preferenceKey, enabled, { signal: request.controller.signal });
      } catch {
        // A lost PUT response can still represent a server-side commit.
        saveFailed = true;
      }
      const currentRequest = isCurrent(request);
      if (!saveFailed || currentRequest) announcePreferenceChange(changeKey);
      if (!currentRequest) return;
      active.current = null;
      if (request.invalidated || saveFailed) read(saveFailed);
      else publish({ status: 'ready', enabled, saving: false, saveFailed: false });
    })();
  }

  return (
    <BrowserNotificationPreferenceContext.Provider value={{ state, canDeliver, retry: read, setEnabled }}>
      {children}
    </BrowserNotificationPreferenceContext.Provider>
  );
}

export function BrowserNotificationPreferenceProvider({ children }: React.PropsWithChildren) {
  const { status, user } = useAuth();
  const owner = status === 'authenticated' ? user?.id : undefined;
  const scope = getRendererAccountScopeToken();
  return (
    <AccountPreference key={scope} owner={owner} scope={scope}>
      {children}
    </AccountPreference>
  );
}

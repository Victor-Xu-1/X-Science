import { useCallback, useRef, useState, type RefObject } from 'react';
import {
  saveXScienceConversationAsSkill,
  XScienceSkillLibraryError,
} from '@/renderer/services/skills/xScienceSkillLibrary';

export type ConversationSkillSaveState =
  | { status: 'idle' }
  | { status: 'saving' }
  | { status: 'error' }
  | { status: 'conflict' }
  | { status: 'saved'; name: string; refresh: 'pending' | 'ready' | 'error' };

type SaveScope = {
  authority: object;
  ownerId: string;
  inFlight: boolean;
  state: ConversationSkillSaveState;
};

type ConversationSkillSaveOptions = {
  conversationId: string;
  ownerId: string;
  authority: object;
  mountedRef: RefObject<boolean>;
  disabled: boolean;
  refreshCatalogs: (isCurrent: () => boolean) => Promise<void>;
};

// The scope follows the composer's existing conversation authority. In-flight
// requests are not cancelled or retried when their view disappears: a save may
// already have committed, but its late result must not affect a different view.
export function useConversationSkillSave({
  conversationId,
  ownerId,
  authority,
  mountedRef,
  disabled,
  refreshCatalogs,
}: ConversationSkillSaveOptions) {
  const scopeRef = useRef<SaveScope>({ authority, ownerId, inFlight: false, state: { status: 'idle' } });
  if (scopeRef.current.authority !== authority || scopeRef.current.ownerId !== ownerId) {
    scopeRef.current = { authority, ownerId, inFlight: false, state: { status: 'idle' } };
  }
  const scope = scopeRef.current;
  const [snapshot, setSnapshot] = useState({ scope, state: scope.state });
  const state = snapshot.scope === scope ? snapshot.state : scope.state;

  const isCurrent = useCallback(
    (request: SaveScope) => mountedRef.current === true && scopeRef.current === request,
    [mountedRef]
  );
  const publish = useCallback((request: SaveScope, next: ConversationSkillSaveState) => {
    request.state = next;
    setSnapshot({ scope: request, state: next });
  }, []);

  const refreshSavedSkill = useCallback(
    async (request: SaveScope, name: string) => {
      try {
        await refreshCatalogs(() => isCurrent(request));
        if (isCurrent(request)) publish(request, { status: 'saved', name, refresh: 'ready' });
      } catch {
        // A committed save remains successful. Retrying this state only reloads
        // the existing catalogs; it must never create a second personal Skill.
        if (isCurrent(request)) publish(request, { status: 'saved', name, refresh: 'error' });
      } finally {
        request.inFlight = false;
      }
    },
    [isCurrent, publish, refreshCatalogs]
  );

  const save = useCallback(async () => {
    if (!isCurrent(scope) || disabled || scope.inFlight || !conversationId.trim()) return;
    if (scope.state.status === 'saved' && scope.state.refresh !== 'ready') return;
    scope.inFlight = true;
    publish(scope, { status: 'saving' });
    let name: string;
    try {
      const result = await saveXScienceConversationAsSkill(conversationId);
      name = result.name;
    } catch (error) {
      scope.inFlight = false;
      if (isCurrent(scope)) {
        publish(scope, {
          status: error instanceof XScienceSkillLibraryError && error.status === 409 ? 'conflict' : 'error',
        });
      }
      return;
    }
    if (!isCurrent(scope)) {
      scope.inFlight = false;
      return;
    }
    publish(scope, { status: 'saved', name, refresh: 'pending' });
    await refreshSavedSkill(scope, name);
  }, [conversationId, disabled, isCurrent, publish, refreshSavedSkill, scope]);

  const retryRefresh = useCallback(async () => {
    if (!isCurrent(scope) || scope.inFlight || scope.state.status !== 'saved' || scope.state.refresh !== 'error') {
      return;
    }
    const name = scope.state.name;
    scope.inFlight = true;
    publish(scope, { status: 'saved', name, refresh: 'pending' });
    await refreshSavedSkill(scope, name);
  }, [isCurrent, publish, refreshSavedSkill, scope]);

  return {
    state,
    save,
    retryRefresh,
    saveDisabled: disabled || state.status === 'saving' || (state.status === 'saved' && state.refresh !== 'ready'),
  };
}

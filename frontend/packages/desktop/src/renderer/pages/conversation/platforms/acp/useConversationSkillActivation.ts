import { ipcBridge } from '@/common';
import {
  loadXScienceExpertProfiles,
  updateXScienceExpertSkills,
} from '@/renderer/services/agents/xScienceExpertProfiles';
import { loadXScienceSkills } from '@/renderer/services/xScienceCapabilities';
import { useCallback, useRef, useState, type RefObject } from 'react';

export type SkillActivationRequest = {
  name: string;
  phase: 'confirm' | 'enabling' | 'error' | 'refresh-error';
  pending: boolean;
  attached: boolean;
};
type ActivationScope = {
  authority: object;
  ownerId: string;
  target: string;
  request: SkillActivationRequest | null;
};
type Options = {
  authority: object;
  ownerId: string;
  assistantId?: string;
  runtimeAgentName?: string;
  locale: string;
  mountedRef: RefObject<boolean>;
  disabled: boolean;
  refreshCatalogs: (isCurrent: () => boolean) => Promise<string[]>;
  onSelectSkill: (name: string) => void;
};
const contains = (names: string[], name: string) => names.some((item) => item.toLowerCase() === name.toLowerCase());

// Admission is an explicit user action, separate from Save and from Send.
// Reuse the existing additive profile API; never replace its other bindings.
export function useConversationSkillActivation(options: Options) {
  const { authority, ownerId, assistantId, runtimeAgentName, mountedRef, disabled } = options;
  const target = JSON.stringify([assistantId ?? '', runtimeAgentName ?? '', disabled]);
  const scopeRef = useRef<ActivationScope>({ authority, ownerId, target, request: null });
  if (
    scopeRef.current.authority !== authority ||
    scopeRef.current.ownerId !== ownerId ||
    scopeRef.current.target !== target
  ) {
    scopeRef.current = { authority, ownerId, target, request: null };
  }
  const scope = scopeRef.current;
  const [snapshot, setSnapshot] = useState({ scope, request: scope.request });
  const state = snapshot.scope === scope ? snapshot.request : scope.request;
  const current = useCallback(
    (request: SkillActivationRequest) =>
      mountedRef.current === true && scopeRef.current === scope && scope.request === request,
    [mountedRef, scope]
  );
  const publish = (request: SkillActivationRequest | null) => {
    scope.request = request;
    setSnapshot({ scope, request: request ? { ...request } : null });
  };
  const canActivate = Boolean(assistantId?.trim() || runtimeAgentName?.trim()) && !disabled;
  const request = (name: string) => {
    if (!canActivate || !mountedRef.current || scopeRef.current !== scope || scope.request?.pending || !name.trim())
      return;
    publish({ name: name.trim(), phase: 'confirm', pending: false, attached: false });
  };
  const cancel = () => {
    if (scopeRef.current === scope && !scope.request?.pending) publish(null);
  };
  const confirm = async () => {
    const active = scope.request;
    if (!active || active.pending || disabled || !current(active)) return;
    active.pending = true;
    active.phase = 'enabling';
    publish(active);
    try {
      if (!active.attached) {
        const library = await loadXScienceSkills();
        if (!current(active)) return;
        const skill = library.find((item) => item.name.toLowerCase() === active.name.toLowerCase() && item.enabled);
        if (!skill) throw new Error('Skill is not enabled in the current library');
        let canonicalAgent = runtimeAgentName?.trim() ?? '';
        if (assistantId) {
          const detail = await ipcBridge.assistants.get.invoke({ id: assistantId, locale: options.locale });
          if (!current(active)) return;
          if (detail.id !== assistantId || !detail.engine.agent_id.trim())
            throw new Error('Assistant identity changed');
          canonicalAgent = detail.engine.agent_id;
        }
        const profiles = await loadXScienceExpertProfiles();
        if (!current(active)) return;
        const profile = profiles.find((item) => item.name.toLowerCase() === canonicalAgent.toLowerCase());
        if (!profile || !profile.enabled) throw new Error('Current expert is unavailable');
        if (!contains(profile.skillNames, skill.name)) {
          const updated = await updateXScienceExpertSkills(profile.name, [], [skill.name]);
          if (!current(active)) return;
          if (updated.name !== profile.name || !contains(updated.skillNames, skill.name))
            throw new Error('Binding not confirmed');
        }
        active.attached = true;
      }
      const admitted = await options.refreshCatalogs(() => current(active));
      if (!current(active)) return;
      if (!contains(admitted, active.name)) throw new Error('Current assistant still excludes this Skill');
      options.onSelectSkill(active.name);
      if (current(active)) publish(null);
    } catch {
      if (current(active)) {
        active.phase = active.attached ? 'refresh-error' : 'error';
        publish(active);
      }
    } finally {
      active.pending = false;
      if (current(active)) publish(active);
    }
  };
  return { state, canActivate, request, confirm, cancel };
}

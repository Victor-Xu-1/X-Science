import { useCallback, useMemo, type RefObject } from 'react';
import useSWR, { type KeyedMutator } from 'swr';
import {
  loadXScienceComposerCapabilities,
  type XScienceComposerCapabilities,
} from '@/renderer/services/xScienceGateway';
import { loadXScienceSkills } from '@/renderer/services/xScienceCapabilities';
import { canActivateConversationSkill, useConversationSkillActivation } from './useConversationSkillActivation';

type Options = {
  conversationId: string;
  ownerId: string;
  authority: object;
  mountedRef: RefObject<boolean>;
  disabled: boolean;
  isXScience: boolean;
  isMobile: boolean;
  assistantId?: string;
  runtimeAgentName?: string;
  locale: string;
  loadedSkills: string[];
  mutateComposerCapabilities: KeyedMutator<XScienceComposerCapabilities>;
  fetchSlashCommands: (options?: { fresh?: boolean }) => Promise<void>;
  onSelectSkill: (name: string) => void;
};

// One catalog refresh barrier serves Save and explicit current-expert activation.
// Selection and Send remain owned by the composer, not by catalog mutation.
export function useAcpConversationSkills(options: Options) {
  const { conversationId, ownerId, authority, mountedRef, disabled, mutateComposerCapabilities, fetchSlashCommands } =
    options;
  const activationDisabled = disabled || !options.isXScience;
  const mobileLibraryActive =
    options.isMobile &&
    canActivateConversationSkill({
      assistantId: options.assistantId,
      runtimeAgentName: options.runtimeAgentName,
      disabled: activationDisabled,
    });
  const { data: mobileSkillLibrary, mutate: mutateMobileSkillLibrary } = useSWR(
    mobileLibraryActive ? ['x-science-mobile-skill-library', ownerId, conversationId] : null,
    () => loadXScienceSkills(),
    { keepPreviousData: false }
  );
  const refreshCatalogs = useCallback(
    async (isCurrent: () => boolean) => {
      if (!isCurrent()) throw new Error('Skill catalog refresh no longer current');
      const readCurrent = async <T>(load: () => Promise<T>) => {
        if (!isCurrent()) throw new Error('Skill catalog refresh no longer current');
        const value = await load();
        if (!isCurrent()) throw new Error('Skill catalog refresh no longer current');
        return value;
      };
      // Start the bound mutation before yielding so SWR captures this conversation's
      // key. The mounted mobile library shares the same readback barrier.
      const refreshed = await Promise.allSettled([
        mutateComposerCapabilities(() => readCurrent(() => loadXScienceComposerCapabilities(conversationId)), {
          revalidate: false,
        }),
        readCurrent(() => fetchSlashCommands({ fresh: true })),
        mobileLibraryActive
          ? mutateMobileSkillLibrary(() => readCurrent(loadXScienceSkills), { revalidate: false })
          : undefined,
      ]);
      if (!isCurrent()) throw new Error('Skill catalog refresh no longer current');
      if (refreshed.some((result) => result.status === 'rejected')) throw new Error('Skill catalog refresh failed');
      const capabilities = refreshed[0];
      if (capabilities.status !== 'fulfilled' || !capabilities.value) throw new Error('Skill capabilities unavailable');
      return capabilities.value.skills;
    },
    [conversationId, fetchSlashCommands, mobileLibraryActive, mutateComposerCapabilities, mutateMobileSkillLibrary]
  );
  const skillActivation = useConversationSkillActivation({
    authority,
    ownerId,
    mountedRef,
    assistantId: options.assistantId,
    runtimeAgentName: options.runtimeAgentName,
    locale: options.locale,
    disabled: activationDisabled,
    refreshCatalogs,
    onSelectSkill: options.onSelectSkill,
  });
  const mobileLibraryNames = useMemo(
    () =>
      skillActivation.canActivate
        ? (mobileSkillLibrary ?? [])
            .filter((skill) => skill.enabled && skill.source === 'personal')
            .map((skill) => skill.name)
        : [],
    [mobileSkillLibrary, skillActivation.canActivate]
  );
  return { refreshCatalogs, skillActivation, mobileLibraryNames };
}

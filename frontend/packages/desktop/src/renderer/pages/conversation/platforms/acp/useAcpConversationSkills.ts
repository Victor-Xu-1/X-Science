import { useCallback, useMemo, type RefObject } from 'react';
import useSWR, { type KeyedMutator } from 'swr';
import {
  loadXScienceComposerCapabilities,
  type XScienceComposerCapabilities,
} from '@/renderer/services/xScienceGateway';
import { loadXScienceSkills } from '@/renderer/services/xScienceCapabilities';
import { useConversationSkillActivation } from './useConversationSkillActivation';

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
  const refreshCatalogs = useCallback(
    async (isCurrent: () => boolean) => {
      // Start the bound mutation before yielding so SWR captures this conversation's
      // key. Await both real reads; a failed read is not an empty Skill catalog.
      const refreshed = await Promise.allSettled([
        mutateComposerCapabilities(
          async () => {
            const capabilities = await loadXScienceComposerCapabilities(conversationId);
            if (!isCurrent()) throw new Error('Skill catalog refresh no longer current');
            return capabilities;
          },
          { revalidate: false }
        ),
        fetchSlashCommands({ fresh: true }),
      ]);
      if (refreshed.some((result) => result.status === 'rejected')) throw new Error('Skill catalog refresh failed');
      const capabilities = refreshed[0];
      if (capabilities.status !== 'fulfilled' || !capabilities.value) throw new Error('Skill capabilities unavailable');
      return capabilities.value.skills;
    },
    [conversationId, fetchSlashCommands, mutateComposerCapabilities]
  );
  const skillActivation = useConversationSkillActivation({
    authority,
    ownerId,
    mountedRef,
    assistantId: options.assistantId,
    runtimeAgentName: options.runtimeAgentName,
    locale: options.locale,
    disabled: disabled || !options.isXScience,
    refreshCatalogs,
    onSelectSkill: options.onSelectSkill,
  });
  const { data: mobileSkillLibrary } = useSWR(
    options.isMobile && skillActivation.canActivate
      ? ['x-science-mobile-skill-library', ownerId, conversationId]
      : null,
    () => loadXScienceSkills(),
    { keepPreviousData: false }
  );
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

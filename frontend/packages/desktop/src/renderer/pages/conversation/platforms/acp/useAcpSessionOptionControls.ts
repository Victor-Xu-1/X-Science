import { useCallback, type Dispatch, type RefObject, type SetStateAction } from 'react';
import { useXScienceMemoryAvailability } from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import { updateXScienceSessionConfig, type XScienceSessionOptions } from '@/renderer/services/xScienceSessionOptions';

type ConversationOwner = { conversationId: string; ownerId: string };

/** Reuses the existing composer mutation owner; desktop/mobile share its one capability read. */
export function useAcpSessionOptionControls(
  conversationId: string,
  ownerKey: string,
  authority: RefObject<ConversationOwner>,
  mounted: RefObject<boolean>,
  setOptions: Dispatch<SetStateAction<XScienceSessionOptions>>,
  setMobileOpen: (open: boolean) => void
) {
  const memoryControl = useXScienceMemoryAvailability(ownerKey);
  const updatePersistentSessionOption = useCallback(
    async (key: 'autoReview' | 'memory', value: boolean) => {
      const requestOwner = authority.current;
      if (key === 'memory' && !memoryControl.canUpdate()) return;
      try {
        await updateXScienceSessionConfig(conversationId, { [key]: value });
      } catch (error) {
        if (!mounted.current || authority.current !== requestOwner || !memoryControl.current()) return;
        throw error;
      }
      if (!mounted.current || authority.current !== requestOwner || !memoryControl.current()) return;
      setOptions((current) => ({ ...current, [key]: value }));
    },
    [authority, conversationId, memoryControl, mounted, setOptions]
  );
  const openMobileControls = () => {
    memoryControl.refresh();
    setMobileOpen(true);
  };
  return { memoryControl, updatePersistentSessionOption, openMobileControls };
}

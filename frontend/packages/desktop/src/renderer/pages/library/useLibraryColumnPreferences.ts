import { useEffect, useMemo, useSyncExternalStore } from 'react';
import {
  DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  projectLibraryNotesColumns,
} from '@/common/adapter/libraryColumnPreferences';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import {
  loadLibraryColumnPreferences,
  saveLibraryColumnPreferences,
} from '@/renderer/services/runtime/libraryColumnPreferences';
import {
  LibraryColumnPreferenceOwner,
  type LibraryColumnPreferenceView,
} from '@/renderer/services/runtime/libraryColumnPreferenceOwner';

const LOADING_VIEW: LibraryColumnPreferenceView = {
  preferences: DEFAULT_LIBRARY_COLUMN_PREFERENCES,
  status: 'loading',
};

/** Call once at the Library view; changing columns never reloads catalogue/detail data. */
export function useLibraryColumnPreferences(account: string, notesEnabled = false) {
  const owner = useMemo(
    () =>
      new LibraryColumnPreferenceOwner(
        { load: loadLibraryColumnPreferences, save: saveLibraryColumnPreferences },
        () => account === getRendererAccountScopeToken(),
        notesEnabled
      ),
    [account, notesEnabled]
  );
  const view = useSyncExternalStore(owner.subscribe, owner.getSnapshot, owner.getSnapshot);
  useEffect(() => {
    const reloadVisible = () => {
      if (document.visibilityState === 'visible') owner.reload();
    };
    owner.start();
    window.addEventListener('focus', reloadVisible);
    document.addEventListener('visibilitychange', reloadVisible);
    return () => {
      window.removeEventListener('focus', reloadVisible);
      document.removeEventListener('visibilitychange', reloadVisible);
      owner.dispose();
    };
  }, [owner]);
  const scoped = account === getRendererAccountScopeToken() ? view : LOADING_VIEW;
  return {
    ...scoped,
    preferences: notesEnabled
      ? (scoped.notesPreferences ?? projectLibraryNotesColumns(scoped.preferences))
      : scoped.preferences,
    changePreferences: owner.change,
    retry: owner.retry,
    reset: owner.reset,
  };
}

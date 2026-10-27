import {
  parseLibraryColumnPreferences,
  type LibraryColumnPreferences,
} from '@/common/adapter/libraryColumnPreferences';
import { getClientBusinessSetting, setClientBusinessSetting } from './clientBusinessSettings';

export const LIBRARY_COLUMN_PREFERENCES_SETTING = 'xScience.library.columnPrefs' as const;

/** Authentication/account isolation stays with the existing settings endpoint. */
export function loadLibraryColumnPreferences(signal: AbortSignal): Promise<unknown> {
  return getClientBusinessSetting(LIBRARY_COLUMN_PREFERENCES_SETTING, { signal, timeoutMs: 15_000 });
}

export async function saveLibraryColumnPreferences(
  preferences: LibraryColumnPreferences,
  signal: AbortSignal
): Promise<void> {
  const value = parseLibraryColumnPreferences(preferences);
  if (!value) throw new Error('Invalid Library column preferences');
  await setClientBusinessSetting(LIBRARY_COLUMN_PREFERENCES_SETTING, value, { signal, timeoutMs: 15_000 });
}

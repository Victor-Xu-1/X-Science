export type XScienceUserProfile = {
  displayName: string;
  avatarDataUrl: string | null;
};

type StoredXScienceUserProfile = {
  displayName?: string;
  avatarDataUrl?: string | null;
};

const STORAGE_KEY = 'x-science:user-profiles:v1';
const PROFILE_EVENT = 'x-science:user-profile-changed';
const ALLOWED_AVATAR_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp']);
export const X_SCIENCE_AVATAR_MAX_BYTES = 1024 * 1024;
export const X_SCIENCE_DISPLAY_NAME_MAX_LENGTH = 40;

const normalizeDisplayName = (value: string): string =>
  Array.from(value.normalize('NFKC'))
    .filter((character) => {
      const codePoint = character.codePointAt(0) ?? 0;
      return codePoint > 31 && codePoint !== 127;
    })
    .join('')
    .trim()
    .slice(0, X_SCIENCE_DISPLAY_NAME_MAX_LENGTH);

const normalizeFallbackName = (value: string): string => {
  const normalized = normalizeDisplayName(value) || 'User';
  return `${normalized.charAt(0).toUpperCase()}${normalized.slice(1)}`;
};

const profileStorageKey = (userId: string): string => encodeURIComponent(userId.trim() || 'local');

const normalizeAvatarDataUrl = (value: unknown): string | null => {
  if (typeof value !== 'string') return null;
  const normalized = value.trim();
  if (!/^data:image\/(?:jpeg|png|webp);base64,[a-z0-9+/=]+$/i.test(normalized)) return null;
  if (normalized.length > Math.ceil((X_SCIENCE_AVATAR_MAX_BYTES * 4) / 3) + 128) return null;
  return normalized;
};

const readStore = (): Record<string, StoredXScienceUserProfile> => {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed)
      ? (parsed as Record<string, StoredXScienceUserProfile>)
      : {};
  } catch {
    return {};
  }
};

export function loadXScienceUserProfile(userId: string, fallbackName: string): XScienceUserProfile {
  const stored = readStore()[profileStorageKey(userId)];
  return {
    displayName: normalizeDisplayName(stored?.displayName ?? '') || normalizeFallbackName(fallbackName),
    avatarDataUrl: normalizeAvatarDataUrl(stored?.avatarDataUrl),
  };
}

export function saveXScienceUserProfile(userId: string, input: XScienceUserProfile): XScienceUserProfile {
  if (typeof window === 'undefined') throw new Error('user_profile_storage_unavailable');
  const displayName = normalizeDisplayName(input.displayName);
  if (!displayName) throw new Error('user_profile_display_name_required');
  const avatarDataUrl = input.avatarDataUrl === null ? null : normalizeAvatarDataUrl(input.avatarDataUrl);
  if (input.avatarDataUrl !== null && !avatarDataUrl) throw new Error('user_profile_avatar_invalid');
  const next = { displayName, avatarDataUrl };
  const store = readStore();
  store[profileStorageKey(userId)] = next;
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(store));
  window.dispatchEvent(new CustomEvent(PROFILE_EVENT, { detail: { userId } }));
  return next;
}

export function subscribeXScienceUserProfile(userId: string, listener: () => void): () => void {
  if (typeof window === 'undefined') return () => {};
  const handleProfileEvent = (event: Event) => {
    if ((event as CustomEvent<{ userId?: string }>).detail?.userId === userId) listener();
  };
  const handleStorage = (event: StorageEvent) => {
    if (event.key === STORAGE_KEY) listener();
  };
  window.addEventListener(PROFILE_EVENT, handleProfileEvent);
  window.addEventListener('storage', handleStorage);
  return () => {
    window.removeEventListener(PROFILE_EVENT, handleProfileEvent);
    window.removeEventListener('storage', handleStorage);
  };
}

export async function readXScienceAvatarFile(file: File): Promise<string> {
  if (!ALLOWED_AVATAR_TYPES.has(file.type)) throw new Error('user_profile_avatar_type_invalid');
  if (file.size <= 0 || file.size > X_SCIENCE_AVATAR_MAX_BYTES) {
    throw new Error('user_profile_avatar_size_invalid');
  }
  return await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener('error', () => reject(new Error('user_profile_avatar_read_failed')));
    reader.addEventListener('load', () => {
      const value = normalizeAvatarDataUrl(reader.result);
      if (!value) {
        reject(new Error('user_profile_avatar_invalid'));
        return;
      }
      resolve(value);
    });
    reader.readAsDataURL(file);
  });
}

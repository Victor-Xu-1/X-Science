import { useCallback, useEffect, useState } from 'react';
import {
  loadXScienceUserProfile,
  saveXScienceUserProfile,
  subscribeXScienceUserProfile,
  type XScienceUserProfile,
} from '@/renderer/services/xScienceUserProfile';

export function useXScienceUserProfile(userId: string | undefined, fallbackName: string) {
  const profileUserId = userId?.trim() || 'local';
  const [profile, setProfile] = useState<XScienceUserProfile>(() =>
    loadXScienceUserProfile(profileUserId, fallbackName)
  );

  useEffect(() => {
    const refresh = () => setProfile(loadXScienceUserProfile(profileUserId, fallbackName));
    refresh();
    return subscribeXScienceUserProfile(profileUserId, refresh);
  }, [fallbackName, profileUserId]);

  const saveProfile = useCallback(
    (next: XScienceUserProfile) => {
      const saved = saveXScienceUserProfile(profileUserId, next);
      setProfile(saved);
      return saved;
    },
    [profileUserId]
  );

  return { profile, saveProfile };
}

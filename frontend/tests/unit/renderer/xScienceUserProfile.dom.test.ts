import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  loadXScienceUserProfile,
  readXScienceAvatarFile,
  saveXScienceUserProfile,
  subscribeXScienceUserProfile,
  X_SCIENCE_AVATAR_MAX_BYTES,
} from '@/renderer/services/xScienceUserProfile';

describe('X-Science user profile preferences', () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it('stores profiles per account and notifies same-tab subscribers', () => {
    const listener = vi.fn();
    const unsubscribe = subscribeXScienceUserProfile('user-1', listener);

    expect(loadXScienceUserProfile('user-1', 'victor')).toEqual({
      displayName: 'Victor',
      avatarDataUrl: null,
    });

    saveXScienceUserProfile('user-1', {
      displayName: '  研究员 Victor  ',
      avatarDataUrl: 'data:image/png;base64,iVBORw0KGgo=',
    });

    expect(listener).toHaveBeenCalledTimes(1);
    expect(loadXScienceUserProfile('user-1', 'victor')).toEqual({
      displayName: '研究员 Victor',
      avatarDataUrl: 'data:image/png;base64,iVBORw0KGgo=',
    });
    expect(loadXScienceUserProfile('user-2', 'alice').displayName).toBe('Alice');
    unsubscribe();
  });

  it('rejects malformed stored avatar data', () => {
    expect(() =>
      saveXScienceUserProfile('user-1', {
        displayName: 'Victor',
        avatarDataUrl: 'javascript:alert(1)',
      })
    ).toThrow('user_profile_avatar_invalid');
  });

  it('validates avatar file type and size before reading', async () => {
    await expect(readXScienceAvatarFile(new File(['text'], 'avatar.txt', { type: 'text/plain' }))).rejects.toThrow(
      'user_profile_avatar_type_invalid'
    );
    await expect(
      readXScienceAvatarFile(
        new File([new Uint8Array(X_SCIENCE_AVATAR_MAX_BYTES + 1)], 'avatar.png', { type: 'image/png' })
      )
    ).rejects.toThrow('user_profile_avatar_size_invalid');
  });
});

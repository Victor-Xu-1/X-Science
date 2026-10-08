import { beforeEach, describe, expect, it, vi } from 'vitest';

const config = vi.hoisted(() => ({
  get: vi.fn(),
  set: vi.fn(),
  setLocal: vi.fn(),
  whenReady: vi.fn(),
}));

vi.mock('@/common/config/configService', () => ({ configService: config }));
vi.mock('@/common', () => ({
  ipcBridge: { systemSettings: { changeLanguage: { invoke: vi.fn().mockResolvedValue(undefined) } } },
}));
vi.mock('@/renderer/services/languagePreference', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/renderer/services/languagePreference')>()),
  subscribeLanguagePreference: vi.fn(() => () => {}),
}));

describe('renderer language initialization and complete dictionaries', () => {
  beforeEach(() => {
    vi.resetModules();
    vi.restoreAllMocks();
    vi.clearAllMocks();
    localStorage.clear();
    delete window.__initialLanguage;
    config.get.mockReturnValue(undefined);
    config.set.mockResolvedValue(undefined);
    config.whenReady.mockResolvedValue(undefined);
  });

  it('defaults a fresh session to English even in a Chinese browser', async () => {
    vi.spyOn(navigator, 'language', 'get').mockReturnValue('zh-CN');
    const { default: i18n, ensureFullLocale } = await import('@/renderer/services/i18n');
    await vi.waitFor(() => expect(i18n.language).toBe('en-US'));
    await ensureFullLocale();
    expect(i18n.t('conversation.welcome.modelListUnavailable')).toBe(
      (await import('@/renderer/services/i18n/locales/en-US/conversation.json')).default.welcome.modelListUnavailable
    );
    expect(localStorage.getItem('i18nextLng')).toBe('en-US');
    expect(config.set).not.toHaveBeenCalled();
  });

  it('restores a saved Chinese preference without replacing it with the new default', async () => {
    config.get.mockReturnValue('zh-CN');
    const { default: i18n, ensureFullLocale } = await import('@/renderer/services/i18n');
    await vi.waitFor(() => expect(i18n.language).toBe('zh-CN'));
    await ensureFullLocale();
    expect(i18n.t('conversation.welcome.modelListUnavailable')).toBe(
      (await import('@/renderer/services/i18n/locales/zh-CN/conversation.json')).default.welcome.modelListUnavailable
    );
    expect(config.set).not.toHaveBeenCalled();
  });

  it('keeps an explicit login-shell language choice until it can be saved', async () => {
    localStorage.setItem('i18nextLng', 'zh-CN');
    const { LANGUAGE_PREAUTH_OVERRIDE_KEY } = await import('@/renderer/services/languagePreference');
    localStorage.setItem(LANGUAGE_PREAUTH_OVERRIDE_KEY, '1');
    config.get.mockReturnValue('en-US');
    const { default: i18n } = await import('@/renderer/services/i18n');
    await vi.waitFor(() => expect(config.get).toHaveBeenCalledWith('language'));
    expect(i18n.language).toBe('zh-CN');
    expect(config.set).not.toHaveBeenCalled();
  });

  it('loads the correct full dictionaries while switching Chinese and English', async () => {
    const { default: i18n, changeLanguage } = await import('@/renderer/services/i18n');
    for (const language of ['zh-CN', 'en-US', 'zh-CN'] as const) {
      await changeLanguage(language);
      const dictionary =
        language === 'zh-CN'
          ? (await import('@/renderer/services/i18n/locales/zh-CN/conversation.json')).default
          : (await import('@/renderer/services/i18n/locales/en-US/conversation.json')).default;
      expect(i18n.language).toBe(language);
      expect(i18n.t('conversation.welcome.modelListUnavailable')).toBe(dictionary.welcome.modelListUnavailable);
      expect(localStorage.getItem('i18nextLng')).toBe(language);
      expect(config.set).toHaveBeenLastCalledWith('language', language);
    }
  });
});

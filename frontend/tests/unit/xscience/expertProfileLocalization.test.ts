import { describe, expect, it } from 'vitest';
import {
  localizeXScienceExpertProfile,
  resolveXScienceBuiltinExpertText,
} from '@/renderer/services/agents/xScienceExpertLocalization';
import type { XScienceExpertProfile } from '@/renderer/services/agents/xScienceExpertProfiles';
import { createTestI18n } from '../i18nTestUtils';

const bundledProfile: XScienceExpertProfile = {
  name: 'AIDD_EXPERT',
  source: 'bundled',
  displayName: 'AI药物研发专家',
  description: 'AI驱动的药物发现与设计专家',
  systemPrompt: '',
  iconKey: 'robot',
  colorKey: 'neutral',
  enabled: true,
  userHidden: false,
  unrestricted: false,
  skillNames: [],
};

describe('X-Science expert profile localization', () => {
  it('presents bundled profiles in English without mutating backend metadata', async () => {
    const i18n = await createTestI18n('en-US');
    const localized = localizeXScienceExpertProfile(bundledProfile, i18n.t);

    expect(localized.displayName).toBe('AI Drug Discovery Expert');
    expect(localized.description).toContain('generative models');
    expect(localized).not.toBe(bundledProfile);
    expect(bundledProfile.displayName).toBe('AI药物研发专家');
  });

  it('presents bundled profiles in Chinese', async () => {
    const i18n = await createTestI18n('zh-CN');
    const localized = localizeXScienceExpertProfile(bundledProfile, i18n.t);

    expect(localized.displayName).toBe('AI 药物研发专家');
    expect(localized.description).toContain('虚拟筛选');
  });

  it('loads the bilingual resource used by capability metadata', () => {
    expect(resolveXScienceBuiltinExpertText(' aidd_expert ')).toEqual({
      'en-US': {
        displayName: 'AI Drug Discovery Expert',
        description:
          'AI-driven drug discovery and design specialist covering generative models, virtual screening, and molecular optimization.',
      },
      'zh-CN': {
        displayName: 'AI 药物研发专家',
        description: 'AI 驱动的药物发现与设计专家，覆盖生成模型、虚拟筛选和分子优化。',
      },
    });
    expect(resolveXScienceBuiltinExpertText('PARTNER_EXPERT')).toBeNull();
  });

  it('does not rewrite user-created or unknown expert profiles', async () => {
    const i18n = await createTestI18n('en-US');
    const userProfile = { ...bundledProfile, source: 'user' };
    const extensionProfile = { ...bundledProfile, name: 'PARTNER_EXPERT' };

    expect(localizeXScienceExpertProfile(userProfile, i18n.t)).toBe(userProfile);
    expect(localizeXScienceExpertProfile(extensionProfile, i18n.t)).toBe(extensionProfile);
  });
});

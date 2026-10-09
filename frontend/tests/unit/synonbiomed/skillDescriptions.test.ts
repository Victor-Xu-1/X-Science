import { describe, expect, it } from 'vitest';
import {
  resolveSkillDescription,
  resolveSkillDisplayName,
} from '@/renderer/services/skills/synonBiomedSkillDescriptions';

describe('Skill descriptions from the catalog', () => {
  it('uses localized display names without replacing execution IDs or external titles', () => {
    const names = { 'en-US': 'X-Science Research', 'zh-CN': 'X-Science 科研研究' };
    expect(resolveSkillDisplayName('synon-research', 'X-Science Research', undefined, names)).toBe(
      'X-Science Research'
    );
    expect(resolveSkillDisplayName('synon-research', 'X-Science Research', 'zh-CN', names)).toBe('X-Science 科研研究');
    expect(resolveSkillDisplayName('synon-research', 'My workflow', 'zh-CN')).toBe('My workflow');
    expect(resolveSkillDisplayName('raw-id', undefined, 'zh-CN', { 'en-US': 'English title' })).toBe('English title');
  });
  it('uses the declared locale and keeps the source description as its fallback', () => {
    const locales = { 'zh-CN': '科研证据', en: 'Research evidence' };
    expect(resolveSkillDescription('custom', 'Source', 'zh-CN', locales)).toBe('科研证据');
    expect(resolveSkillDescription('custom', 'Source', 'en-US', locales)).toBe('Research evidence');
    expect(resolveSkillDescription('custom', 'Source', 'zh-CN')).toBe('Source');
  });

  it('does not assign a bundled description to an external Skill sharing its name', () => {
    expect(resolveSkillDescription('alphafold2', 'My custom workflow', 'zh-CN')).toBe('My custom workflow');
    expect(resolveSkillDescription('custom', '', 'zh-CN')).toBe('custom');
  });
});

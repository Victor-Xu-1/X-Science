import { randomBytes } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import { loadXScienceSkills } from '@/renderer/services/xScienceCapabilities';
import {
  deleteXSciencePersonalSkill,
  importXScienceSkillFile,
  loadXScienceSkillDrafts,
  loadXScienceSkillFileContent,
  loadXScienceSkillFiles,
  loadXScienceSkillSources,
  saveXScienceSkillDraftFile,
} from '@/renderer/services/skills/xScienceSkillLibrary';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

type CatalogSkill = { name: string; source?: string };

describe('X-Science skill library gateway', () => {
  it('reads library metadata and atomically edits/restores a real personal Skill', async () => {
    const fetchImpl = await createXScienceTestFetch(gatewayBaseUrl);
    const options = { baseUrl: gatewayBaseUrl, fetchImpl };
    const name = `real-skill-${Date.now().toString(36)}-${randomBytes(3).toString('hex')}`;
    const original = `---
name: ${name}
description: Temporary real integration Skill used to verify the writable library lifecycle.
---

# ${name}

Return the exact text requested by the caller.
`;
    let imported = false;
    try {
      await importXScienceSkillFile(new File([original], 'SKILL.md', { type: 'text/markdown' }), name, options);
      imported = true;

      const skills = (await loadXScienceSkills(options)) as CatalogSkill[];
      expect(skills).toContainEqual(expect.objectContaining({ name, source: 'personal' }));
      await expect(loadXScienceSkillDrafts(options)).resolves.toEqual(expect.any(Array));
      await expect(loadXScienceSkillSources(options)).resolves.toEqual(expect.any(Array));

      const files = await loadXScienceSkillFiles(name, options);
      expect(files).toContain('SKILL.md');
      await expect(loadXScienceSkillFileContent(name, 'SKILL.md', options)).resolves.toBe(original);

      const marker = `\n<!-- x-science-real-edit-${Date.now()} -->\n`;
      await saveXScienceSkillDraftFile(name, 'SKILL.md', original, original + marker, options);
      await expect(loadXScienceSkillFileContent(name, 'SKILL.md', options)).resolves.toContain(marker.trim());
      await saveXScienceSkillDraftFile(name, 'SKILL.md', original + marker, original, options);
      await expect(loadXScienceSkillFileContent(name, 'SKILL.md', options)).resolves.toBe(original);
    } finally {
      if (imported) await deleteXSciencePersonalSkill(name, options);
    }
  });
});

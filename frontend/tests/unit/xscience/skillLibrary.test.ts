import { describe, expect, it, vi } from 'vitest';
import type {
  XScienceSkillLibraryError,
  XScienceSkillRepoPreview,
} from '@/renderer/services/skills/xScienceSkillLibrary';
import * as skillLibrary from '@/renderer/services/skills/xScienceSkillLibrary';
import {
  importXScienceRepositorySkills,
  importXScienceSkillFile,
  loadXScienceSkillDrafts,
  loadXScienceSkillFileContent,
  loadXScienceSkillFileSnapshot,
  loadXScienceSkillFiles,
  loadXScienceSkillSources,
  previewXScienceSkillRepository,
  publishXScienceSkillDraft,
  saveXScienceSkillDraftFile,
} from '@/renderer/services/skills/xScienceSkillLibrary';

describe('X-Science skill library service', () => {
  it('publishes only the exact reviewed draft revision without a new read', async () => {
    const revision = 'a'.repeat(64);
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(json({ ok: true }));
    const options = { fetchImpl, expectedDraftRevision: revision };
    await publishXScienceSkillDraft('reviewed-workflow', false, options);
    expect(fetchImpl).toHaveBeenCalledTimes(1);
    expect(fetchImpl).toHaveBeenCalledWith(
      '/api/skills/reviewed-workflow/publish',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ overwrite: false, expected_draft_revision: revision }),
      })
    );
  });

  it('keeps required and conflicting draft identities as typed failures', async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValue(json({ code: 'draft_revision_conflict', detail: 'Read and review the current draft.' }, 409));
    const options = { fetchImpl, expectedDraftRevision: 'b'.repeat(64) };
    await expect(publishXScienceSkillDraft('reviewed-workflow', false, options)).rejects.toMatchObject({
      status: 409,
      code: 'draft_revision_conflict',
    });
  });

  it('reads the explicitly selected draft instead of a same-named published package', async () => {
    const payload = {
      content: '# Reviewed workflow',
      origin: 'draft',
      draft_revision: 'c'.repeat(64),
      files: ['SKILL.md'],
    };
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(json(payload));
    const options = { fetchImpl, source: 'draft' as const };
    await expect(loadXScienceSkillFileContent('reviewed-workflow', 'SKILL.md', options)).resolves.toBe(
      '# Reviewed workflow'
    );
    expect(fetchImpl).toHaveBeenCalledWith(
      '/api/skills/catalog/reviewed-workflow/content?path=SKILL.md&source=draft',
      expect.anything()
    );
  });

  it('never turns a published or malformed snapshot identity into draft authorization', async () => {
    for (const [origin, draft_revision, source] of [
      ['personal', 'a'.repeat(64), 'draft'],
      ['draft', 'not-a-package-hash', 'draft'],
      ['draft', 'a'.repeat(64), 'published'],
    ] as const) {
      const fetchImpl = vi
        .fn<typeof fetch>()
        .mockResolvedValue(json({ content: '# Workflow', origin, draft_revision }));
      await expect(loadXScienceSkillFileSnapshot('workflow', 'SKILL.md', { source, fetchImpl })).resolves.toMatchObject(
        {
          content: '# Workflow',
          draftRevision: null,
        }
      );
    }
  });

  it('rejects an incomplete draft snapshot instead of pairing its token with a preceding file list', async () => {
    const invalidInventories: unknown[] = [
      undefined,
      [],
      ['other.md'],
      ['SKILL.md', 'SKILL.md'],
      ['SKILL.md', 7],
      ['SKILL.md', '../outside.md'],
    ];
    const draft = { content: '# Draft', origin: 'draft', draft_revision: 'd'.repeat(64) };
    for (const files of invalidInventories) {
      const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(json({ ...draft, files }));
      const read = loadXScienceSkillFileSnapshot('workflow', 'SKILL.md', { source: 'draft', fetchImpl });
      await expect(read).rejects.toMatchObject({
        status: 502,
        code: 'draft_snapshot_invalid',
      });
    }
    const validFiles: string[] = ['SKILL.md', 'references/current.md'];
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(json({ ...draft, files: validFiles }));
    const resolved = await loadXScienceSkillFileSnapshot('workflow', 'SKILL.md', { source: 'draft', fetchImpl });
    expect(resolved).toMatchObject({
      files: validFiles,
      draftRevision: draft.draft_revision,
    });
  });

  it('does not send an unbound publish request when the revision is malformed', async () => {
    const fetchImpl = vi.fn<typeof fetch>();
    await expect(
      publishXScienceSkillDraft('workflow', true, { fetchImpl, expectedDraftRevision: 'unreviewed' })
    ).rejects.toMatchObject({
      status: 428,
      code: 'draft_revision_required',
    });
    expect(fetchImpl).not.toHaveBeenCalled();
  });

  it('normalizes drafts, imported sources, repository previews and file content', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async (input) => {
      const url = String(input);
      if (url.endsWith('/api/skills/drafts')) {
        return json({
          drafts: [
            'local-molecular-docking',
            {
              name: 'my-skill',
              display_name: 'My Skill',
              description: 'Personal workflow',
              files: ['SKILL.md', 'references/notes.md'],
              updated_at: '2026-07-14T00:00:00Z',
            },
          ],
        });
      }
      if (url.endsWith('/api/marketplace/sources')) {
        return json({
          sources: [
            {
              slug: 'research-skills',
              repo: 'https://github.com/example/research-skills',
              pinned_sha: 'abc123',
              license: 'Apache-2.0',
              skill_names: ['literature-review'],
              imported_at: '2026-07-13T00:00:00Z',
            },
          ],
        });
      }
      if (url.endsWith('/api/marketplace/preview')) {
        return json({
          repo: 'https://github.com/example/research-skills',
          slug: 'research-skills',
          sha: 'abc123',
          license: 'Apache-2.0',
          skills: [{ name: 'literature-review', display_name: 'Literature Review', description: 'Review papers' }],
        });
      }
      if (url.endsWith('/api/skills/catalog/my-skill/files')) return json({ files: ['SKILL.md'] });
      if (url.includes('/api/skills/catalog/my-skill/content')) return json({ content: '# My Skill' });
      throw new Error(`unexpected URL: ${url}`);
    });
    const options = { baseUrl: 'http://fusion.test', fetchImpl };

    await expect(loadXScienceSkillDrafts(options)).resolves.toEqual([
      {
        name: 'local-molecular-docking',
        displayName: 'Local Molecular Docking',
        description: '',
        files: [],
        updatedAt: null,
      },
      {
        name: 'my-skill',
        displayName: 'My Skill',
        description: 'Personal workflow',
        files: ['SKILL.md', 'references/notes.md'],
        updatedAt: '2026-07-14T00:00:00Z',
      },
    ]);
    await expect(loadXScienceSkillSources(options)).resolves.toEqual([
      {
        slug: 'research-skills',
        repo: 'https://github.com/example/research-skills',
        sha: 'abc123',
        license: 'Apache-2.0',
        skills: ['literature-review'],
        importedAt: '2026-07-13T00:00:00Z',
        removable: true,
      },
    ]);
    await expect(
      previewXScienceSkillRepository('https://github.com/example/research-skills', options)
    ).resolves.toMatchObject({
      slug: 'research-skills',
      sha: 'abc123',
      skills: [{ name: 'literature-review', displayName: 'Literature Review', selected: true }],
    });
    await expect(loadXScienceSkillFiles('my-skill', options)).resolves.toEqual(['SKILL.md']);
    await expect(loadXScienceSkillFileContent('my-skill', 'SKILL.md', options)).resolves.toBe('# My Skill');
  });

  it('writes exact repository, multipart import and draft edit contracts', async () => {
    const fetchImpl = vi.fn<typeof fetch>(async (input, init) => {
      const url = String(input);
      if (url.endsWith('/api/marketplace/import')) return json({ imported: ['literature-review'], skipped: [] });
      if (url.endsWith('/api/skills/import')) {
        expect(init?.body).toBeInstanceOf(FormData);
        const body = init?.body as FormData;
        expect(body.get('name')).toBe('uploaded-skill');
        expect((body.get('file') as File).name).toBe('SKILL.md');
        return json({ name: 'uploaded-skill' }, 201);
      }
      if (url.endsWith('/api/skills/my-skill/edit')) return json({ ok: true });
      throw new Error(`unexpected URL: ${url}`);
    });
    const options = { baseUrl: 'http://fusion.test', fetchImpl };
    const preview: XScienceSkillRepoPreview = {
      repo: 'https://github.com/example/research-skills',
      slug: 'research-skills',
      sha: 'abc123',
      license: 'Apache-2.0',
      skills: [],
    };

    await importXScienceRepositorySkills(preview, ['literature-review'], options);
    expect(fetchImpl).toHaveBeenCalledWith(
      'http://fusion.test/api/marketplace/import',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({
          repo: preview.repo,
          sha: preview.sha,
          skills: ['literature-review'],
          license: preview.license,
        }),
      })
    );

    await importXScienceSkillFile(
      new File(['# Uploaded'], 'SKILL.md', { type: 'text/markdown' }),
      'uploaded-skill',
      options
    );
    await saveXScienceSkillDraftFile('my-skill', 'SKILL.md', '# Original', '# Updated', options);
    expect(fetchImpl).toHaveBeenLastCalledWith(
      'http://fusion.test/api/skills/my-skill/edit',
      expect.objectContaining({
        body: JSON.stringify({ path: 'SKILL.md', old_string: '# Original', new_string: '# Updated' }),
      })
    );
  });

  it('does not expose a second conversation-to-Skill authoring or installation API', () => {
    expect(skillLibrary).not.toHaveProperty('saveXScienceConversationAsSkill');
  });

  it('returns typed, non-leaking HTTP failures', async () => {
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(json({ detail: 'Repository is not allowed' }, 400));

    await expect(
      previewXScienceSkillRepository('file:///etc/passwd', { baseUrl: 'http://fusion.test', fetchImpl })
    ).rejects.toEqual(
      expect.objectContaining<Pick<XScienceSkillLibraryError, 'status' | 'message'>>({
        status: 400,
        message: 'Repository is not allowed',
      })
    );
  });
});

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
}

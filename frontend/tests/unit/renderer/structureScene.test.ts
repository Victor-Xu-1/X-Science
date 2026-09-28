import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import {
  loadStructureSceneSources,
  parseStructureSceneManifest,
  STRUCTURE_SCENE_MANIFEST_SCHEMA,
} from '@/renderer/pages/conversation/Preview/components/viewers/structureScene';

describe('structure scene delivery contract', () => {
  it('loads the scene contract taught by the chemistry rendering skill', () => {
    const skill = readFileSync(
      new URL('../../../../skills/synonbiomed/cheminfo-render/SKILL.md', import.meta.url),
      'utf8'
    );
    const example = skill.split('```json\n')[1]?.split('\n```')[0];
    expect(example).toBeDefined();
    const scene = parseStructureSceneManifest(JSON.parse(example!));
    expect(scene?.mother_structure).toEqual({ name: 'receptor.pdb', version_id: 'receptor-version' });
    expect(scene?.derived_structures).toEqual([{ name: 'complex.pdb', version_id: 'complex-version' }]);
  });

  it('accepts exact mother and derived version references', () => {
    const manifest = parseStructureSceneManifest({
      schema: STRUCTURE_SCENE_MANIFEST_SCHEMA,
      scene_id: 'scene-1',
      mother_structure: { name: 'mother.pdb', version_id: 'mother-v1' },
      derived_structures: [{ name: 'pocket.pdb', version_id: 'pocket-v1' }],
    });

    expect(manifest).toEqual({
      schema: STRUCTURE_SCENE_MANIFEST_SCHEMA,
      scene_id: 'scene-1',
      mother_structure: { name: 'mother.pdb', version_id: 'mother-v1' },
      derived_structures: [{ name: 'pocket.pdb', version_id: 'pocket-v1' }],
    });
  });

  it('rejects duplicate structure versions and missing references', () => {
    expect(
      parseStructureSceneManifest({
        schema: STRUCTURE_SCENE_MANIFEST_SCHEMA,
        scene_id: 'scene-1',
        mother_structure: { name: 'mother.pdb', version_id: 'same-v1' },
        derived_structures: [{ name: 'pocket.pdb', version_id: 'same-v1' }],
      })
    ).toBeNull();
  });

  it('resolves the scene layers through the existing companion URL map', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          schema: STRUCTURE_SCENE_MANIFEST_SCHEMA,
          scene_id: 'scene-1',
          mother_structure: { name: 'mother.pdb', version_id: 'mother-v1' },
          derived_structures: [{ name: 'pocket.pdb', version_id: 'pocket-v1' }],
        }),
        { status: 200, headers: { 'content-type': 'application/json' } }
      )
    );
    vi.stubGlobal('fetch', fetchMock);

    await expect(
      loadStructureSceneSources(
        {
          'scene.json': '/artifacts/scene.json',
          'mother.pdb': '/api/artifacts/mother/versions/mother-v2',
          'pocket.pdb': '/api/artifacts/pocket/versions/pocket-v1',
        },
        new AbortController().signal
      )
    ).resolves.toEqual({
      sceneId: 'scene-1',
      sources: [
        { name: 'mother.pdb', versionId: 'mother-v1', url: '/api/artifacts/mother/versions/mother-v1' },
        { name: 'pocket.pdb', versionId: 'pocket-v1', url: '/api/artifacts/pocket/versions/pocket-v1' },
      ],
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

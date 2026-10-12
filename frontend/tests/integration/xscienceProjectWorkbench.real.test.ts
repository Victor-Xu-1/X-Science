import { loadXScienceProjectBenches, loadXScienceProjectWorkbench } from '@/renderer/services/xScienceGateway';
import {
  buildProjectArtifactGroups,
  getVisibleProjectArtifacts,
} from '@/renderer/pages/project/projectArtifactLibraryModel';
import { describe, expect, it } from 'vitest';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science project workbench gateway', () => {
  it('loads a real project together with its tasks, files, and folders', async () => {
    const workbench = await loadXScienceProjectWorkbench('proj_example', {
      baseUrl: gatewayBaseUrl,
      fetchImpl: await createXScienceTestFetch(gatewayBaseUrl),
    });

    expect(workbench.project.projectId).toBe('proj_example');
    expect(workbench.benches.length).toBeGreaterThan(0);
    expect(workbench.benches.some((bench) => bench.rootFrameId === bench.frameId)).toBe(true);
    expect(workbench.artifacts.some((artifact) => artifact.contentType === 'image/png')).toBe(true);
    expect(workbench.folders.some((folder) => folder.isUserUploadsFolder)).toBe(true);

    const visibleArtifacts = getVisibleProjectArtifacts(workbench.artifacts);
    const groups = buildProjectArtifactGroups({
      artifacts: workbench.artifacts,
      benches: workbench.benches,
      query: '',
    });
    const groupedArtifactIds = groups.flatMap((group) => group.artifacts.map((artifact) => artifact.artifactId));

    expect(visibleArtifacts.length).toBeGreaterThan(40);
    expect(groupedArtifactIds).toHaveLength(visibleArtifacts.length);
    expect(new Set(groupedArtifactIds).size).toBe(visibleArtifacts.length);
    expect(groups.some((group) => group.kind === 'frame')).toBe(true);

    const benches = await loadXScienceProjectBenches('proj_example', {
      baseUrl: gatewayBaseUrl,
      fetchImpl: await createXScienceTestFetch(gatewayBaseUrl),
    });
    expect(benches.map((bench) => bench.frameId)).toEqual(workbench.benches.map((bench) => bench.frameId));
    expect(benches.some((bench) => bench.frameId === '2ccd996d-7f3c-4197-a02f-46c70dea49c2')).toBe(false);
  });
});

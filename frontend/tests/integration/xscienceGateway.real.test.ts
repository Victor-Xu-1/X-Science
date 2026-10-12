import { describe, expect, it } from 'vitest';
import {
  createXScienceProject,
  deleteXScienceProject,
  loadXScienceProjectArtifacts,
  loadXScienceProjects,
  updateXScienceProject,
} from '@/renderer/services/xScienceGateway';
import { createXScienceTestFetch } from './xscienceTestAuth';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';

describe('X-Science gateway integration', () => {
  it('reads a real project and its scientific artifacts without the catalog facade', async () => {
    const fetchImpl = await createXScienceTestFetch(gatewayBaseUrl);
    const projects = await loadXScienceProjects({ baseUrl: gatewayBaseUrl, fetchImpl });
    const project = projects.find((item) => item.projectId === 'proj_example');

    expect(project).toBeDefined();

    const artifacts = await loadXScienceProjectArtifacts(project!.projectId, { baseUrl: gatewayBaseUrl, fetchImpl });

    expect(artifacts.length).toBeGreaterThan(0);
    expect(artifacts.some((artifact) => artifact.projectId === project!.projectId)).toBe(true);
    expect(artifacts.some((artifact) => artifact.filename.endsWith('.png'))).toBe(true);
  });

  it('creates, updates, reads and deletes a real temporary project through the WebHost CSRF bridge', async () => {
    const fetchImpl = await createXScienceTestFetch(gatewayBaseUrl);
    const suffix = `${Date.now()}-${Math.random().toString(16).slice(2)}`;
    const created = await createXScienceProject(
      { name: `UI parity ${suffix}`, description: 'temporary integration project', context: 'Use GRCh38.' },
      { baseUrl: gatewayBaseUrl, fetchImpl }
    );

    try {
      const updated = await updateXScienceProject(
        created.projectId,
        { name: `UI parity updated ${suffix}`, description: 'updated integration project', context: 'Use GRCh38.' },
        { baseUrl: gatewayBaseUrl, fetchImpl }
      );
      expect(updated.name).toBe(`UI parity updated ${suffix}`);

      const projects = await loadXScienceProjects({ baseUrl: gatewayBaseUrl, fetchImpl });
      expect(projects.some((project) => project.projectId === created.projectId && project.name === updated.name)).toBe(
        true
      );
    } finally {
      await deleteXScienceProject(created.projectId, { baseUrl: gatewayBaseUrl, fetchImpl });
    }

    const projectsAfterDelete = await loadXScienceProjects({ baseUrl: gatewayBaseUrl, fetchImpl });
    expect(projectsAfterDelete.some((project) => project.projectId === created.projectId)).toBe(false);
  });
});

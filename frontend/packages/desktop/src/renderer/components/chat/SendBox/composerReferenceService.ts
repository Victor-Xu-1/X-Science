import {
  loadXScienceProjectArtifacts,
  loadXScienceProjectBenches,
  loadXScienceProjects,
  type XScienceGatewayOptions,
  type XScienceProject,
} from '@/renderer/services/xScienceGateway';
import type { ArtifactComposerReference, SessionComposerReference } from './composerReferenceModel';

function orderProjects(projects: XScienceProject[], currentProjectId: string): XScienceProject[] {
  return projects.toSorted((left, right) => {
    if (left.projectId === currentProjectId) return -1;
    if (right.projectId === currentProjectId) return 1;
    return left.name.localeCompare(right.name);
  });
}

export async function loadArtifactComposerReferences(
  currentProjectId: string,
  options: XScienceGatewayOptions = {}
): Promise<ArtifactComposerReference[]> {
  const projects = orderProjects(await loadXScienceProjects(options), currentProjectId);
  const projectArtifacts = await Promise.all(
    projects.map(async (project) => ({
      project,
      artifacts: await loadXScienceProjectArtifacts(project.projectId, options),
    }))
  );

  return projectArtifacts.flatMap(({ project, artifacts }) =>
    artifacts.flatMap((artifact) => {
      return [
        {
          kind: 'artifact' as const,
          key: `artifact:${artifact.artifactId}:${artifact.versionId || 'latest'}`,
          label: artifact.filename,
          detail: project.name,
          projectId: project.projectId,
          projectName: project.name,
          isCurrentProject: project.projectId === currentProjectId,
          artifactId: artifact.artifactId,
          versionId: artifact.versionId || undefined,
        },
      ];
    })
  );
}

export async function loadSessionComposerReferences(
  currentProjectId: string,
  currentFrameId?: string,
  options: XScienceGatewayOptions = {}
): Promise<SessionComposerReference[]> {
  const projects = orderProjects(await loadXScienceProjects(options), currentProjectId);
  const projectBenches = await Promise.all(
    projects.map(async (project) => ({
      project,
      benches: await loadXScienceProjectBenches(project.projectId, options),
    }))
  );

  return projectBenches.flatMap(({ project, benches }) =>
    benches.flatMap((bench) => {
      if (bench.frameId === currentFrameId) return [];
      return [
        {
          kind: 'session' as const,
          key: `session:${bench.frameId}`,
          label: bench.name,
          detail: bench.taskSummary || project.name,
          projectId: project.projectId,
          projectName: project.name,
          isCurrentProject: project.projectId === currentProjectId,
          frameId: bench.frameId,
        },
      ];
    })
  );
}

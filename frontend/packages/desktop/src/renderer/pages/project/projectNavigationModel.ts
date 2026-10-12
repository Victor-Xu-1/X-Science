import type { XScienceProject, XScienceProjectBench } from '@/renderer/services/xScienceGateway';

export type ProjectNavigationTarget =
  | { pathname: string; state?: undefined }
  | {
      pathname: '/guid';
      state: {
        resetAssistant: true;
        workspace: string;
      };
    };

function timestamp(value: string | null): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function benchActivityTime(bench: XScienceProjectBench): number {
  return Math.max(
    timestamp(bench.lastActivityAt),
    timestamp(bench.updatedAt),
    timestamp(bench.completedAt),
    timestamp(bench.createdAt)
  );
}

export function resolveLatestProjectConversationId(benches: XScienceProjectBench[]): string | null {
  const rootActivity = new Map<string, { activityTime: number; sourceOrder: number }>();

  benches.forEach((bench, sourceOrder) => {
    const rootFrameId = bench.rootFrameId || bench.frameId;
    if (!rootFrameId) return;
    const activityTime = benchActivityTime(bench);
    const current = rootActivity.get(rootFrameId);
    if (!current || activityTime > current.activityTime) {
      rootActivity.set(rootFrameId, { activityTime, sourceOrder: current?.sourceOrder ?? sourceOrder });
    }
  });

  let latest: { frameId: string; activityTime: number; sourceOrder: number } | null = null;
  rootActivity.forEach((value, frameId) => {
    if (
      !latest ||
      value.activityTime > latest.activityTime ||
      (value.activityTime === latest.activityTime && value.sourceOrder < latest.sourceOrder)
    ) {
      latest = { frameId, ...value };
    }
  });

  return latest?.frameId ?? null;
}

export function resolveProjectNavigationTarget(
  projectId: string,
  benches: XScienceProjectBench[]
): ProjectNavigationTarget {
  const latestConversationId = resolveLatestProjectConversationId(benches);
  if (latestConversationId) {
    return { pathname: `/conversation/${encodeURIComponent(latestConversationId)}` };
  }

  return {
    pathname: '/guid',
    state: {
      resetAssistant: true,
      workspace: `x-science://project/${encodeURIComponent(projectId)}`,
    },
  };
}

export function resolveProjectSummaryNavigationTarget(
  project: XScienceProject | null | undefined
): ProjectNavigationTarget | null {
  if (!project) return null;
  if (project.latestConversationId) {
    return { pathname: `/conversation/${encodeURIComponent(project.latestConversationId)}` };
  }
  return project.conversationCount === 0 ? resolveProjectNavigationTarget(project.projectId, []) : null;
}

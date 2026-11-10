import { matchPath } from 'react-router';
import type { TChatConversation } from '@/common/config/storage';
import type { PreviewWorkspace } from './previewWorkspacePersistence';

const routeId = (value?: string): string | undefined => {
  if (!value) return undefined;
  try {
    return decodeURIComponent(value);
  } catch {
    return undefined;
  }
};

export function previewRouteWorkspace(
  ownerId: string,
  location: { pathname: string; key: string; state?: unknown }
): PreviewWorkspace {
  const conversationId = routeId(matchPath('/conversation/:id', location.pathname)?.params.id);
  const projectId = routeId(matchPath('/projects/:projectId', location.pathname)?.params.projectId);
  const workspace = (location.state as { workspace?: unknown } | null)?.workspace;
  let guidProjectId: string | undefined;
  if (location.pathname === '/guid' && typeof workspace === 'string' && workspace.startsWith('x-science://project/')) {
    try {
      const url = new URL(workspace);
      guidProjectId = decodeURIComponent(url.pathname.replace(/^\/+/, '')) || undefined;
    } catch {
      // Invalid navigation data does not select a project.
    }
  }
  return {
    ownerId,
    routeKey: JSON.stringify([location.key, location.pathname]),
    conversationId,
    projectId: projectId ?? guidProjectId,
    area: location.pathname.startsWith('/library') ? 'library' : 'unassigned',
  };
}

export function conversationPreviewWorkspace(conversationId: string, conversation?: TChatConversation | null) {
  if (!conversation || conversation.id !== conversationId) return null;
  const extra = conversation.extra as { project_id?: unknown; workspace?: unknown } | undefined;
  return {
    projectId: typeof extra?.project_id === 'string' ? extra.project_id.trim() || undefined : undefined,
    workspaceId: typeof extra?.workspace === 'string' ? extra.workspace.trim() || undefined : undefined,
  };
}

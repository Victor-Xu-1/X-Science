import type { PreviewPresentationMode, PreviewTab } from './PreviewContext';

export type PreviewWorkspace = {
  ownerId: string;
  /** Current navigation identity, used to reject a late conversation binding. */
  routeKey: string;
  conversationId?: string;
  projectId?: string;
  workspaceId?: string;
  area?: 'library' | 'unassigned';
};

export type PreviewWorkspaceSlice = {
  isOpen: boolean;
  tabs: PreviewTab[];
  activeTabId: string | null;
  presentationMode: PreviewPresentationMode;
  boardColumns: 1 | 2 | 3;
  focusedTabId: string | null;
};

export const emptyPreviewWorkspaceSlice = (): PreviewWorkspaceSlice => ({
  isOpen: false,
  tabs: [],
  activeTabId: null,
  presentationMode: 'single',
  boardColumns: 2,
  focusedTabId: null,
});

export function previewWorkspaceStorageKey(workspace: PreviewWorkspace): string {
  return `x-science_preview_project_v1:${encodeURIComponent(
    JSON.stringify([
      workspace.ownerId,
      workspace.projectId ? 'project' : (workspace.area ?? 'unassigned'),
      workspace.projectId || workspace.workspaceId || '',
    ])
  )}`;
}

const MAX_CONTENT_LENGTH = 80_000;
const CONTENT_TYPES = new Set(['markdown', 'html', 'code', 'diff']);

function durableTabs(input: unknown): PreviewTab[] {
  if (!Array.isArray(input)) return [];
  const seen = new Set<string>();
  return input.flatMap((value: unknown) => {
    if (!value || typeof value !== 'object') return [];
    const tab = value as Partial<PreviewTab>;
    if (
      typeof tab.id !== 'string' ||
      !tab.id ||
      seen.has(tab.id) ||
      typeof tab.title !== 'string' ||
      typeof tab.content !== 'string' ||
      tab.content.length > MAX_CONTENT_LENGTH ||
      !CONTENT_TYPES.has(tab.content_type ?? '') ||
      (tab.metadata != null && (typeof tab.metadata !== 'object' || Array.isArray(tab.metadata)))
    )
      return [];
    seen.add(tab.id);
    const originalContent = typeof tab.originalContent === 'string' ? tab.originalContent : tab.content;
    if (originalContent.length > MAX_CONTENT_LENGTH) return [];
    return [{ ...tab, originalContent, isDirty: tab.content !== originalContent } as PreviewTab];
  });
}

export function loadPreviewWorkspace(workspace: PreviewWorkspace): PreviewWorkspaceSlice {
  const empty = emptyPreviewWorkspaceSlice();
  try {
    const parsed = JSON.parse(localStorage.getItem(previewWorkspaceStorageKey(workspace)) ?? 'null');
    if (!parsed || parsed.schema !== 1 || parsed.scope !== previewWorkspaceStorageKey(workspace)) return empty;
    const tabs = durableTabs(parsed.tabs);
    const activeTabId = tabs.some((tab) => tab.id === parsed.activeTabId) ? parsed.activeTabId : (tabs[0]?.id ?? null);
    return {
      isOpen: tabs.length > 0 && parsed.isOpen === true,
      tabs,
      activeTabId,
      presentationMode: parsed.presentationMode === 'board' ? 'board' : 'single',
      boardColumns: [1, 2, 3].includes(parsed.boardColumns) ? parsed.boardColumns : 2,
      focusedTabId: tabs.some((tab) => tab.id === parsed.focusedTabId) ? parsed.focusedTabId : null,
    };
  } catch {
    return empty;
  }
}

export function savePreviewWorkspace(workspace: PreviewWorkspace, slice: PreviewWorkspaceSlice): void {
  try {
    const scope = previewWorkspaceStorageKey(workspace);
    localStorage.setItem(scope, JSON.stringify({ schema: 1, scope, ...slice, tabs: durableTabs(slice.tabs) }));
  } catch {
    // Storage may be disabled or full; the live project slice remains usable.
  }
}

// The old global keys have neither a durable owner nor a reliable project.
// Leave them recoverable in place. Never attribute their contents to a route,
// and never erase them as a side effect of opening a different project.

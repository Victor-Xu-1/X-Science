/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import type { PdfReadingPreviewOwner } from '@/renderer/services/runtime/pdfReadingPreview';
import type { PreviewContentType } from '@/common/types/office/preview';
import { emitter } from '@/renderer/utils/emitter';
import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { usePreviewFocusReturn } from './usePreviewFocusReturn';
import type { NotebookSessionNavigation } from '@/renderer/services/notebook/manualNotebookTypes';
import { sameNotebookPreview } from '@/renderer/services/notebook/notebookPreviewNavigation';
import { registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { rendererAccountScopedKey } from '@/renderer/services/rendererAccountScope';
import { usePreviewWorkspaceState } from './usePreviewWorkspaceState';
import type { PreviewWorkspace } from './previewWorkspacePersistence';
import { usePreviewArtifactEdits, type PreviewArtifactEditState } from './usePreviewArtifactEdits';

type PreviewDefaultTitleKey =
  | 'preview.defaultTitles.markdown'
  | 'preview.defaultTitles.diff'
  | 'preview.defaultTitles.code'
  | 'preview.defaultTitles.image'
  | 'preview.defaultTitles.preview';

/** DOM 片段数据结构 / DOM snippet data structure */
export interface DomSnippet {
  /** 唯一 ID / Unique ID */
  id: string;
  /** 简化标签名（用于显示）/ Simplified tag name (for display) */
  tag: string;
  /** 完整 HTML / Full HTML */
  html: string;
}

export interface PreviewMetadata {
  /** Exact source coordinates; content authorization stays on the server. */
  pdfReadingSource?: import('@/common/adapter/pdfReadingContext').PdfReadingSource;
  targetPdfPage?: number;
  pdfRevealId?: string;
  notebookSession?: NotebookSessionNavigation;
  language?: string;
  title?: string;
  diff?: string;
  file_name?: string;
  file_path?: string; // 工作空间文件的绝对路径 / Absolute file path in workspace
  workspace?: string; // 工作空间根目录 / Workspace root directory
  editable?: boolean; // 是否可编辑 / Whether editable
  truncated?: boolean; // 预览内容是否被截断 / Whether preview content was truncated
  targetLine?: number; // 打开文件后定位到的目标行 / Target line to reveal after opening
  targetColumn?: number; // 打开文件后定位到的目标列 / Target column to reveal after opening
  missingFile?: boolean; // 文件不存在或无法读取 / Whether the referenced file is missing or unreadable
  artifactId?: string; // X-Science artifact identity for remote lifecycle operations
  versionId?: string; // Current X-Science artifact version
  contentUrl?: string; // Authenticated WebHost content endpoint for remote artifacts
  companionArtifactUrls?: Readonly<Record<string, string>>; // Same deliverable set, keyed by lowercase filename
  projectId?: string;
  rootFrameId?: string;
  generatedTitleKey?: PreviewDefaultTitleKey;
}

export interface PreviewTab {
  id: string;
  content: string;
  content_type: PreviewContentType;
  metadata?: PreviewMetadata;
  title: string; // Tab 标题
  isDirty?: boolean; // 是否有未保存的修改 / Whether there are unsaved changes
  originalContent?: string; // 原始内容，用于对比 / Original content for comparison
}

const localPreviewFileKey = (metadata?: PreviewMetadata): string | null => {
  const path = metadata?.file_path;
  if (!path || metadata?.artifactId || metadata?.contentUrl || path.startsWith('x-science://')) return null;
  return JSON.stringify([metadata?.workspace ?? '', path]);
};

export const shouldPollPreviewFileMetadata = (
  tab: PreviewTab | null,
  visibilityState: DocumentVisibilityState = 'visible'
): boolean => {
  if (visibilityState !== 'visible') return false;
  return localPreviewFileKey(tab?.metadata) !== null;
};

export interface OpenPreviewOptions {
  /**
   * Reuse the active tab instead of opening a new one — used by file-tree
   * browsing so switching files swaps the single preview instead of stacking
   * tabs. Ignored when the active tab has unsaved edits (falls back to a new
   * tab to avoid losing changes).
   */
  replace?: boolean;
  /**
   * Controls how opened files are presented. Project file browsing uses the
   * board so every selected file remains visible in its own preview tile.
   */
  presentation?: PreviewPresentationMode;
}

export type PreviewPresentationMode = 'single' | 'board';

export interface PreviewContextValue {
  bindWorkspaceProject: (conversationId: string, projectId?: string, workspaceId?: string) => void;
  boardColumns: 1 | 2 | 3;
  setBoardColumns: React.Dispatch<React.SetStateAction<1 | 2 | 3>>;
  focusedTabId: string | null;
  setFocusedTabId: React.Dispatch<React.SetStateAction<string | null>>;
  workspaceGeneration: number;
  pdfReadingOwner: PdfReadingPreviewOwner | null;
  registerPdfReadingOwner: (owner: PdfReadingPreviewOwner) => () => void;
  previewFocusScopeId?: string;
  // 预览面板状态 / Preview panel state
  isOpen: boolean;
  tabs: PreviewTab[]; // 所有打开的 tabs
  activeTabId: string | null; // 当前激活的 tab ID
  presentationMode: PreviewPresentationMode;

  // 获取当前激活的 tab / Get active tab
  activeTab: PreviewTab | null;

  // 预览面板操作 / Preview panel operations
  openPreview: (
    content: string,
    type: PreviewContentType,
    metadata?: PreviewMetadata,
    options?: OpenPreviewOptions
  ) => void;
  closePreview: () => void;
  closeTab: (tabId: string) => void;
  switchTab: (tabId: string) => void;
  updateContent: (content: string) => void;
  updateTabContent: (tabId: string, content: string) => void;
  markContentSaved: (metadata?: Partial<PreviewMetadata>) => void;
  commitTabContent: (tabId: string, content: string, metadata?: Partial<PreviewMetadata>) => void;
  saveContent: (tabId?: string, contentOverride?: string) => Promise<boolean>; // 保存内容 / Save content
  beginContentEdit: (tabId: string) => Promise<boolean>;
  cancelContentEdit: (tabId: string) => void;
  artifactEditStates: Record<string, PreviewArtifactEditState>;
  openLatestArtifactEdit: (tabId: string) => Promise<void>;
  findPreviewTab: (type: PreviewContentType, content?: string, metadata?: PreviewMetadata) => PreviewTab | null; // 查找匹配的 tab
  closePreviewByIdentity: (type: PreviewContentType, content?: string, metadata?: PreviewMetadata) => void; // 根据内容关闭指定 tab

  // 发送框集成 / Sendbox integration
  addToSendBox: (text: string) => void;
  setSendBoxHandler: (handler: ((text: string) => void) | null) => void;

  // DOM 片段管理 / DOM snippet management
  domSnippets: DomSnippet[];
  addDomSnippet: (tag: string, html: string) => void;
  removeDomSnippet: (id: string) => void;
  clearDomSnippets: () => void;
}

const PreviewContext = createContext<PreviewContextValue | null>(null);

type PreviewScope = 'workspace' | 'embedded';
type PreviewProviderProps = { children: React.ReactNode; scope?: PreviewScope; workspace?: PreviewWorkspace };

const PreviewProviderSession: React.FC<PreviewProviderProps> = ({ children, scope = 'workspace', workspace }) => {
  const {
    isOpen,
    tabs,
    activeTabId,
    presentationMode,
    boardColumns,
    focusedTabId,
    updateSlice,
    setTabs,
    setActiveTabId,
    setBoardColumns,
    setFocusedTabId,
    bindWorkspaceProject,
    ownsWorkspace,
    reset: resetWorkspace,
    generation: workspaceGeneration,
  } = usePreviewWorkspaceState(workspace, scope === 'embedded');
  const [pdfReadingOwner, setPdfReadingOwner] = useState<PdfReadingPreviewOwner | null>(null);
  const registerPdfReadingOwner = useCallback(
    (owner: PdfReadingPreviewOwner) => {
      if (!ownsWorkspace()) return () => {};
      setPdfReadingOwner(owner);
      return () => {
        if (ownsWorkspace()) setPdfReadingOwner((current) => (current === owner ? null : current));
      };
    },
    [ownsWorkspace]
  );
  const { t, i18n } = useTranslation();
  const activeTabIdRef = useRef<string | null>(activeTabId);
  activeTabIdRef.current = activeTabId;
  const liveTabsRef = useRef(tabs);
  liveTabsRef.current = tabs;
  // Passive epochs retire filesystem results; the original workspace and
  // single-flight polling owner still decide whether a result can apply.
  const fileReadEpochsRef = useRef(new Map<string, object>());
  const retireLocalFileRead = useCallback((metadata?: PreviewMetadata) => {
    const key = localPreviewFileKey(metadata);
    if (key) fileReadEpochsRef.current.set(key, {});
  }, []);
  const sendBoxHandler = useRef<((text: string) => void) | null>(null);
  const [domSnippets, setDomSnippets] = useState<DomSnippet[]>([]);
  const previewFocusIds = useMemo(() => tabs.map((tab) => tab.id), [tabs]);
  const focusBoundary = useRef<HTMLDivElement>(null);
  const embeddedFocusId = useId();
  useEffect(
    () =>
      registerRendererAccountReset(`preview:${embeddedFocusId}`, () => {
        setPdfReadingOwner(null);
        resetWorkspace();
      }),
    [embeddedFocusId, resetWorkspace]
  );
  useLayoutEffect(() => {
    setPdfReadingOwner(null);
    setDomSnippets([]);
    sendBoxHandler.current = null;
    fileReadEpochsRef.current.clear();
  }, [workspaceGeneration]);
  const previewFocusScopeId = scope === 'embedded' ? embeddedFocusId : undefined;
  const { remember: rememberPreviewOpener, restore: restorePreviewOpener } = usePreviewFocusReturn(
    previewFocusIds,
    scope === 'embedded' ? focusBoundary : undefined,
    previewFocusScopeId
  );

  useEffect(() => {
    setTabs((currentTabs) => {
      let changed = false;
      const translatedTabs = currentTabs.map((tab) => {
        const titleKey = tab.metadata?.generatedTitleKey;
        if (!titleKey) return tab;
        const translatedTitle = t(titleKey);
        if (translatedTitle === tab.title) return tab;
        changed = true;
        return { ...tab, title: translatedTitle };
      });
      return changed ? translatedTabs : currentTabs;
    });
  }, [i18n.language, i18n.resolvedLanguage, t, setTabs]);

  // 追踪是否正在保存（避免与流式更新冲突）/ Track if currently saving (to avoid conflicts with streaming updates)
  const savingFilesRef = useRef<Set<string>>(new Set());

  // 获取当前激活的 tab / Get active tab
  const activeTab = useMemo(() => {
    return tabs.find((tab) => tab.id === activeTabId) || null;
  }, [tabs, activeTabId]);

  const normalize = useCallback((value?: string | null) => value?.trim() || '', []);

  // 从可能包含描述的字符串中提取文件名 / Extract filename from string that may contain description
  const extractFileName = useCallback((str?: string): string | undefined => {
    if (!str) return undefined;
    // 匹配 "Writing to xxx.md" 或 "Reading xxx.txt" 等模式，提取文件名 / Match patterns like "Writing to xxx.md" and extract filename
    const match = str.match(/(?:Writing to|Reading|Creating|Updating)\s+(.+)$/i);
    return match ? match[1] : str;
  }, []);

  const findPreviewTabInList = useCallback(
    (tabList: PreviewTab[], type: PreviewContentType, content?: string, meta?: PreviewMetadata) => {
      const normalizedFileName = normalize(meta?.file_name);
      const normalizedTitle = normalize(meta?.title);
      const normalizedFilePath = normalize(meta?.file_path);
      const normalizedVersionId = normalize(meta?.versionId);
      const normalizedContentUrl = normalize(meta?.contentUrl);
      const normalizedArtifactId = normalize(meta?.artifactId);

      return (
        tabList.find((tab) => {
          if (tab.content_type !== type) return false;
          const notebookMatch = sameNotebookPreview(meta?.notebookSession, tab.metadata?.notebookSession);
          if (notebookMatch !== null) return notebookMatch;
          const tabFileName = normalize(tab.metadata?.file_name);
          const tabTitle = normalize(tab.metadata?.title);
          const tabFilePath = normalize(tab.metadata?.file_path);
          const tabVersionId = normalize(tab.metadata?.versionId);
          const tabContentUrl = normalize(tab.metadata?.contentUrl);
          const tabArtifactId = normalize(tab.metadata?.artifactId);

          // Remote artifact versions are immutable preview identities. Resolve
          // them before filename/path fallbacks so two versions of report.md
          // can remain open side by side on the board.
          if (normalizedVersionId || tabVersionId) {
            return Boolean(normalizedVersionId && tabVersionId && normalizedVersionId === tabVersionId);
          }
          if (normalizedContentUrl || tabContentUrl) {
            return Boolean(normalizedContentUrl && tabContentUrl && normalizedContentUrl === tabContentUrl);
          }
          if (normalizedArtifactId || tabArtifactId) {
            return Boolean(normalizedArtifactId && tabArtifactId && normalizedArtifactId === tabArtifactId);
          }

          // 优先通过 file_path 匹配（最可靠）/ Prefer matching by file_path (most reliable)
          if (normalizedFilePath && tabFilePath && normalizedFilePath === tabFilePath) return true;

          // 通过 file_name 匹配时，需要确保路径兼容（避免同名文件在不同目录的冲突）
          // When matching by file_name, ensure path compatibility (avoid conflicts of same-named files in different directories)
          if (normalizedFileName && tabFileName && normalizedFileName === tabFileName) {
            // 如果两边都有 file_path，则必须完全匹配
            // If both have file_path, they must match exactly
            if (normalizedFilePath && tabFilePath) {
              return normalizedFilePath === tabFilePath;
            }
            // 如果只有一边有 file_path，不能仅凭 file_name 匹配
            // If only one side has file_path, cannot match by file_name alone
            if (normalizedFilePath || tabFilePath) {
              return false;
            }
            // 都没有 file_path 时，可以通过 file_name 匹配
            // When neither has file_path, can match by file_name
            return true;
          }

          // 再通过 title 匹配 / Then match by title
          if (!normalizedFileName && normalizedTitle && tabTitle && normalizedTitle === tabTitle) return true;

          // 最后才通过 content 匹配（仅用于小文件）/ Finally match by content (only for small files)
          // 对于大文件（PPT/Excel/Word），不使用 content 比较，避免性能问题
          // For large files (PPT/Excel/Word), skip content comparison to avoid performance issues
          if (!normalizedFileName && !normalizedTitle && !normalizedFilePath && content !== undefined) {
            // 只对小于 100KB 的内容进行比较 / Only compare content smaller than 100KB
            if (content.length < 100000 && tab.content === content) return true;
          }

          return false;
        }) || null
      );
    },
    [normalize]
  );

  const findPreviewTab = useCallback(
    (type: PreviewContentType, content?: string, meta?: PreviewMetadata) => {
      return findPreviewTabInList(tabs, type, content, meta);
    },
    [findPreviewTabInList, tabs]
  );

  const openPreview = useCallback(
    (new_content: string, type: PreviewContentType, meta?: PreviewMetadata, options?: OpenPreviewOptions) => {
      if (!ownsWorkspace()) return;
      retireLocalFileRead(
        localPreviewFileKey(meta) ? meta : findPreviewTabInList(liveTabsRef.current, type, new_content, meta)?.metadata
      );
      // A repeated reveal is an action even if the version and page are equal.
      if (type === 'pdf' && meta?.targetPdfPage !== undefined) meta = { ...meta, pdfRevealId: crypto.randomUUID() };
      const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      const requestedTabId = `${type}-${Date.now()}-${Math.random().toString(36).substring(2, 9)}`;
      updateSlice((current) => {
        const prevTabs = current.tabs;
        const activateTab = (tabId: string, nextTabs: PreviewTab[]) => {
          rememberPreviewOpener(tabId, opener);
          activeTabIdRef.current = tabId;
          return {
            ...current,
            tabs: nextTabs,
            activeTabId: tabId,
            isOpen: true,
            presentationMode: options?.presentation ?? current.presentationMode,
          };
        };
        // 如果同一个文件已经打开，则直接激活现有 tab，避免重复 / Focus existing tab when the same file is opened again
        const existingTab = findPreviewTabInList(prevTabs, type, new_content, meta);

        if (existingTab) {
          return activateTab(
            existingTab.id,
            prevTabs.map((tab) => {
              if (tab.id !== existingTab.id) return tab;

              // 如果用户已编辑内容，则保留当前内容，仅更新元数据 / Keep edited content, only merge metadata
              if (tab.isDirty) {
                return meta ? { ...tab, metadata: { ...tab.metadata, ...meta } } : tab;
              }

              return {
                ...tab,
                content: new_content,
                metadata: meta ? { ...tab.metadata, ...meta } : tab.metadata,
                originalContent: new_content,
              };
            })
          );
        }

        // Tab 标题：优先使用文件名，并从 title 中提取实际文件名
        // Tab title: Prefer file_name and extract actual filename from title
        const fallbackTitle = (() => {
          // 根据内容类型设置默认标题 / Set default title based on content type
          if (type === 'markdown')
            return {
              title: t('preview.defaultTitles.markdown'),
              key: 'preview.defaultTitles.markdown',
            } as const;
          if (type === 'diff')
            return {
              title: t('preview.defaultTitles.diff'),
              key: 'preview.defaultTitles.diff',
            } as const;
          if (type === 'code' && meta?.language) return { title: meta.language } as const;
          if (type === 'code')
            return {
              title: t('preview.defaultTitles.code'),
              key: 'preview.defaultTitles.code',
            } as const;
          if (type === 'image')
            return {
              title: t('preview.defaultTitles.image'),
              key: 'preview.defaultTitles.image',
            } as const;
          return {
            title: t('preview.defaultTitles.preview'),
            key: 'preview.defaultTitles.preview',
          } as const;
        })();

        const explicitTitle = extractFileName(meta?.file_name) || extractFileName(meta?.title);
        const title = explicitTitle || fallbackTitle.title;
        const nextMetadata =
          explicitTitle || !('key' in fallbackTitle)
            ? meta
            : { ...meta, generatedTitleKey: fallbackTitle.key as PreviewDefaultTitleKey };

        // 生成唯一 ID / Generate unique ID
        const tabId = requestedTabId;

        const newTab: PreviewTab = {
          id: tabId,
          content: new_content,
          content_type: type,
          metadata: nextMetadata,
          title,
          isDirty: false,
          originalContent: new_content, // 保存原始内容 / Save original content
        };

        // Single-preview browse mode: reuse the active tab in place instead of
        // stacking a new one — unless it has unsaved edits, then fall back to a
        // new tab so changes aren't lost.
        if (options?.replace && options.presentation !== 'board') {
          const activeIdx = current.activeTabId ? prevTabs.findIndex((tab) => tab.id === current.activeTabId) : -1;
          const activePreviewTab = activeIdx >= 0 ? prevTabs[activeIdx] : null;
          if (activePreviewTab && !activePreviewTab.isDirty) {
            const replacedTab: PreviewTab = { ...newTab, id: activePreviewTab.id };
            return activateTab(
              activePreviewTab.id,
              prevTabs.map((tab, idx) => (idx === activeIdx ? replacedTab : tab))
            );
          }
        }

        return activateTab(tabId, [...prevTabs, newTab]);
      });
    },
    [ownsWorkspace, extractFileName, findPreviewTabInList, rememberPreviewOpener, t, updateSlice, retireLocalFileRead]
  );

  const closePreview = useCallback(() => {
    if (!ownsWorkspace()) return;
    fileReadEpochsRef.current.clear();
    restorePreviewOpener(activeTabIdRef.current, true);
    updateSlice((current) => ({
      ...current,
      isOpen: false,
      tabs: [],
      activeTabId: null,
      presentationMode: 'single',
      focusedTabId: null,
    }));
    setDomSnippets([]);
  }, [ownsWorkspace, restorePreviewOpener, updateSlice]);

  // Track last-known mtime per file path for external change detection
  const fileMtimeRef = useRef<Map<string, number>>(new Map());
  const fileMetadataChecksRef = useRef<Set<string>>(new Set());

  const closeTab = useCallback(
    (tabId: string) => {
      if (!ownsWorkspace()) return;
      restorePreviewOpener(tabId);
      updateSlice((current) => {
        const closing = current.tabs.find((tab) => tab.id === tabId);
        const sourceKey = localPreviewFileKey(closing?.metadata);
        if (sourceKey) {
          fileMtimeRef.current.delete(sourceKey);
          fileReadEpochsRef.current.delete(sourceKey);
        }
        const nextTabs = current.tabs.filter((tab) => tab.id !== tabId);
        const nextActive =
          current.activeTabId === tabId ? (nextTabs[nextTabs.length - 1]?.id ?? null) : current.activeTabId;
        return {
          ...current,
          tabs: nextTabs,
          activeTabId: nextActive,
          focusedTabId: current.focusedTabId === tabId ? null : current.focusedTabId,
          presentationMode: nextTabs.length <= 1 ? 'single' : current.presentationMode,
          isOpen: nextTabs.length > 0 && current.isOpen,
        };
      });
    },
    [ownsWorkspace, restorePreviewOpener, updateSlice]
  );

  const closePreviewByIdentity = useCallback(
    (type: PreviewContentType, content?: string, meta?: PreviewMetadata) => {
      const tab = findPreviewTab(type, content, meta);
      if (tab) {
        closeTab(tab.id);
      }
    },
    [findPreviewTab, closeTab]
  );

  const updateTabContent = useCallback(
    (tabId: string, new_content: string) => {
      // 严格的类型检查，防止 Event 对象被错误传递 / Strict type checking to prevent Event object from being passed incorrectly
      if (typeof new_content !== 'string' || !ownsWorkspace()) {
        return;
      }
      retireLocalFileRead(liveTabsRef.current.find((tab) => tab.id === tabId)?.metadata);

      setTabs((prevTabs) =>
        prevTabs.map((tab) => {
          if (tab.id !== tabId) return tab;

          // 检查内容是否与原始内容不同 / Check if content differs from original
          const isDirty = new_content !== tab.originalContent;
          return { ...tab, content: new_content, isDirty };
        })
      );
    },
    [setTabs, ownsWorkspace, retireLocalFileRead]
  );

  const updateContent = useCallback(
    (new_content: string) => {
      if (!activeTabId) return;
      updateTabContent(activeTabId, new_content);
    },
    [activeTabId, updateTabContent]
  );

  const commitTabContent = useCallback(
    (tabId: string, content: string, metadata?: Partial<PreviewMetadata>) => {
      if (typeof content !== 'string' || !ownsWorkspace()) return;
      retireLocalFileRead(liveTabsRef.current.find((tab) => tab.id === tabId)?.metadata);
      setTabs((prevTabs) =>
        prevTabs.map((tab) =>
          tab.id === tabId
            ? {
                ...tab,
                content,
                metadata: metadata ? { ...tab.metadata, ...metadata } : tab.metadata,
                originalContent: content,
                isDirty: false,
              }
            : tab
        )
      );
    },
    [setTabs, ownsWorkspace, retireLocalFileRead]
  );

  const markContentSaved = useCallback(
    (metadata?: Partial<PreviewMetadata>) => {
      if (!activeTabId || !ownsWorkspace()) return;
      retireLocalFileRead(liveTabsRef.current.find((tab) => tab.id === activeTabId)?.metadata);
      setTabs((prevTabs) =>
        prevTabs.map((tab) =>
          tab.id === activeTabId
            ? {
                ...tab,
                metadata: metadata ? { ...tab.metadata, ...metadata } : tab.metadata,
                originalContent: tab.content,
                isDirty: false,
              }
            : tab
        )
      );
    },
    [activeTabId, setTabs, ownsWorkspace, retireLocalFileRead]
  );

  const commitArtifactEdit = useCallback(
    (tabId: string, sourceVersionId: string, content: string, metadata: Partial<PreviewMetadata>, account: string) => {
      setTabs((current) =>
        current.map((tab) =>
          tab.id === tabId && tab.metadata?.versionId === sourceVersionId && rendererAccountScopedKey('') === account
            ? {
                ...tab,
                metadata: { ...tab.metadata, ...metadata },
                originalContent: content,
                isDirty: tab.content !== content,
              }
            : tab
        )
      );
    },
    [setTabs]
  );
  const artifactEdits = usePreviewArtifactEdits({
    tabs,
    activeTabId,
    commit: commitArtifactEdit,
    open: openPreview,
    presentation: presentationMode,
  });

  useLayoutEffect(() => {
    for (const tabId of Object.keys(artifactEdits.states)) artifactEdits.cancel(tabId);
  }, [workspaceGeneration]);

  const saveContent = useCallback(
    async (tabId?: string, contentOverride?: string) => {
      if (!ownsWorkspace()) return false;
      const targetTabId = tabId || activeTabId;
      if (!targetTabId) return false;

      const tab = tabs.find((candidate) => candidate.id === targetTabId);
      if (!tab) return false;
      const contentToSave = typeof contentOverride === 'string' ? contentOverride : tab.content;

      if (tab.metadata?.artifactId) {
        const outcome = await artifactEdits.save(targetTabId, contentToSave);
        return outcome.kind === 'published' && outcome.originApplied && outcome.draftClean;
      }

      // 如果有 file_path 和 workspace，写回工作空间文件 / If file_path and workspace exist, write back to workspace file
      if (tab.metadata?.file_path && tab.metadata?.workspace) {
        try {
          const file_path = tab.metadata.file_path;
          retireLocalFileRead(tab.metadata);

          // 标记文件正在保存（避免触发文件监听回调）/ Mark file as being saved (to avoid triggering file watch callback)
          savingFilesRef.current.add(file_path);

          // 使用 IPC 写入文件 / Write file via IPC
          const success = await ipcBridge.fs.writeFile.invoke({
            path: file_path,
            data: contentToSave,
            workspace: tab.metadata.workspace,
          });

          if (success) {
            commitTabContent(targetTabId, contentToSave);
          }

          // 延迟移除保存标记（给文件监听一点时间忽略变化）/ Delay removing save flag (give file watch time to ignore change)
          setTimeout(() => {
            savingFilesRef.current.delete(file_path);
          }, 500);

          return success;
        } catch (error) {
          // 发生错误，静默处理（只记录到控制台）/ Error occurred, handle silently (log only)
          // 确保移除保存标记 / Ensure save flag is removed
          if (tab.metadata?.file_path) {
            savingFilesRef.current.delete(tab.metadata.file_path);
          }
          throw error;
        }
      }
      return false;
    },
    [ownsWorkspace, activeTabId, tabs, commitTabContent, artifactEdits.save, retireLocalFileRead]
  );

  const addToSendBox = useCallback(
    (text: string) => {
      if (!ownsWorkspace()) return;
      if (sendBoxHandler.current) {
        sendBoxHandler.current(text);
      }
    },
    [ownsWorkspace]
  );

  const setSendBoxHandler = useCallback(
    (handler: ((text: string) => void) | null) => {
      if (ownsWorkspace()) sendBoxHandler.current = handler;
    },
    [ownsWorkspace]
  );

  // DOM 片段管理函数 / DOM snippet management functions
  // 只保留最新的一个片段 / Only keep the latest snippet
  const addDomSnippet = useCallback(
    (tag: string, html: string) => {
      if (!ownsWorkspace()) return;
      const id = `snippet-${Date.now()}-${Math.random().toString(36).substring(2, 9)}`;
      setDomSnippets([{ id, tag, html }]);
    },
    [ownsWorkspace]
  );

  const removeDomSnippet = useCallback(
    (id: string) => {
      if (ownsWorkspace()) setDomSnippets((prev) => prev.filter((s) => s.id !== id));
    },
    [ownsWorkspace]
  );

  const clearDomSnippets = useCallback(() => {
    if (ownsWorkspace()) setDomSnippets([]);
  }, [ownsWorkspace]);

  // File mtime polling: detect external file changes (external editors or command-line tools) by comparing lastModified.
  // Only polls the active tab to minimize IPC overhead; checks other tabs once on tab switch.
  // Uses polling instead of fileWatch IPC events because buildEmitter's main→renderer event delivery
  // is unreliable after the first emission in Electron (only the first event reaches the renderer).
  const checkFileUpdate = useCallback(
    async (tab: PreviewTab) => {
      if (!ownsWorkspace()) return;
      const file_path = tab.metadata?.file_path;
      const sourceKey = localPreviewFileKey(tab.metadata);
      if (
        !shouldPollPreviewFileMetadata(tab, document.visibilityState) ||
        !file_path ||
        !sourceKey ||
        tab.isDirty ||
        savingFilesRef.current.has(file_path) ||
        fileMetadataChecksRef.current.has(sourceKey)
      ) {
        return;
      }

      fileMetadataChecksRef.current.add(sourceKey);
      let epoch = fileReadEpochsRef.current.get(sourceKey);
      if (!epoch) {
        epoch = {};
        fileReadEpochsRef.current.set(sourceKey, epoch);
      }
      const isCurrentRead = () => ownsWorkspace() && fileReadEpochsRef.current.get(sourceKey) === epoch;
      try {
        const metadata = await ipcBridge.fs.getFileMetadata.invoke({
          path: file_path,
          workspace: tab.metadata?.workspace,
        });
        if (!isCurrentRead() || !metadata) return;
        const prevMtime = fileMtimeRef.current.get(sourceKey);
        if (prevMtime === undefined) {
          fileMtimeRef.current.set(sourceKey, metadata.lastModified);
          return;
        }
        if (metadata.lastModified === prevMtime) return;

        if (['pdf', 'word', 'excel', 'ppt', 'audio', 'video', 'hdf5', 'unsupported'].includes(tab.content_type)) {
          fileMtimeRef.current.set(sourceKey, metadata.lastModified);
          return;
        }
        const content =
          tab.content_type === 'image'
            ? await ipcBridge.fs.getImageBase64.invoke({
                path: file_path,
                workspace: tab.metadata?.workspace,
              })
            : await ipcBridge.fs.readFile.invoke({
                path: file_path,
                workspace: tab.metadata?.workspace,
              });
        if (!isCurrentRead() || content == null) return;
        setTabs((latest) => {
          if (!isCurrentRead()) return latest;
          let applied = false;
          const next = latest.map((candidate) => {
            if (localPreviewFileKey(candidate.metadata) !== sourceKey) return candidate;
            if (savingFilesRef.current.has(file_path) || candidate.isDirty) return candidate;
            applied = true;
            return { ...candidate, content, originalContent: content, isDirty: false };
          });
          // Only accepted body reads acknowledge changed mtime. Failures and
          // retired reads remain eligible for the next ordinary polling tick.
          if (applied) fileMtimeRef.current.set(sourceKey, metadata.lastModified);
          return applied ? next : latest;
        });
      } catch (error) {
        console.error('[PreviewContext] Failed to refresh file metadata:', file_path, error);
      } finally {
        fileMetadataChecksRef.current.delete(sourceKey);
      }
    },
    [setTabs, ownsWorkspace]
  );

  // Keep a ref to activeTab so the polling interval always sees the latest object
  // without re-running the effect on every tabs state change.
  const activeTabRef = useRef<PreviewTab | null>(null);
  activeTabRef.current = activeTab;

  const activeFilePath = activeTab?.metadata?.file_path;

  // Poll only the foreground active tab. One in-flight check per file prevents slow
  // filesystem I/O from accumulating interval work and blocking renderer updates.
  useEffect(() => {
    if (scope !== 'workspace' || !activeFilePath) return;

    let pollId: ReturnType<typeof setInterval> | undefined;
    const checkActiveFile = () => {
      const current = activeTabRef.current;
      if (current) void checkFileUpdate(current);
    };
    const syncPolling = () => {
      if (pollId) {
        clearInterval(pollId);
        pollId = undefined;
      }
      const current = activeTabRef.current;
      if (!shouldPollPreviewFileMetadata(current, document.visibilityState)) return;
      checkActiveFile();
      pollId = setInterval(checkActiveFile, 1000);
    };

    document.addEventListener('visibilitychange', syncPolling);
    syncPolling();

    return () => {
      document.removeEventListener('visibilitychange', syncPolling);
      if (pollId) clearInterval(pollId);
    };
  }, [scope, activeFilePath, checkFileUpdate]);

  // Browser preview opens are renderer-local. The HTTP/WebUI bridge has no
  // desktop main-process preview channel, so subscribing to its unavailable
  // compatibility emitter would only produce a false runtime warning.
  useEffect(() => {
    if (scope !== 'workspace') return;
    const handleEmitterPreviewOpen = (data: {
      content: string;
      contentType: PreviewContentType;
      metadata?: PreviewMetadata;
    }) => {
      if (data && data.content) {
        openPreview(data.content, data.contentType, data.metadata);
      }
    };

    emitter.on('preview.open', handleEmitterPreviewOpen);

    return () => {
      emitter.off('preview.open', handleEmitterPreviewOpen);
    };
  }, [scope, openPreview]);

  const previewContextValue = useMemo(() => {
    return {
      bindWorkspaceProject,
      workspaceGeneration,
      boardColumns,
      setBoardColumns,
      focusedTabId,
      setFocusedTabId,
      pdfReadingOwner,
      registerPdfReadingOwner,
      previewFocusScopeId,
      isOpen,
      tabs,
      activeTabId,
      presentationMode,
      activeTab,
      openPreview,
      closePreview,
      closeTab,
      switchTab: setActiveTabId,
      updateContent,
      updateTabContent,
      markContentSaved,
      commitTabContent,
      saveContent,
      beginContentEdit: artifactEdits.begin,
      cancelContentEdit: artifactEdits.cancel,
      artifactEditStates: artifactEdits.states,
      openLatestArtifactEdit: artifactEdits.openLatest,
      findPreviewTab,
      closePreviewByIdentity,
      addToSendBox,
      setSendBoxHandler,
      domSnippets,
      addDomSnippet,
      removeDomSnippet,
      clearDomSnippets,
    };
  }, [
    bindWorkspaceProject,
    workspaceGeneration,
    boardColumns,
    setBoardColumns,
    focusedTabId,
    setFocusedTabId,
    pdfReadingOwner,
    registerPdfReadingOwner,
    previewFocusScopeId,
    isOpen,
    tabs,
    activeTabId,
    presentationMode,
    activeTab,
    openPreview,
    closePreview,
    closeTab,
    setActiveTabId,
    updateContent,
    updateTabContent,
    markContentSaved,
    commitTabContent,
    saveContent,
    artifactEdits.begin,
    artifactEdits.cancel,
    artifactEdits.states,
    artifactEdits.openLatest,
    findPreviewTab,
    closePreviewByIdentity,
    addToSendBox,
    setSendBoxHandler,
    domSnippets,
    addDomSnippet,
    removeDomSnippet,
    clearDomSnippets,
  ]);

  return (
    <PreviewContext.Provider value={previewContextValue}>
      {scope === 'embedded' ? (
        <div ref={focusBoundary} style={{ display: 'contents' }} data-preview-scope='embedded'>
          {children}
        </div>
      ) : (
        children
      )}
    </PreviewContext.Provider>
  );
};

export const PreviewProvider: React.FC<PreviewProviderProps> = (props) => (
  <PreviewProviderSession key={props.scope ?? 'workspace'} {...props} />
);

export const usePreviewContext = () => {
  const context = useContext(PreviewContext);
  if (!context) {
    throw new Error('usePreviewContext must be used within PreviewProvider');
  }
  return context;
};

export const useOptionalPreviewContext = () => useContext(PreviewContext);

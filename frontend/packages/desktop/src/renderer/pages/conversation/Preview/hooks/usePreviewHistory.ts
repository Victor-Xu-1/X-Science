/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import type { PreviewHistoryTarget, PreviewSnapshotInfo } from '@/common/types/office/preview';
import { Message } from '@arco-design/web-react';
import { rendererAccountScopedKey } from '@/renderer/services/rendererAccountScope';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SNAPSHOT_DEBOUNCE_TIME } from '../constants';
import { isPreviewContentStoredAsText } from '../previewContentPersistence';
import {
  getXScienceArtifactVersionContentUrl,
  loadXScienceArtifactVersions,
  loadXScienceArtifactVersionText,
} from '@/renderer/services/xScienceArtifacts';

/**
 * 预览历史 Hook 配置
 * Preview history hook configuration
 */
interface UsePreviewHistoryOptions {
  /**
   * 当前活动 Tab
   * Current active tab
   */
  activeTab: {
    content_type: string;
    content: string;
    title: string;
    metadata?: {
      file_path?: string;
      workspace?: string;
      file_name?: string;
      title?: string;
      language?: string;
      artifactId?: string;
      versionId?: string;
      contentUrl?: string;
    };
  } | null;

  /**
   * 更新内容回调
   * Update content callback
   */
  updateContent: (content: string) => void;
  markContentSaved: (metadata?: { versionId?: string; contentUrl?: string }) => void;
  saveContent: () => Promise<boolean>;
}

/**
 * 预览历史 Hook 返回值
 * Preview history hook return value
 */
interface UsePreviewHistoryReturn {
  /**
   * 历史版本列表
   * History versions list
   */
  historyVersions: PreviewSnapshotInfo[];

  /**
   * 是否正在加载历史
   * Whether history is loading
   */
  historyLoading: boolean;

  /**
   * 是否正在保存快照
   * Whether snapshot is saving
   */
  snapshotSaving: boolean;

  /**
   * 历史加载错误信息
   * History loading error message
   */
  historyError: boolean;

  /**
   * 历史目标（用于 IPC 调用）
   * History target (for IPC calls)
   */
  historyTarget: PreviewHistoryTarget | null;

  /**
   * 刷新历史列表
   * Refresh history list
   */
  refreshHistory: () => Promise<void>;

  /**
   * 保存快照
   * Save snapshot
   */
  handleSaveSnapshot: () => Promise<boolean>;

  /**
   * 选择历史快照
   * Select history snapshot
   */
  handleSnapshotSelect: (snapshot: PreviewSnapshotInfo) => Promise<void>;

  /**
   * Message API 实例（用于显示提示信息）
   * Message API instance (for displaying notifications)
   */
  messageApi: ReturnType<typeof Message.useMessage>[0];

  /**
   * Message Context Holder（需要渲染在组件中）
   * Message Context Holder (needs to be rendered in component)
   */
  messageContextHolder: ReturnType<typeof Message.useMessage>[1];
}

/**
 * 预览历史管理 Hook
 * Preview history management hook
 *
 * 处理历史版本的加载、保存和选择
 * Handles loading, saving, and selecting history versions
 *
 * @param options - 配置选项 / Configuration options
 * @returns 历史管理相关状态和方法 / History management related states and methods
 */
export const usePreviewHistory = ({
  activeTab,
  updateContent,
  markContentSaved,
  saveContent,
}: UsePreviewHistoryOptions): UsePreviewHistoryReturn => {
  const { t } = useTranslation();
  const [historyVersions, setHistoryVersions] = useState<PreviewSnapshotInfo[]>([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [snapshotSaving, setSnapshotSaving] = useState(false);
  const [historyError, setHistoryError] = useState(false);
  const [messageApi, messageContextHolder] = Message.useMessage();
  const historyOwner = rendererAccountScopedKey(
    JSON.stringify([
      activeTab?.metadata?.artifactId,
      activeTab?.metadata?.versionId,
      activeTab?.metadata?.file_path,
      activeTab?.content_type,
    ])
  );
  const latestHistoryOwner = useRef(historyOwner);
  latestHistoryOwner.current = historyOwner;
  const readRevision = useRef(0);
  const selectionRevision = useRef(0);
  const lastSnapshotTimeRef = useRef<number>(0); // 记录上次快照保存时间 / Track last snapshot save time

  // 构建历史目标对象 / Build history target object
  const historyTarget = useMemo<PreviewHistoryTarget | null>(() => {
    if (!activeTab) return null;
    const meta = activeTab.metadata;
    const fallbackName = meta?.file_name || meta?.title || activeTab.title;
    return {
      contentType: activeTab.content_type as import('@/common/types/office/preview').PreviewContentType,
      file_path: meta?.file_path,
      workspace: meta?.workspace,
      file_name: fallbackName,
      title: meta?.title || activeTab.title,
      language: meta?.language,
      artifact_id: meta?.artifactId,
      version_id: meta?.versionId,
      content_url: meta?.contentUrl,
    };
  }, [
    activeTab?.content_type,
    activeTab?.title,
    activeTab?.metadata?.file_path,
    activeTab?.metadata?.workspace,
    activeTab?.metadata?.file_name,
    activeTab?.metadata?.title,
    activeTab?.metadata?.language,
    activeTab?.metadata?.artifactId,
    activeTab?.metadata?.versionId,
    activeTab?.metadata?.contentUrl,
  ]);

  // 刷新历史列表 / Refresh history list
  const refreshHistory = useCallback(async () => {
    if (historyOwner !== latestHistoryOwner.current) return;
    const owner = historyOwner,
      revision = ++readRevision.current;
    const owns = () => owner === latestHistoryOwner.current && revision === readRevision.current;
    if (!historyTarget) {
      setHistoryVersions([]);
      return;
    }

    setHistoryLoading(true);
    setHistoryError(false);
    try {
      if (historyTarget.artifact_id) {
        const versions = await loadXScienceArtifactVersions(historyTarget.artifact_id);
        if (!owns()) return;
        setHistoryVersions(
          versions.map((version) => ({
            id: version.versionId,
            label: t('preview.versionLabel', { version: version.versionNumber }),
            created_at: version.createdAt ? new Date(version.createdAt).getTime() : 0,
            size: version.sizeBytes,
            contentType: historyTarget.contentType,
            file_name: historyTarget.file_name,
            file_path: getXScienceArtifactVersionContentUrl(version.versionId),
          }))
        );
        setHistoryError(false);
        return;
      }
      const versions = await ipcBridge.previewHistory.list.invoke({ target: historyTarget });
      if (!owns()) return;
      setHistoryVersions(versions || []);
      setHistoryError(false);
    } catch {
      if (!owns()) return;
      setHistoryError(true);
      setHistoryVersions([]);
    } finally {
      if (owns()) setHistoryLoading(false);
    }
  }, [historyTarget, historyOwner, t]);

  // 当 historyTarget 变化时自动刷新历史 / Auto refresh history when historyTarget changes
  useEffect(() => {
    void refreshHistory();
  }, [refreshHistory]);

  // 保存快照 / Save snapshot
  const handleSaveSnapshot = useCallback(async () => {
    if (!historyTarget || !activeTab) {
      return false;
    }
    if (snapshotSaving) return false;

    // 防抖检查：如果距离上次保存快照时间小于1秒，则忽略 / Debounce check: Ignore if less than 1 second since last save
    const now = Date.now();
    if (!historyTarget.artifact_id && now - lastSnapshotTimeRef.current < SNAPSHOT_DEBOUNCE_TIME) {
      messageApi.info(t('preview.tooFrequent'));
      return false;
    }

    try {
      setSnapshotSaving(true);
      lastSnapshotTimeRef.current = now; // 更新最后保存时间 / Update last save time
      if (historyTarget.artifact_id) {
        if (!(await saveContent())) return false;
      } else {
        await ipcBridge.previewHistory.save.invoke({
          target: historyTarget,
          content: activeTab.content,
        });
        markContentSaved();
      }
      messageApi.success(t('preview.snapshotSaved'));
      await refreshHistory();
      return true;
    } catch {
      messageApi.error(t('preview.snapshotSaveFailed'));
      return false;
    } finally {
      setSnapshotSaving(false);
    }
  }, [
    historyTarget,
    historyOwner,
    activeTab,
    snapshotSaving,
    messageApi,
    refreshHistory,
    markContentSaved,
    saveContent,
    t,
  ]);

  // 选择历史快照 / Select history snapshot
  const handleSnapshotSelect = useCallback(
    async (snapshot: PreviewSnapshotInfo) => {
      const owner = historyOwner,
        revision = ++selectionRevision.current;
      const owns = () => owner === latestHistoryOwner.current && revision === selectionRevision.current;
      if (!historyTarget) {
        return;
      }
      try {
        if (historyTarget.artifact_id) {
          const remoteContent = isPreviewContentStoredAsText(historyTarget.contentType, historyTarget.file_name)
            ? await loadXScienceArtifactVersionText(snapshot.id)
            : getXScienceArtifactVersionContentUrl(snapshot.id);
          if (!owns()) return;
          updateContent(remoteContent);
          markContentSaved({
            versionId: snapshot.id,
            contentUrl: getXScienceArtifactVersionContentUrl(snapshot.id),
          });
          messageApi.success(t('preview.historyLoaded'));
          return;
        }
        const result = await ipcBridge.previewHistory.getContent.invoke({
          target: historyTarget,
          snapshot_id: snapshot.id,
        });
        if (!owns()) return;
        if (result?.content) {
          updateContent(result.content);
          markContentSaved();
          messageApi.success(t('preview.historyLoaded'));
        } else {
          messageApi.error(t('preview.errors.emptySnapshot'));
          return;
        }
      } catch {
        if (owns()) messageApi.error(t('preview.historyLoadFailed'));
      }
    },
    [historyTarget, historyOwner, messageApi, updateContent, markContentSaved, t]
  );

  return {
    historyVersions,
    historyLoading,
    snapshotSaving,
    historyError,
    historyTarget,
    refreshHistory,
    handleSaveSnapshot,
    handleSnapshotSelect,
    messageApi,
    messageContextHolder,
  };
};

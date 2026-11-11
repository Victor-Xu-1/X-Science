/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { ipcBridge } from '@/common';
import type { PreviewHistoryTarget } from '@/common/types/office/preview';
import { Message } from '@arco-design/web-react';
import { rendererAccountScopedKey } from '@/renderer/services/rendererAccountScope';
import { useCallback, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { SNAPSHOT_DEBOUNCE_TIME } from '../constants';
import { useOptionalPreviewContext } from '../context/PreviewContext';
import { usePreviewHistoryOwner, type PreviewHistoryLease } from './usePreviewHistoryOwner';

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
    id?: string;
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

  markContentSaved: (metadata?: { versionId?: string; contentUrl?: string }) => void;
  saveContent: () => Promise<boolean>;
}

/**
 * 预览历史 Hook 返回值
 * Preview history hook return value
 */
interface UsePreviewHistoryReturn {
  /**
   * 是否正在保存快照
   * Whether snapshot is saving
   */
  snapshotSaving: boolean;

  /**
   * 历史目标（用于 IPC 调用）
   * History target (for IPC calls)
   */
  historyTarget: PreviewHistoryTarget | null;

  /**
   * 保存快照
   * Save snapshot
   */
  handleSaveSnapshot: () => Promise<boolean>;

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
 * 预览快照保存 Hook
 * Preview snapshot save hook
 *
 * 保存当前预览，并将状态与反馈绑定到接收端生命周期。
 * Saves the current preview with receiver-scoped state and feedback.
 *
 * @param options - 配置选项 / Configuration options
 * @returns 快照保存状态和方法 / Snapshot save state and methods
 */
export const usePreviewHistory = ({
  activeTab,
  markContentSaved,
  saveContent,
}: UsePreviewHistoryOptions): UsePreviewHistoryReturn => {
  const { t } = useTranslation();
  const [saving, setSaving] = useState<PreviewHistoryLease | null>(null);
  const saveFlight = useRef<PreviewHistoryLease | null>(null);
  const canvasGeneration = useOptionalPreviewContext()?.workspaceGeneration;
  const [messageApi, messageContextHolder] = Message.useMessage();
  const receiver = [canvasGeneration, activeTab?.id];
  // An artifact ACK changes version in the original save owner. That does not
  // change its receiving tab/canvas or invalidate the save lease.
  const saveOwner = rendererAccountScopedKey(
    JSON.stringify([
      ...receiver,
      activeTab?.metadata?.artifactId,
      activeTab?.metadata?.artifactId ? null : activeTab?.metadata?.workspace,
      activeTab?.metadata?.artifactId ? null : activeTab?.metadata?.file_path,
      activeTab?.content_type,
    ])
  );
  const beginSave = usePreviewHistoryOwner(saveOwner);
  const snapshotSaving = saving?.owns() ?? false;
  const lastSnapshot = useRef<{ lease: PreviewHistoryLease; time: number } | null>(null);

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

  // 保存快照 / Save snapshot
  const handleSaveSnapshot = useCallback(async () => {
    if (!historyTarget || !activeTab) {
      return false;
    }
    const lease = beginSave();
    if (!lease || saveFlight.current?.owns()) return false;

    // 防抖检查：如果距离上次保存快照时间小于1秒，则忽略 / Debounce check: Ignore if less than 1 second since last save
    const now = Date.now();
    if (
      !historyTarget.artifact_id &&
      lastSnapshot.current?.lease.owns() &&
      now - lastSnapshot.current.time < SNAPSHOT_DEBOUNCE_TIME
    ) {
      messageApi.info(t('preview.tooFrequent'));
      return false;
    }

    saveFlight.current = lease;
    setSaving(lease);
    lastSnapshot.current = { lease, time: now };
    try {
      // Catch write failures only. Notification/receiver errors must not turn
      // a successful server write into a pretend save failure.
      try {
        if (historyTarget.artifact_id) {
          const saved = await saveContent();
          if (!lease.owns() || !saved) return false;
        } else {
          await ipcBridge.previewHistory.save.invoke({ target: historyTarget, content: activeTab.content });
        }
      } catch {
        if (lease.owns()) messageApi.error(t('preview.snapshotSaveFailed'));
        return false;
      }
      if (!lease.owns()) return false;
      if (!historyTarget.artifact_id) markContentSaved();
      if (!lease.owns()) return false;
      messageApi.success(t('preview.snapshotSaved'));
      return lease.owns();
    } finally {
      if (lease.owns() && saveFlight.current === lease) {
        saveFlight.current = null;
        setSaving((current) => (current === lease ? null : current));
      }
    }
  }, [historyTarget, beginSave, activeTab, messageApi, markContentSaved, saveContent, t]);

  return {
    snapshotSaving,
    historyTarget,
    handleSaveSnapshot,
    messageApi,
    messageContextHolder,
  };
};

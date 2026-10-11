/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { IConversationMcpStatus } from '@/common/config/storage';
import React, { createContext, useContext, useRef } from 'react';

/**
 * Conversation context interface
 * 会话上下文接口
 */
export interface ConversationContextValue {
  /**
   * Conversation ID
   * 会话 ID
   */
  conversation_id: string;

  /**
   * Workspace directory path
   * 工作空间目录路径
   */
  workspace?: string;

  /**
   * X-Science project that owns this conversation.
   */
  projectId?: string;

  /**
   * X-Science root frame for excluding self-references.
   */
  currentFrameId?: string;

  /**
   * Conversation type
   * 会话类型
   */
  type: 'acp';

  /**
   * Cron job ID (if this conversation was created by a scheduled task)
   */
  cron_job_id?: string;

  /**
   * When true, the conversation view hides its SendBox.
   */
  hideSendBox?: boolean;

  /**
   * Loaded skill names for this conversation (snapshot from conversation.extra.skills).
   * Surfaced inside the SendBox `+` menu so users can review/jump to active skills.
   */
  loadedSkills?: string[];

  /**
   * Loaded MCP server names for this conversation (snapshot from
   * conversation.extra.mcp_servers).
   */
  loadedMcpServers?: string[];

  /**
   * Structured MCP status snapshot for this conversation (from
   * conversation.extra.mcp_statuses).
   */
  loadedMcpStatuses?: IConversationMcpStatus[];

  /**
   * Assistant id bound to this conversation snapshot, if any.
   */
  assistantId?: string;
}

/**
 * Conversation context
 * 会话上下文 - 提供会话级别的信息，如工作空间路径
 */
const ConversationContext = createContext<ConversationContextValue | null>(null);

const sameArrayItems = <T,>(left: readonly T[] | undefined, right: readonly T[] | undefined): boolean => {
  if (left === right) return true;
  if (!left || !right || left.length !== right.length) return false;
  return left.every((item, index) => item === right[index]);
};

const sameMcpStatuses = (
  left: readonly IConversationMcpStatus[] | undefined,
  right: readonly IConversationMcpStatus[] | undefined
): boolean => {
  if (left === right) return true;
  if (!left || !right || left.length !== right.length) return false;
  return left.every((item, index) => {
    const other = right[index];
    return (
      item === other ||
      (item.id === other.id && item.name === other.name && item.status === other.status && item.reason === other.reason)
    );
  });
};

/**
 * AcpChat rebuilds this value object on every stream tick. Consumers such as
 * MessageText sit below a memoized row, so a new object identity would still
 * rerender every visible message and rerun markdown/artifact work.
 */
export const areConversationContextValuesEqual = (
  previous: ConversationContextValue,
  next: ConversationContextValue
): boolean =>
  previous.conversation_id === next.conversation_id &&
  previous.workspace === next.workspace &&
  previous.projectId === next.projectId &&
  previous.currentFrameId === next.currentFrameId &&
  previous.type === next.type &&
  previous.cron_job_id === next.cron_job_id &&
  previous.hideSendBox === next.hideSendBox &&
  previous.assistantId === next.assistantId &&
  sameArrayItems(previous.loadedSkills, next.loadedSkills) &&
  sameArrayItems(previous.loadedMcpServers, next.loadedMcpServers) &&
  sameMcpStatuses(previous.loadedMcpStatuses, next.loadedMcpStatuses);

/**
 * Conversation context provider
 * 会话上下文提供者
 */
export const ConversationProvider: React.FC<{
  children: React.ReactNode;
  value: ConversationContextValue;
}> = ({ children, value }) => {
  const valueRef = useRef(value);
  if (!areConversationContextValuesEqual(valueRef.current, value)) {
    valueRef.current = value;
  }
  return <ConversationContext.Provider value={valueRef.current}>{children}</ConversationContext.Provider>;
};

/**
 * Hook to use conversation context
 * 使用会话上下文的 Hook
 */
export const useConversationContext = (): ConversationContextValue => {
  const context = useContext(ConversationContext);
  if (!context) {
    throw new Error('useConversationContext must be used within ConversationProvider');
  }
  return context;
};

/**
 * Hook to safely use conversation context (returns null if not in provider)
 * 安全使用会话上下文的 Hook（如果不在 provider 中则返回 null）
 */
export const useConversationContextSafe = (): ConversationContextValue | null => {
  return useContext(ConversationContext);
};

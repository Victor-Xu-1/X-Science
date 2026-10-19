/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { IConversationMcpStatus } from '@/common/config/storage';
import { Button, Message, Tooltip, Trigger } from '@arco-design/web-react';
import { Check, FolderOpen, ListCheckbox, Paperclip, Shield } from '@icon-park/react';
import { BookMarked, ChartNoAxesCombined, Loader2, Plus as ControlPlus } from 'lucide-react';
import ComposerCapabilityPicker from './ComposerCapabilityPicker';
import ComposerHostFiles from '@/renderer/components/chat/SendBox/hostFiles/ComposerHostFiles';
import { getRendererAccountScopeToken, registerRendererAccountReset } from '@/renderer/services/rendererAccountScope';
import { useConversationContextSafe } from '@/renderer/hooks/context/ConversationContext';
import { isElectronDesktop } from '@/renderer/utils/platform';
import { FileService } from '@/renderer/services/FileService';
import type { FileMetadata } from '@/renderer/services/FileService';
import { emitter } from '@/renderer/utils/emitter';
import { getMenuItems, handleMenuNavigation } from '@/renderer/utils/menuKeyboard';
import React, { useCallback, useEffect, useId, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import './conversationControlMenu.css';

interface FileAttachButtonProps {
  openFileSelector: () => void;
  onSelectProjectFiles?: () => void;
  onLocalFilesAdded?: (files: Pick<FileMetadata, 'path'>[]) => void;
  /** Keep browser-selected files in a new-conversation draft until send. */
  onLocalFilesSelected?: (files: File[]) => void | Promise<void>;
  loadedSkills?: string[];
  loadedMcpStatuses?: IConversationMcpStatus[];
  selectedSkillNames?: string[];
  selectedMcpServerIds?: string[];
  onSelectSkill?: (name: string) => void;
  onEnableSkill?: (name: string) => void;
  onSelectMcpServer?: (server: IConversationMcpStatus) => void;
  onViewPlan?: () => void;
  onRequestReview?: () => void;
  onSaveSkill?: () => void;
  onViewContextWindow?: (returnFocus: HTMLButtonElement | null) => void;
  planEnabled?: boolean;
  reviewInFlight?: boolean;
  reviewDisabled?: boolean;
  saveAsSkillDisabled?: boolean;
  saveAsSkillInFlight?: boolean;
  reviewLabelMode?: 'findings' | 'manual';
  triggerTestId?: string;
  showUnavailableConversationActions?: boolean;
}

const MenuItem: React.FC<{
  icon: React.ReactNode;
  label: React.ReactNode;
  description?: React.ReactNode;
  suffix?: React.ReactNode;
  onClick?: () => void;
  checked?: boolean;
  disabled?: boolean;
  busy?: boolean;
  keepDisabledFocusable?: boolean;
}> = ({
  icon,
  label,
  description,
  suffix,
  onClick,
  checked,
  disabled = false,
  busy,
  keepDisabledFocusable = false,
}) => {
  const menuItem = (
    <button
      type='button'
      role={checked === undefined ? 'menuitem' : 'menuitemcheckbox'}
      aria-label={typeof label === 'string' ? label : undefined}
      aria-checked={checked}
      aria-disabled={disabled || undefined}
      aria-busy={busy || undefined}
      tabIndex={onClick && (!disabled || keepDisabledFocusable) ? 0 : -1}
      disabled={disabled && !keepDisabledFocusable}
      className={`composer-control-menu__row box-border flex w-full items-center gap-10px border-0 bg-transparent px-12px py-9px text-left rounded-8px transition-colors text-14px select-none ${disabled ? 'cursor-not-allowed text-t-secondary' : 'cursor-pointer text-t-primary hover:bg-fill-2'}`}
      onClick={disabled ? undefined : onClick}
    >
      <span className='flex-shrink-0 inline-flex items-center justify-center color-#86909c w-18px leading-none'>
        {icon}
      </span>
      <span className='min-w-0 flex-1'>
        <span className='block leading-none'>{label}</span>
      </span>
      {suffix}
    </button>
  );

  if (!description) return menuItem;

  return (
    <Tooltip
      content={<span className='block max-w-220px whitespace-normal break-words'>{description}</span>}
      mini
      position='right'
    >
      <span className='block'>{menuItem}</span>
    </Tooltip>
  );
};

const buildLoadedMcpStatuses = (
  statuses?: IConversationMcpStatus[],
  legacyNames?: string[]
): IConversationMcpStatus[] => {
  if (Array.isArray(statuses) && statuses.length > 0) {
    return statuses;
  }

  return (legacyNames ?? []).map((name) => ({
    id: name,
    name,
    status: 'loaded',
  }));
};

const FileAttachButton: React.FC<FileAttachButtonProps> = ({
  openFileSelector,
  onSelectProjectFiles,
  onLocalFilesAdded,
  onLocalFilesSelected,
  loadedSkills,
  loadedMcpStatuses,
  selectedSkillNames = [],
  selectedMcpServerIds = [],
  onSelectSkill,
  onEnableSkill,
  onSelectMcpServer,
  onViewPlan,
  onRequestReview,
  onSaveSkill,
  onViewContextWindow,
  planEnabled,
  reviewInFlight = false,
  reviewDisabled = false,
  saveAsSkillDisabled = false,
  saveAsSkillInFlight = false,
  reviewLabelMode = 'findings',
  triggerTestId = 'conversation-attach-folder-btn',
  showUnavailableConversationActions = false,
}) => {
  const conversationContext = useConversationContextSafe();
  const { t } = useTranslation();
  const navigate = useNavigate();
  const fileInputRef = useRef<HTMLInputElement>(null);
  const triggerButtonRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const keyboardOpen = useRef<'first' | 'last' | null>(null);
  const [uploading, setUploading] = useState(false);
  const [open, setOpen] = useState(false);
  const scopeId = useId();
  const menuId = `${scopeId}-menu`;
  const bindMenu = useCallback(
    (node: HTMLDivElement | null) => {
      menuRef.current = node;
      if (!node || !open || !keyboardOpen.current) return;
      const items = getMenuItems(node);
      const target = keyboardOpen.current === 'last' ? items.at(-1) : items[0];
      keyboardOpen.current = null;
      target?.focus();
    },
    [open]
  );
  const [accountScope, setAccountScope] = useState(getRendererAccountScopeToken);
  const hostFilesScope = useMemo(
    () => ({ account: accountScope, conversationId: conversationContext?.conversation_id }),
    [accountScope, conversationContext?.conversation_id]
  );
  useEffect(
    () =>
      registerRendererAccountReset(`file-attach:${scopeId}`, () => {
        setAccountScope(getRendererAccountScopeToken());
        setOpen(false);
      }),
    [scopeId]
  );
  useEffect(() => {
    setOpen(false);
  }, [hostFilesScope]);

  const skillNames = loadedSkills ?? conversationContext?.loadedSkills ?? [];
  const mcpStatuses = buildLoadedMcpStatuses(
    loadedMcpStatuses ?? conversationContext?.loadedMcpStatuses,
    conversationContext?.loadedMcpServers
  );
  const handleSkillClick = useCallback(
    (name: string) => {
      setOpen(false);
      if (onSelectSkill) {
        onSelectSkill(name);
        return;
      }
      emitter.emit('sendbox.fill', `/${name} `);
    },
    [onSelectSkill]
  );

  const handleMcpClick = useCallback(
    (server: IConversationMcpStatus) => {
      if (server.status !== 'loaded' || !onSelectMcpServer) return;
      setOpen(false);
      onSelectMcpServer(server);
    },
    [onSelectMcpServer]
  );

  const handleOpenMcpSettings = useCallback(() => {
    setOpen(false);
    void navigate('/settings/tools');
  }, [navigate]);

  const handleLocalFileChange = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      const fileList = e.target.files;
      if (!fileList || fileList.length === 0 || (!onLocalFilesAdded && !onLocalFilesSelected)) return;
      if (onLocalFilesSelected) {
        setUploading(true);
        try {
          await onLocalFilesSelected(Array.from(fileList));
        } catch {
          Message.error(t('common.fileAttach.failed'));
        } finally {
          setUploading(false);
        }
        e.target.value = '';
        return;
      }
      setUploading(true);
      try {
        const processed = await FileService.processDroppedFiles(fileList, conversationContext?.conversation_id);
        if (processed.length > 0) onLocalFilesAdded(processed);
      } catch {
        Message.error(t('common.fileAttach.failed'));
      } finally {
        setUploading(false);
      }
      e.target.value = '';
    },
    [conversationContext?.conversation_id, onLocalFilesAdded, onLocalFilesSelected, t]
  );

  const isDesktop = isElectronDesktop();
  const hasSkills = skillNames.length > 0 || Boolean(onEnableSkill);
  const hasMcpServers = mcpStatuses.length > 0;
  const plusIcon = <ControlPlus size={16} strokeWidth={2} aria-hidden='true' />;

  const cardStyle: React.CSSProperties = {
    padding: '6px 0',
    minWidth: 208,
    zIndex: 1050,
  };

  const closeAndRun = (action?: () => void) => {
    setOpen(false);
    action?.();
  };

  const closeAndRestoreFocus = () => {
    setOpen(false);
    queueMicrotask(() => triggerButtonRef.current?.focus());
  };

  const menu = (
    <div
      ref={bindMenu}
      id={menuId}
      className='app-overlay-menu composer-control-menu conversation-control-menu'
      style={{ ...cardStyle, minWidth: 'min(224px, calc(100vw - 24px))', width: 'min(224px, calc(100vw - 24px))' }}
      role='menu'
      aria-label={t('conversation.attachMenu.addToMessage')}
      onClick={(event) => event.stopPropagation()}
      onKeyDown={(event) => {
        if (handleMenuNavigation(event)) return;
        if (event.target instanceof Element && event.target.closest('[role="menu"]') !== event.currentTarget) return;
        if (event.key !== 'Escape') return;
        event.preventDefault();
        event.stopPropagation();
        closeAndRestoreFocus();
      }}
    >
      {hasMcpServers || hasSkills ? (
        <>
          <ComposerCapabilityPicker
            skillNames={skillNames}
            mcpStatuses={mcpStatuses}
            selectedSkillNames={selectedSkillNames}
            selectedMcpServerIds={selectedMcpServerIds}
            onSelectSkill={handleSkillClick}
            onEnableSkill={onEnableSkill}
            onSelectMcpServer={onSelectMcpServer ? handleMcpClick : undefined}
            onOpenMcpSettings={handleOpenMcpSettings}
            onRequestClose={closeAndRestoreFocus}
            menuOpen={open}
          />
          <div className='mx-12px my-4px h-1px bg-[var(--color-border-1)]' />
        </>
      ) : null}
      <div className='px-6px'>
        <MenuItem
          icon={<Paperclip theme='outline' size={15} />}
          label={t('conversation.attachMenu.addFile')}
          description={t('conversation.attachMenu.addFileDescription')}
          onClick={() =>
            closeAndRun(() => {
              if (!isDesktop && (onLocalFilesSelected || onLocalFilesAdded)) {
                fileInputRef.current?.click();
              } else {
                openFileSelector();
              }
            })
          }
        />
        {onSelectProjectFiles && (
          <MenuItem
            icon={<FolderOpen theme='outline' size={15} />}
            label={t('conversation.hostFiles.projectFiles')}
            description={t('conversation.attachMenu.yourFilesDescription')}
            onClick={() => closeAndRun(onSelectProjectFiles)}
          />
        )}
        {onLocalFilesAdded && (
          <ComposerHostFiles
            key={JSON.stringify(hostFilesScope)}
            menuOpen={open}
            onReference={(file) => {
              if (hostFilesScope.account === getRendererAccountScopeToken()) onLocalFilesAdded([file]);
            }}
          />
        )}
      </div>
      {onViewPlan || showUnavailableConversationActions ? (
        <>
          <div className='mx-12px my-4px h-1px bg-[var(--color-border-1)]' />
          <div className='px-6px'>
            <MenuItem
              icon={<ListCheckbox theme='outline' size={15} />}
              label={t('conversation.attachMenu.viewPlan')}
              description={t('conversation.attachMenu.viewPlanDescription')}
              checked={planEnabled}
              suffix={planEnabled ? <Check theme='outline' size={15} className='text-primary' /> : undefined}
              onClick={() => closeAndRun(onViewPlan)}
              disabled={!onViewPlan}
            />
          </div>
        </>
      ) : null}
      {onRequestReview || showUnavailableConversationActions ? (
        <>
          <div className='mx-12px my-4px h-1px bg-[var(--color-border-1)]' />
          <div className='px-6px'>
            <MenuItem
              icon={<Shield theme='outline' size={15} />}
              label={t(
                reviewInFlight
                  ? 'conversation.attachMenu.reviewing'
                  : reviewLabelMode === 'manual'
                    ? 'conversation.attachMenu.manualReview'
                    : 'conversation.attachMenu.reviewFindings'
              )}
              description={t(
                reviewInFlight
                  ? 'conversation.attachMenu.reviewingDescription'
                  : reviewDisabled
                    ? 'conversation.attachMenu.reviewUnavailableDuringTask'
                    : 'conversation.attachMenu.manualReviewDescription'
              )}
              onClick={() => closeAndRun(onRequestReview)}
              disabled={!onRequestReview || reviewInFlight || reviewDisabled}
            />
          </div>
        </>
      ) : null}
      {onSaveSkill || showUnavailableConversationActions ? (
        <>
          <div className='mx-12px my-4px h-1px bg-[var(--color-border-1)]' />
          <div className='px-6px'>
            <MenuItem
              icon={
                saveAsSkillInFlight ? (
                  <Loader2
                    size={16}
                    strokeWidth={2}
                    aria-hidden='true'
                    className='conversation-control-menu__spinner'
                  />
                ) : (
                  <BookMarked size={16} strokeWidth={2} aria-hidden='true' />
                )
              }
              label={t(
                saveAsSkillInFlight
                  ? 'conversation.attachMenu.saveAsSkillTaskLabel'
                  : 'conversation.attachMenu.saveAsSkill'
              )}
              description={t(
                saveAsSkillInFlight
                  ? 'conversation.attachMenu.saveAsSkillTaskRunning'
                  : saveAsSkillDisabled
                    ? 'conversation.attachMenu.saveAsSkillBusyDescription'
                    : 'conversation.attachMenu.saveAsSkillDescription'
              )}
              onClick={() => closeAndRun(onSaveSkill)}
              disabled={!onSaveSkill || saveAsSkillDisabled}
              busy={saveAsSkillInFlight}
              keepDisabledFocusable
            />
          </div>
        </>
      ) : null}
      {onViewContextWindow ? (
        <>
          <div className='mx-12px my-4px h-1px bg-[var(--color-border-1)]' />
          <div className='px-6px'>
            <MenuItem
              icon={<ChartNoAxesCombined size={16} strokeWidth={2} aria-hidden='true' />}
              label={t('conversation.contextUsage.title')}
              onClick={() => closeAndRun(() => onViewContextWindow(triggerButtonRef.current))}
            />
          </div>
        </>
      ) : null}
    </div>
  );

  return (
    <>
      {/* Cache ordinary closes, but dispose old portals when the account or conversation changes. */}
      <Trigger
        key={JSON.stringify(hostFilesScope)}
        popup={() => menu}
        trigger='click'
        position='tl'
        popupVisible={open}
        onVisibleChange={setOpen}
        clickToClose
        popupAlign={{ bottom: 8 }}
        unmountOnExit={false}
      >
        <Button
          ref={triggerButtonRef}
          type='secondary'
          shape='circle'
          className='composer-icon-control conversation-control-trigger'
          icon={plusIcon}
          loading={uploading}
          disabled={uploading}
          data-testid={triggerTestId}
          aria-label={t('conversation.attachMenu.addToMessage')}
          aria-haspopup='menu'
          aria-expanded={open}
          aria-controls={open ? menuId : undefined}
          onKeyDown={(event) => {
            if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
            if (!['ArrowDown', 'ArrowUp', 'Enter', ' '].includes(event.key)) return;
            event.preventDefault();
            event.stopPropagation();
            keyboardOpen.current = event.key === 'ArrowUp' ? 'last' : 'first';
            if (open) {
              const items = getMenuItems(menuRef.current);
              (keyboardOpen.current === 'last' ? items.at(-1) : items[0])?.focus();
              keyboardOpen.current = null;
            } else setOpen(true);
          }}
        />
      </Trigger>
      <input
        ref={fileInputRef}
        type='file'
        multiple
        style={{ display: 'none' }}
        onChange={handleLocalFileChange}
        data-testid='conversation-file-upload-input'
      />
    </>
  );
};

export default FileAttachButton;

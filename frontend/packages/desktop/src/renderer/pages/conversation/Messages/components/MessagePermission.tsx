/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { IMessagePermission } from '@/common/chat/chatLib';
import { ipcBridge } from '@/common';
import { Message } from '@arco-design/web-react';
import React, { useState } from 'react';
import { useTranslation } from 'react-i18next';
import XSciencePermissionIcon from '@/renderer/components/xScience/runtime/XSciencePermissionIcon';
import '@/renderer/components/xScience/runtime/XScienceApprovalCard.css';

interface MessagePermissionProps {
  message: IMessagePermission;
}

export type ConversationApprovalOption = {
  label: string;
  value: string;
  alwaysAllow?: boolean;
};

type ConversationApprovalCardProps = {
  title: string;
  description?: string;
  command?: string;
  options: ConversationApprovalOption[];
  cardTestId: string;
  optionTestIdPrefix: string;
  confirmTestId: string;
  onConfirm: (option: ConversationApprovalOption) => Promise<void>;
};

export const ConversationApprovalCard: React.FC<ConversationApprovalCardProps> = React.memo(
  ({ title, description, command, options, cardTestId, optionTestIdPrefix, confirmTestId, onConfirm }) => {
    const { t } = useTranslation();
    const [selected, setSelected] = useState<string | null>(null);
    const [isResponding, setIsResponding] = useState(false);
    const [hasResponded, setHasResponded] = useState(false);

    const handleConfirm = async () => {
      if (hasResponded || !selected) return;
      const selectedOption = options.find((option) => option.value === selected);
      if (!selectedOption) return;

      setIsResponding(true);
      try {
        await onConfirm(selectedOption);
        setHasResponded(true);
      } catch (error) {
        console.warn('[ConversationApprovalCard] Failed to confirm permission', {
          errorName: error instanceof Error ? error.name : typeof error,
        });
        Message.error(t('messages.permissionResponseFailed'));
      } finally {
        setIsResponding(false);
      }
    };

    return (
      <section
        className='x-science-approval-card x-science-approval-card--compact x-science-conversation-approval-card mb-8px'
        aria-label={t('messages.permissionRequest')}
        data-testid={cardTestId}
      >
        <div className='x-science-approval-card__header'>
          <div className='x-science-approval-card__heading'>
            <div className='flex items-center gap-8px x-science-approval-card__title'>
              <XSciencePermissionIcon variant='request' size={20} />
              <span>{title}</span>
            </div>
          </div>
        </div>
        {command || (description && description !== title) ? (
          <details className='x-science-approval-card__details'>
            <summary>{t('common.more')}</summary>
            <div className='x-science-approval-card__details-content'>
              {command ? (
                <div>
                  <div className='x-science-approval-card__code-label'>{t('messages.command')}</div>
                  <code className='x-science-conversation-approval-card__command'>{command}</code>
                </div>
              ) : null}
              {description && description !== title ? (
                <p className='x-science-approval-card__description'>{description}</p>
              ) : null}
            </div>
          </details>
        ) : null}
        {!hasResponded ? (
          <>
            <div
              className='x-science-conversation-approval-card__options'
              role='radiogroup'
              aria-label={t('messages.chooseAction')}
            >
              {options.length > 0 ? (
                options.map((option, index) => (
                  <button
                    type='button'
                    role='radio'
                    aria-checked={selected === option.value}
                    key={option.value || `option_${index}`}
                    data-testid={`${optionTestIdPrefix}-${option.value || `option_${index}`}`}
                    className={`x-science-conversation-approval-card__option${selected === option.value ? ' is-selected' : ''}`}
                    onClick={() => setSelected(option.value)}
                  >
                    {option.label}
                  </button>
                ))
              ) : (
                <span className='x-science-approval-card__hint'>{t('messages.noOptionsAvailable')}</span>
              )}
            </div>
            <div className='x-science-approval-card__footer'>
              <button
                type='button'
                className='x-science-approval-card__allow x-science-approval-card__allow--single'
                disabled={!selected || isResponding}
                onClick={() => void handleConfirm()}
                data-testid={confirmTestId}
              >
                {isResponding ? t('messages.processing') : t('messages.confirm')}
              </button>
            </div>
          </>
        ) : null}
        {hasResponded ? (
          <div className='x-science-conversation-approval-card__success'>
            ✓ {t('messages.responseSentSuccessfully')}
          </div>
        ) : null}
      </section>
    );
  }
);

ConversationApprovalCard.displayName = 'ConversationApprovalCard';

const MessagePermission: React.FC<MessagePermissionProps> = React.memo(({ message }) => {
  const { t, i18n } = useTranslation();
  const { options = [], description, title, call_id, command_type } = message.content || {};
  const displayTitle = title || description || t('messages.permissionRequest');
  const managedLabels: Record<string, { value: string; key: string }> = {
    'Allow once': { value: 'proceed_once', key: 'agentMode.sessionGrants.allowOnce' },
    'Allow this session': { value: 'proceed_conversation', key: 'agentMode.sessionGrants.allow' },
    'Always allow': { value: 'proceed_always', key: 'agentMode.sessionGrants.alwaysAllow' },
    Deny: { value: 'deny', key: 'agentMode.sessionGrants.deny' },
  };
  const approvalOptions: ConversationApprovalOption[] = options.map((option) => {
    const managed = managedLabels[option.label];
    const labelKey = managed?.value === String(option.value) ? managed.key : option.label;
    return {
      label: i18n.exists(labelKey) ? t(labelKey, option.params) : option.label,
      value: String(option.value),
      alwaysAllow: String(option.value) === 'proceed_always',
    };
  });

  return (
    <ConversationApprovalCard
      title={displayTitle}
      description={description}
      command={command_type}
      options={approvalOptions}
      cardTestId='message-permission-card'
      optionTestIdPrefix='message-permission-option'
      confirmTestId='message-permission-confirm'
      onConfirm={(option) =>
        ipcBridge.conversation.confirmation.confirm.invoke({
          conversation_id: message.conversation_id,
          call_id,
          msg_id: message.msg_id || '',
          data: { value: option.value },
          always_allow: option.alwaysAllow ?? false,
        })
      }
    />
  );
});

export default MessagePermission;

import React, { useEffect, useRef, useState } from 'react';
import { Message, Tooltip } from '@arco-design/web-react';
import { IconBranch, IconLoading } from '@arco-design/web-react/icon';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import { emitter } from '@/renderer/utils/emitter';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { createSynonBiomedBranchMutationId } from '@/renderer/services/synonBiomedConversationBranches';
import { branchSynonBiomedReply } from '@/renderer/services/synonBiomedReplyBranch';
import { useConversationRuntimeView } from '../../runtime/useConversationRuntimeView';
import styles from './MessageRoundFooter.module.css';

type Props = {
  conversationId: string;
  throughAttempt: number;
  sourceBranchId?: string | null;
};

export default function MessageReplyBranchButton(props: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const runtime = useConversationRuntimeView(props.conversationId);
  const [pending, setPending] = useState(false);
  const request = useRef<{ key: string; id: string; pending: boolean } | null>(null);
  const key = `${props.conversationId}:${props.sourceBranchId ?? ''}:${props.throughAttempt}`;
  const current = useRef(key);
  current.current = key;
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const disabled = !runtime.hydrated || runtime.isProcessing || pending;
  const label = t('messages.roundSummary.branchReply');
  const branch = async () => {
    if (disabled || request.current?.pending) return;
    const entry =
      request.current?.key === key ? request.current : { key, id: createSynonBiomedBranchMutationId(), pending: false };
    request.current = entry;
    entry.pending = true;
    setPending(true);
    const account = getRendererAccountScopeToken();
    try {
      const id = await branchSynonBiomedReply({ ...props, intentId: entry.id });
      if (!mounted.current || current.current !== key || getRendererAccountScopeToken() !== account) return;
      emitter.emit('chat.history.refresh');
      void navigate(`/conversation/${encodeURIComponent(id)}`);
    } catch {
      if (mounted.current && current.current === key && getRendererAccountScopeToken() === account)
        Message.error(t('messages.roundSummary.branchFailed'));
    } finally {
      entry.pending = false;
      if (mounted.current) setPending(false);
    }
  };
  return (
    <Tooltip content={runtime.isProcessing ? t('messages.roundSummary.branchBusy') : label}>
      <button
        type='button'
        aria-label={label}
        aria-busy={pending}
        disabled={disabled}
        onClick={() => void branch()}
        className={styles.iconButton}
      >
        {pending ? <IconLoading className={styles.icon} spin /> : <IconBranch className={styles.icon} />}
      </button>
    </Tooltip>
  );
}

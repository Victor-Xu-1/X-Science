import { Button, Message } from '@arco-design/web-react';
import { Edit } from '@icon-park/react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useConversationRuntimeView } from '@/renderer/pages/conversation/runtime/useConversationRuntimeView';
import { ensureConversationRuntime } from '@/renderer/pages/conversation/utils/ensureConversationRuntime';
import {
  forkXScienceAtAskUserAnswer,
  loadXScienceRuntimeSnapshot,
  type XScienceAskUserResponse,
} from '@/renderer/services/xScienceRuntimeOperations';
import { redactErrorText } from '@/renderer/pages/conversation/platforms/acp/errorDiagnostics';
import {
  X_SCIENCE_SESSION_WORKFLOW_CAPABILITIES,
  createXScienceBranchMutationId,
  selectXScienceBranch,
} from '@/renderer/services/xScienceConversationBranches';
import AskUserCard from './AskUserCard';
import { normalizeXScienceAskUserHistory, normalizeXScienceAskUserToolInput } from './askUserHistoryModel';
import './AskUserHistoryCard.css';

type AskUserHistoryCardProps = {
  conversationId: string;
  sourceBranchId: string | null;
  toolUseId: string;
  input: unknown;
  output: unknown;
};

const AskUserHistoryCard: React.FC<AskUserHistoryCardProps> = ({
  conversationId,
  sourceBranchId,
  toolUseId,
  input,
  output,
}) => {
  const { t } = useTranslation();
  const runtime = useConversationRuntimeView(conversationId);
  const canonicalSourceBranch = sourceBranchId && /^br_[0-9a-f]{8}$/.test(sourceBranchId) ? sourceBranchId : null;
  const authorityKey = `${conversationId}\u0000${toolUseId}\u0000${canonicalSourceBranch ?? ''}`;
  const authorityRef = useRef(authorityKey);
  authorityRef.current = authorityKey;
  const mountedRef = useRef(true);
  const mutationRef = useRef<{ authority: string; response: string; id: string } | null>(null);
  const [editingOwner, setEditingOwner] = useState<string | null>(null);
  const [busyOwner, setBusyOwner] = useState<string | null>(null);
  const editing = editingOwner === authorityKey;
  const busy = busyOwner === authorityKey;
  const [messageApi, messageContext] = Message.useMessage();
  const question = useMemo(() => normalizeXScienceAskUserToolInput(input), [input]);
  const history = useMemo(
    () => normalizeXScienceAskUserHistory(question?.question ?? '', output),
    [output, question?.question]
  );

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    setEditingOwner(null);
    mutationRef.current = null;
  }, [authorityKey]);

  if (!question || history.status === 'pending') return null;

  const changeAnswer = async (response: XScienceAskUserResponse) => {
    if (response.action === 'cancel') {
      mutationRef.current = null;
      setEditingOwner((current) => (current === authorityKey ? null : current));
      return;
    }
    if (!canonicalSourceBranch) return;
    const requestAuthority = authorityKey;
    const responseKey = JSON.stringify(response);
    const existingMutation = mutationRef.current;
    const clientMutationId =
      existingMutation?.authority === requestAuthority && existingMutation.response === responseKey
        ? existingMutation.id
        : createXScienceBranchMutationId();
    mutationRef.current = { authority: requestAuthority, response: responseKey, id: clientMutationId };
    setBusyOwner(requestAuthority);
    try {
      const snapshot = await loadXScienceRuntimeSnapshot(conversationId);
      if (!mountedRef.current || authorityRef.current !== requestAuthority) return;
      runtime.markSendStarted();
      const result = await forkXScienceAtAskUserAnswer({
        rootFrameId: snapshot.rootFrameId,
        sourceFrameId: conversationId,
        sourceBranchId: canonicalSourceBranch,
        toolUseId,
        response,
        clientMutationId,
      });
      const ensured = await ensureConversationRuntime(conversationId);
      // The runtime gate is conversation-scoped. Settle it before selecting
      // the new branch can replace this historical card and unmount it.
      runtime.markSendAccepted(ensured.runtime.turn_id ?? conversationId, ensured.runtime);
      if (!mountedRef.current || authorityRef.current !== requestAuthority) return;
      selectXScienceBranch(conversationId, result.branchId);
      mutationRef.current = null;
      setEditingOwner(null);
      messageApi.success(t('conversation.xscienceRuntime.askUserHistory.branchCreated'));
    } catch (reason) {
      runtime.markSendFailed('ask_user_branch_create_failed');
      if (!mountedRef.current || authorityRef.current !== requestAuthority) return;
      console.error(
        'Failed to change historical answer:',
        redactErrorText(reason instanceof Error ? reason.message : String(reason || 'unknown_error'))
      );
      messageApi.error(t('conversation.xscienceRuntime.askUserHistory.changeFailed'));
    } finally {
      if (mountedRef.current) {
        setBusyOwner((current) => (current === requestAuthority ? null : current));
      }
    }
  };

  if (editing) {
    return (
      <div className='w-full min-w-0' data-testid='x-science-ask-user-history-editing'>
        {messageContext}
        <div className='mb-6px text-11px text-t-tertiary'>{t('conversation.xscienceRuntime.askUserHistory.editHint')}</div>
        <AskUserCard
          question={question}
          busy={busy}
          onBack={() => setEditingOwner((current) => (current === authorityKey ? null : current))}
          onResolve={(response) => changeAnswer(response)}
        />
      </div>
    );
  }

  const answer =
    history.status === 'answered'
      ? history.answer
      : history.status === 'deferred'
        ? t('conversation.xscienceRuntime.askUserHistory.deferred')
        : history.status === 'discussed'
          ? t('conversation.xscienceRuntime.askUserHistory.discussed')
          : history.status === 'unavailable'
            ? t('conversation.xscienceRuntime.askUserHistory.unavailable')
            : t('conversation.xscienceRuntime.askUserHistory.skipped');
  const changeable =
    history.status !== 'discussed' &&
    canonicalSourceBranch !== null &&
    X_SCIENCE_SESSION_WORKFLOW_CAPABILITIES.askUserAnswerFork.state === 'supported';

  return (
    <section
      data-testid='x-science-ask-user-history'
      data-status={history.status}
      aria-label={t('conversation.xscienceRuntime.askUserHistory.label')}
      className='x-science-ask-user-history group'
    >
      {messageContext}
      <div className='x-science-ask-user-history__layout'>
        <div className='x-science-ask-user-history__content'>
          {question.header ? <div className='x-science-ask-user-history__eyebrow'>{question.header}</div> : null}
          <h3 className='x-science-ask-user-history__question'>{question.question}</h3>
          <div className='x-science-ask-user-history__answer' data-testid='x-science-ask-user-history-answer'>
            <span className='x-science-ask-user-history__status-dot' aria-hidden='true' />
            <span>{answer}</span>
          </div>
        </div>
        {changeable ? (
          <Button
            size='mini'
            type='text'
            className='x-science-ask-user-history__edit'
            disabled={runtime.isProcessing}
            aria-label={t('conversation.xscienceRuntime.askUserHistory.editAnswer')}
            icon={<Edit theme='outline' size={14} />}
            onClick={() => setEditingOwner(authorityKey)}
          />
        ) : null}
      </div>
    </section>
  );
};

export default AskUserHistoryCard;

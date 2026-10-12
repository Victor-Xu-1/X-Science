import { IconRight } from '@arco-design/web-react/icon';
import React, { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import type { IMessagePlan } from '@/common/chat/chatLib';
import type { XSciencePlanDocument, XSciencePlanStep } from './runtimeOperationsModel';
import XSciencePlanTree from './XSciencePlanTree';
import { useConversationDisclosure } from '@/renderer/services/runtime/conversationDisclosureStore';
import { getSelectedXScienceBranch } from '@/renderer/services/xScienceConversationBranches';
import { toolPublicDetailText } from '@/renderer/services/i18n/toolPublicDetailLocale';
import './XSciencePlanReviewContent.css';

const XScienceTimelinePlan: React.FC<{ message: IMessagePlan }> = ({ message }) => {
  const { i18n } = useTranslation();
  const chinese = i18n.resolvedLanguage?.toLowerCase().startsWith('zh') ?? false;
  const { expanded, toggle } = useConversationDisclosure(
    {
      conversationId: message.conversation_id,
      branchId: getSelectedXScienceBranch(message.conversation_id) ?? undefined,
      operationId: message.content.session_id || message.id,
      path: 'timeline-plan',
    },
    { defaultExpanded: true }
  );
  const document = useMemo(
    (): XSciencePlanDocument => ({
      version: 1,
      taskSummary: toolPublicDetailText(chinese, 'planTitle'),
      phases: [
        {
          id: `timeline-plan-${message.content.session_id || message.id}`,
          name: toolPublicDetailText(chinese, 'planSteps'),
          delegations: [],
          steps: message.content.entries.map((entry, index): XSciencePlanStep => ({
            id: `timeline-plan-step-${message.content.session_id || message.id}-${index}`,
            title: entry.content,
            description: '',
            status:
              entry.status === 'completed' ? 'completed' : entry.status === 'in_progress' ? 'in_progress' : 'pending',
          })),
        },
      ],
      feasibility: null,
    }),
    [chinese, message]
  );

  return (
    <section className='x-science-plan-review x-science-plan-review--timeline' data-testid='timeline-plan'>
      <button
        type='button'
        className='x-science-plan-review__timeline-trigger'
        aria-expanded={expanded}
        onClick={toggle}
      >
        <IconRight
          aria-hidden='true'
          className={`x-science-plan-review__timeline-chevron${
            expanded ? ' x-science-plan-review__timeline-chevron--expanded' : ''
          }`}
        />
        <span>{document.taskSummary}</span>
        <span className='x-science-plan-review__timeline-count'>
          {toolPublicDetailText(chinese, 'stepCount', {
            count: message.content.entries.length,
          })}
        </span>
      </button>
      {expanded ? <XSciencePlanTree document={document} /> : null}
    </section>
  );
};

export default XScienceTimelinePlan;

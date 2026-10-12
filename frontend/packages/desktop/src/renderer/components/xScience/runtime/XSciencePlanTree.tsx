import React, { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import XScienceAvatar from '@/renderer/components/xScience/XScienceAvatar';
import type { XSciencePlanDocument } from './runtimeOperationsModel';
import XSciencePlanStepList from './XSciencePlanStepList';
import './XSciencePlanReviewContent.css';

const XSciencePlanTree: React.FC<{
  document: XSciencePlanDocument;
}> = ({ document }) => {
  const { t } = useTranslation();
  const phases = useMemo(
    () =>
      document.phases.map((phase) => {
        const delegatedIds = new Set(
          phase.delegations.flatMap((delegation) => delegation.steps.map((step) => step.id))
        );
        return {
          ...phase,
          steps: phase.steps.filter((step) => !delegatedIds.has(step.id)),
        };
      }),
    [document]
  );
  return (
    <div className='x-science-plan-review__phases'>
      {phases.map((phase, phaseIndex) => (
        <section key={phase.id} className='x-science-plan-review__phase' data-testid='x-science-plan-phase'>
          <div className='x-science-plan-review__phase-title'>
            {phaseIndex + 1}. {phase.name}
          </div>
          {phase.steps.length > 0 ? <XSciencePlanStepList steps={phase.steps} /> : null}
          {phase.delegations.length > 0 ? (
            <div className='x-science-plan-review__delegations'>
              {phase.delegations.map((delegation) => (
                <div
                  key={delegation.id}
                  className='x-science-plan-review__delegation'
                  data-testid='x-science-plan-delegation'
                >
                  <div className='x-science-plan-review__delegation-heading'>
                    <XScienceAvatar size={14} className='shrink-0' />
                    <span className='truncate'>{delegation.name}</span>
                    {delegation.agentName ? (
                      <span className='x-science-plan-review__agent'>{delegation.agentName}</span>
                    ) : null}
                  </div>
                  <XSciencePlanStepList steps={delegation.steps} />
                </div>
              ))}
            </div>
          ) : phase.steps.length === 0 ? (
            <div className='x-science-plan-review__empty-steps'>
              {t('conversation.xscienceRuntime.runtimeOperations.noExecutionSteps')}
            </div>
          ) : null}
        </section>
      ))}
    </div>
  );
};

export default XSciencePlanTree;

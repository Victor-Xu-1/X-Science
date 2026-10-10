import React from 'react';
import { useTranslation } from 'react-i18next';
import type { StructureActionReason } from './structureActionAvailability';

export interface StructureActionRequirement {
  label: string;
  reason?: StructureActionReason;
}

/** Present each missing prerequisite once; the caller retains execution authority. */
export function StructureActionRequirements({
  actions,
  onClose,
}: {
  actions: readonly StructureActionRequirement[];
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const groups = new Map<StructureActionReason, string[]>();
  for (const action of actions) {
    if (!action.reason) continue;
    const labels = groups.get(action.reason) ?? [];
    labels.push(action.label);
    groups.set(action.reason, labels);
  }
  return (
    <div className='synon-biomed-molstar__action-requirements'>
      <header>
        <h3>{t('preview.scientific.structure.quickActions.availability.title')}</h3>
        <button type='button' className='synon-biomed-molstar__quick-chip' onClick={onClose}>
          {t('common.close')}
        </button>
      </header>
      {groups.size === 0 ? (
        <p>{t('preview.scientific.structure.quickActions.availability.inputsReady')}</p>
      ) : (
        <dl>
          {[...groups].map(([reason, labels]) => (
            <div key={reason}>
              <dt>{labels.join(' · ')}</dt>
              <dd>{t(`preview.scientific.structure.quickActions.availability.${reason}`)}</dd>
            </div>
          ))}
        </dl>
      )}
    </div>
  );
}

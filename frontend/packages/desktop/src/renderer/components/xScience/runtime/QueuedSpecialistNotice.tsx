import React from 'react';
import { useTranslation } from 'react-i18next';
import type { SpecialistSendAuthority } from '@/common/adapter/specialistBinding';

export default function QueuedSpecialistNotice({
  authority,
  invalid,
  onClear,
}: {
  authority?: SpecialistSendAuthority;
  invalid: boolean;
  onClear: () => void;
}) {
  const { t } = useTranslation();
  if (!authority && !invalid) return null;
  return (
    <div className='mb-6px flex items-center gap-8px text-11px text-t-secondary' role='status'>
      <span>
        {t('conversation.xscienceRuntime.sessionOptions.specialistState.frozen', {
          name: authority?.targetAgent ?? '',
        })}
      </span>
      <button type='button' className='border-0 bg-transparent text-primary' onClick={onClear}>
        {t('conversation.xscienceRuntime.sessionOptions.specialistState.useCurrent')}
      </button>
    </div>
  );
}

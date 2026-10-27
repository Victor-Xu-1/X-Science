import { Select, Tooltip } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceForkedViewRecovery } from '@/renderer/services/xScienceMemory';

export function MemoryRecoveryPolicy({
  value,
  disabled,
  onChange,
}: {
  value: XScienceForkedViewRecovery;
  disabled: boolean;
  onChange: (value: XScienceForkedViewRecovery) => void;
}) {
  const { t } = useTranslation();
  return (
    <Tooltip content={t('settings.memoryRecoveryPolicyDescription')}>
      <div className='memory-manager__status-control'>
        <span className='memory-manager__status-label'>{t('settings.memoryRecoveryPolicy')}</span>
        <Select
          data-testid='memory-recovery-policy'
          aria-label={t('settings.memoryRecoveryPolicy')}
          size='small'
          style={{ width: 240, maxWidth: '100%' }}
          value={value}
          disabled={disabled}
          onChange={(next: string) => {
            if (next === 'strict' || next === 'compact-on-oversize') onChange(next);
          }}
          options={[
            { value: 'strict', label: t('settings.memoryRecoveryStrict') },
            { value: 'compact-on-oversize', label: t('settings.memoryRecoveryCompact') },
          ]}
        />
      </div>
    </Tooltip>
  );
}

import {
  findSynonBiomedExpertUsage,
  type SynonBiomedExpertUsageByName,
} from '@/renderer/services/agents/synonBiomedExpertUsage';
import React from 'react';
import { useTranslation } from 'react-i18next';

// undefined is pending enrichment; null is an observed failure. A map contains
// the actual read-only usage projection, including known zero counts.
export type ExpertUsageView = SynonBiomedExpertUsageByName | null | undefined;

export default function ExpertUsageSummary({
  profileName,
  usageByName,
}: {
  profileName: string;
  usageByName: ExpertUsageView;
}) {
  const { i18n, t } = useTranslation();
  const usage = usageByName ? findSynonBiomedExpertUsage(usageByName, profileName) : null;
  const timestamp = usage?.lastUsedAt ? new Date(usage.lastUsedAt) : null;
  const lastUsed =
    timestamp && !Number.isNaN(timestamp.getTime())
      ? new Intl.DateTimeFormat(i18n.language || undefined, {
          month: '2-digit',
          day: '2-digit',
          hour: '2-digit',
          minute: '2-digit',
          hour12: false,
        }).format(timestamp)
      : '';
  return (
    <span
      className='expert-card__usage settings-library-card__meta hidden shrink-0 items-center sm:flex'
      aria-busy={usageByName === undefined}
    >
      {usageByName == null ? (
        <span>{usageByName === undefined ? t('common.loading') : t('settings.expertsSettings.usage.unavailable')}</span>
      ) : (
        <>
          <span data-testid={'expert-usage-count-' + profileName}>
            {t('settings.expertsSettings.usage.count', { count: usage?.invocationCount ?? 0 })}
          </span>
          <span data-testid={'expert-last-used-' + profileName}>
            {lastUsed
              ? t('settings.expertsSettings.usage.lastUsed', { time: lastUsed })
              : t('settings.expertsSettings.usage.never')}
          </span>
        </>
      )}
    </span>
  );
}

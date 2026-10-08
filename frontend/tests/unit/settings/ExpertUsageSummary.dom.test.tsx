import { render, screen } from '@testing-library/react';
import React from 'react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';
import ExpertUsageSummary, {
  type ExpertUsageView,
} from '@/renderer/pages/settings/SynonBiomedExpertsSettings/ExpertUsageSummary';
import { createTestI18n } from '../i18nTestUtils';

async function view(usage: ExpertUsageView) {
  const i18n = await createTestI18n('en-US');
  return render(
    <I18nextProvider i18n={i18n}>
      <ExpertUsageSummary profileName='Expert A' usageByName={usage} />
    </I18nextProvider>
  );
}

describe('Expert usage enrichment presentation', () => {
  it('does not report a pending request as unavailable or invent a zero count', async () => {
    const { container } = await view(undefined);
    expect(container.querySelector('[aria-busy="true"]')).toBeInTheDocument();
    expect(screen.queryByText('Usage unavailable')).not.toBeInTheDocument();
    expect(screen.queryByTestId('expert-usage-count-Expert A')).not.toBeInTheDocument();
  });
  it('shows one truthful unavailable message after an observed failure', async () => {
    await view(null);
    expect(screen.getAllByText('Usage unavailable')).toHaveLength(1);
    expect(screen.queryByTestId('expert-usage-count-Expert A')).not.toBeInTheDocument();
  });
  it('preserves known zero counts and actual usage dates', async () => {
    const { unmount } = await view({});
    expect(screen.getByTestId('expert-usage-count-Expert A')).toHaveTextContent('Used 0 times');
    unmount();
    await view({ 'Expert A': { invocationCount: 4, lastUsedAt: '2026-10-07T01:02:00Z' } });
    expect(screen.getByTestId('expert-usage-count-Expert A')).toHaveTextContent('Used 4 times');
    expect(screen.getByTestId('expert-last-used-Expert A')).not.toHaveTextContent('never');
  });
});

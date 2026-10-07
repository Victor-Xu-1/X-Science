import { cleanup, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { ComputeJobRow } from '@/renderer/pages/settings/components/compute/ComputeJobs';
import type { SynonBiomedComputeJob } from '@/renderer/services/synonBiomedCompute';
import { renderWithSettingsI18n } from './settingsI18nTestUtils';

afterEach(cleanup);
const job: SynonBiomedComputeJob = {
  jobId: 'job-control',
  provider: 'ssh:fixture',
  providerLabel: 'Fixture',
  providerFamily: 'ssh',
  environment: 'remote',
  tierType: 'remote',
  projectId: 'project',
  state: 'running',
  frameId: null,
  rootFrameId: null,
  originToolUseId: null,
  externalId: 'original-job',
  externalUrl: null,
  startedAt: null,
  startedAtIso: null,
  endedAtIso: null,
  intent: null,
  hardwareDetails: null,
  supportsTail: true,
  harvest: null,
  leftOnRemote: [],
  errorKind: 'control_unreachable',
  systemHint: 'Remote control is unavailable',
};

describe('compute control status rendering', () => {
  it.each(['zh-CN', 'en-US'] as const)('renders an unknown outcome instead of a live claim in %s', async (language) => {
    await renderWithSettingsI18n(<ComputeJobRow job={job} onOpen={() => undefined} />, language);
    expect(screen.getByText(language === 'zh-CN' ? '连接不可达' : 'Control unreachable')).toBeInTheDocument();
    expect(screen.queryByText(language === 'zh-CN' ? '运行中' : 'Running')).not.toBeInTheDocument();
    expect(screen.getByText(language === 'zh-CN' ? /结果未知/ : /outcome is unknown/)).toBeInTheDocument();
    expect(screen.queryByText('control_unreachable')).not.toBeInTheDocument();
  });

  it('returns to the live state after committed reconnection', async () => {
    await renderWithSettingsI18n(
      <ComputeJobRow job={{ ...job, errorKind: null, systemHint: null }} onOpen={() => undefined} />
    );
    expect(screen.getByText('运行中')).toBeInTheDocument();
    expect(screen.queryByText(/结果未知/)).not.toBeInTheDocument();
  });
});

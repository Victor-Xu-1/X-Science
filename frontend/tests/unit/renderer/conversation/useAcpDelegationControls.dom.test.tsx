import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import MobileActionSheet from '@/renderer/components/chat/MobileActionSheet';
import { useAcpMobileActionSheetController } from '@/renderer/pages/conversation/platforms/acp/useAcpMobileActionSheetController';
import type { XScienceDelegationControl } from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';
import { renderWithI18n } from '../../i18nTestUtils';

const change = vi.fn(),
  retry = vi.fn(),
  refresh = vi.fn(),
  localChange = vi.fn();
const control = (patch: Partial<XScienceDelegationControl> = {}): XScienceDelegationControl => ({
  enabled: true,
  pending: false,
  available: true,
  hasLiveAttempts: false,
  error: null,
  retryable: false,
  change,
  retry,
  refresh,
  current: () => true,
  ...patch,
});
function Harness({ delegationControl, open = true }: { delegationControl: XScienceDelegationControl; open?: boolean }) {
  const { t, i18n } = useTranslation();
  const entries = useAcpMobileActionSheetController({
    isMobile: true,
    runtimeMode: { id: 'mode', currentValue: 'send', options: [{ value: 'send', label: 'Send' }] } as never,
    runtimeThoughtLevel: null,
    runtimeConfig: { setStatus: { state: 'idle' } } as never,
    canSwitchModel: false,
    model_info: null,
    isModelListLoading: false,
    modelListError: null,
    retryModelList: vi.fn(),
    selectModel: vi.fn(),
    xscienceSessionOptions: { delegation: false, autoReview: false, memory: true, targetAgent: 'OPERON' },
    sessionOptionsReady: false,
    setXScienceSessionOptions: localChange,
    updatePersistentSessionOption: vi.fn(),
    delegationControl,
    mobileSpecialists: [],
    mobileComputeProviders: [],
    mobileEnabledCompute: [],
    handleSessionComputeChange: vi.fn(),
    handleSheetModeChange: vi.fn(),
    handleThoughtLevelSetOption: vi.fn(),
    attachEntries: [],
    loadedSkills: [],
    loadedMcpStatuses: [],
    selectedSkillNames: [],
    selectedMcpServerIds: [],
    onSelectSkill: vi.fn(),
    onSelectMcpServer: vi.fn(),
    t,
    i18n,
  });
  return <MobileActionSheet open={open} onClose={vi.fn()} entries={entries} />;
}
async function openControls(value: XScienceDelegationControl) {
  const view = await renderWithI18n(<Harness delegationControl={value} />);
  fireEvent.click(await screen.findByTestId('mobile-action-sheet-session-options'));
  return { view, row: await screen.findByTestId('session-config-row-delegation') };
}
describe('actual mobile delegation control', () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);
  it('uses the shared current policy and submits through its owner even while unrelated options load', async () => {
    const { row } = await openControls(control());
    expect(row).toHaveAttribute('aria-checked', 'true');
    expect(row).not.toBeDisabled();
    fireEvent.click(row);
    expect(change).toHaveBeenCalledWith(false);
    expect(localChange).not.toHaveBeenCalled();
    expect(refresh).toHaveBeenCalledTimes(1);
  });
  it('does not pretend an unknown policy is off and keeps unrelated permissions available', async () => {
    const { row } = await openControls(control({ enabled: undefined, pending: true }));
    expect(row).toHaveAttribute('aria-checked', 'mixed');
    expect(row).toBeDisabled();
    fireEvent.click(row);
    expect(change).not.toHaveBeenCalled();
    expect(screen.getByTestId('mobile-action-sheet-send-mode')).not.toBeDisabled();
  });
  it('exposes explicit retry and refreshes the same owner after close and reopen', async () => {
    const value = control({ error: 'uncertain', retryable: true, hasLiveAttempts: true });
    const { view, row } = await openControls(value);
    expect(row).toBeDisabled();
    fireEvent.click(await screen.findByRole('menuitem', { name: '重试' }));
    expect(retry).toHaveBeenCalledTimes(1);
    expect(localChange).not.toHaveBeenCalled();
    view.rerender(<Harness delegationControl={value} open={false} />);
    view.rerender(<Harness delegationControl={value} open />);
    fireEvent.click(await screen.findByTestId('mobile-action-sheet-session-options'));
    await waitFor(() => expect(refresh.mock.calls.length).toBeGreaterThanOrEqual(2));
  });
});

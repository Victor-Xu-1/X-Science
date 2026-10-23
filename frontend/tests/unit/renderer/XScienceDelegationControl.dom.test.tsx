import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/services/xScienceCatalog', () => ({
  loadXScienceAssistants: vi.fn().mockResolvedValue([]),
}));
vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: vi.fn().mockResolvedValue([]),
  loadXScienceSessionComputeProviders: vi.fn().mockResolvedValue([]),
}));

const value = {
  delegation: false,
  autoReview: false,
  memory: false,
  targetAgent: 'OPERON',
  goalText: null,
  asRoutine: false,
};
const change = vi.fn();
const localChange = vi.fn();
const refresh = vi.fn();
async function open(overrides: Record<string, unknown> = {}) {
  // Structural spread deliberately works with the old menu: these RED checks
  // fail on real checked/click behavior, not a missing new module or TS prop.
  const props = {
    delegationControl: {
      enabled: true,
      pending: false,
      available: true,
      hasLiveAttempts: false,
      error: null,
      retryable: false,
      change,
      retry: vi.fn(),
      refresh,
      current: () => true,
      ...overrides,
    },
  };
  await renderWithI18n(
    <MemoryRouter>
      <XScienceSessionOptionsMenu value={value} onChange={localChange} {...props} />
    </MemoryRouter>
  );
  fireEvent.click(screen.getByRole('button', { name: '会话选项' }));
  return screen.findByTestId('session-config-row-delegation');
}

describe('authoritative delegation menu control', () => {
  beforeEach(() => vi.clearAllMocks());
  afterEach(cleanup);
  it('renders the current policy instead of a late legacy session option', async () => {
    const row = await open();
    expect(row).toHaveAttribute('aria-checked', 'true');
    expect(refresh).toHaveBeenCalled();
    expect(screen.getByRole('button', { name: '会话选项' }).querySelector('.bg-primary')).toBeNull();
  });
  it('submits only through the policy owner rather than flipping the local option', async () => {
    fireEvent.click(await open());
    await waitFor(() => expect(change).toHaveBeenCalledWith(false));
    expect(localChange).not.toHaveBeenCalled();
  });
  it('keeps an unknown policy indeterminate and disables only that control', async () => {
    const row = await open({ enabled: undefined, pending: true });
    expect(row).toHaveAttribute('aria-checked', 'mixed');
    expect(row).toBeDisabled();
    expect(screen.getByTestId('session-config-row-autoReview')).not.toBeDisabled();
    expect(screen.getByRole('button', { name: '会话选项' }).querySelector('.bg-primary')).toBeNull();
  });
  it('does not advertise an unrequested identity as a policy read in progress', async () => {
    const row = await open({ enabled: undefined, pending: false, available: false, current: () => false });
    expect(row).toHaveAttribute('aria-checked', 'mixed');
    expect(row).toBeDisabled();
    expect(row).not.toHaveAttribute('aria-busy', 'true');
    expect(row).not.toHaveAttribute('title', expect.stringMatching(/Reading|正在读取/i));
    expect(screen.getByTestId('session-config-row-autoReview')).not.toBeDisabled();
    fireEvent.click(row);
    expect(change).not.toHaveBeenCalled();
    expect(localChange).not.toHaveBeenCalled();
  });
  it('marks an explicit durable deny as customized instead of marking default allow', async () => {
    expect(await open({ enabled: false })).toHaveAttribute('aria-checked', 'false');
    expect(screen.getByRole('button', { name: '会话选项' }).querySelector('.bg-primary')).not.toBeNull();
  });
});

import React from 'react';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { createXScienceSessionDefaults } from '@/renderer/services/xScienceSessionOptions';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({ choose: vi.fn(), localChange: vi.fn(), providers: vi.fn() }));
vi.mock('@/renderer/services/xScienceCatalog', () => ({
  loadXScienceAssistants: async () => [
    { id: 'assistant-a', name: 'Alpha', agent_id: 'AGENT_A', enabled: true },
    { id: 'assistant-b', name: 'Beta', agent_id: 'AGENT_B', enabled: true },
  ],
}));
vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: mocks.providers,
  loadXScienceSessionComputeProviders: async () => [],
  setXScienceSessionComputeProvider: vi.fn(),
}));
const selected = {
  agent_name: 'ENGINE_B',
  selection_name: 'AGENT_B',
  assistant_id: 'assistant-b',
  display_name: 'Beta',
  is_default: false,
  snapshot_id: 'snapshot-b',
  revision: 1,
};
function control(pending = false) {
  return {
    applied: selected,
    desired: selected,
    choices: [
      { id: 'assistant-a', name: 'AGENT_A', label: 'Alpha' },
      { id: 'assistant-b', name: 'AGENT_B', label: 'Beta' },
    ],
    pending,
    available: true,
    unavailable: false,
    error: null,
    retryable: false,
    catalogLoading: false,
    catalogError: false,
    hasLiveAttempt: false,
    prepared: false,
    change: mocks.choose,
    refresh: vi.fn(),
    retry: vi.fn(),
    current: () => true,
    capture: () => ({}),
  };
}
async function open(pending = false, patch: Partial<ReturnType<typeof control>> = {}) {
  const props = { specialistControl: { ...control(pending), ...patch } };
  await renderWithI18n(
    <MemoryRouter>
      <XScienceSessionOptionsMenu
        {...props}
        value={createXScienceSessionDefaults('AGENT_A')}
        onChange={mocks.localChange}
      />
    </MemoryRouter>,
    'en-US'
  );
  fireEvent.click(screen.getByRole('button', { name: 'Conversation options' }));
  fireEvent.mouseEnter(screen.getByTestId('session-config-row-specialist'));
  return screen.findByRole('menuitemradio', { name: 'Select expert Beta' });
}
beforeEach(() => mocks.providers.mockResolvedValue([]));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
it('uses the specialist owner selection rather than the legacy local target', async () => {
  expect(await open()).toHaveAttribute('aria-checked', 'true');
});
it('prepares through the specialist owner without writing a local target', async () => {
  fireEvent.click(await open());
  expect(mocks.choose).toHaveBeenCalledWith('AGENT_B');
  expect(mocks.localChange).not.toHaveBeenCalled();
});
it('disables specialist changes while its command is unresolved', async () => {
  expect(await open(true)).toBeDisabled();
  expect(screen.getByRole('menuitemcheckbox', { name: 'Auto review' })).toBeEnabled();
});
it('allows explicit None or a new selection when the current logical binding is unavailable', async () => {
  await open(false, { applied: null, desired: null, unavailable: true });
  expect(screen.getByRole('menuitemradio', { name: 'Select expert None' })).toBeEnabled();
  fireEvent.click(screen.getByRole('menuitemradio', { name: 'Select expert Beta' }));
  expect(mocks.choose).toHaveBeenCalledWith('AGENT_B');
});

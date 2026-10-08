import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import OnboardingModelSetup from '@/renderer/pages/onboarding/OnboardingModelSetup';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({
  load: vi.fn(),
  save: vi.fn(),
  activate: vi.fn(),
  test: vi.fn(),
}));
vi.mock('@/renderer/services/synonBiomedLlm', () => ({
  loadSynonBiomedLlmProviders: mocks.load,
  saveSynonBiomedLlmProfile: mocks.save,
  activateSynonBiomedLlmProfile: mocks.activate,
  testSynonBiomedLlmProfile: mocks.test,
}));
const empty = { profiles: [], templates: [] };

describe('Onboarding model availability', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.load.mockResolvedValue(empty);
  });
  it('distinguishes an unread status from a real empty profile list and supports explicit recovery', async () => {
    mocks.load.mockRejectedValueOnce(new Error('offline fixture'));
    await renderWithI18n(<OnboardingModelSetup />, 'en-US');
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(screen.queryByTestId('onboarding-model-status')).not.toBeInTheDocument();
    expect(screen.getByTestId('onboarding-model-add')).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByTestId('onboarding-model-status')).toHaveTextContent('No model configured yet');
    expect(mocks.load).toHaveBeenCalledTimes(2);
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.activate).not.toHaveBeenCalled();
  });
  it('ignores a read that resolves after the setup is unmounted', async () => {
    let resolve!: (value: typeof empty) => void;
    mocks.load.mockReturnValueOnce(
      new Promise((done) => {
        resolve = done;
      })
    );
    const view = await renderWithI18n(<OnboardingModelSetup />, 'en-US');
    expect(screen.getByTestId('onboarding-model-setup')).toHaveAttribute('aria-busy', 'true');
    view.unmount();
    await act(async () => resolve(empty));
    await waitFor(() => expect(screen.queryByTestId('onboarding-model-status')).not.toBeInTheDocument());
  });
});

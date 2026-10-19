import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import React, { useState } from 'react';
import { MemoryRouter } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import XScienceSessionOptionsMenu from '@/renderer/components/xScience/runtime/XScienceSessionOptionsMenu';
import { createXScienceSessionDefaults } from '@/renderer/services/xScienceSessionOptions';
import type {
  XScienceMemoryAvailability,
  XScienceMemoryControl,
} from '@/renderer/hooks/xScience/runtime/useXScienceMemoryAvailability';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({ persist: vi.fn(), refresh: vi.fn(), setMode: vi.fn() }));
vi.mock('@/renderer/services/xScienceCatalog', () => ({ loadXScienceAssistants: async () => [] }));
vi.mock('@/renderer/services/xScienceCompute', () => ({
  loadXScienceComputeProviders: async () => [],
  loadXScienceSessionComputeProviders: async () => [],
  setXScienceSessionComputeProvider: vi.fn(),
}));

type HarnessProps = {
  availability: XScienceMemoryAvailability;
  memory?: boolean;
  mode?: 'default' | 'smart';
};

function Harness({ availability, memory, mode = 'default' }: HarnessProps) {
  const [value, setValue] = useState(() => ({
    ...createXScienceSessionDefaults(),
    ...(memory === undefined ? {} : { memory }),
  }));
  const memoryControl: XScienceMemoryControl = {
    availability,
    refresh: mocks.refresh,
    current: () => true,
    canUpdate: () => availability.status === 'ready' && availability.enabled,
  };
  return (
    <MemoryRouter>
      <XScienceSessionOptionsMenu
        value={value}
        onChange={setValue}
        memoryControl={memoryControl}
        permissionControl={{
          option: {
            id: 'mode',
            category: 'mode',
            currentValue: mode,
            options: [
              { value: 'default', label: 'Default' },
              { value: 'smart', label: 'Smart' },
            ],
          },
          setStatus: { state: 'idle' },
          setConfigOption: mocks.setMode,
        }}
        onPersistentOptionChange={async (key, checked) => {
          await mocks.persist(key, checked);
          setValue((current) => ({ ...current, [key]: checked }));
        }}
      />
      <output data-testid='retained-memory'>{String(value.memory)}</output>
    </MemoryRouter>
  );
}

const trigger = () => screen.getByTestId('x-science-session-options-trigger');
const memoryDot = () => trigger().querySelector('span.bg-primary');

describe('inherited Memory customization indicator', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.persist.mockResolvedValue(undefined);
    mocks.setMode.mockResolvedValue(undefined);
  });
  afterEach(() => cleanup());

  it('does not mark actual inherited Memory-on as customized when globally available', async () => {
    await renderWithI18n(<Harness availability={{ status: 'ready', enabled: true }} />, 'en-US');
    expect(memoryDot()).toBeNull();
    fireEvent.click(trigger());
    const memory = await screen.findByRole('menuitemcheckbox', { name: 'Memory' });
    expect(memory).toBeChecked();
    expect(memory).toBeEnabled();
    expect(mocks.persist).not.toHaveBeenCalled();
  });

  it('marks explicit Memory-off and removes the dot after the next successful opt-in', async () => {
    await renderWithI18n(<Harness availability={{ status: 'ready', enabled: true }} memory={false} />, 'en-US');
    expect(memoryDot()).not.toBeNull();
    fireEvent.click(trigger());
    const memory = await screen.findByRole('menuitemcheckbox', { name: 'Memory' });
    expect(memory).not.toBeChecked();
    expect(memory).toBeEnabled();
    fireEvent.click(memory);
    await waitFor(() => expect(mocks.persist).toHaveBeenCalledWith('memory', true));
    await waitFor(() => expect(memoryDot()).toBeNull());
    expect(screen.getByTestId('retained-memory')).toHaveTextContent('true');
  });

  it('does not classify global-off or unknown capability as customization or rewrite preference', async () => {
    const view = await renderWithI18n(<Harness availability={{ status: 'ready', enabled: false }} />, 'en-US');
    expect(memoryDot()).toBeNull();
    view.rerender(<Harness availability={{ status: 'loading' }} />);
    expect(memoryDot()).toBeNull();
    view.rerender(<Harness availability={{ status: 'unavailable' }} />);
    expect(memoryDot()).toBeNull();
    expect(screen.getByTestId('retained-memory')).toHaveTextContent('true');
    expect(mocks.persist).not.toHaveBeenCalled();
  });

  it('retains the existing non-default permission dot while Memory is unavailable', async () => {
    const view = await renderWithI18n(
      <Harness availability={{ status: 'ready', enabled: false }} mode='smart' />,
      'en-US'
    );
    expect(memoryDot()).not.toBeNull();
    view.rerender(<Harness availability={{ status: 'loading' }} mode='smart' />);
    expect(memoryDot()).not.toBeNull();
    view.rerender(<Harness availability={{ status: 'unavailable' }} mode='smart' />);
    expect(memoryDot()).not.toBeNull();
    view.rerender(<Harness availability={{ status: 'unavailable' }} mode='default' />);
    expect(memoryDot()).toBeNull();
    expect(mocks.setMode).not.toHaveBeenCalled();
    expect(mocks.persist).not.toHaveBeenCalled();
  });
});

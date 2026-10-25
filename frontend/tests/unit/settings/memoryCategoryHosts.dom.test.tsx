import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { Message } from '@arco-design/web-react';
import React, { useState } from 'react';
import { HashRouter, Route, Routes } from 'react-router';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import SettingsRoute from '@/renderer/pages/settings/SettingsRoute';
import SettingsPageWrapper from '@/renderer/pages/settings/components/SettingsPageWrapper';
import { XScienceMemoryManager } from '@/renderer/pages/settings/components/XScienceMemoryManager';
import SettingsModal from '@/renderer/components/settings/SettingsModal';
import { navigateSettingsRoute } from '@/renderer/pages/settings/settingsNavigation';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../i18nTestUtils';
import { createFetchMock, memoryContext } from './memoryManagerFixtures';

// Only unrelated destinations and a presentational font-scale dependency are replaced.
vi.mock('@/renderer/pages/settings/XScienceModelsSettings', async () => {
  const { createElement } = await import('react');
  const page = () => createElement('h1', { 'data-testid': 'models-destination' }, 'Models');
  return { default: page, XScienceModelsSettingsContent: page };
});
vi.mock('@/renderer/hooks/context/ThemeContext', () => ({ useThemeContext: () => ({ fontScale: 1 }) }));

function resetHash(hash = '#/guid') {
  window.history.replaceState(null, '', window.location.pathname + window.location.search + hash);
}

async function historyMove(direction: 'back' | 'forward') {
  await act(async () => {
    const moved = new Promise<void>((resolve) => window.addEventListener('popstate', () => resolve(), { once: true }));
    window.history[direction]();
    await moved;
  });
}

function fillDraft(add = screen.getByTestId('memory-category-add')) {
  fireEvent.click(add);
  fireEvent.change(screen.getByTestId('memory-category-name'), { target: { value: 'Methods' } });
  fireEvent.change(screen.getByTestId('memory-category-guidance'), { target: { value: 'Use only for methods.' } });
}

function gatedWrite() {
  const { fetchMock, observed } = createFetchMock();
  let resolve!: () => void;
  const ack = new Promise<void>((yes) => {
    resolve = yes;
  });
  let committed = false;
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
      const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url;
      const response = await fetchMock(input, init);
      if (url === '/api/memory/categories' && init?.method === 'POST') {
        await ack;
        committed = true;
      }
      if (url === '/api/memory/context' && (init?.method ?? 'GET') === 'GET' && committed) {
        return Response.json({
          ...memoryContext,
          categories: [
            ...memoryContext.categories,
            {
              id: 'memcat_created',
              name: 'Methods',
              guidance: 'Use only for methods.',
              auto_recall: true,
              row_count: 0,
            },
          ],
        });
      }
      return response;
    })
  );
  return {
    resolve,
    writes: () => observed.filter((r) => r.url === '/api/memory/categories' && r.method === 'POST'),
    reads: () => observed.filter((r) => r.url === '/api/memory/context' && r.method === 'GET'),
  };
}

describe('Memory category actual Settings hosts', () => {
  beforeEach(() => {
    setRendererAccountOwner('memory-host-fixture');
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    vi.spyOn(Message, 'success').mockReturnValue({ close: () => undefined });
    vi.spyOn(Message, 'warning').mockReturnValue({ close: () => undefined });
    vi.spyOn(Message, 'error').mockReturnValue({ close: () => undefined });
  });

  afterEach(() => {
    setRendererAccountOwner(null);
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('restores populated values through actual Back/Forward and an entire Settings-host exit', async () => {
    const { fetchMock, observed } = createFetchMock();
    vi.stubGlobal('fetch', fetchMock);
    resetHash();
    await renderWithI18n(
      <HashRouter>
        <Routes>
          <Route path='/guid' element={<h1 data-testid='workspace-destination'>Workspace</h1>} />
          <Route path='/settings/:section' element={<SettingsRoute />} />
        </Routes>
      </HashRouter>,
      'en-US'
    );
    await screen.findByTestId('workspace-destination');
    act(() => navigateSettingsRoute('governance'));
    await screen.findByTestId('memory-category-add');
    fillDraft();
    await historyMove('back'); // Exits SettingsRoute entirely.
    await screen.findByTestId('workspace-destination');
    expect(screen.queryByTestId('memory-category-name')).not.toBeInTheDocument();
    await historyMove('forward');
    expect(await screen.findByTestId('memory-category-name')).toHaveValue('Methods');
    expect(screen.getByTestId('memory-category-guidance')).toHaveValue('Use only for methods.');

    act(() => navigateSettingsRoute('models')); // The original explicit-tab authority.
    await screen.findByTestId('models-destination');
    await historyMove('back');
    expect(await screen.findByTestId('memory-category-name')).toHaveValue('Methods');
    expect(observed.filter((r) => r.url.startsWith('/api/memory/categories') && r.method !== 'GET')).toEqual([]);
    expect(screen.getAllByTestId('memory-category-name')).toHaveLength(1);
  });

  it('finishes the same write offscreen and invokes only a current page readback/onChanged', async () => {
    const gate = gatedWrite();
    const changed = vi.fn(async () => undefined);
    resetHash('#/settings/governance');
    try {
      await renderWithI18n(
        <HashRouter>
          <Routes>
            <Route
              path='/settings/governance'
              element={
                <SettingsPageWrapper>
                  <XScienceMemoryManager onChanged={changed} />
                </SettingsPageWrapper>
              }
            />
            <Route path='/settings/models' element={<h1 data-testid='models-destination'>Models</h1>} />
          </Routes>
        </HashRouter>,
        'en-US'
      );
      await screen.findByTestId('memory-category-add');
      fillDraft();
      fireEvent.click(document.querySelector<HTMLButtonElement>('#memory-category-save')!);
      await waitFor(() => expect(gate.writes()).toHaveLength(1));
      act(() => navigateSettingsRoute('models'));
      await screen.findByTestId('models-destination');
      await act(async () => gate.resolve());
      expect(changed).not.toHaveBeenCalled();
      expect(Message.success).not.toHaveBeenCalled();
      await historyMove('back');
      await waitFor(() => expect(changed).toHaveBeenCalledTimes(1));
      expect(screen.queryByTestId('memory-category-name')).not.toBeInTheDocument();
      expect(screen.getByTestId('memory-category-count')).toHaveTextContent('2/10');
      expect(gate.writes()).toHaveLength(1);
      expect(Message.success).toHaveBeenCalledTimes(1);
    } finally {
      await act(async () => gate.resolve());
    }
  });

  it('uses actual SettingsModal visibility outside its cached subtree with one page/modal editor', async () => {
    const gate = gatedWrite();
    resetHash('#/settings/governance');
    const App = () => {
      const [visible, setVisible] = useState(false);
      return (
        <HashRouter>
          <button type='button' onClick={() => setVisible(true)}>
            Open actual Settings modal
          </button>
          <Routes>
            <Route path='/settings/:section' element={<SettingsRoute />} />
          </Routes>
          <SettingsModal visible={visible} defaultTab='governance' onCancel={() => setVisible(false)} />
        </HashRouter>
      );
    };
    try {
      await renderWithI18n(<App />, 'en-US');
      await screen.findByTestId('memory-category-add');
      fillDraft();
      const pageInput = screen.getByTestId('memory-category-name');
      fireEvent.click(screen.getByRole('button', { name: 'Open actual Settings modal' }));
      const outer = await waitFor(() => {
        const modal = document.querySelector<HTMLElement>('.settings-modal');
        if (!modal) throw new Error('Actual SettingsModal not mounted');
        return modal;
      });
      await within(outer).findByTestId('memory-category-add');
      await waitFor(() => expect(pageInput).not.toBeInTheDocument());
      expect(screen.getAllByTestId('memory-category-name')).toHaveLength(1);
      expect(screen.getByTestId('memory-category-name')).toHaveValue('Methods');
      fireEvent.change(pageInput, { target: { value: 'Stale page event' } });
      expect(screen.getByTestId('memory-category-name')).toHaveValue('Methods');

      fireEvent.click(document.querySelector<HTMLButtonElement>('#memory-category-save')!);
      await waitFor(() => expect(gate.writes()).toHaveLength(1));
      const modalInput = screen.getByTestId('memory-category-name');
      fireEvent.click(within(outer).getByRole('button', { name: 'Close' })); // Real outer host onCancel.
      await waitFor(() => expect(modalInput).not.toBeInTheDocument());
      expect(outer).toBeInTheDocument(); // Native host keeps its subtree cached while hidden.
      expect(screen.getAllByTestId('memory-category-name')).toHaveLength(1);
      expect(screen.getByTestId('memory-category-name')).toBeDisabled();
      const beforeAckReads = gate.reads().length;
      await act(async () => gate.resolve());
      await waitFor(() => expect(Message.success).toHaveBeenCalledTimes(1));
      expect(gate.reads().length - beforeAckReads).toBe(1); // Hidden cached host performed no ACK readback.
      expect(screen.queryByTestId('memory-category-name')).not.toBeInTheDocument();

      const beforeReopenReads = gate.reads().length;
      fireEvent.click(screen.getByRole('button', { name: 'Open actual Settings modal' }));
      await waitFor(() => expect(within(outer).getByTestId('memory-category-count')).toHaveTextContent('2/10'));
      await waitFor(() => expect(gate.reads().length - beforeReopenReads).toBe(1));
      expect(Message.success).toHaveBeenCalledTimes(1);
      expect(gate.writes()).toHaveLength(1);
      expect(screen.queryAllByTestId('memory-category-name')).toHaveLength(0);
    } finally {
      await act(async () => gate.resolve());
    }
  });
});

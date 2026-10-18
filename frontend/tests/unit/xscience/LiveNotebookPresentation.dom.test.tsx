import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { notebookFixture, renderNotebook, submissions } from './manualNotebookDomHarness';
import { inventory, owner, rawScope, rawTarget, request, response } from './manualNotebookFixtures';

const originalClipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
beforeEach(() => {
  setRendererAccountOwner(owner);
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  if (originalClipboard) Object.defineProperty(navigator, 'clipboard', originalClipboard);
  else Reflect.deleteProperty(navigator, 'clipboard');
});
const ready = () => waitFor(() => expect(screen.getByRole('textbox', { name: 'Code' })).toBeEnabled());

describe('live Notebook keyboard and canonical presentation', () => {
  it('submits on Enter only, preserves Shift+Enter and IME, and supports keyboard resizing', async () => {
    const fixture = notebookFixture();
    await renderNotebook();
    await ready();
    const code = screen.getByRole('textbox', { name: 'Code' });
    fireEvent.change(code, { target: { value: 'print("keyboard")\n' } });
    fireEvent.keyDown(code, { key: 'Enter', shiftKey: true });
    fireEvent.keyDown(code, { key: 'Enter', isComposing: true });
    expect(submissions(fixture)).toHaveLength(0);
    const separator = screen.getByRole('separator', { name: 'Resize notebook and terminal' });
    const originalSize = Number(separator.getAttribute('aria-valuenow'));
    fireEvent.keyDown(separator, { key: 'ArrowUp' });
    expect(separator).toHaveAttribute('aria-valuenow', String(originalSize + 5));
    fireEvent.keyDown(code, { key: 'Enter' });
    await waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    expect(JSON.parse(String(submissions(fixture)[0][1]?.body)).code).toBe('print("keyboard")\n');
  });

  it('renders canonical history, both output streams and copy without executing historical source', async () => {
    const fixture = notebookFixture();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
    fixture.history = [
      {
        id: 'log-1',
        kind: 'cell',
        cell_index: 9,
        frame_id: 'frame-1',
        kernel_id: 'kernel-1',
        origin: 'user',
        language: 'python',
        conda_env: 'python',
        kernel_kind: 'analysis',
        source: 'print("canonical")\nraise ValueError()',
        stdout: 'canonical stdout',
        stderr: 'canonical stderr',
        exit_status: 'error',
        error_lineno: 2,
      },
      {
        id: 'other-frame-log',
        cell_index: 10,
        frame_id: 'frame-2',
        kernel_id: 'kernel-1',
        language: 'python',
        source: 'not this frame',
        exit_status: 'ok',
      },
    ];
    const view = await renderNotebook();
    await ready();
    const cell = await screen.findByTestId('live-notebook-cell');
    expect(within(cell).getByText('you', { exact: true })).toBeVisible();
    expect(within(cell).getByLabelText('Standard output')).toHaveTextContent('canonical stdout');
    expect(within(cell).getByLabelText('Standard error')).toHaveTextContent('canonical stderr');
    expect(screen.queryByText('not this frame')).not.toBeInTheDocument();
    fireEvent.click(within(cell).getByRole('button', { name: 'Copy code' }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('print("canonical")\nraise ValueError()'));
    expect(submissions(fixture)).toHaveLength(0);
    await act(async () => {
      await view.i18n.changeLanguage('zh-CN');
    });
    expect(screen.getByRole('textbox', { name: '代码' })).toBeVisible();
    expect(screen.queryByText('conversation.manualNotebook.code')).not.toBeInTheDocument();
  });

  it('keeps unknown outcome and unavailable physical state distinct from a stopped result', async () => {
    const fixture = notebookFixture();
    fixture.accept(request, 'outcome_unknown');
    await renderNotebook();
    expect(await screen.findByText('Outcome unknown', { exact: true })).toBeVisible();
    expect(screen.getByRole('button', { name: 'Run', exact: true })).toBeDisabled();
    expect(screen.queryByRole('button', { name: 'Retry same request' })).not.toBeInTheDocument();
    expect(screen.queryByText('Stopped', { exact: true })).not.toBeInTheDocument();
    expect(submissions(fixture)).toHaveLength(0);
  });

  it('disables the old terminal when a fresh inventory proves frame incarnation replacement', async () => {
    const fixture = notebookFixture();
    await renderNotebook();
    await ready();
    fireEvent.change(screen.getByRole('textbox', { name: 'Code' }), { target: { value: 'old frame draft' } });
    fixture.kernels = async () =>
      response({
        ...inventory([{ ...rawTarget, frame_incarnation_id: 'replacement-incarnation' }]),
        manual_execution_scope: { ...rawScope, frame_incarnation_id: 'replacement-incarnation' },
      });
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(await screen.findByText(/This frame was replaced/)).toBeVisible();
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(submissions(fixture)).toHaveLength(0);
  });
});

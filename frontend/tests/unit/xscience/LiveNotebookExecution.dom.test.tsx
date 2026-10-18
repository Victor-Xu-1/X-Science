import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import {
  deferred,
  events,
  notebookFixture,
  notebookViews,
  renderNotebook,
  submissions,
} from './manualNotebookDomHarness';
import { owner, rawTarget, receipt, response } from './manualNotebookFixtures';
import type { ManualNotebookRequest } from '@/renderer/services/notebook/manualNotebookTypes';

beforeEach(() => {
  setRendererAccountOwner(owner);
});
afterEach(() => {
  cleanup();
  setRendererAccountOwner(null);
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});
async function ready() {
  await waitFor(() => expect(screen.getAllByRole('textbox', { name: 'Code' })[0]).toBeEnabled());
}

describe('shared live Notebook execution owner', () => {
  it('shares draft and one submission reservation across two simultaneously mounted views', async () => {
    const fixture = notebookFixture(),
      pending = deferred<Response>();
    fixture.submit = async (body) => {
      fixture.accept(body);
      return pending.promise;
    };
    const view = await renderNotebook(2);
    await ready();
    expect(events.wakes.size).toBe(1);
    expect(fixture.fetch.mock.calls.filter(([input]) => String(input).endsWith('/kernels'))).toHaveLength(1);
    const inputs = screen.getAllByRole('textbox', { name: 'Code' });
    fireEvent.change(inputs[0], { target: { value: 'print("once")\n' } });
    expect(inputs[1]).toHaveValue('print("once")\n');
    const run = screen.getAllByRole('button', { name: 'Run', exact: true });
    fireEvent.click(run[0]);
    fireEvent.click(run[1]);
    await waitFor(() => expect(submissions(fixture)).toHaveLength(1));
    const body = JSON.parse(String(submissions(fixture)[0][1]?.body)) as ManualNotebookRequest;
    view.rerender(notebookViews(1));
    await act(async () => {
      pending.resolve(response(receipt(body)));
      await pending.promise;
    });
    expect(submissions(fixture)).toHaveLength(1);
    expect(screen.getByTestId('manual-notebook-request')).toHaveAttribute('data-request-id', body.request_id);
    expect(events.wakes.size).toBe(1);
  });

  it('allows only explicit same-UUID retry after a lost reply and an absent receipt', async () => {
    const fixture = notebookFixture();
    fixture.submit = async () => {
      throw new TypeError('fixture lost response');
    };
    await renderNotebook();
    await ready();
    fireEvent.change(screen.getByRole('textbox', { name: 'Code' }), { target: { value: 'print("frozen")\n' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run', exact: true }));
    const retry = await screen.findByRole('button', { name: 'Retry same request' });
    expect(submissions(fixture)).toHaveLength(1);
    expect(screen.getByRole('button', { name: 'Run', exact: true })).toBeDisabled();
    fixture.submit = async (body) => {
      fixture.accept(body);
      return response(receipt(body));
    };
    fireEvent.click(retry);
    await waitFor(() => expect(submissions(fixture)).toHaveLength(2));
    expect(submissions(fixture)[1][1]?.body).toBe(submissions(fixture)[0][1]?.body);
    expect(await screen.findByText('Queued', { exact: true })).toBeVisible();
  });

  it('does not execute the old draft after the user changes target during final inventory read', async () => {
    const fixture = notebookFixture();
    fixture.targets = [
      rawTarget,
      { ...rawTarget, kernel_id: 'kernel-2', kernel_generation: 8, environment: 'analysis' },
    ];
    await renderNotebook();
    await ready();
    const pending = deferred<Response>();
    fixture.kernels = () => pending.promise;
    fireEvent.change(screen.getByRole('textbox', { name: 'Code' }), { target: { value: 'print("old target")' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run', exact: true }));
    await waitFor(() =>
      expect(fixture.fetch.mock.calls.filter(([input]) => String(input).endsWith('/kernels'))).toHaveLength(2)
    );
    fireEvent.click(screen.getByRole('tab', { name: /analysis.*kernel-2/ }));
    const { inventory } = await import('./manualNotebookFixtures');
    await act(async () => {
      pending.resolve(response(inventory(fixture.targets)));
      await pending.promise;
    });
    expect(submissions(fixture)).toHaveLength(0);
    expect(screen.getByRole('textbox', { name: 'Code' })).toHaveValue('');
    expect(await screen.findByText(/selected kernel changed/)).toBeVisible();
  });

  it('keeps cancellation requested separate from terminal evidence and refreshes through the existing event', async () => {
    const fixture = notebookFixture();
    await renderNotebook();
    await ready();
    fireEvent.change(screen.getByRole('textbox', { name: 'Code' }), { target: { value: 'print("cancelled")' } });
    fireEvent.click(screen.getByRole('button', { name: 'Run', exact: true }));
    fireEvent.click(await screen.findByRole('button', { name: 'Cancel request' }));
    expect(await screen.findByText('Cancellation requested — awaiting terminal evidence')).toBeVisible();
    expect(screen.queryByText('Cancelled before start', { exact: true })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run', exact: true })).toBeDisabled();
    const entry = [...fixture.requests.values()][0];
    entry.state = 'terminal';
    act(() => {
      events.wakes.forEach((listener) => listener({ frame_id: 'frame-1', root_frame_id: 'root-1' }));
    });
    expect(await screen.findByText('Cancelled before start', { exact: true })).toBeVisible();
    expect(submissions(fixture)).toHaveLength(1);
  });
});

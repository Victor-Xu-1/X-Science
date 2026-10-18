import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { deferred, notebookFixture, renderNotebook, submissions } from './manualNotebookDomHarness';
import { owner, pendingPage, receipt, request, response } from './manualNotebookFixtures';

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
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Code' })).toBeEnabled());
}

describe('manual Notebook recovery and read-only boundaries', () => {
  it('retains an uncertain UUID through close and recovers the canonical receipt without another POST', async () => {
    const fixture = notebookFixture();
    fixture.submit = async (body) => {
      fixture.accept(body);
      throw new TypeError('reply lost');
    };
    fixture.lookup = async () => response({ code: 'MANUAL_EXECUTION_NOT_ACCEPTED' }, 404);
    const view = await renderNotebook();
    await ready();
    fireEvent.change(screen.getByRole('textbox', { name: 'Code' }), {
      target: { value: 'print("recover unchanged")' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Run', exact: true }));
    await screen.findByRole('button', { name: 'Retry same request' });
    const body = String(submissions(fixture)[0][1]?.body);
    view.unmount();
    fixture.lookup = null;
    await renderNotebook();
    expect(await screen.findByText('Queued', { exact: true })).toBeVisible();
    expect(submissions(fixture)).toHaveLength(1);
    expect(screen.getByTestId('manual-notebook-request')).toHaveAttribute(
      'data-request-id',
      JSON.parse(body).request_id
    );
    fireEvent.click(screen.getByText('Original request code'));
    expect(screen.getByText('print("recover unchanged")')).toBeVisible();
  });

  it('does not treat an empty incomplete discovery page as no pending work or scan ahead automatically', async () => {
    const fixture = notebookFixture();
    fixture.pending = (cursor) => response(pendingPage([], cursor ? '' : 'next-window'));
    await renderNotebook();
    const more = await screen.findByRole('button', { name: 'Continue pending scan' });
    expect(screen.getByRole('button', { name: 'Run', exact: true })).toBeDisabled();
    expect(fixture.fetch.mock.calls.filter(([input]) => String(input).includes('/kernel-executions?'))).toHaveLength(1);
    fireEvent.click(more);
    await ready();
    expect(fixture.fetch.mock.calls.filter(([input]) => String(input).includes('/kernel-executions?'))).toHaveLength(2);
    expect(submissions(fixture)).toHaveLength(0);
  });

  it('recovers legacy and old-generation requests without typed execution or cancellation controls', async () => {
    const fixture = notebookFixture();
    fixture.accept({ ...request, expected_kernel_generation: 3 }, 'queued', 'typed-v1');
    fixture.accept({ ...request, request_id: 'c87acdbe-5689-4617-b570-8416ecf541ad' }, 'queued', 'legacy');
    await renderNotebook();
    expect(await screen.findByText(/Legacy request · read-only/)).toBeVisible();
    expect(await screen.findByText(/Original kernel generation · read-only/)).toBeVisible();
    await waitFor(() => expect(screen.getAllByTestId('manual-notebook-request')).toHaveLength(2));
    expect(screen.queryByRole('button', { name: 'Cancel request' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'Retry same request' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Run', exact: true })).toBeDisabled();
    expect(fixture.fetch.mock.calls.every(([, init]) => !init?.method || init.method === 'GET')).toBe(true);
  });

  it('ignores a late previous-account receipt and does not send source during recovery', async () => {
    const fixture = notebookFixture(),
      late = deferred<Response>();
    fixture.accept(request);
    fixture.lookup = () => late.promise;
    await renderNotebook();
    await waitFor(() =>
      expect(fixture.fetch.mock.calls.some(([input]) => String(input).includes('/by-request/'))).toBe(true)
    );
    fixture.requests.clear();
    fixture.lookup = null;
    fixture.owner = 'second-owner';
    act(() => {
      setRendererAccountOwner('second-owner');
    });
    await ready();
    await act(async () => {
      late.resolve(response(receipt(request)));
      await late.promise;
    });
    expect(screen.queryByTestId('manual-notebook-request')).not.toBeInTheDocument();
    expect(screen.queryByText(request.code.trim())).not.toBeInTheDocument();
    expect(submissions(fixture)).toHaveLength(0);
    expect(fixture.fetch.mock.calls.every(([input]) => !String(input).includes('unchanged'))).toBe(true);
  });
});

import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import type { XSciencePlanReference } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

const load = vi.hoisted(() => vi.fn());
vi.mock('@/renderer/services/xScienceRuntimeOperations', () => ({ loadXSciencePlanDocument: load }));
import { useXSciencePlanPreview } from '@/renderer/components/xScience/runtime/useXSciencePlanPreview';

// Observes the production owner-to-presenter props contract, not a shipped progress UI.
function PreviewOwner({ reference }: { reference: XSciencePlanReference }) {
  const preview = useXSciencePlanPreview('frame-plan', reference);
  return (
    <>
      <button onClick={preview.open}>Open</button>
      <button onClick={preview.retry}>Retry</button>
      <output data-testid='progress-contract'>{preview.reference?.progress?.completed ?? 'unknown'}</output>
      <output data-testid='version-contract'>{preview.reference?.versionId}</output>
      <output data-testid='replaced-contract'>{String(preview.replaced)}</output>
    </>
  );
}

it('C397 updates current progress but hides historical progress without rebinding its retry source', async () => {
  load.mockResolvedValue({ version: 1, taskSummary: 'Synthetic immutable A', phases: [], feasibility: null });
  const source = { artifactId: 'plan', versionId: 'version-A', progress: { completed: 1, total: 3 } };
  const view = await renderWithI18n(<PreviewOwner reference={source} />);
  fireEvent.click(screen.getByRole('button', { name: 'Open' }));
  await waitFor(() => expect(load).toHaveBeenCalledOnce());
  expect(screen.getByTestId('progress-contract')).toHaveTextContent('1');
  view.rerender(<PreviewOwner reference={{ ...source, progress: { completed: 2, total: 3 } }} />);
  await waitFor(() => expect(screen.getByTestId('progress-contract')).toHaveTextContent('2'));
  view.rerender(
    <PreviewOwner reference={{ ...source, versionId: 'version-B', progress: { completed: 3, total: 3 } }} />
  );
  expect(screen.getByTestId('progress-contract')).toHaveTextContent('unknown');
  expect(screen.getByTestId('version-contract')).toHaveTextContent('version-A');
  expect(screen.getByTestId('replaced-contract')).toHaveTextContent('true');
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(load).toHaveBeenCalledTimes(2));
  expect(load.mock.calls.map(([target]) => target)).toEqual([source, source]);
});

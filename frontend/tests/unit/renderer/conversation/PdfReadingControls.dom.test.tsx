import React, { useSyncExternalStore } from 'react';
import { act, fireEvent, screen, waitFor, cleanup } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PdfReadingController } from '@/renderer/services/runtime/PdfReadingController';
import { PdfReadingBar } from '@/renderer/components/chat/SendBox/reading/PdfReadingBar';
import { ReadingContextPicker } from '@/renderer/components/chat/SendBox/reading/ReadingContextPicker';
import { PdfReadingPreviewAction } from '@/renderer/pages/conversation/Preview/components/viewers/PdfReadingPreviewAction';
import { renderWithI18n } from '../../i18nTestUtils';
import { pdfBinding, pdfContext, deferredPdf } from './pdfReadingFixtures';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import type { PdfReadingPreviewOwner } from '@/renderer/services/runtime/pdfReadingPreview';
import type { PdfReadingContext } from '@/common/adapter/pdfReadingContext';

const mocks = vi.hoisted(() => ({ options: vi.fn(), owner: null as PdfReadingPreviewOwner | null }));
vi.mock('@/renderer/services/runtime/pdfReadingContext', async (original) => ({
  ...(await original<typeof import('@/renderer/services/runtime/pdfReadingContext')>()),
  loadProjectReadingOptions: mocks.options,
}));
vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  useOptionalPreviewContext: () => ({ pdfReadingOwner: mocks.owner }),
}));
const Harness = ({ controller, open }: { controller: PdfReadingController; open: (id: string) => void }) => {
  const view = useSyncExternalStore(controller.subscribe, controller.getSnapshot, controller.getSnapshot);
  return <PdfReadingBar view={view} controller={controller} onOpen={open} />;
};

describe('C12 PDF reading controls', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    mocks.options.mockReset();
    mocks.owner = null;
  });
  afterEach(() => {
    cleanup();
  });
  it('retries a Project candidate read, searches, then links one immutable version through the composer owner', async () => {
    mocks.options
      .mockRejectedValueOnce(new Error('temporary'))
      .mockResolvedValue({
        options: [{ source: { artifact_id: 'artifact-2', version_id: 'version-2' }, name: 'second.pdf', pageCount: 9 }],
        unavailable: 0,
      });
    const mutation = deferredPdf<PdfReadingContext>();
    const mutate = vi.fn().mockReturnValue(mutation.promise);
    const controller = new PdfReadingController({
      ownerId: 'owner',
      frameId: 'frame',
      persistPending: vi.fn(),
      load: vi.fn().mockResolvedValue(pdfContext()),
      mutate,
    });
    await controller.load();
    await renderWithI18n(<Harness controller={controller} open={vi.fn()} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Choose PDFs for Reading' }));
    await screen.findByText('Could not load PDF reading context.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('option', { name: 'second.pdf' });
    fireEvent.change(screen.getByRole('textbox', { name: 'Search PDFs' }), { target: { value: 'missing' } });
    expect(screen.queryByRole('option')).toBeNull();
    fireEvent.change(screen.getByRole('textbox', { name: 'Search PDFs' }), { target: { value: 'second' } });
    fireEvent.click(screen.getByRole('option', { name: 'second.pdf' }));
    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(1));
    expect(mutate.mock.calls[0][1]).toMatchObject({
      expected_revision: 1,
      sources: [{ artifact_id: 'artifact-2', version_id: 'version-2' }],
    });
    await act(async () => {
      mutation.resolve({ ...pdfContext([pdfBinding(), pdfBinding('2')], 2), changed: true });
      await mutation.promise;
    });
    expect(screen.getByTestId('pdf-context-bar')).toHaveTextContent('paper-2.pdf');
    expect(screen.queryByText('Library')).toBeNull();
  });
  it('opens a bound PDF and unlinks without deleting its underlying file', async () => {
    const mutate = vi.fn().mockResolvedValue({ ...pdfContext([], 2), changed: true });
    const open = vi.fn();
    const controller = new PdfReadingController({
      ownerId: 'owner',
      frameId: 'frame',
      persistPending: vi.fn(),
      load: vi.fn().mockResolvedValue(pdfContext()),
      mutate,
    });
    await controller.load();
    await renderWithI18n(<Harness controller={controller} open={open} />, 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Open PDF context paper-1.pdf' }));
    expect(open).toHaveBeenCalledWith('binding-1');
    fireEvent.click(screen.getByRole('button', { name: 'Remove PDF context paper-1.pdf' }));
    await waitFor(() => expect(mutate).toHaveBeenCalledTimes(1));
    expect(mutate.mock.calls[0][1]).toMatchObject({
      schema: 'x-science.pdf-reading-unlink.v1',
      binding_id: 'binding-1',
    });
    expect(mutate.mock.calls[0][1]).not.toHaveProperty('delete_file');
  });
  it('exposes Read with agent only after actual multi-page observation on an owned immutable Project version', async () => {
    const link = vi.fn().mockResolvedValue(undefined);
    const reportPosition = vi.fn();
    mocks.owner = {
      ownerId: 'owner',
      frameId: 'frame',
      projectId: 'project',
      context: pdfContext([], 0),
      pending: false,
      link,
      unlink: vi.fn(),
      reportPosition,
    };
    const source = { artifact_id: 'artifact-1', version_id: 'version-1' };
    const view = await renderWithI18n(
      <PdfReadingPreviewAction source={source} projectId='project' position={{ pageNumber: 1, pageCount: 1 }} />,
      'en-US'
    );
    expect(screen.queryByRole('button')).toBeNull();
    view.rerender(
      <PdfReadingPreviewAction source={source} projectId='project' position={{ pageNumber: 4, pageCount: 9 }} />
    );
    fireEvent.click(screen.getByRole('button', { name: 'Read with agent' }));
    await waitFor(() =>
      expect(link).toHaveBeenCalledWith(source, { name: 'PDF', position: { pageNumber: 4, pageCount: 9 } })
    );
    expect(reportPosition).toHaveBeenCalledWith(source, { pageNumber: 4, pageCount: 9 });
    view.rerender(
      <PdfReadingPreviewAction source={source} projectId='foreign' position={{ pageNumber: 4, pageCount: 9 }} />
    );
    expect(screen.queryByRole('button')).toBeNull();
  });
  it('does not surface an old picker mutation failure in a replacement frame', async () => {
    const action = deferredPdf<void>();
    const select = vi.fn(() => action.promise);
    mocks.options.mockResolvedValue({
      options: [{ source: { artifact_id: 'artifact-2', version_id: 'version-2' }, name: 'second.pdf', pageCount: 9 }],
      unavailable: 0,
    });
    const view = await renderWithI18n(
      <ReadingContextPicker context={pdfContext()} pending={false} onSelect={select}>
        <button>pick</button>
      </ReadingContextPicker>,
      'en-US'
    );
    fireEvent.click(screen.getByText('pick'));
    fireEvent.click(await screen.findByRole('option', { name: 'second.pdf' }));
    view.rerender(
      <ReadingContextPicker
        context={{ ...pdfContext(), scope: { ...pdfContext().scope, frame_incarnation_id: 'replacement' } }}
        pending={false}
        onSelect={select}
      >
        <button>pick</button>
      </ReadingContextPicker>
    );
    await act(async () => {
      action.reject(new Error('old request'));
      await action.promise.catch(() => undefined);
    });
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

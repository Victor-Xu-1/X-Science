import React from 'react';
import { cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, useLocation } from 'react-router';
import type { LiteratureCommand, LiteratureReceipt } from '@/common/adapter/literatureLibrary';
import { ReadingContextPicker } from '@/renderer/components/chat/SendBox/reading/ReadingContextPicker';
import { getLiteratureMutationController } from '@/renderer/services/runtime/LiteratureMutationController';
import {
  createLiteratureReadingBulk,
  claimLiteratureReadingBulk,
} from '@/renderer/services/runtime/literatureReadingBulkIntent';
import {
  createUploadIntent,
  encodePendingRecord,
  withArtifactReceipt,
  withInitReceipt,
} from '@/renderer/services/runtime/literatureUploadIntent';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { pdfContext } from './pdfReadingFixtures';
import { installLiteratureLocks } from './literatureMutationLockFixtures';

const mocks = vi.hoisted(() => ({ run: vi.fn(), project: vi.fn(), library: vi.fn() }));
vi.mock('@/renderer/services/runtime/pdfReadingContext', async (original) => ({
  ...(await original<typeof import('@/renderer/services/runtime/pdfReadingContext')>()),
  loadProjectReadingOptions: mocks.project,
}));
vi.mock('@/renderer/services/runtime/literatureLibrary', async (original) => ({
  ...(await original<typeof import('@/renderer/services/runtime/literatureLibrary')>()),
  loadLibraryReadingOptions: mocks.library,
  runLiteratureCommand: mocks.run,
}));

const key = 'x-science.literature-mutation.v1:owner';
const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, '0')}`;
const checksum = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad';
const context = pdfContext([], 0);
const picker = () => (
  <ReadingContextPicker context={context} pending={false} onSelect={vi.fn().mockResolvedValue(undefined)}>
    <button>Choose reading sources</button>
  </ReadingContextPicker>
);
function Location() {
  return <span data-testid='location'>{useLocation().pathname}</span>;
}

describe('PDF reading Library recovery', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('owner');
    localStorage.clear();
    installLiteratureLocks();
    mocks.project.mockReset().mockResolvedValue({ options: [], unavailable: 0 });
    mocks.library.mockReset().mockResolvedValue({ options: [], unavailable: 0 });
    mocks.run.mockReset();
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new Error('Unexpected upload dispatch')));
  });
  afterEach(() => {
    cleanup();
    setRendererAccountOwner(null);
    localStorage.clear();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it('routes every durable PDF phase to Library without retrying or replacing the original operation', async () => {
    const initial = createUploadIntent({
      projectId: 'project',
      title: 'reference',
      filename: 'reference.pdf',
      size: 3,
      contentType: 'application/pdf',
      hash: checksum,
      catalogueOperationId: id(1),
      initOperationId: id(2),
    });
    const admitted = withInitReceipt(initial, { upload_id: id(3), chunk_size: 2, total_chunks: 2 });
    const uploaded = withArtifactReceipt(admitted, {
      artifactId: 'source-artifact',
      versionId: 'source-version',
      filename: 'reference.pdf',
      sizeBytes: 3,
      checksum,
    });
    const controller = getLiteratureMutationController('owner');
    const retry = vi.spyOn(controller, 'retry');
    for (const pending of [initial, admitted, uploaded]) {
      const original = encodePendingRecord('owner', pending);
      localStorage.setItem(key, original);
      controller.restore();
      expect(controller.getSnapshot().pending).toEqual(pending);
      expect(controller.getSnapshot().uploadState).toBe(pending.phase === 'catalogue' ? 'paused' : 'file-required');
      const rendered = await renderWithI18n(
        <MemoryRouter initialEntries={['/conversation']}>
          {picker()}
          <Location />
        </MemoryRouter>,
        'en-US'
      );
      fireEvent.click(screen.getByRole('button', { name: 'Choose reading sources' }));
      const link = await screen.findByRole('link', { name: 'Continue PDF import in Library' });
      const notice = link.closest('[role="alert"]') as HTMLElement;
      expect(notice).toHaveTextContent('The original upload request is retained.');
      expect(within(notice).queryByRole('button', { name: 'Retry' })).toBeNull();
      expect(link).toHaveAttribute('href', '/library');
      fireEvent.click(link);
      await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/library'));
      await waitFor(() => expect(screen.queryByRole('link', { name: 'Continue PDF import in Library' })).toBeNull());
      expect(controller.getSnapshot().pending).toEqual(pending);
      expect(localStorage.getItem(key)).toBe(original);
      expect(retry).not.toHaveBeenCalled();
      expect(mocks.run).not.toHaveBeenCalled();
      expect(fetch).not.toHaveBeenCalled();
      rendered.unmount();
    }
  });

  it('routes original bulk reading work to Library without implicitly resuming any child', async () => {
    let sequence = 20;
    const initial = createLiteratureReadingBulk(
      Array.from({ length: 201 }, (_, index) => `paper-${index}`),
      'read',
      'original-library-scope',
      () => id(++sequence)
    );
    const controller = getLiteratureMutationController('owner');
    const retry = vi.spyOn(controller, 'retry');
    const resume = vi.spyOn(controller, 'resumeReadingStatusBulk');
    for (const pending of [initial, claimLiteratureReadingBulk(initial)]) {
      const original = encodePendingRecord('owner', pending);
      localStorage.setItem(key, original);
      controller.restore();
      const rendered = await renderWithI18n(
        <MemoryRouter initialEntries={['/conversation']}>
          {picker()}
          <Location />
        </MemoryRouter>,
        'en-US'
      );
      fireEvent.click(screen.getByRole('button', { name: 'Choose reading sources' }));
      const link = await screen.findByRole('link', { name: 'Resume batch' });
      const notice = link.closest('[role="alert"]') as HTMLElement;
      expect(notice).toHaveTextContent('0 of 201 confirmed.');
      expect(within(notice).queryByRole('button', { name: 'Retry' })).toBeNull();
      fireEvent.click(link);
      await waitFor(() => expect(screen.getByTestId('location')).toHaveTextContent('/library'));
      expect(localStorage.getItem(key)).toBe(original);
      expect(controller.getSnapshot().pending).toEqual(pending);
      expect(retry).not.toHaveBeenCalled();
      expect(resume).not.toHaveBeenCalled();
      expect(fetch).not.toHaveBeenCalled();
      rendered.unmount();
    }
  });

  it('keeps the original legacy edit retry and settlement without requiring a router', async () => {
    const command: LiteratureCommand = {
      kind: 'edit',
      operation_id: id(8),
      item_id: 'reference',
      expected_revision: 1,
      title: 'Updated title',
    };
    const receipt: LiteratureReceipt = {
      schema: 'x-science.literature-receipt.v1',
      operation_id: id(8),
      item: {
        id: 'reference',
        title: 'Updated title',
        metadata_revision: 2,
        metadata: {},
        deleted: false,
        project_ids: ['project'],
        attachments: [],
      },
    };
    localStorage.setItem(key, encodePendingRecord('owner', command));
    const controller = getLiteratureMutationController('owner');
    const retry = vi.spyOn(controller, 'retry');
    mocks.run.mockResolvedValue(receipt);
    await renderWithI18n(picker(), 'en-US');
    fireEvent.click(screen.getByRole('button', { name: 'Choose reading sources' }));
    const notice = await screen.findByText(
      'The Library change was sent, but its receipt is unknown. Retry the same request.'
    );
    fireEvent.click(within(notice.closest('[role="alert"]') as HTMLElement).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(controller.getSnapshot().pending).toBeNull());
    expect(retry).toHaveBeenCalledTimes(1);
    expect(mocks.run).toHaveBeenCalledExactlyOnceWith(command);
    expect(controller.getSnapshot().receipt).toEqual(receipt);
    expect(localStorage.getItem(key)).toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });
});

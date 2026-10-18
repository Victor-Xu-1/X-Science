import React from 'react';
import { act, cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BackendHttpError } from '@/common/adapter/httpBridge';
import type {
  PdfReadingContext,
  PdfReadingPendingMutation,
  PdfReadingSource,
} from '@/common/adapter/pdfReadingContext';
import { PdfReadingComposerPrefix } from '@/renderer/components/chat/SendBox/reading/PdfReadingComposerPrefix';
import { usePdfReadingComposer } from '@/renderer/hooks/chat/usePdfReadingComposer';
import { useAcpSendBoxDraftController } from '@/renderer/pages/conversation/platforms/acp/useAcpSendBoxDraftController';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import {
  loadSendBoxDraft,
  saveSendBoxDraft,
  sendBoxDraftStorageKey,
} from '@/renderer/hooks/chat/sendBoxDraftPersistence';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { renderWithI18n } from '../../i18nTestUtils';
import { deferredPdf, pdfBinding, pdfContext } from './pdfReadingFixtures';

const transport = vi.hoisted(() => ({ load: vi.fn(), mutate: vi.fn() }));
vi.mock('@/renderer/services/runtime/pdfReadingContext', () => ({
  loadPdfReadingContext: transport.load,
  mutatePdfReadingContext: transport.mutate,
  inspectPdfReadingCandidates: vi.fn(),
  loadProjectReadingOptions: vi.fn(),
}));
vi.mock('@/common', () => ({
  ipcBridge: { conversation: { listChanged: { on: () => () => {} } } },
}));

const sourceC: PdfReadingSource = { artifact_id: 'artifact-3', version_id: 'version-3' };

function PreviewAttempt({ onAttempt }: { onAttempt: (attempt: Promise<void>) => void }) {
  const { pdfReadingOwner } = usePreviewContext();
  return (
    <button
      onClick={() => {
        if (!pdfReadingOwner) throw new Error('Missing real composer preview owner');
        const attempt = pdfReadingOwner.link(sourceC);
        void attempt.catch(() => undefined);
        onAttempt(attempt);
      }}
    >
      Attempt C through preview owner
    </button>
  );
}

function ComposerOwner({
  ownerId,
  frameId,
  onAttempt,
  staleHint,
}: {
  ownerId: string;
  frameId: string;
  onAttempt: (attempt: Promise<void>) => void;
  staleHint?: () => PdfReadingPendingMutation | undefined;
}) {
  const draft = useAcpSendBoxDraftController(frameId, ownerId);
  const reading = usePdfReadingComposer({
    enabled: true,
    ownerId,
    conversationId: frameId,
    frameIncarnationId: 'frame-inc',
    pending: draft.pdfReadingMutation,
    persistPending: draft.setPdfReadingMutation,
  });
  return (
    <>
      <PdfReadingComposerPrefix
        enabled
        reading={reading}
        initialSend={{ failed: false, retry: () => {} }}
        suggestion={{ visible: false, keepAttachments: () => {} }}
        frozenQueue={{ present: false, useCurrent: () => {} }}
        draftPersistence={{ failed: draft.draftSaveFailed, retry: draft.retryDraftSave }}
      >
        {null}
      </PdfReadingComposerPrefix>
      <output data-testid='reading-owner-activity'>
        {reading.view.loading || reading.view.mutating ? 'busy' : 'settled'}
      </output>
      <PreviewAttempt onAttempt={onAttempt} />
      <button onClick={() => reading.controller.restorePending(staleHint?.())}>Replay old render hint</button>
      <input
        aria-label='Ordinary draft text'
        value={draft.content}
        onChange={(event) => draft.setContent(event.target.value)}
      />
      <button
        onClick={() =>
          draft.setContextItems([
            { kind: 'artifact', artifactId: 'attachment-4', versionId: 'version-4', label: 'notes.md' },
          ])
        }
      >
        Add ordinary attachment
      </button>
    </>
  );
}

describe('C12 PDF pending owner lifecycle', () => {
  beforeEach(() => {
    localStorage.clear();
    resetRendererAccountScopeForTest();
    transport.load.mockReset();
    transport.mutate.mockReset();
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it.each(['text', 'attachment'] as const)(
    'preserves pending B through ordinary %s editing before A ACK and its delayed storage event',
    async (edit) => {
      const ownerId = `owner-ordinary-${edit}`;
      const frameId = `frame-ordinary-${edit}`;
      setRendererAccountOwner(ownerId);
      const initial = pdfContext([pdfBinding(), pdfBinding('2')]);
      initial.scope.frame_id = frameId;
      const late = deferredPdf<PdfReadingContext>();
      transport.load.mockResolvedValue(initial);
      transport.mutate.mockReturnValueOnce(late.promise);
      await renderWithI18n(
        <PreviewProvider>
          <ComposerOwner ownerId={ownerId} frameId={frameId} onAttempt={() => {}} />
        </PreviewProvider>,
        'en-US'
      );
      await screen.findByTestId('pdf-context-bar');
      fireEvent.click(screen.getByRole('button', { name: 'Remove PDF context paper-1.pdf' }));
      await waitFor(() => expect(transport.mutate).toHaveBeenCalledTimes(1));
      const pendingA = loadSendBoxDraft(ownerId, frameId)!.pdfReadingMutation!;
      const pendingB = {
        ...pendingA,
        request: {
          ...pendingA.request,
          operation_id: '00000000-0000-4000-8000-000000000002',
          expected_revision: 2,
          binding_id: 'binding-2',
        },
      };
      expect(
        saveSendBoxDraft(ownerId, frameId, {
          _type: 'acp',
          content: 'newer durable draft',
          atPath: [],
          uploadFile: [],
          contextItems: [],
          pdfReadingMutation: pendingB,
        })
      ).toBe(true);
      // The other tab's write is complete, but its storage event has not arrived.
      // Ordinary edits are allowed while a manual reading mutation is pending.
      if (edit === 'text')
        fireEvent.change(screen.getByRole('textbox', { name: 'Ordinary draft text' }), {
          target: { value: 'new typed text' },
        });
      else fireEvent.click(screen.getByRole('button', { name: 'Add ordinary attachment' }));
      const expectedText = edit === 'text' ? 'new typed text' : 'newer durable draft';
      expect
        .soft(loadSendBoxDraft(ownerId, frameId))
        .toMatchObject({ content: expectedText, pdfReadingMutation: pendingB });
      if (edit === 'attachment')
        expect
          .soft(loadSendBoxDraft(ownerId, frameId)?.contextItems)
          .toEqual([{ kind: 'artifact', artifactId: 'attachment-4', versionId: 'version-4', label: 'notes.md' }]);
      await act(async () => {
        late.resolve({ ...initial, revision: 2, changed: true, bindings: [pdfBinding('2')] });
        await late.promise;
      });
      await waitFor(() => expect(screen.getByTestId('reading-owner-activity')).toHaveTextContent('settled'));
      expect
        .soft(loadSendBoxDraft(ownerId, frameId))
        .toMatchObject({ content: expectedText, pdfReadingMutation: pendingB });
      transport.mutate.mockResolvedValueOnce({ ...initial, revision: 3, changed: true, bindings: [] });
      const retry = screen.queryByRole('button', { name: 'Retry' });
      expect.soft(retry).not.toBeNull();
      if (retry) {
        fireEvent.click(retry);
        await waitFor(() => expect(transport.mutate).toHaveBeenCalledTimes(2));
      }
      expect.soft(transport.mutate.mock.calls[1]).toEqual([frameId, pendingB.request]);
      expect.soft(loadSendBoxDraft(ownerId, frameId)?.content).toBe(expectedText);
    }
  );

  it.each(['read', 'write'] as const)(
    'keeps ordinary edits in memory without overwriting pending B when storage %s fails',
    async (failure) => {
      const ownerId = `owner-edit-failure-${failure}`;
      const frameId = `frame-edit-failure-${failure}`;
      setRendererAccountOwner(ownerId);
      const initial = pdfContext([pdfBinding(), pdfBinding('2')]);
      initial.scope.frame_id = frameId;
      const late = deferredPdf<PdfReadingContext>();
      transport.load.mockResolvedValue(initial);
      transport.mutate.mockReturnValueOnce(late.promise);
      await renderWithI18n(
        <PreviewProvider>
          <ComposerOwner ownerId={ownerId} frameId={frameId} onAttempt={() => {}} />
        </PreviewProvider>,
        'en-US'
      );
      await screen.findByTestId('pdf-context-bar');
      fireEvent.click(screen.getByRole('button', { name: 'Remove PDF context paper-1.pdf' }));
      await waitFor(() => expect(transport.mutate).toHaveBeenCalledTimes(1));
      const pendingA = loadSendBoxDraft(ownerId, frameId)!.pdfReadingMutation!;
      const pendingB = {
        ...pendingA,
        request: {
          ...pendingA.request,
          operation_id: '00000000-0000-4000-8000-000000000002',
          expected_revision: 2,
          binding_id: 'binding-2',
        },
      };
      expect(
        saveSendBoxDraft(ownerId, frameId, {
          _type: 'acp',
          content: 'durable text',
          atPath: [],
          uploadFile: ['/other-tab.pdf'],
          contextItems: [],
          pdfReadingMutation: pendingB,
        })
      ).toBe(true);
      const key = sendBoxDraftStorageKey(ownerId, frameId)!;
      const originalGet = Storage.prototype.getItem;
      const before = originalGet.call(localStorage, key);
      const originalSet = Storage.prototype.setItem;
      const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, name, value) {
        if (failure === 'write' && name === key) throw new Error('storage unavailable');
        originalSet.call(this, name, value);
      });
      const read = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, name) {
        if (failure === 'read' && name === key) throw new Error('storage unavailable');
        return originalGet.call(this, name);
      });
      fireEvent.change(screen.getByRole('textbox', { name: 'Ordinary draft text' }), {
        target: { value: 'unsaved new text' },
      });
      expect(screen.getByRole('textbox', { name: 'Ordinary draft text' })).toHaveValue('unsaved new text');
      expect(await screen.findByRole('button', { name: 'Retry saving draft' })).toBeEnabled();
      expect(
        screen.getByText('Draft changes are kept in this tab but could not be saved. Retry before closing.')
      ).toBeVisible();
      expect(originalGet.call(localStorage, key)).toBe(before);
      if (failure === 'read') expect(write.mock.calls.filter(([name]) => name === key)).toHaveLength(0);
      read.mockRestore();
      write.mockRestore();
      await act(async () => {
        late.resolve({ ...initial, revision: 2, changed: true, bindings: [pdfBinding('2')] });
        await late.promise;
      });
      await waitFor(() => expect(screen.getByTestId('reading-owner-activity')).toHaveTextContent('settled'));
      expect(screen.getByRole('textbox', { name: 'Ordinary draft text' })).toHaveValue('unsaved new text');
      expect(loadSendBoxDraft(ownerId, frameId)).toMatchObject({
        content: 'durable text',
        uploadFile: ['/other-tab.pdf'],
        pdfReadingMutation: pendingB,
      });
      fireEvent.click(screen.getByRole('button', { name: 'Retry saving draft' }));
      await waitFor(() => expect(screen.queryByRole('button', { name: 'Retry saving draft' })).toBeNull());
      expect(loadSendBoxDraft(ownerId, frameId)).toMatchObject({
        content: 'unsaved new text',
        uploadFile: ['/other-tab.pdf'],
        pdfReadingMutation: pendingB,
      });
      expect(transport.mutate).toHaveBeenCalledTimes(1);
      transport.mutate.mockResolvedValueOnce({ ...initial, revision: 3, changed: true, bindings: [] });
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      await waitFor(() => expect(transport.mutate).toHaveBeenCalledTimes(2));
      expect(transport.mutate.mock.calls[1]).toEqual([frameId, pendingB.request]);
      expect(loadSendBoxDraft(ownerId, frameId)?.content).toBe('unsaved new text');
    }
  );

  it.each([
    { settlement: 'ack', delivery: 'delivered' },
    { settlement: 'ack', delivery: 'delayed' },
    { settlement: 'refresh', delivery: 'delivered' },
    { settlement: 'refresh', delivery: 'delayed' },
  ] as const)(
    'retains pending B recovery and rejects link C after A $settlement with storage event $delivery',
    async ({ settlement, delivery }) => {
      const ownerId = `owner-${settlement}-${delivery}`;
      const frameId = `frame-${settlement}-${delivery}`;
      setRendererAccountOwner(ownerId);
      const initial = pdfContext([pdfBinding(), pdfBinding('2')]);
      initial.scope.frame_id = frameId;
      const late = deferredPdf<PdfReadingContext>();
      transport.load.mockResolvedValue(initial);
      transport.mutate.mockRejectedValue(new Error('A new C mutation must never reach transport'));
      if (settlement === 'ack') transport.mutate.mockReturnValueOnce(late.promise);
      else
        transport.mutate.mockRejectedValueOnce(
          new BackendHttpError({
            method: 'POST',
            path: '/reading-context/unlink',
            status: 409,
            body: { code: 'PDF_READING_REVISION_STALE', message: 'changed' },
          })
        );
      let attempted: Promise<void> | undefined;
      let pendingA: PdfReadingPendingMutation | undefined;
      await renderWithI18n(
        <PreviewProvider>
          <ComposerOwner
            ownerId={ownerId}
            frameId={frameId}
            staleHint={() => pendingA}
            onAttempt={(attempt) => {
              attempted = attempt;
            }}
          />
        </PreviewProvider>,
        'en-US'
      );
      await screen.findByTestId('pdf-context-bar');
      fireEvent.click(screen.getByRole('button', { name: 'Remove PDF context paper-1.pdf' }));
      await waitFor(() => expect(transport.mutate).toHaveBeenCalledTimes(1));
      const originalPending = loadSendBoxDraft(ownerId, frameId)!.pdfReadingMutation!;
      pendingA = originalPending;
      expect(originalPending.request.binding_id).toBe('binding-1');
      if (settlement === 'refresh') {
        await screen.findByRole('alert');
        transport.load.mockReturnValueOnce(late.promise);
        fireEvent.click(screen.getByRole('button', { name: 'Refresh choices' }));
        await waitFor(() => expect(transport.load).toHaveBeenCalledTimes(2));
      }
      const pendingB = {
        ...originalPending,
        request: {
          ...originalPending.request,
          operation_id: '00000000-0000-4000-8000-000000000002',
          expected_revision: 2,
          binding_id: 'binding-2',
        },
      };
      expect(
        saveSendBoxDraft(ownerId, frameId, {
          _type: 'acp',
          content: 'retain original B recovery',
          atPath: [],
          uploadFile: [],
          contextItems: [],
          pdfReadingMutation: pendingB,
        })
      ).toBe(true);
      if (delivery === 'delivered')
        act(() => {
          window.dispatchEvent(
            new StorageEvent('storage', {
              key: sendBoxDraftStorageKey(ownerId, frameId),
              storageArea: localStorage,
              newValue: localStorage.getItem(sendBoxDraftStorageKey(ownerId, frameId)!),
            })
          );
        });
      await act(async () => {
        late.resolve({ ...initial, revision: 2, changed: settlement === 'ack', bindings: [pdfBinding('2')] });
        await late.promise;
      });
      await waitFor(() => expect(screen.getByTestId('reading-owner-activity')).toHaveTextContent('settled'));
      expect(loadSendBoxDraft(ownerId, frameId)?.pdfReadingMutation).toEqual(pendingB);
      // A visible recovery or explicit blocking explanation must survive A's cleanup.
      expect.soft(screen.queryByRole('alert')).not.toBeNull();
      // Exercise the real borrowed port as well as the UI: a disabled presentation
      // alone is not a reservation check, and must not permit C to replace B.
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Attempt C through preview owner' }));
        await attempted?.catch(() => undefined);
      });
      expect.soft(transport.mutate).toHaveBeenCalledTimes(1);
      expect.soft(loadSendBoxDraft(ownerId, frameId)).toMatchObject({
        content: 'retain original B recovery',
        pdfReadingMutation: pendingB,
      });
      transport.mutate.mockResolvedValueOnce({ ...initial, revision: 3, changed: true, bindings: [] });
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
      await waitFor(() => expect(transport.mutate).toHaveBeenCalledTimes(2));
      expect(transport.mutate.mock.calls[1]).toEqual([frameId, pendingB.request]);
      await waitFor(() => expect(loadSendBoxDraft(ownerId, frameId)?.pdfReadingMutation).toBeUndefined());
      fireEvent.click(screen.getByRole('button', { name: 'Replay old render hint' }));
      expect(screen.queryByRole('alert')).toBeNull();
      expect(loadSendBoxDraft(ownerId, frameId)?.pdfReadingMutation).toBeUndefined();
      transport.mutate.mockResolvedValueOnce({ ...initial, revision: 4, changed: true, bindings: [pdfBinding('3')] });
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Attempt C through preview owner' }));
        await attempted;
      });
      expect(transport.mutate).toHaveBeenCalledTimes(3);
      expect(transport.mutate.mock.calls[2][1]).toMatchObject({ expected_revision: 3, sources: [sourceC] });
      expect(loadSendBoxDraft(ownerId, frameId)?.content).toBe('retain original B recovery');
    }
  );

  it.each(['read', 'write'] as const)(
    'rejects unavailable initial pending storage before HTTP when %s fails',
    async (failure) => {
      const ownerId = `owner-storage-${failure}`;
      const frameId = `frame-storage-${failure}`;
      setRendererAccountOwner(ownerId);
      const initial = pdfContext();
      initial.scope.frame_id = frameId;
      transport.load.mockResolvedValue(initial);
      let attempted: Promise<void> | undefined;
      await renderWithI18n(
        <PreviewProvider>
          <ComposerOwner
            ownerId={ownerId}
            frameId={frameId}
            onAttempt={(attempt) => {
              attempted = attempt;
            }}
          />
        </PreviewProvider>,
        'en-US'
      );
      await screen.findByTestId('pdf-context-bar');
      const key = sendBoxDraftStorageKey(ownerId, frameId)!;
      if (failure === 'read') {
        const original = Storage.prototype.getItem;
        vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, name) {
          if (name === key) throw new Error('storage read unavailable');
          return original.call(this, name);
        });
      } else {
        const original = Storage.prototype.setItem;
        vi.spyOn(Storage.prototype, 'setItem').mockImplementation(function (this: Storage, name, value) {
          if (name === key) throw new Error('storage write unavailable');
          original.call(this, name, value);
        });
      }
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: 'Attempt C through preview owner' }));
        await expect(attempted).rejects.toMatchObject({ code: 'PDF_READING_DRAFT_UNAVAILABLE' });
      });
      expect(transport.mutate).not.toHaveBeenCalled();
      expect(screen.getByRole('alert')).toHaveTextContent('Could not update PDF reading context');
      expect(screen.getByRole('button', { name: 'Choose PDFs for Reading' })).toBeDisabled();
    }
  );
});

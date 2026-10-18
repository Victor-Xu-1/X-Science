import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { getSendBoxDraftHook } from '@/renderer/hooks/chat/useSendBoxDraft';
import { clearSendBoxDraft, loadSendBoxDraft, saveSendBoxDraft } from '@/renderer/hooks/chat/sendBoxDraftPersistence';

const initial = { _type: 'acp' as const, content: '', atPath: [], uploadFile: [], contextItems: [] };
const useDraft = getSendBoxDraftHook('acp', initial);
const pendingFor = (ownerId: string, frameId: string) => ({
  ownerId,
  frameId,
  request: {
    schema: 'x-science.pdf-reading-unlink.v1' as const,
    operation_id: '00000000-0000-4000-8000-000000000001',
    expected_frame_incarnation_id: 'frame-inc',
    expected_root_frame_id: 'root',
    expected_root_frame_incarnation_id: 'root-inc',
    expected_revision: 1,
    binding_id: 'binding-1',
  },
});

describe('ordinary draft reserved fields', () => {
  it('does not resurrect cached pending A after durable absence on ordinary save', async () => {
    const ownerId = 'owner-ordinary-absence';
    const frameId = 'frame-ordinary-absence';
    const pendingA = pendingFor(ownerId, frameId);
    expect(saveSendBoxDraft(ownerId, frameId, { ...initial, content: 'cached', pdfReadingMutation: pendingA })).toBe(
      true
    );
    const draft = renderHook(() => useDraft(frameId, ownerId));
    try {
      await waitFor(() => expect(draft.result.current.data?.pdfReadingMutation).toEqual(pendingA));
      clearSendBoxDraft(ownerId, frameId);
      // No storage event. Even an ordinary caller carrying A cannot restore it.
      act(() =>
        draft.result.current.mutate((previous) => ({ ...previous, content: 'new text', pdfReadingMutation: pendingA }))
      );
      await waitFor(() => expect(draft.result.current.data?.content).toBe('new text'));
      expect(draft.result.current.data?.pdfReadingMutation).toBeUndefined();
      expect(loadSendBoxDraft(ownerId, frameId)).toMatchObject({ content: 'new text' });
      expect(loadSendBoxDraft(ownerId, frameId)?.pdfReadingMutation).toBeUndefined();
    } finally {
      draft.unmount();
    }
  });

  it('clears ordinary fields without removing a newer durable pending B', async () => {
    const ownerId = 'owner-ordinary-clear';
    const frameId = 'frame-ordinary-clear';
    const pendingA = pendingFor(ownerId, frameId);
    expect(saveSendBoxDraft(ownerId, frameId, { ...initial, content: 'cached', pdfReadingMutation: pendingA })).toBe(
      true
    );
    const draft = renderHook(() => useDraft(frameId, ownerId));
    try {
      await waitFor(() => expect(draft.result.current.data?.pdfReadingMutation).toEqual(pendingA));
      const pendingB = {
        ...pendingA,
        request: { ...pendingA.request, operation_id: '00000000-0000-4000-8000-000000000002', binding_id: 'binding-2' },
      };
      expect(
        saveSendBoxDraft(ownerId, frameId, {
          ...initial,
          content: 'other tab',
          uploadFile: ['/new.pdf'],
          pdfReadingMutation: pendingB,
        })
      ).toBe(true);
      act(() => draft.result.current.mutate(() => undefined));
      await waitFor(() => expect(draft.result.current.data).toEqual({ ...initial, pdfReadingMutation: pendingB }));
      expect(loadSendBoxDraft(ownerId, frameId)).toEqual({ ...initial, pdfReadingMutation: pendingB });
      act(() =>
        draft.result.current.mutate((previous) => ({
          ...previous,
          content: 'after clear',
          pdfReadingMutation: undefined,
        }))
      );
      expect(loadSendBoxDraft(ownerId, frameId)).toMatchObject({
        content: 'after clear',
        pdfReadingMutation: pendingB,
      });
      act(() =>
        draft.result.current.mutate((previous) => {
          if (previous.pdfReadingMutation) previous.pdfReadingMutation.request.expected_revision = 99;
          return previous;
        })
      );
      expect(loadSendBoxDraft(ownerId, frameId)?.pdfReadingMutation).toEqual(pendingB);
    } finally {
      draft.unmount();
    }
  });
});

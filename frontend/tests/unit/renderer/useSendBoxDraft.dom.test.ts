import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { getSendBoxDraftHook } from '@/renderer/hooks/chat/useSendBoxDraft';
import { pdfSelection } from './conversation/pdfReadingFixtures';
import {
  clearSendBoxDraft,
  loadSendBoxDraft,
  saveSendBoxDraft,
  sendBoxDraftStorageKey,
} from '@/renderer/hooks/chat/sendBoxDraftPersistence';

const initialDraft = {
  _type: 'acp' as const,
  content: '',
  atPath: [],
  uploadFile: [],
  contextItems: [],
};

describe('useSendBoxDraft', () => {
  it('C12 persists pending exact mutation and edited queued selection only for their original owner', () => {
    const pending = {
      ownerId: 'owner-pdf',
      frameId: 'draft-pdf',
      request: {
        schema: 'x-science.pdf-reading-unlink.v1' as const,
        operation_id: '00000000-0000-4000-8000-000000000001',
        expected_frame_incarnation_id: 'frame-inc',
        expected_root_frame_id: 'root',
        expected_root_frame_incarnation_id: 'root-inc',
        expected_revision: 1,
        binding_id: 'binding-1',
      },
    };
    expect(
      saveSendBoxDraft('owner-pdf', 'draft-pdf', {
        ...initialDraft,
        content: 'keep this message',
        pdfReadingMutation: pending,
        pdfReadingQueuedSelection: pdfSelection(),
        pdfReadingAttachmentsDisabled: true,
      })
    ).toBe(true);
    expect(loadSendBoxDraft('owner-pdf', 'draft-pdf')).toMatchObject({
      content: 'keep this message',
      pdfReadingMutation: pending,
      pdfReadingQueuedSelection: pdfSelection(),
      pdfReadingAttachmentsDisabled: true,
    });
    expect(loadSendBoxDraft('other-owner', 'draft-pdf')).toBeUndefined();
  });

  beforeEach(() => window.localStorage.clear());

  it('restores a durable owner-scoped draft after remount and removes it when cleared', async () => {
    const useDraft = getSendBoxDraftHook('acp', initialDraft);
    const conversationId = 'draft-persistence-test';
    const first = renderHook(() => useDraft(conversationId, 'owner-a'));

    act(() => {
      first.result.current.mutate((draft) => ({
        ...draft,
        content: 'saved draft',
        uploadFile: ['/private/upload.csv'],
        contextItems: [
          { kind: 'artifact', artifactId: 'artifact-1', versionId: 'version-1', label: 'report.md' },
          { kind: 'skill', name: 'autodock-vina', label: 'autodock-vina' },
          { kind: 'mcp', serverId: 'pubmed', label: 'PubMed' },
        ],
      }));
    });
    await waitFor(() => expect(first.result.current.data?.content).toBe('saved draft'));
    expect(loadSendBoxDraft('owner-a', conversationId)).toEqual(first.result.current.data);
    first.unmount();

    const second = renderHook(() => useDraft(conversationId, 'owner-a'));
    await waitFor(() => expect(second.result.current.data?.content).toBe('saved draft'));

    act(() => {
      second.result.current.mutate(() => undefined);
    });
    await waitFor(() => expect(second.result.current.data).toBeUndefined());
    expect(loadSendBoxDraft('owner-a', conversationId)).toBeUndefined();
  });

  it('chains same-tick draft mutations without restoring stale content', async () => {
    const useDraft = getSendBoxDraftHook('acp', initialDraft);
    const draft = renderHook(() => useDraft('draft-same-tick-update-test', 'owner-a'));

    act(() => {
      draft.result.current.mutate((current) => ({
        ...current,
        content: 'message to send',
        atPath: ['/workspace/reference.txt'],
        uploadFile: ['/tmp/upload.txt'],
      }));
    });
    await waitFor(() => expect(draft.result.current.data?.content).toBe('message to send'));

    act(() => {
      draft.result.current.mutate((current) => ({ ...current, content: '' }));
      draft.result.current.mutate((current) => ({ ...current, atPath: [], uploadFile: [] }));
    });

    await waitFor(() => {
      expect(draft.result.current.data).toMatchObject({
        content: '',
        atPath: [],
        uploadFile: [],
      });
    });
    expect(loadSendBoxDraft('owner-a', 'draft-same-tick-update-test')).toBeUndefined();
  });

  it('replaces only the expected persisted draft and preserves newer user edits', async () => {
    const useDraft = getSendBoxDraftHook('acp', initialDraft);
    const conversationId = 'draft-compare-and-set-test';
    const draft = renderHook(() => useDraft(conversationId, 'owner-a'));

    act(() => {
      draft.result.current.mutate((current) => ({ ...current, content: 'original' }));
    });
    await waitFor(() => expect(draft.result.current.data?.content).toBe('original'));

    let replaced = false;
    act(() => {
      replaced = draft.result.current.replaceContentIfUnchanged('original', 'optimized');
    });
    expect(replaced).toBe(true);
    expect(loadSendBoxDraft('owner-a', conversationId)?.content).toBe('optimized');

    act(() => {
      draft.result.current.mutate((current) => ({ ...current, content: 'later edit' }));
      replaced = draft.result.current.replaceContentIfUnchanged('optimized', 'original');
    });
    expect(replaced).toBe(false);
    expect(loadSendBoxDraft('owner-a', conversationId)?.content).toBe('later edit');
  });

  it('rejects a stale cross-tab draft and preserves other-tab attachments', async () => {
    const useDraft = getSendBoxDraftHook('acp', initialDraft);
    const conversationId = 'draft-cross-tab-test';
    const draft = renderHook(() => useDraft(conversationId, 'owner-a'));
    act(() => {
      draft.result.current.mutate((current) => ({ ...current, content: 'original' }));
    });
    await waitFor(() => expect(draft.result.current.data?.content).toBe('original'));

    expect(
      saveSendBoxDraft('owner-a', conversationId, {
        ...initialDraft,
        content: 'other tab edit',
        uploadFile: ['/workspace/new-attachment.csv'],
      })
    ).toBe(true);
    expect(draft.result.current.replaceContentIfUnchanged('original', 'optimized')).toBe(false);
    expect(loadSendBoxDraft('owner-a', conversationId)).toMatchObject({
      content: 'other tab edit',
      uploadFile: ['/workspace/new-attachment.csv'],
    });
  });

  it('isolates drafts by owner and rejects malformed stored state', () => {
    expect(
      saveSendBoxDraft('owner-a', 'shared-conversation', {
        ...initialDraft,
        content: 'owner-a only',
        contextItems: [{ kind: 'skill', name: 'autodock-vina', label: 'autodock-vina' }],
      })
    ).toBe(true);
    expect(loadSendBoxDraft('owner-a', 'shared-conversation')?.content).toBe('owner-a only');
    expect(loadSendBoxDraft('owner-b', 'shared-conversation')).toBeUndefined();

    const malformedKey = sendBoxDraftStorageKey('owner-b', 'shared-conversation');
    expect(malformedKey).not.toBeNull();
    window.localStorage.setItem(malformedKey!, '{');
    expect(loadSendBoxDraft('owner-b', 'shared-conversation')).toBeUndefined();
    expect(window.localStorage.getItem(malformedKey!)).toBeNull();

    clearSendBoxDraft('owner-a', 'shared-conversation');
    expect(loadSendBoxDraft('owner-a', 'shared-conversation')).toBeUndefined();
  });
});

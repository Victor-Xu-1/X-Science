/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const sendMessageInvokeMock = vi.fn();
const setComputeProviderMock = vi.fn();

vi.mock('@/common', () => ({
  ipcBridge: {
    acpConversation: {
      sendMessage: {
        invoke: (...args: unknown[]) => sendMessageInvokeMock(...args),
      },
    },
  },
}));

vi.mock('@/renderer/services/xScienceCompute', () => ({
  setXScienceSessionComputeProvider: (...args: unknown[]) => setComputeProviderMock(...args),
}));

vi.mock('@/renderer/utils/file/messageFiles', () => ({
  buildDisplayMessage: (input: string) => input,
}));

vi.mock('@/renderer/utils/emitter', () => ({
  emitter: { emit: vi.fn() },
}));

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string) => (key === 'common.unknownError' ? 'Unknown error' : key),
  }),
}));

import { useAcpInitialMessage } from '@/renderer/pages/conversation/platforms/acp/useAcpInitialMessage';
import { pdfSelection } from './pdfReadingFixtures';

const createDeferred = <T>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
};

const createParams = (conversationId: string, streamReady = true) => ({
  conversation_id: conversationId,
  backend: 'xscience',
  workspacePath: '',
  streamReady,
  setAiProcessing: vi.fn(),
  resetState: vi.fn(),
  markSendStarted: vi.fn(),
  markSendAccepted: vi.fn(),
  markSendFailed: vi.fn(),
  checkAndUpdateTitle: vi.fn(),
  addOrUpdateMessage: vi.fn(),
});

describe('useAcpInitialMessage', () => {
  it('C12 prepares the original uploaded versions before first send and retries lost ACK with identical selection', async () => {
    const conversationId = 'initial-pdf-reading';
    const storageKey = `acp_initial_message_${conversationId}`;
    const sources = [{ artifact_id: 'artifact-1', version_id: 'version-1' }];
    sessionStorage.setItem(
      storageKey,
      JSON.stringify({
        input: 'read this page',
        loading_id: 'original-reading-input',
        pdf_reading_owner_id: 'owner',
        pdf_reading_sources: sources,
      })
    );
    const preparation = createDeferred<ReturnType<typeof pdfSelection>>();
    const prepareReadingSources = vi.fn(() => preparation.promise);
    sendMessageInvokeMock
      .mockRejectedValueOnce(new Error('lost response'))
      .mockResolvedValue({ turn_id: conversationId, msg_id: 'canonical', runtime: {} });
    const params = { ...createParams(conversationId), ownerId: 'owner', prepareReadingSources };
    const { result } = renderHook(() => useAcpInitialMessage(params));
    await vi.waitFor(() => expect(prepareReadingSources).toHaveBeenCalledWith(sources, undefined));
    expect(params.markSendStarted).toHaveBeenCalledTimes(1);
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    await act(async () => {
      preparation.resolve(pdfSelection());
      await preparation.promise;
    });
    await vi.waitFor(() => expect(result.current.failed).toBe(true));
    const submitted = structuredClone(sendMessageInvokeMock.mock.calls[0][0]);
    expect(result.current.canKeepPdfAttachments).toBe(false);
    expect(submitted).toMatchObject({ loading_id: 'original-reading-input', reading_context: pdfSelection() });
    expect(JSON.parse(sessionStorage.getItem(storageKey)!)).toMatchObject({
      reading_context: pdfSelection(),
      pdf_reading_prepared: true,
    });
    await act(async () => {
      result.current.retry();
    });
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(2));
    expect(sendMessageInvokeMock.mock.calls[1][0]).toEqual(submitted);
    expect(prepareReadingSources).toHaveBeenCalledTimes(1);
    await vi.waitFor(() => expect(sessionStorage.getItem(storageKey)).toBeNull());
  });

  it('C12 cannot send a PDF handoff belonging to another owner', async () => {
    const conversationId = 'initial-foreign-pdf';
    const key = `acp_initial_message_${conversationId}`;
    const original = JSON.stringify({
      input: 'private original',
      loading_id: 'private-input',
      pdf_reading_owner_id: 'original-owner',
      pdf_reading_sources: [{ artifact_id: 'artifact-1', version_id: 'version-1' }],
    });
    sessionStorage.setItem(key, original);
    const prepareReadingSources = vi.fn();
    const { result } = renderHook(() =>
      useAcpInitialMessage({ ...createParams(conversationId), ownerId: 'other-owner', prepareReadingSources })
    );
    await vi.waitFor(() => expect(result.current.failed).toBe(true));
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    expect(prepareReadingSources).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(key)).toBe(original);
  });

  it('C12 allows explicit ordinary-attachment fallback only before the first message submission', async () => {
    const conversationId = 'initial-unavailable-pdf';
    const key = `acp_initial_message_${conversationId}`;
    sessionStorage.setItem(
      key,
      JSON.stringify({
        input: 'use this attachment',
        loading_id: 'original-unsent-input',
        pdf_reading_owner_id: 'owner',
        pdf_reading_sources: [{ artifact_id: 'a', version_id: 'v' }],
        artifact_refs: [{ artifact_id: 'a', version_id: 'v', relation: 'attached' }],
      })
    );
    const prepareReadingSources = vi.fn().mockRejectedValue(new Error('inspection unavailable'));
    sendMessageInvokeMock.mockResolvedValue({ turn_id: conversationId, msg_id: 'accepted', runtime: {} });
    const { result } = renderHook(() =>
      useAcpInitialMessage({
        ...createParams(conversationId),
        ownerId: 'owner',
        prepareReadingSources,
        canDiscardReadingPreparation: () => true,
      })
    );
    await vi.waitFor(() => expect(result.current.canKeepPdfAttachments).toBe(true));
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    await act(async () => {
      result.current.keepPdfAttachments();
    });
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    expect(sendMessageInvokeMock.mock.calls[0][0]).toMatchObject({
      loading_id: 'original-unsent-input',
      artifact_refs: [{ artifact_id: 'a', version_id: 'v', relation: 'attached' }],
    });
    expect(sendMessageInvokeMock.mock.calls[0][0]).not.toHaveProperty('reading_context');
    expect(prepareReadingSources).toHaveBeenCalledTimes(1);
  });
  it('C12 never rewrites the original handoff while a link mutation acknowledgement remains unknown', async () => {
    const conversationId = 'initial-unknown-pdf-link';
    const key = `acp_initial_message_${conversationId}`;
    const original = JSON.stringify({
      input: 'original',
      loading_id: 'unknown-link-input',
      pdf_reading_owner_id: 'owner',
      pdf_reading_sources: [{ artifact_id: 'a', version_id: 'v' }],
    });
    sessionStorage.setItem(key, original);
    const { result } = renderHook(() =>
      useAcpInitialMessage({
        ...createParams(conversationId),
        ownerId: 'owner',
        prepareReadingSources: vi.fn().mockRejectedValue(new Error('link response lost')),
        canDiscardReadingPreparation: () => false,
      })
    );
    await vi.waitFor(() => expect(result.current.failed).toBe(true));
    act(() => result.current.keepPdfAttachments());
    expect(sessionStorage.getItem(key)).toBe(original);
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
  });

  beforeEach(() => {
    sessionStorage.clear();
    sendMessageInvokeMock.mockReset();
    setComputeProviderMock.mockReset();
    setComputeProviderMock.mockResolvedValue(undefined);
  });

  it('waits for realtime subscriptions before sending the routed initial message', async () => {
    const conversationId = 'initial-stream-gate';
    const storageKey = `acp_initial_message_${conversationId}`;
    const stored = JSON.stringify({ input: 'render this without refresh' });
    sessionStorage.setItem(storageKey, stored);
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: 'turn-stream-gate',
      msg_id: 'msg-stream-gate',
      runtime: { backend: 'xscience', status: 'running' },
    });

    const params = createParams(conversationId, false);
    const { rerender } = renderHook(({ streamReady }) => useAcpInitialMessage({ ...params, streamReady }), {
      initialProps: { streamReady: false },
    });

    await act(async () => Promise.resolve());
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(storageKey)).toBe(stored);

    rerender({ streamReady: true });
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(sessionStorage.getItem(storageKey)).toBeNull());

    rerender({ streamReady: true });
    await act(async () => Promise.resolve());
    expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1);
  });

  it('settles a terminal input receipt immediately without waiting for another SSE', async () => {
    const conversationId = 'initial-terminal-receipt';
    const ticket = {};
    const receipt = {
      version: 1,
      frame_id: conversationId,
      frame_incarnation_id: 'incarnation',
      input_message_id: 'original-input',
      request_sha256: 'a'.repeat(64),
      replayed: true,
      state: 'accepted',
      source: 'transcript',
      event_id: 'original-event',
      input_revision: 2,
      branch_id: 'branch',
      branch_generation: 1,
    };
    const runtime = {
      state: 'idle',
      can_send_message: true,
      has_task: false,
      task_status: 'finished',
      is_processing: false,
      pending_confirmations: 0,
      turn_id: null,
    };
    sessionStorage.setItem(
      `acp_initial_message_${conversationId}`,
      JSON.stringify({ input: 'already accepted', loading_id: 'original-input' })
    );
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: conversationId,
      msg_id: 'original-input',
      runtime,
      input_receipt: receipt,
    });
    const params = createParams(conversationId);
    params.markSendStarted.mockReturnValue(ticket);
    params.markSendAccepted.mockReturnValue({ isProcessing: false, state: 'idle' });
    renderHook(() => useAcpInitialMessage(params));
    await vi.waitFor(() =>
      expect(params.markSendAccepted).toHaveBeenCalledWith(conversationId, runtime, 'original-input', {
        ticket,
        receipt,
      })
    );
    expect(params.setAiProcessing).toHaveBeenLastCalledWith(false);
    expect(sessionStorage.getItem(`acp_initial_message_${conversationId}`)).toBeNull();
  });

  it('does not apply a late initial acknowledgement after the conversation unmounts', async () => {
    const conversationId = 'initial-unmounted-ack';
    const storageKey = `acp_initial_message_${conversationId}`;
    const stored = JSON.stringify({ input: 'preserve unmounted draft', loading_id: 'unmounted-input' });
    const pending = createDeferred<unknown>();
    sessionStorage.setItem(storageKey, stored);
    sendMessageInvokeMock.mockReturnValue(pending.promise);
    const params = createParams(conversationId);
    const { unmount } = renderHook(() => useAcpInitialMessage(params));
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    unmount();
    await act(async () => {
      pending.resolve({ turn_id: conversationId, msg_id: 'unmounted-input', runtime: {} });
      await pending.promise;
    });
    expect(params.markSendAccepted).not.toHaveBeenCalled();
    expect(params.addOrUpdateMessage).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(storageKey)).toBe(stored);
  });

  it('keeps automatic review off when an initial-message payload does not explicitly enable it', async () => {
    const conversationId = 'initial-review-default-off';
    sessionStorage.setItem(
      `acp_initial_message_${conversationId}`,
      JSON.stringify({ input: 'start task', session_options: { ultra_mode: false } })
    );
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: 'turn-review-default-off',
      msg_id: 'msg-review-default-off',
      runtime: { backend: 'xscience', status: 'running' },
    });

    renderHook(() => useAcpInitialMessage(createParams(conversationId)));
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        session_options: expect.objectContaining({ verifier_mode: 'off' }),
      })
    );
  });

  it('forwards validated project artifact references with the first message', async () => {
    const conversationId = 'initial-artifact-refs';
    sessionStorage.setItem(
      `acp_initial_message_${conversationId}`,
      JSON.stringify({
        input: 'review the attached results',
        artifact_refs: [
          {
            artifact_id: 'artifact-1',
            version_id: 'version-1',
            relation: 'attached',
            availability: 'available',
            filename: 'results.csv',
            size_bytes: 128,
          },
          { artifact_id: 42, version_id: 'ignored' },
        ],
      })
    );
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: 'turn-artifact-refs',
      msg_id: 'msg-artifact-refs',
      runtime: { backend: 'xscience', status: 'running' },
    });

    renderHook(() => useAcpInitialMessage(createParams(conversationId)));
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));

    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        artifact_refs: [
          {
            artifact_id: 'artifact-1',
            version_id: 'version-1',
            relation: 'attached',
            availability: 'available',
            filename: 'results.csv',
            size_bytes: 128,
          },
        ],
      })
    );
  });

  it('forwards bounded fixed Skill and MCP selections with the first message', async () => {
    const conversationId = 'initial-fixed-capabilities';
    sessionStorage.setItem(
      `acp_initial_message_${conversationId}`,
      JSON.stringify({
        input: 'use the selected capabilities',
        inject_skills: [' autodock-vina ', 'autodock-vina', '', 42],
        inject_mcp_server_ids: [' bundled:pubmed ', 'bundled:pubmed', null],
      })
    );
    sendMessageInvokeMock.mockResolvedValue({
      turn_id: 'turn-fixed-capabilities',
      msg_id: 'msg-fixed-capabilities',
      runtime: { backend: 'xscience', status: 'running' },
    });

    renderHook(() => useAcpInitialMessage(createParams(conversationId)));
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));

    expect(sendMessageInvokeMock).toHaveBeenCalledWith(
      expect.objectContaining({
        inject_skills: ['autodock-vina'],
        inject_mcp_server_ids: ['bundled:pubmed'],
      })
    );
  });

  it('keeps the draft while the first send is pending and removes it only after runtime acceptance', async () => {
    const deferred = createDeferred<{
      turn_id: string;
      msg_id: string;
      runtime: { backend: string; status: string };
    }>();
    sendMessageInvokeMock.mockReturnValue(deferred.promise);
    const conversationId = 'initial-pending';
    const storageKey = `acp_initial_message_${conversationId}`;
    const stored = JSON.stringify({
      input: 'hello',
      loading_id: 'initial-pending-once',
      compute_providers: ['local'],
      session_options: {
        ultra_mode: true,
        plan_mode: true,
        verifier_mode: 'off',
        memory_mode: 'on',
        target_agent: 'AIDD_EXPERT',
        model: 'primary-model',
        subagent_model: 'delegate-model',
        effort: 'high',
      },
    });
    sessionStorage.setItem(storageKey, stored);
    const params = createParams(conversationId);

    renderHook(() => useAcpInitialMessage(params));
    await vi.waitFor(() => expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1));
    expect(sessionStorage.getItem(storageKey)).toBe(stored);
    expect(sendMessageInvokeMock).toHaveBeenCalledWith({
      input: 'hello',
      loading_id: 'initial-pending-once',
      conversation_id: conversationId,
      files: [],
      session_options: {
        ultra_mode: true,
        plan_mode: true,
        verifier_mode: 'off',
        memory_mode: 'on',
        target_agent: 'AIDD_EXPERT',
        model: 'primary-model',
        subagent_model: 'delegate-model',
        effort: 'high',
      },
    });

    await act(async () => {
      deferred.resolve({
        turn_id: 'turn-1',
        msg_id: 'msg-1',
        runtime: { backend: 'xscience', status: 'running' },
      });
      await deferred.promise;
    });

    await vi.waitFor(() => expect(sessionStorage.getItem(storageKey)).toBeNull());
    expect(params.markSendAccepted).toHaveBeenCalledWith(
      'turn-1',
      { backend: 'xscience', status: 'running' },
      'msg-1',
      {
        ticket: undefined,
        receipt: undefined,
      }
    );
    expect(setComputeProviderMock).toHaveBeenCalledWith(conversationId, 'local', true);
  });

  it('retains the first-message draft after a runtime failure and does not duplicate the send on rerender', async () => {
    const conversationId = 'initial-failure';
    const storageKey = `acp_initial_message_${conversationId}`;
    const stored = JSON.stringify({ input: 'retry me', loading_id: 'initial-failure-once' });
    sessionStorage.setItem(storageKey, stored);
    sendMessageInvokeMock.mockRejectedValue(new Error('runtime unavailable'));
    const params = createParams(conversationId);

    const { rerender } = renderHook(() => useAcpInitialMessage(params));
    await vi.waitFor(() => expect(params.markSendFailed).toHaveBeenCalledWith('Unknown error', undefined));

    rerender();
    await Promise.resolve();

    expect(sendMessageInvokeMock).toHaveBeenCalledTimes(1);
    expect(sendMessageInvokeMock).toHaveBeenCalledWith(expect.objectContaining({ loading_id: 'initial-failure-once' }));
    expect(sessionStorage.getItem(storageKey)).toBe(stored);
    expect(params.addOrUpdateMessage).toHaveBeenCalledTimes(1);
  });

  it('stages draft_only payloads into the composer without sending anything', async () => {
    const conversationId = 'initial-staged-draft';
    const storageKey = `acp_initial_message_${conversationId}`;
    sessionStorage.setItem(
      storageKey,
      JSON.stringify({
        input: 'stage me, do not send',
        files: ['workspace/notes.md'],
        artifact_refs: [
          {
            artifact_id: 'artifact-stage',
            version_id: 'version-stage',
            relation: 'attached',
            availability: 'available',
            filename: 'cohort.csv',
            size_bytes: 64,
          },
        ],
        inject_skills: ['literature'],
        inject_mcp_server_ids: ['bundled:pubmed'],
        compute_providers: ['local'],
        session_options: {
          ultra_mode: true,
          verifier_mode: 'on',
          memory_mode: 'on',
          target_agent: 'AIDD_EXPERT',
          plan_mode: true,
        },
        draft_only: true,
      })
    );
    const params = createParams(conversationId);
    const onDraftPrefill = vi.fn();

    renderHook(() => useAcpInitialMessage({ ...params, onDraftPrefill }));

    await vi.waitFor(() => expect(onDraftPrefill).toHaveBeenCalledTimes(1));
    expect(onDraftPrefill).toHaveBeenCalledWith({
      input: 'stage me, do not send',
      files: ['workspace/notes.md'],
      artifactRefs: [
        {
          artifact_id: 'artifact-stage',
          version_id: 'version-stage',
          relation: 'attached',
          availability: 'available',
          filename: 'cohort.csv',
          size_bytes: 64,
        },
      ],
      injectSkills: ['literature'],
      injectMcpServerIds: ['bundled:pubmed'],
      sessionOptions: {
        delegation: true,
        autoReview: true,
        memory: true,
        targetAgent: 'AIDD_EXPERT',
      },
      planMode: true,
    });
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    await vi.waitFor(() => expect(sessionStorage.getItem(storageKey)).toBeNull());
    expect(params.markSendStarted).not.toHaveBeenCalled();
    expect(params.setAiProcessing).not.toHaveBeenCalled();
    expect(setComputeProviderMock).toHaveBeenCalledWith(conversationId, 'local', true);
  });

  it('retains the queued draft when compute provider restoration fails and reports the issue', async () => {
    const conversationId = 'initial-staged-provider-failure';
    const storageKey = `acp_initial_message_${conversationId}`;
    const stored = JSON.stringify({
      input: 'staged with failing compute',
      draft_only: true,
      compute_providers: ['broken'],
    });
    sessionStorage.setItem(storageKey, stored);
    setComputeProviderMock.mockRejectedValue(new Error('provider unavailable'));
    const params = createParams(conversationId);
    const onDraftPrefill = vi.fn();
    const onDraftRestoreIssue = vi.fn();

    renderHook(() => useAcpInitialMessage({ ...params, onDraftPrefill, onDraftRestoreIssue }));

    // The composer draft is applied before the asynchronous restore so the
    // staged task itself never depends on provider availability.
    await vi.waitFor(() => expect(onDraftPrefill).toHaveBeenCalledTimes(1));
    expect(onDraftPrefill).toHaveBeenCalledWith({
      input: 'staged with failing compute',
      files: [],
      artifactRefs: [],
      injectSkills: [],
      injectMcpServerIds: [],
      sessionOptions: null,
      planMode: false,
    });
    await vi.waitFor(() => expect(onDraftRestoreIssue).toHaveBeenCalledWith({ providers: ['broken'] }));
    expect(sendMessageInvokeMock).not.toHaveBeenCalled();
    // The queued payload survives so a later mount can retry the restore.
    expect(sessionStorage.getItem(storageKey)).toBe(stored);
  });

  it('keeps the queued draft when the effect is torn down mid restore and retries on remount', async () => {
    const conversationId = 'initial-staged-unmount';
    const storageKey = `acp_initial_message_${conversationId}`;
    const stored = JSON.stringify({
      input: 'staged across unmount',
      draft_only: true,
      compute_providers: ['local'],
    });
    sessionStorage.setItem(storageKey, stored);
    const gate = createDeferred<void>();
    setComputeProviderMock.mockImplementation(() => gate.promise);
    const params = createParams(conversationId);
    const onDraftPrefill = vi.fn();
    const onDraftRestoreIssue = vi.fn();

    const { unmount } = renderHook(() => useAcpInitialMessage({ ...params, onDraftPrefill, onDraftRestoreIssue }));
    await vi.waitFor(() => expect(onDraftPrefill).toHaveBeenCalledTimes(1));
    unmount();
    await act(async () => {
      gate.resolve();
      await Promise.resolve();
    });

    // The torn-down run must not consume the payload nor report an issue.
    expect(onDraftRestoreIssue).not.toHaveBeenCalled();
    expect(sessionStorage.getItem(storageKey)).toBe(stored);

    setComputeProviderMock.mockResolvedValue(undefined);
    const retry = renderHook(() => useAcpInitialMessage({ ...createParams(conversationId), onDraftPrefill }));
    await vi.waitFor(() => expect(sessionStorage.getItem(storageKey)).toBeNull());
    expect(onDraftRestoreIssue).not.toHaveBeenCalled();
    expect(setComputeProviderMock).toHaveBeenCalledWith(conversationId, 'local', true);
    retry.unmount();
  });
});

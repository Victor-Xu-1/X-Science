import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  asideRequestBody,
  captureXScienceAsideIntent,
  closeXScienceAside,
  createXScienceAside,
  lookupXScienceAside,
  sendXScienceAsideFollowup,
  cancelXScienceAsideMessageIntent,
  asideFollowupRequestBody,
} from '@/renderer/services/xScienceAside';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';
import { getSelectedXScienceBranch, selectXScienceBranch } from '@/renderer/services/xScienceConversationBranches';

const intent = {
  parentFrameId: 'parent/1',
  intentId: '61234567-89ab-4cde-8f01-234567890abc',
  request: 'Check independently',
  model: 'selected-model',
  asSession: false,
  sourceBranchId: 'br_12345678',
  expectedGeneration: 3,
};
const receipt = {
  accepted: true,
  frame_id: 'child-1',
  intent_id: intent.intentId,
  input_message_id: intent.intentId,
  source_branch_id: intent.sourceBranchId,
  source_generation: 3,
  through_publication_sequence: 12,
  as_session: false,
  replayed: false,
};
const response = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json' } });
const branchState = {
  root_frame_id: intent.parentFrameId,
  active_branch_id: 'br_87654321',
  generation: 3,
  branches: [
    { id: 'br_87654321', active: true },
    { id: 'br_12345678', active: false },
  ],
};

describe('X-Science Aside receipt transport', () => {
  beforeEach(() => {
    resetRendererAccountScopeForTest();
    setRendererAccountOwner('aside-service-owner');
  });

  it('captures the selected source and generation once without altering its selection', async () => {
    selectXScienceBranch(intent.parentFrameId, intent.sourceBranchId);
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(response(branchState));
    expect(
      await captureXScienceAsideIntent(intent.parentFrameId, '  Check independently  ', {
        model: intent.model,
        intentId: intent.intentId,
        fetchImpl,
      })
    ).toEqual(intent);
    expect(fetchImpl).toHaveBeenCalledOnce();
    expect(fetchImpl.mock.calls[0][1]).toMatchObject({ credentials: 'include', signal: expect.any(AbortSignal) });
  });

  it('rejects a source selection ABA while the branch snapshot is pending', async () => {
    selectXScienceBranch(intent.parentFrameId, intent.sourceBranchId);
    const fetchImpl = vi.fn<typeof fetch>().mockImplementation(async () => {
      selectXScienceBranch(intent.parentFrameId, 'br_87654321');
      selectXScienceBranch(intent.parentFrameId, intent.sourceBranchId);
      return response(branchState);
    });
    await expect(captureXScienceAsideIntent(intent.parentFrameId, intent.request, { fetchImpl })).rejects.toThrow(
      'aside_source_changed'
    );
    expect(fetchImpl).toHaveBeenCalledOnce();
  });

  it('keeps a late previous-account snapshot out of the new account branch selection', async () => {
    const fetchImpl = vi.fn<typeof fetch>().mockImplementation(async () => {
      setRendererAccountOwner('new-owner');
      selectXScienceBranch(intent.parentFrameId, 'br_aaaaaaaa');
      return response(branchState);
    });
    await expect(captureXScienceAsideIntent(intent.parentFrameId, intent.request, { fetchImpl })).rejects.toThrow(
      'aside_action_owner_changed'
    );
    expect(getSelectedXScienceBranch(intent.parentFrameId)).toBe('br_aaaaaaaa');
  });

  it.each([false, true])('returns exact acceptance without polling final output, asSession=%s', async (asSession) => {
    const frozen = { ...intent, asSession };
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(response({ ...receipt, as_session: asSession }));
    expect(await createXScienceAside(frozen, { fetchImpl })).toEqual({
      frameId: 'child-1',
      intentId: intent.intentId,
      inputMessageId: intent.intentId,
      sourceBranchId: intent.sourceBranchId,
      sourceGeneration: 3,
      throughPublicationSequence: 12,
      asSession,
      replayed: false,
    });
    expect(fetchImpl).toHaveBeenCalledOnce();
    const [url, init] = fetchImpl.mock.calls[0];
    expect(url).toBe('/api/frames/parent%2F1/aside');
    expect(init?.method).toBe('POST');
    expect(JSON.parse(String(init?.body))).toEqual(asideRequestBody(frozen));
  });

  it('distinguishes an absent GET from a real cancelled-before-acceptance tombstone', async () => {
    const fetchImpl = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(response({ accepted: false, intent_id: intent.intentId }))
      .mockResolvedValueOnce(
        response({
          accepted: false,
          intent_id: intent.intentId,
          cancelled: true,
          cancellation_status: 'cancelled_before_acceptance',
        })
      );
    expect(await lookupXScienceAside(intent, { fetchImpl })).toEqual({ status: 'absent' });
    expect(await lookupXScienceAside(intent, { fetchImpl })).toEqual({
      status: 'cancelled-before-acceptance',
      intentId: intent.intentId,
    });
    expect(fetchImpl.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });

  it('Close sends the same frozen body and exact followup target, never the latest input', async () => {
    const input = '71234567-89ab-4cde-8f01-234567890abc';
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(
      response({
        ...receipt,
        cancelled: true,
        cancellation_status: 'cancelled',
        cancelled_input_message_id: input,
        target_input_message_id: input,
      })
    );
    expect(await closeXScienceAside(intent, input, { fetchImpl })).toMatchObject({
      status: 'cancelled',
      inputMessageId: input,
    });
    const [url, init] = fetchImpl.mock.calls[0];
    expect(url).toBe('/api/frames/parent%2F1/aside?intent_id=' + intent.intentId);
    expect(init?.method).toBe('DELETE');
    expect(JSON.parse(String(init?.body))).toEqual({ ...asideRequestBody(intent), expected_input_message_id: input });
  });

  it('rejects stale cancellation without a second call or latest-input fallback', async () => {
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(
      response(
        {
          code: 'CONVERSATION_CANCEL_INPUT_STALE',
          error: 'Input changed',
        },
        409
      )
    );
    await expect(closeXScienceAside(intent, intent.intentId, { fetchImpl })).rejects.toMatchObject({
      status: 409,
      code: 'CONVERSATION_CANCEL_INPUT_STALE',
    });
    expect(fetchImpl).toHaveBeenCalledOnce();
  });

  it('does not treat GET absence, terminal state or another input cancellation as Close proof', async () => {
    for (const body of [
      { accepted: false, intent_id: intent.intentId },
      { ...receipt, cancelled: true, cancellation_status: 'cancelled', cancelled_input_message_id: 'other-input' },
      { ...receipt, cancelled: false, cancellation_status: 'already_terminal', target_input_message_id: 'other-input' },
    ]) {
      const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(response(body));
      await expect(closeXScienceAside(intent, intent.intentId, { fetchImpl })).rejects.toThrow(
        /^aside_(?:receipt|close)_unverified$/
      );
      expect(fetchImpl).toHaveBeenCalledOnce();
    }
  });

  it.each(['intent_id', 'input_message_id', 'source_branch_id', 'source_generation', 'as_session'])(
    'rejects acceptance with a mismatched %s',
    async (field) => {
      const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(response({ ...receipt, [field]: 'foreign' }));
      await expect(createXScienceAside(intent, { fetchImpl })).rejects.toThrow('aside_receipt_unverified');
    }
  );

  it('posts followup to the owned child with its stable loading_id and frozen branch controls', async () => {
    const command = {
      loadingId: '71234567-89ab-4cde-8f01-234567890abc',
      text: 'Continue independently',
      sessionOptions: {
        ultra_mode: false,
        verifier_mode: 'off' as const,
        memory_mode: 'off' as const,
        target_agent: 'OPERON',
        target_branch_id: 'br_12345678',
        expected_branch_id: 'br_12345678',
        expected_generation: 3,
      },
    };
    const runtime = {
      state: 'running',
      can_send_message: false,
      has_task: true,
      task_status: 'running',
      is_processing: true,
      pending_confirmations: 0,
      turn_id: 'turn-followup',
    };
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(
      response({
        msg_id: 'branch-continue:' + command.loadingId,
        turn_id: 'turn-followup',
        runtime,
      })
    );
    expect(await sendXScienceAsideFollowup('child-1', command, { fetchImpl })).toEqual({
      msg_id: 'branch-continue:' + command.loadingId,
      turn_id: 'turn-followup',
      runtime,
    });
    expect(fetchImpl.mock.calls[0][0]).toBe('/api/conversations/child-1/messages');
    expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body))).toEqual({
      content: command.text,
      files: [],
      loading_id: command.loadingId,
      session_options: command.sessionOptions,
    });
  });

  it('keeps the canonical runtime decoder for contradictory followup acceptance', async () => {
    const command = { loadingId: '71234567-89ab-4cde-8f01-234567890abc', text: 'Followup', sessionOptions: {} };
    const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(
      response({
        msg_id: 'branch-continue:' + command.loadingId,
        turn_id: 'turn-followup',
        runtime: {
          state: 'starting',
          task_status: 'running',
          has_task: true,
          is_processing: true,
          can_send_message: false,
          pending_confirmations: 0,
          turn_id: 'turn-followup',
        },
      })
    );
    await expect(sendXScienceAsideFollowup('child-1', command, { fetchImpl })).rejects.toThrow(
      'conversation_runtime_invalid'
    );
    expect(fetchImpl).toHaveBeenCalledOnce();
  });

  it.each(['cancelled_before_acceptance', 'cancelled', 'already_terminal'] as const)(
    'cancels only the exact frozen message mutation with %s proof, without a POST',
    async (status) => {
      const command = {
        loadingId: '71234567-89ab-4cde-8f01-234567890abc',
        text: 'Followup question',
        sessionOptions: {
          ultra_mode: false,
          verifier_mode: 'off' as const,
          memory_mode: 'off' as const,
          target_agent: 'OPERON',
          target_branch_id: 'br_12345678',
          expected_branch_id: 'br_12345678',
          expected_generation: 3,
        },
      };
      const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(
        response({
          frame_id: 'child-1',
          loading_id: command.loadingId,
          input_message_id: 'branch-continue:' + command.loadingId,
          request_sha256: 'a'.repeat(64),
          replayed: false,
          accepted: status !== 'cancelled_before_acceptance',
          cancelled: status !== 'already_terminal',
          cancellation_status: status,
        })
      );
      const proof = await cancelXScienceAsideMessageIntent('child-1', command, { fetchImpl });
      expect(proof).toEqual({
        frameId: 'child-1',
        loadingId: command.loadingId,
        requestSha256: 'a'.repeat(64),
        replayed: false,
        status:
          status === 'already_terminal'
            ? 'already-terminal'
            : status === 'cancelled'
              ? 'cancelled'
              : 'cancelled-before-acceptance',
        inputMessageId: status === 'cancelled_before_acceptance' ? null : 'branch-continue:' + command.loadingId,
      });
      expect(fetchImpl).toHaveBeenCalledOnce();
      expect(fetchImpl.mock.calls[0][0]).toBe('/api/conversations/child-1/message-intents/' + command.loadingId);
      expect(fetchImpl.mock.calls[0][1]?.method).toBe('DELETE');
      expect(JSON.parse(String(fetchImpl.mock.calls[0][1]?.body))).toEqual(asideFollowupRequestBody(command));
    }
  );

  it('does not infer cancellation from an unrelated or contradictory message-intent receipt', async () => {
    const command = {
      loadingId: '71234567-89ab-4cde-8f01-234567890abc',
      text: 'Followup question',
      sessionOptions: {
        ultra_mode: false,
        verifier_mode: 'off' as const,
        memory_mode: 'off' as const,
        target_agent: 'OPERON',
      },
    };
    const base = {
      frame_id: 'child-1',
      loading_id: command.loadingId,
      input_message_id: 'canonical-input',
      request_sha256: 'a'.repeat(64),
      replayed: false,
      accepted: true,
      cancelled: true,
      cancellation_status: 'cancelled',
    };
    for (const mismatch of [
      { frame_id: 'other-child' },
      { loading_id: intent.intentId },
      { request_sha256: 'invalid' },
      { accepted: false },
      { cancelled: false },
    ]) {
      const fetchImpl = vi.fn<typeof fetch>().mockResolvedValue(response({ ...base, ...mismatch }));
      await expect(cancelXScienceAsideMessageIntent('child-1', command, { fetchImpl })).rejects.toThrow(
        'aside_message_cancellation_unverified'
      );
      expect(fetchImpl).toHaveBeenCalledOnce();
    }
  });
});

import {
  approveXSciencePlan,
  forkXScienceAtAskUserAnswer,
  notifyXScienceRuntimeInvalidation,
  resolveXScienceInputRequest,
  resolveXScienceAskUserRequest,
  subscribeXScienceRuntimeInvalidation,
} from '@/renderer/services/xScienceRuntimeOperations';
import { describe, expect, it, vi } from 'vitest';
import { decodeMessageStreamPayload, type IResponseMessage } from '@/common/adapter/messageStreamProtocol';
import { normalizeXScienceRuntimeSnapshot } from '@/renderer/components/xScience/runtime/runtimeOperationsModel';

const ipcRuntimeInvalidationOnMock = vi.hoisted(() => vi.fn(() => () => {}));
const ipcResponseStreamOnMock = vi.hoisted(() => vi.fn((_listener: (event: IResponseMessage) => void) => () => {}));

vi.mock('@/common', () => ({
  ipcBridge: {
    runtime: { statusChanged: { on: ipcRuntimeInvalidationOnMock } },
    acpConversation: { responseStream: { on: ipcResponseStreamOnMock } },
    conversation: {
      confirmation: {
        add: { on: ipcRuntimeInvalidationOnMock },
        update: { on: ipcRuntimeInvalidationOnMock },
        remove: { on: ipcRuntimeInvalidationOnMock },
      },
    },
    realtime: { reconnected: { on: ipcRuntimeInvalidationOnMock } },
  },
}));

const request = {
  requestId: 'request-ask-1',
  toolId: 'toolu_ask_1',
  kind: 'ask_user',
  tool: 'ask_user',
  code: null,
  description: null,
  environment: null,
  mode: null,
  questions: [],
};

const approvalRequest = {
  requestId: 'request-exec-1',
  toolId: 'toolu_exec_1',
  kind: 'local_exec',
  tool: 'python',
  code: 'print("STAT6")',
  description: null,
  environment: 'python',
  mode: 'live',
  questions: [],
};

describe('X-Science ask_user runtime service', () => {
  it('invalidates retry snapshots once for current public progress, never for private, stale or per-token traffic', () => {
    let snapshot = normalizeXScienceRuntimeSnapshot({
      id: 'retry-frame',
      status: 'processing',
      runtime_retry_reason: 'model_provider_temporarily_unavailable',
      runtime_retry_attempt_id: 'canonical-attempt-2',
      runtime_attempt: 2,
      runtime_input_revision: 3,
      runtime_observed_at: '2026-10-14T10:00:00.000Z',
    });
    const invalidate = vi.fn();
    const release = subscribeXScienceRuntimeInvalidation('retry-frame', invalidate, {
      getRetrySnapshot: () => snapshot,
    });
    const progress = ipcResponseStreamOnMock.mock.calls.at(-1)![0];
    const current: IResponseMessage = {
      type: 'text',
      data: 'The provider returned public progress.',
      msg_id: 'segment-4',
      conversation_id: 'retry-frame',
      assistant_attempt_id: 'canonical-attempt-2',
      created_at: Date.parse('2026-10-14T10:00:01.000Z'),
    };
    for (const event of [
      { ...current, type: 'thinking' },
      { ...current, data: '  ' },
      { ...current, hidden: true },
      { ...current, conversation_id: 'another-frame' },
      { ...current, assistant_attempt_id: 'older-attempt' },
      { ...current, created_at: Date.parse('2026-10-14T09:59:59.000Z') },
    ])
      progress(event);
    expect(
      decodeMessageStreamPayload({
        ...current,
        type: 'message.stream',
        stream_type: 'text',
        provisional: true,
      })
    ).toBeUndefined();
    expect(invalidate).not.toHaveBeenCalled();
    for (let token = 0; token < 100; token += 1) progress(current);
    expect(invalidate).toHaveBeenCalledTimes(1);
    // A failed read leaves this snapshot consumed, so polling/reconnect—not
    // every token—owns recovery. A fresh authority reply may re-arm the wake.
    snapshot = { ...snapshot, runtimeObservedAt: '2026-10-14T10:00:02.000Z' };
    progress(current);
    expect(invalidate).toHaveBeenCalledTimes(1);
    progress({ ...current, type: 'content', created_at: Date.parse('2026-10-14T10:00:03.000Z') });
    expect(invalidate).toHaveBeenCalledTimes(2);
    snapshot = { ...snapshot, retryReason: undefined };
    progress({ ...current, created_at: Date.parse('2026-10-14T10:00:04.000Z') });
    expect(invalidate).toHaveBeenCalledTimes(2);
    release();
  });
  it('binds plan approval to the artifact and version the user reviewed', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ status: 'accepted' }), { status: 200 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ id: 'plan-fence', status: 'processing' }), { status: 200 }));
    await approveXSciencePlan(
      'plan-fence',
      { baseUrl: 'http://gateway.test', fetchImpl },
      { artifactId: 'reviewed-plan', versionId: 'reviewed-version' }
    );
    expect(JSON.parse(String(fetchImpl.mock.calls[0][1].body))).toEqual({
      expected_plan_artifact_id: 'reviewed-plan',
      expected_plan_version_id: 'reviewed-version',
    });
  });
  it('routes local runtime invalidations only to the matching frame subscription', () => {
    const first = vi.fn();
    const second = vi.fn();
    const releaseFirst = subscribeXScienceRuntimeInvalidation('frame-1', first);
    const releaseSecond = subscribeXScienceRuntimeInvalidation('frame-2', second);

    notifyXScienceRuntimeInvalidation('frame-1');
    expect(first).toHaveBeenCalledOnce();
    expect(second).not.toHaveBeenCalled();

    releaseFirst();
    notifyXScienceRuntimeInvalidation('frame-1');
    expect(first).toHaveBeenCalledOnce();
    releaseSecond();
  });

  it('defaults to a once-only approval, omits persistent scope and does not forward runtime mode', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ id: 'frame-1', root_frame_id: 'frame-1', status: 'processing', output_data: {} }),
          { status: 200 }
        )
      );

    await resolveXScienceInputRequest('frame-1', approvalRequest, 'allow', undefined, {
      baseUrl: 'http://gateway.test',
      fetchImpl,
    });

    const [, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      responses: [
        {
          requestId: 'request-exec-1',
          tool_id: 'toolu_exec_1',
          approved: true,
          action: 'allow',
        },
      ],
    });
  });

  it('sends the exact persistent scope and omits it when denying', async () => {
    const projectFetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ id: 'frame-1', root_frame_id: 'frame-1', status: 'processing', output_data: {} }),
          { status: 200 }
        )
      );
    await resolveXScienceInputRequest('frame-1', approvalRequest, 'allow', 'project', {
      baseUrl: 'http://gateway.test',
      fetchImpl: projectFetch,
    });
    expect(JSON.parse(String((projectFetch.mock.calls[0][1] as RequestInit).body))).toEqual({
      responses: [
        {
          requestId: 'request-exec-1',
          tool_id: 'toolu_exec_1',
          approved: true,
          action: 'allow',
          scope: 'project',
        },
      ],
    });

    const denyFetch = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ id: 'frame-1', root_frame_id: 'frame-1', status: 'processing', output_data: {} }),
          { status: 200 }
        )
      );
    await resolveXScienceInputRequest('frame-1', approvalRequest, 'deny', 'always', {
      baseUrl: 'http://gateway.test',
      fetchImpl: denyFetch,
    });
    expect(JSON.parse(String((denyFetch.mock.calls[0][1] as RequestInit).body))).toEqual({
      responses: [
        {
          requestId: 'request-exec-1',
          tool_id: 'toolu_exec_1',
          approved: false,
          action: 'deny',
        },
      ],
    });
  });

  it('submits an answer using the v1.1 resolve-input response contract', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ id: 'frame-1', root_frame_id: 'frame-1', status: 'processing', output_data: {} }),
          { status: 200 }
        )
      );

    await resolveXScienceAskUserRequest(
      'frame-1',
      request,
      { action: 'answer', answers: { 'Primary endpoint?': 'pIC50' } },
      { baseUrl: 'http://gateway.test', fetchImpl }
    );

    const [, init] = fetchImpl.mock.calls[0] as [string, RequestInit];
    expect(JSON.parse(String(init.body))).toEqual({
      responses: [
        {
          requestId: 'request-ask-1',
          tool_id: 'toolu_ask_1',
          approved: true,
          action: 'answer',
          answers: { 'Primary endpoint?': 'pIC50' },
        },
      ],
    });
  });

  it('forks at the original tool use on the exact canonical branch generation', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            root_frame_id: 'frame-root',
            active_branch_id: 'br_00000001',
            generation: 3,
            branches: [
              {
                id: 'br_00000001',
                parent_id: null,
                fork_point: null,
                active: true,
                created_at: '2026-07-25T00:00:00Z',
                updated_at: '2026-07-25T00:00:00Z',
              },
            ],
          }),
          { status: 200 }
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ root_frame_id: 'frame-root', branch_id: 'br_00000002', generation: 4, status: 'accepted' }),
          { status: 200 }
        )
      );
    await expect(
      forkXScienceAtAskUserAnswer(
        {
          rootFrameId: 'frame-root',
          sourceFrameId: 'frame-root',
          sourceBranchId: 'br_00000001',
          toolUseId: 'toolu_ask_1',
          response: { action: 'decide_for_me' },
          clientMutationId: 'ask-mutation-1',
        },
        { baseUrl: 'http://gateway.test', fetchImpl }
      )
    ).resolves.toEqual({ rootFrameId: 'frame-root', branchId: 'br_00000002', generation: 4 });
    const request = fetchImpl.mock.calls[1][1] as RequestInit;
    const body = JSON.parse(String(request.body)) as Record<string, unknown>;
    expect(body).toMatchObject({
      tool_use_id: 'toolu_ask_1',
      response: { action: 'decide_for_me' },
      source_branch_id: 'br_00000001',
      expected_branch_id: 'br_00000001',
      expected_generation: 3,
    });
    expect(body.client_mutation_id).toBe('ask-mutation-1');
    expect((request.headers as Record<string, string>)['Idempotency-Key']).toBe('ask-mutation-1');
  });
});

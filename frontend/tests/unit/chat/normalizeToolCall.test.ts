import { describe, expect, it } from 'vitest';
import { normalizeToolMessages } from '@/common/chat/normalizeToolCall';
import { buildToolProgressPublicPresentation } from '@/renderer/pages/conversation/Messages/components/toolProgressPresentation';

describe('normalizeToolMessages', () => {
  it('does not splice a conflicting later input and detail reference onto an earlier terminal receipt', () => {
    const messages = ['first', 'second'].map((source, index) => ({
      id: source,
      conversation_id: 'frame',
      type: 'tool_call',
      content: {
        call_id: 'same-call',
        name: 'web_fetch',
        status: 'completed',
        input: { url: `https://${source}.example/article` },
        output: JSON.stringify({ url: `https://${source}.example/article` }),
        revision: index + 1,
      },
    }));
    const [tool] = normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0]);
    expect(tool.messageId).toBe('first');
    expect(tool.input).toContain('first.example');
    expect(tool.output).toContain('first.example');
    expect(tool.revision).toBe(1);
  });
  it('keeps bounded observed progress on the same durable tool call', () => {
    const messages = [
      {
        id: 'message-progress',
        conversation_id: 'conversation-a',
        type: 'tool_call',
        content: {
          call_id: 'environment-call',
          name: 'manage_environments',
          status: 'running',
          progress: {
            phase: 'downloading_packages',
            phasePercent: 64.2,
            bytesPerSecond: 224_320,
            bytesCompleted: 50_000_000,
            bytesTotal: 100_000_000,
            completedItems: 1,
            totalItems: 8,
            elapsedMs: 30_000,
            indeterminate: false,
          },
        },
      },
    ];

    expect(normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0])).toMatchObject([
      {
        key: 'environment-call',
        status: 'running',
        progress: {
          phase: 'downloading_packages',
          phasePercent: 64.2,
          bytesPerSecond: 224_320,
          bytesCompleted: 50_000_000,
          bytesTotal: 100_000_000,
          completedItems: 1,
          totalItems: 8,
          elapsedMs: 30_000,
        },
      },
    ]);
  });

  it('reconciles a resumed durable tool call into one terminal row', () => {
    const messages = [
      {
        id: 'message-terminal',
        conversation_id: 'conversation-a',
        type: 'tool_call',
        content: {
          call_id: 'call-resumed',
          name: 'python',
          status: 'error',
          output: 'safe failure summary',
        },
      },
      {
        id: 'message-stale-live',
        conversation_id: 'conversation-a',
        type: 'tool_call',
        content: {
          call_id: 'call-resumed',
          name: 'python',
          status: 'running',
          description: 'continued analysis',
        },
      },
    ];

    const tools = normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0]);

    expect(tools).toHaveLength(1);
    expect(tools[0]).toMatchObject({
      key: 'call-resumed',
      status: 'error',
      output: 'safe failure summary',
      description: 'continued analysis',
    });
  });

  it('does not let a conflicting later terminal update overwrite the first receipt', () => {
    const messages = ['error', 'completed'].map((status, index) => ({
      id: `message-terminal-${index}`,
      conversation_id: 'conversation-a',
      type: 'tool_call',
      content: {
        call_id: 'call-terminal',
        name: 'python',
        status,
        output: index === 0 ? 'failed evidence' : 'conflicting success',
      },
    }));

    const [tool] = normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0]);
    expect(tool.status).toBe('error');
    expect(tool.output).toBe('failed evidence');
  });

  it('maps an unsupported history status to unknown instead of an active pending state', () => {
    const [tool] = normalizeToolMessages([
      {
        id: 'message-unsupported-status',
        conversation_id: 'conversation-a',
        type: 'tool_call',
        content: { call_id: 'call-unsupported', name: 'python', status: 'successful' },
      } as Parameters<typeof normalizeToolMessages>[0][number],
    ]);

    expect(tool.status).toBe('unknown');
  });

  it('projects a completed transport envelope with a failed outcome as a failed tool step', () => {
    const messages = [
      {
        id: 'message-semantic-failure',
        conversation_id: 'conversation-a',
        type: 'acp_tool_call',
        content: {
          update: {
            sessionUpdate: 'tool_call_update',
            tool_call_id: 'call-oom',
            title: 'python',
            kind: 'execute',
            status: 'completed',
            content: [
              {
                type: 'content',
                content: {
                  type: 'text',
                  text: JSON.stringify({
                    ok: false,
                    status: 'failed',
                    code: 'python_memory_exhausted',
                  }),
                },
              },
            ],
          },
        },
      },
    ];

    expect(normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0])).toMatchObject([
      { key: 'call-oom', status: 'error' },
    ]);
  });

  it('keeps reused call ids isolated across explicit execution attempts', () => {
    const messages = [1, 2].map((attempt) => ({
      id: `message-${attempt}`,
      conversation_id: 'conversation-a',
      type: 'tool_call',
      content: {
        call_id: 'reused-call',
        operation_id: 'reused-call',
        attempt,
        revision: attempt,
        name: 'python',
        status: attempt === 1 ? 'interrupted' : 'running',
      },
    }));

    expect(normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0])).toMatchObject([
      { key: '1:reused-call', attempt: 1, status: 'interrupted' },
      { key: '2:reused-call', attempt: 2, status: 'running' },
    ]);
  });

  it('preserves the stable public description from compact metadata when the input preview is truncated', () => {
    const messages = [
      {
        id: 'message-compact-description',
        conversation_id: 'conversation-a',
        type: 'tool_call',
        content: {
          call_id: 'compact-call',
          name: 'python',
          status: 'completed',
          input: '{"human_description":"Compare',
          _compact: {
            truncated: true,
            human_description: 'Compare the candidate structures',
          },
          xScience: { messageIndex: 4, blockIndex: 0, branchId: 'br_deadbeef' },
        },
      },
    ];

    expect(normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0])).toMatchObject([
      {
        key: 'compact-call',
        truncated: true,
        humanDescription: 'Compare the candidate structures',
        branchId: 'br_deadbeef',
      },
    ]);
  });
});

describe('resource admission progress snapshots', () => {
  const admission = () => ({
    decisionAt: '2026-01-02T03:04:05Z',
    reason: 'insufficient_capacity',
    capacityAvailableBytes: 900,
    unusedPromisedBytes: 600,
    netAvailableBytes: 300,
    requestedBytes: 400,
    requiredBytes: 400,
    reservedBytes: 0,
    precedingWaiters: 0,
  });
  const message = (
    id: string,
    revision: number,
    phase: string,
    snapshot?: unknown,
    branchId = 'br_00000001',
    conversationId = 'resource-frame'
  ) => ({
    id,
    conversation_id: conversationId,
    type: 'tool_call',
    content: {
      call_id: 'resource-call',
      name: 'bash',
      status: 'running',
      input: { purpose: 'synthetic resource observation' },
      revision,
      xScience: { branchId },
      progress: {
        phase,
        message: 'private runtime/process detail must never become a public row',
        indeterminate: true,
        ...(snapshot === undefined ? {} : { resourceAdmission: snapshot }),
      },
    },
  });
  const normalize = (messages: unknown[]) =>
    normalizeToolMessages(messages as Parameters<typeof normalizeToolMessages>[0]);

  it('copies only the bounded resource DTO through the existing tool normalizer', () => {
    const snapshot = { ...admission(), backendId: 'private-backend', path: '/private/runtime' };
    const [tool] = normalize([message('first', 1, 'waiting_for_resources', snapshot)]);
    expect(tool.progress?.resourceAdmission).toEqual(admission());
    snapshot.netAvailableBytes = 1;
    expect(tool.progress?.resourceAdmission?.netAvailableBytes).toBe(300);
    const view = buildToolProgressPublicPresentation(tool.progress!, 'en-US');
    expect(JSON.stringify(view)).not.toContain('private');
    expect(view.rows).toContainEqual({ label: 'Admission snapshot (not live)', value: '2026-01-02 03:04:05 UTC' });
    expect(view.rows).toContainEqual({ label: 'Required / unreserved at decision', value: '400 B / 300 B' });
  });

  it.each([
    { name: 'unknown reason', invalid: { reason: 'start another computation' } },
    { name: 'unsafe integer', invalid: { capacityAvailableBytes: Number.MAX_SAFE_INTEGER + 1 } },
    { name: 'changed explicit demand', invalid: { requestedBytes: 401 } },
    { name: 'inconsistent capacity', invalid: { netAvailableBytes: 500 } },
    { name: 'invalid date', invalid: { decisionAt: '2026-02-31T03:04:05Z' } },
  ])('omits $name without turning telemetry into execution state', ({ invalid }) => {
    const [tool] = normalize([message('invalid', 1, 'waiting_for_resources', { ...admission(), ...invalid })]);
    expect(tool.status).toBe('running');
    expect(tool.progress?.phase).toBe('waiting_for_resources');
    expect(tool.progress?.resourceAdmission).toBeUndefined();
  });

  it('retains one historical snapshot on same-phase heartbeats and clears it on phase or terminal changes', () => {
    const first = message('first', 1, 'waiting_for_resources', admission());
    const heartbeat = message('heartbeat', 2, 'waiting_for_resources');
    expect(normalize([first, heartbeat])[0].progress?.resourceAdmission).toEqual(admission());
    const granted = {
      ...admission(),
      decisionAt: '2026-01-02T03:04:06Z',
      reason: 'admitted',
      unusedPromisedBytes: 0,
      netAvailableBytes: 900,
      reservedBytes: 400,
    };
    const starting = message('starting', 3, 'starting', granted);
    expect(normalize([first, heartbeat, starting])[0].progress?.resourceAdmission?.reason).toBe('admitted');
    const executing = message('executing', 4, 'executing');
    const [advanced] = normalize([
      first,
      heartbeat,
      starting,
      executing,
      message('late', 2, 'waiting_for_resources', admission()),
    ]);
    expect(advanced.progress?.phase).toBe('executing');
    expect(advanced.progress?.resourceAdmission).toBeUndefined();
    const finished = message('finished', 5, 'operation_completed');
    finished.content.status = 'completed';
    expect(normalize([starting, finished])[0].progress?.resourceAdmission).toBeUndefined();
    const terminalWithoutProgress = { ...finished, content: { ...finished.content, progress: undefined } };
    const [settled] = normalize([terminalWithoutProgress, first]);
    expect(settled.status).toBe('completed');
    expect(settled.progress?.resourceAdmission).toBeUndefined();
  });

  it('does not inherit snapshot facts across the existing branch or conversation identity fences', () => {
    const first = message('first', 1, 'waiting_for_resources', admission());
    for (const other of [
      message('other-branch', 2, 'waiting_for_resources', undefined, 'br_00000002'),
      message('other-conversation', 2, 'waiting_for_resources', undefined, 'br_00000001', 'other-frame'),
    ]) {
      const tools = normalize([first, other]);
      expect(tools).toHaveLength(2);
      expect(tools[1].progress?.resourceAdmission).toBeUndefined();
    }
  });

  it('uses localized existing detail rows and labels the decision time as a snapshot rather than live memory', () => {
    const fairness = {
      ...admission(),
      reason: 'earlier_waiter',
      unusedPromisedBytes: 0,
      netAvailableBytes: 900,
      precedingWaiters: 2,
    };
    const [tool] = normalize([message('fairness', 1, 'waiting_for_resources', fairness)]);
    const english = buildToolProgressPublicPresentation(tool.progress!, 'en-US');
    const chinese = buildToolProgressPublicPresentation(tool.progress!, 'zh-CN');
    expect(english.rows).toContainEqual({ label: 'Admission decision', value: 'Behind 2 earlier request(s)' });
    expect(chinese.rows).toContainEqual({ label: '准入决定', value: '等待前方 2 个请求' });
    expect(chinese.rows).toContainEqual({ label: '当次准入快照（非实时）', value: '2026-01-02 03:04:05 UTC' });
    const advanced = { ...tool.progress!, phase: 'executing' };
    expect(JSON.stringify(buildToolProgressPublicPresentation(advanced, 'en-US'))).not.toContain('Admission snapshot');
  });
});

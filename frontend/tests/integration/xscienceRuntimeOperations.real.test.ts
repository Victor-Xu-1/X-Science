import { afterEach, describe, expect, it } from 'vitest';
import {
  approveXSciencePlan,
  discardXSciencePlan,
  forkXScienceAtAskUserAnswer,
  loadXSciencePlanDocument,
} from '@/renderer/services/xScienceRuntimeOperations';
import { loadConversationStreamingSnapshot } from '@/renderer/services/runtime/conversationStreamingApi';
import { loadXScienceConversationBranches } from '@/renderer/services/xScienceConversationBranches';
import { createRealArtifactFixture, type RealArtifactFixture } from './xscienceRealArtifactFixture';
import { createRealConversationFixture, type RealConversationFixture } from './xscienceRealConversationFixture';

const gatewayBaseUrl = process.env.X_SCIENCE_GATEWAY_URL ?? 'http://127.0.0.1:8766';
let frameId: string | null = null;
let artifactFixture: RealArtifactFixture | null = null;
let conversationFixture: RealConversationFixture | null = null;

afterEach(async () => {
  await artifactFixture?.dispose();
  artifactFixture = null;
  await conversationFixture?.dispose();
  conversationFixture = null;
  frameId = null;
});

describe('X-Science runtime operations gateway', () => {
  it('creates, cancels, reads execution records, rejects an invalid resume, and deletes a real empty frame', async () => {
    conversationFixture = await createRealConversationFixture({ gatewayBaseUrl });
    frameId = conversationFixture.conversationId;
    const fetchImpl = conversationFixture.fetchImpl;

    const gatewayOptions = { baseUrl: gatewayBaseUrl, fetchImpl };
    await expect(loadConversationStreamingSnapshot(frameId!, gatewayOptions)).resolves.toMatchObject({
      rootFrameId: frameId,
      frames: [],
      historyRevision: 0,
    });

    const resumeResponse = await fetchImpl(`${gatewayBaseUrl}/api/frames/${encodeURIComponent(frameId!)}/resume`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: '{}',
    });
    expect(resumeResponse.status).toBe(428);
    expect(await resumeResponse.json()).toMatchObject({ code: 'RESUME_PRECONDITION_REQUIRED' });

    const messageResponse = await fetchImpl(
      `${gatewayBaseUrl}/api/conversations/${encodeURIComponent(frameId!)}/messages`,
      {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          content: 'Cancel this disposable integration run.',
          files: [],
          artifact_refs: [],
          message_context: '',
          loading_id: '',
          inject_skills: [],
          session_options: {},
        }),
      }
    );
    expect(messageResponse.status).toBe(202);

    const cancelResponse = await fetchImpl(
      `${gatewayBaseUrl}/api/frames/${encodeURIComponent(frameId!)}/cancel?reason=integration-test`,
      { method: 'POST', headers: { 'content-type': 'application/json' }, body: '{}' }
    );
    expect(cancelResponse.ok).toBe(true);
    expect((await cancelResponse.json()) as unknown).toMatchObject({
      root_frame_id: frameId,
      cancelled_frames: [frameId],
    });

    const executionLogResponse = await fetchImpl(
      `${gatewayBaseUrl}/api/frames/${encodeURIComponent(frameId!)}/execution-log`
    );
    expect(executionLogResponse.ok).toBe(true);
    expect(await executionLogResponse.json()).toEqual([]);

    const resolveInputResponse = await fetchImpl(
      `${gatewayBaseUrl}/api/frames/${encodeURIComponent(frameId!)}/resolve-input`,
      {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          responses: [
            {
              requestId: 'missing-request',
              tool_id: 'missing-tool',
              approved: false,
              action: 'deny',
              scope: 'conversation',
            },
          ],
        }),
      }
    );
    expect(resolveInputResponse.status).toBe(400);
    expect(await resolveInputResponse.text()).toMatch(/pending input|awaiting user response/i);

    await Promise.all([
      expect(approveXSciencePlan(frameId!, gatewayOptions)).rejects.toThrow(/plan|awaiting/i),
      expect(discardXSciencePlan(frameId!, gatewayOptions)).rejects.toThrow(/plan|awaiting/i),
    ]);

    const branchState = await loadXScienceConversationBranches(frameId!, fetchImpl);
    await expect(
      forkXScienceAtAskUserAnswer(
        {
          rootFrameId: frameId!,
          sourceFrameId: frameId!,
          sourceBranchId: branchState.activeBranchId!,
          toolUseId: 'missing-ask-user-tool',
          response: { action: 'decide_for_me' },
          clientMutationId: 'missing-ask-user-mutation',
        },
        gatewayOptions
      )
    ).rejects.toThrow(/404|tool use|tool_use/i);

    artifactFixture = await createRealArtifactFixture({
      conversationId: frameId!,
      gatewayBaseUrl,
      filename: `plan_${frameId}.json`,
      contentType: 'application/json',
      content: JSON.stringify({
        version: 3,
        task_summary: 'Characterize an extremophile protein',
        phases: [],
        feasibility: { confidence: 'high', rationale: 'Fixture inputs are available.' },
      }),
    });
    expect(await loadXSciencePlanDocument(artifactFixture.artifactId, gatewayOptions)).toMatchObject({
      version: 3,
      taskSummary: expect.stringContaining('extremophile protein'),
      feasibility: { confidence: 'high' },
    });
  });
});

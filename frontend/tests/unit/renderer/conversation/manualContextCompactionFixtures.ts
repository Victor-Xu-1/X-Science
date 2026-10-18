export const manualCompactionIdentity = {
  ownerId: 'compaction-owner',
  frameId: 'compaction-child',
  frameIncarnationId: 'compaction-child-incarnation',
  rootFrameId: 'compaction-root',
};
export const manualCompactionTarget = {
  frame_incarnation_id: manualCompactionIdentity.frameIncarnationId,
  root_frame_id: manualCompactionIdentity.rootFrameId,
  root_frame_incarnation_id: 'compaction-root-incarnation',
  stream_uid: 'frame:compaction-child',
  stream_epoch: 2,
  branch_id: 'branch-compaction',
  branch_generation: 3,
  input_revision: 7,
  through_publication_seq: 21,
};
export const manualCompactionRequest = {
  schema: 'x-science.context-compaction-request.v1',
  request_id: '02020202-0202-4202-8202-020202020202',
  expected: { ...manualCompactionTarget },
};
export function manualCompactionReceipt(request = manualCompactionRequest) {
  const receipt = {
    schema: 'x-science.context-compaction-receipt.v1',
    request,
    frame_id: manualCompactionIdentity.frameId,
    accepted: true,
    input_message_id: 'canonical-compaction-input',
    input_revision: 8,
    state: 'compacting',
    attempt: 1,
    cancellation_status: 'not_requested',
    execution_observation: 'active',
    can_submit_next_input: false,
    replayed: false,
  };
  return receipt as typeof receipt & { boundary_event_id?: number; terminal_event_id?: number };
}
export function manualCompactionSnapshot(
  operation: ReturnType<typeof manualCompactionReceipt> | null = null,
  available = operation === null,
  reason = available ? 'idle' : 'active'
) {
  return {
    schema: 'x-science.context-compaction-snapshot.v1',
    target: operation
      ? { ...manualCompactionTarget, input_revision: 8, through_publication_seq: 24 }
      : { ...manualCompactionTarget },
    availability: { available, reason },
    operation,
  };
}
export function manualCompactionUsage(usedTokens = 300_000, preflight = false) {
  return {
    status: 'available',
    snapshot: {
      sessionId: manualCompactionIdentity.frameId,
      requestId: 'provider-usage-before-compaction',
      model: 'configured-model',
      observedAt: '2026-10-18T00:00:00Z',
      state: preflight ? 'request' : 'complete',
      source: preflight ? 'estimated' : 'provider',
      usedTokens,
      limitTokens: 1_000_000,
      limitSource: 'configured',
      outputTokens: preflight ? 0 : 100,
      hasMedia: false,
      inputEstimates: [
        { key: 'systemPrompt', tokens: 4000 },
        { key: 'tools', tokens: 2000 },
        { key: 'messages', tokens: 8000 },
        { key: 'mcp', tokens: 1000 },
        { key: 'skills', tokens: 1000 },
      ],
    },
  };
}

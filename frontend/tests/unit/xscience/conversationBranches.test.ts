import {
  X_SCIENCE_SESSION_WORKFLOW_CAPABILITIES,
  forkXScienceUserMessage,
  getSelectedXScienceBranch,
  loadXScienceConversationBranches,
  rollbackXScienceBranchSelection,
  selectXScienceBranch,
} from '@/renderer/services/xScienceConversationBranches';
import { afterEach, describe, expect, it, vi } from 'vitest';

const rootFrameId = 'root-frame';

const branchesResponse = (
  branchIds: string[],
  activeBranchId = branchIds[0],
  responseFrameId = rootFrameId,
  generation = 1
) =>
  new Response(
    JSON.stringify({
      root_frame_id: responseFrameId,
      active_branch_id: activeBranchId,
      generation,
      branches: branchIds.map((id, index) => ({
        id,
        parent_id: index === 0 ? null : branchIds[0],
        fork_point: index === 0 ? null : index,
        created_at: `2026-07-23T00:0${index}:00Z`,
        updated_at: `2026-07-23T00:0${index}:00Z`,
        active: id === activeBranchId,
      })),
    }),
    { status: 200, headers: { 'content-type': 'application/json' } }
  );

describe('conversation branch authority', () => {
  afterEach(() => selectXScienceBranch(rootFrameId, null));

  it('drops a cached selection that is absent from the latest authoritative branch set', async () => {
    selectXScienceBranch(rootFrameId, 'deleted-branch');
    const fetchImpl = vi.fn().mockResolvedValue(branchesResponse(['br_00000001', 'br_00000002']));

    await expect(loadXScienceConversationBranches(rootFrameId, fetchImpl)).resolves.toMatchObject({
      activeBranchId: 'br_00000001',
      selectedBranchId: 'br_00000001',
    });
    expect(getSelectedXScienceBranch(rootFrameId)).toBeNull();
  });

  it('retains an exact cached selection while it remains in the authoritative branch set', async () => {
    selectXScienceBranch(rootFrameId, 'br_00000002');
    const fetchImpl = vi.fn().mockResolvedValue(branchesResponse(['br_00000001', 'br_00000002']));

    await expect(loadXScienceConversationBranches(rootFrameId, fetchImpl)).resolves.toMatchObject({
      selectedBranchId: 'br_00000002',
    });
    expect(getSelectedXScienceBranch(rootFrameId)).toBe('br_00000002');
  });

  it('rolls back only the exact failed selection generation', () => {
    selectXScienceBranch(rootFrameId, 'br_00000001');
    const failedSelection = selectXScienceBranch(rootFrameId, 'br_00000002');

    expect(
      rollbackXScienceBranchSelection(
        rootFrameId,
        failedSelection.branchId,
        failedSelection.previousBranchId,
        failedSelection.revision
      )
    ).toBe(true);
    expect(getSelectedXScienceBranch(rootFrameId)).toBe('br_00000001');
    expect(
      rollbackXScienceBranchSelection(
        rootFrameId,
        failedSelection.branchId,
        failedSelection.previousBranchId,
        failedSelection.revision
      )
    ).toBe(false);
  });

  it('rejects another frame owner and clears its pending branch target', async () => {
    selectXScienceBranch(rootFrameId, 'br_00000002');
    const fetchImpl = vi
      .fn()
      .mockResolvedValue(branchesResponse(['br_00000001', 'br_00000002'], 'br_00000001', 'foreign-frame'));

    await expect(loadXScienceConversationBranches(rootFrameId, fetchImpl)).rejects.toThrow(
      'branch_response_owner_mismatch'
    );
    expect(getSelectedXScienceBranch(rootFrameId)).toBeNull();
  });

  it('forks against the exact authoritative branch generation with an idempotency key', async () => {
    const fetchImpl = vi
      .fn()
      .mockResolvedValueOnce(branchesResponse(['br_00000001'], 'br_00000001', rootFrameId, 4))
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({ root_frame_id: rootFrameId, branch_id: 'br_00000002', generation: 5, status: 'accepted' }),
          { status: 200, headers: { 'content-type': 'application/json' } }
        )
      );

    await expect(
      forkXScienceUserMessage(
        {
          rootFrameId,
          messageIndex: 2,
          sourceClientMessageId: 'client-message-2',
          editedContent: 'Corrected request',
          clientMutationId: 'mutation-edit-1',
        },
        fetchImpl
      )
    ).resolves.toEqual({ rootFrameId, branchId: 'br_00000002', generation: 5, status: 'accepted' });

    expect(fetchImpl).toHaveBeenNthCalledWith(1, `/api/frames/${rootFrameId}/branches`, expect.any(Object));
    const [, request] = fetchImpl.mock.calls[1] as [string, RequestInit];
    expect(request.headers).toMatchObject({ 'Idempotency-Key': 'mutation-edit-1' });
    expect(JSON.parse(String(request.body))).toMatchObject({
      source_branch_id: 'br_00000001',
      expected_branch_id: 'br_00000001',
      expected_generation: 4,
      client_mutation_id: 'mutation-edit-1',
      source_client_message_id: 'client-message-2',
    });
  });
});
it('enables mutation and selection only after canonical Transcript authority is integrated', () => {
  expect(X_SCIENCE_SESSION_WORKFLOW_CAPABILITIES.messageFork).toEqual({ state: 'supported' });
  expect(X_SCIENCE_SESSION_WORKFLOW_CAPABILITIES.branchSelection).toEqual({ state: 'supported' });
  expect(X_SCIENCE_SESSION_WORKFLOW_CAPABILITIES.askUserAnswerFork).toEqual({ state: 'supported' });
});

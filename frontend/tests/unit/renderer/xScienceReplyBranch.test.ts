import { beforeEach, describe, expect, it, vi } from 'vitest';
import { branchXScienceReply } from '@/renderer/services/xScienceReplyBranch';
const calls = vi.hoisted(() => ({ http: vi.fn(), branches: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: calls.http }));
vi.mock('@/renderer/services/xScienceConversationBranches', () => ({
  loadXScienceConversationBranches: calls.branches,
}));
const input = { conversationId: 'source', throughAttempt: 2, intentId: 'intent' };
beforeEach(() => {
  vi.clearAllMocks();
  calls.branches.mockResolvedValue({
    activeBranchId: 'br_12345678',
    selectedBranchId: 'br_87654321',
    branches: [{ id: 'br_12345678' }, { id: 'br_87654321' }],
  });
  calls.http.mockResolvedValue({ id: 'f0db37e8-ec0b-4c37-afc9-f0af2bc9a6a5' });
});
describe('reply branch service', () => {
  it('copies selected history rather than the currently active branch', async () => {
    await branchXScienceReply(input);
    expect(calls.http).toHaveBeenCalledExactlyOnceWith(
      'POST',
      '/api/conversations/clone',
      {
        conversation: { id: 'source' },
        intent_id: 'intent',
        through_attempt: 2,
        source_branch_id: 'br_87654321',
      },
      { timeoutMs: 120000 }
    );
  });
  it('preserves an explicit displayed branch', async () => {
    await branchXScienceReply({ ...input, sourceBranchId: 'br_12345678' });
    expect(calls.http.mock.calls[0][2].source_branch_id).toBe('br_12345678');
  });
  it('rejects invalid boundary and disappeared branch without writing', async () => {
    await expect(branchXScienceReply({ ...input, throughAttempt: 0 })).rejects.toThrow();
    await expect(branchXScienceReply({ ...input, sourceBranchId: 'missing' })).rejects.toThrow();
    expect(calls.http).not.toHaveBeenCalled();
  });
  it('never navigates to an untrusted response path or retries automatically', async () => {
    calls.http.mockResolvedValue({ id: '//external.example' });
    await expect(branchXScienceReply(input)).rejects.toThrow();
    expect(calls.http).toHaveBeenCalledOnce();
  });
});

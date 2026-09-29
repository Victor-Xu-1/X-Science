import { beforeEach, describe, expect, it, vi } from 'vitest';
import { loadConversationMessagePage } from '@/renderer/utils/chat/messagePagination';
import { getSynonBiomedBranchSelectionRevision } from '@/renderer/services/synonBiomedConversationBranches';
import {
  loadConversationTurnIndex,
  mergeLiveTurnPreviews,
} from '@/renderer/pages/conversation/components/ConversationTitleMinimap/turnIndex';
import { buildTurnPreview } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/minimapUtils';
import type { TMessage } from '@/common/chat/chatLib';
vi.mock('@/renderer/utils/chat/messagePagination', () => ({ loadConversationMessagePage: vi.fn() }));
vi.mock('@/renderer/services/synonBiomedConversationBranches', () => ({
  getSynonBiomedBranchSelectionRevision: vi.fn(),
}));
const load = vi.mocked(loadConversationMessagePage);
const revision = vi.mocked(getSynonBiomedBranchSelectionRevision);
const message = (id: string, position: 'left' | 'right' = 'right', content = id): TMessage => ({
  id,
  conversation_id: 'c',
  type: 'text',
  position,
  content: { content },
});
const page = (items: TMessage[], before?: string) => ({
  items,
  has_more_before: Boolean(before),
  has_more_after: false,
  oldest_cursor: before ?? null,
  newest_cursor: null,
  branch_id: 'br_12345678',
  branch_generation: 1,
});
beforeEach(() => {
  load.mockReset();
  revision.mockReset().mockReturnValue(0);
});
describe('paged turn index', () => {
  it('counts an inclusive overlapping page boundary only once by canonical message identity', async () => {
    load.mockResolvedValueOnce(page([message('q2'), message('a2', 'left')], 'older'));
    load.mockResolvedValueOnce(page([message('q1'), message('a1', 'left'), message('q2')]));
    const { items: turns, branchId } = await loadConversationTurnIndex('c');
    expect(branchId).toBe('br_12345678');
    expect(turns.map((turn) => turn.messageId)).toEqual(['q1', 'q2']);
    expect(turns[1].answer).toBe('a2');
  });
  it('joins a turn across page boundaries with one bounded page request at a time', async () => {
    load.mockResolvedValueOnce(page([message('a1', 'left', 'Answer'), message('q2')], 'older'));
    load.mockResolvedValueOnce(page([message('q1')]));
    const { items: result } = await loadConversationTurnIndex('c');
    expect(result.map((x) => [x.messageId, x.answer])).toEqual([
      ['q1', 'Answer'],
      ['q2', ''],
    ]);
    expect(load.mock.calls[1][1]).toMatchObject({
      limit: 200,
      before: 'older',
      branchId: 'br_12345678',
      contentMode: 'compact',
    });
  });
  it('rejects a non-advancing cursor instead of looping or silently truncating history', async () => {
    load.mockResolvedValue(page([message('q')], 'same'));
    await expect(loadConversationTurnIndex('c')).rejects.toThrow('cursor_not_advancing');
    expect(load).toHaveBeenCalledTimes(2);
  });
  it('rejects a branch switch and honors cancellation', async () => {
    revision.mockReturnValueOnce(0).mockReturnValue(1);
    load.mockResolvedValue(page([message('q')]));
    await expect(loadConversationTurnIndex('c')).rejects.toThrow('branch_selection_changed');
    const controller = new AbortController();
    controller.abort();
    await expect(loadConversationTurnIndex('c', { signal: controller.signal })).rejects.toThrow();
    expect(load).toHaveBeenCalledTimes(1);
  });
  it('keeps the first preview and stable numbering when only a tail window updates', () => {
    const history = buildTurnPreview([message('q1'), message('a1', 'left'), message('q2')]);
    const merged = mergeLiveTurnPreviews(history, [message('q2'), message('a2', 'left'), message('q3')]);
    expect(merged.map((x) => [x.messageId, x.index])).toEqual([
      ['q1', 1],
      ['q2', 2],
      ['q3', 3],
    ]);
    expect(merged[0].answer).toBe('a1');
    expect(merged[1].answer).toBe('a2');
  });
  it('does not turn right-positioned non-text records into user prompts', async () => {
    load.mockResolvedValue(page([message('q'), { ...message('tool'), type: 'tool_call', content: {} } as TMessage]));
    expect((await loadConversationTurnIndex('c')).items).toHaveLength(1);
  });
});

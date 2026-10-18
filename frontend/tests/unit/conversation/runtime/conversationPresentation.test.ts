import type { TChatConversation } from '@/common/config/storage';
import { beforeEach, describe, expect, it, vi } from 'vitest';
const mock = vi.hoisted(() => ({ request: vi.fn(), scope: 'owner-a' }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: mock.request }));
vi.mock('@/renderer/services/rendererAccountScope', () => ({ getRendererAccountScopeToken: () => mock.scope }));
import {
  conversationPresentationAuthority,
  reorderPinnedConversations,
  setConversationPinned,
} from '@/renderer/services/runtime/conversationPresentation';
import { mergeConversationSection } from '@/renderer/pages/conversation/GroupedHistory/hooks/conversationListPagination';

const row = (id: string, pinned = false, revision = 0): TChatConversation => ({
  id,
  name: id,
  type: 'acp',
  created_at: 1,
  modified_at: 1,
  extra: { backend: 'xscience', pinned, presentation_revision: revision, frame_incarnation_id: `inc-${id}` },
});

describe('conversation presentation authority', () => {
  beforeEach(() => {
    mock.request.mockReset();
    mock.scope = 'owner-a';
  });
  it('requires durable identity and never writes a guessed revision', async () => {
    const invalid = row('missing');
    delete (invalid.extra as Record<string, unknown>).presentation_revision;
    expect(() => conversationPresentationAuthority(invalid)).toThrow();
    await expect(setConversationPinned(invalid, true)).rejects.toThrow();
    expect(mock.request).not.toHaveBeenCalled();
  });
  it('sends only the desired pin and original CAS without mutating the source row', async () => {
    mock.request.mockResolvedValue(true);
    const original = row('a', false, 2);
    await setConversationPinned(original, true);
    expect(mock.request).toHaveBeenCalledWith('PATCH', '/api/conversations/a', {
      extra: { pinned: true },
      merge_extra: true,
      expected_incarnation_id: 'inc-a',
      expected_presentation_revision: 2,
    });
    expect(original.extra.pinned).toBe(false);
  });
  it('reconciles a lost receipt with a read, never another write', async () => {
    mock.request.mockRejectedValueOnce(new Error('lost reply')).mockResolvedValueOnce(row('a', true, 3));
    await setConversationPinned(row('a', false, 2), true);
    expect(mock.request.mock.calls.map(([method]) => method)).toEqual(['PATCH', 'GET']);
  });
  it('does not retry a stale write against newer authority or a replacement incarnation', async () => {
    mock.request.mockRejectedValueOnce(new Error('409 stale')).mockResolvedValueOnce(row('a', false, 4));
    await expect(setConversationPinned(row('a', false, 2), true)).rejects.toThrow('409 stale');
    expect(mock.request).toHaveBeenCalledTimes(2);
    mock.request.mockReset();
    const replaced = row('a', true, 0);
    (replaced.extra as Record<string, unknown>).frame_incarnation_id = 'replacement';
    mock.request.mockRejectedValueOnce(new Error('lost')).mockResolvedValueOnce(replaced);
    await expect(setConversationPinned(row('a'), true)).rejects.toThrow();
  });
  it('does not read or write under another account after a lost reply', async () => {
    mock.request.mockImplementationOnce(async () => {
      mock.scope = 'owner-b';
      throw new Error('changed account');
    });
    await expect(setConversationPinned(row('a'), true)).rejects.toThrow();
    expect(mock.request).toHaveBeenCalledTimes(1);
  });
  it('reorders all selected rows through one atomic request', async () => {
    mock.request.mockResolvedValue(true);
    await reorderPinnedConversations([row('b', true, 4), row('a', true, 2)]);
    expect(mock.request).toHaveBeenCalledOnce();
    expect(mock.request.mock.calls[0][2]).toEqual({
      pinned_order: [
        { id: 'b', expected_incarnation_id: 'inc-b', expected_presentation_revision: 4, sortOrder: 1000 },
        { id: 'a', expected_incarnation_id: 'inc-a', expected_presentation_revision: 2, sortOrder: 2000 },
      ],
    });
  });
  it('merges independent sections without dropping old pins or duplicating a changed row', () => {
    const pins = [row('old-pin', true, 2)];
    const merged = mergeConversationSection(pins, [row('recent')], 'recent', false);
    expect(merged.map((item) => item.id)).toEqual(['old-pin', 'recent']);
    const moved = mergeConversationSection(merged, [row('recent', true, 1), row('old-pin', true, 2)], 'pinned', false);
    expect(moved.filter((item) => item.id === 'recent')).toHaveLength(1);
    expect(moved.find((item) => item.id === 'recent')?.extra.pinned).toBe(true);
    expect(
      mergeConversationSection(moved, [row('recent', false, 0)], 'recent', true).find((item) => item.id === 'recent')
        ?.extra.pinned
    ).toBe(true);
  });
  it('does not compare presentation revisions across a recreated frame identity', () => {
    const previous = row('recreated', false, 8);
    const replacement = row('recreated', true, 0);
    replacement.extra.frame_incarnation_id = 'replacement-incarnation';
    expect(mergeConversationSection([previous], [replacement], 'pinned', true)).toEqual([replacement]);
  });
});

import { describe, expect, it } from 'vitest';
import {
  turnRailProgress,
  turnRailScale,
  turnRailWindow,
} from '@/renderer/pages/conversation/components/ConversationTitleMinimap/turnRailModel';
import type { TMessage } from '@/common/chat/chatLib';
import { buildTurnPreview } from '@/renderer/pages/conversation/components/ConversationTitleMinimap/minimapUtils';

const text = (id: string, position: 'left' | 'right', content: string, blockIndex = 0): TMessage => ({
  id,
  conversation_id: 'conversation',
  type: 'text',
  position,
  content: { content, synonBiomed: { messageIndex: 0, blockIndex, branchId: null } },
});

describe('conversation turn projection', () => {
  it('keeps separate submissions with identical wording as separate turns', () => {
    expect(buildTurnPreview([text('q1', 'right', 'Same question'), text('q2', 'right', 'Same question')])).toHaveLength(
      2
    );
  });
  it.each([1, 3, 100, 1000])('keeps every one of %i turns without a fixed task count', (count) => {
    const turns = buildTurnPreview(Array.from({ length: count }, (_, i) => text(`q${i}`, 'right', `Question ${i}`)));
    expect(turns).toHaveLength(count);
    const reachable = new Set<number>();
    for (let top = 0; top < count * 8; top += 240)
      for (const index of turnRailWindow(count, top, 480)) reachable.add(index);
    expect(reachable.size).toBe(count);
    expect(turnRailWindow(count, 0, 480).length).toBeLessThanOrEqual(66);
  });
  it('continuously tapers marks and tracks fractional reading progress', () => {
    expect(turnRailScale(null, 0)).toBe(0.4);
    expect(turnRailScale(1, 1)).toBe(1);
    expect(turnRailScale(1.5, 1)).toBeCloseTo(turnRailScale(1.5, 2));
    expect(turnRailScale(1, 5)).toBeCloseTo(0.4);
    expect(
      turnRailProgress(
        new Map([
          [0, 0],
          [1, 100],
        ]),
        50
      )
    ).toBe(0.5);
    expect(turnRailProgress(new Map(), 50, 7)).toBe(7);
  });
  it('indexes logical user turns and tool anchors without exposing hidden or tool payloads', () => {
    const messages: TMessage[] = [
      { ...text('hidden', 'right', 'private input'), hidden: true },
      text('synonbiomed-optimistic-user:pending', 'right', 'Question one'),
      text('q1', 'right', 'Question one'),
      text('q1-file', 'right', 'attachment block', 1),
      { id: 'tool1', conversation_id: 'conversation', type: 'tool_call', position: 'left', content: {} } as TMessage,
      text('a1', 'left', 'First answer'),
      { ...text('secret', 'left', 'hidden answer'), hidden: true },
      text('q2', 'right', 'Question two'),
      text('a2', 'left', 'Second answer'),
    ];
    const turns = buildTurnPreview(messages);
    expect(turns.map((turn) => [turn.messageId, turn.question, turn.answer])).toEqual([
      ['q1', 'Question one', 'First answer'],
      ['q2', 'Question two', 'Second answer'],
    ]);
    expect(turns[0].messageIds).toEqual(['q1', 'q1-file', 'tool1', 'a1']);
    expect(JSON.stringify(turns)).not.toContain('private input');
    expect(JSON.stringify(turns)).not.toContain('hidden answer');
  });
});

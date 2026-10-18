import { describe, expect, it } from 'vitest';
import { getCleanFileName } from '@/renderer/services/FileService';
import { buildDisplayMessage } from '@/renderer/utils/file/messageFiles';
import {
  createQueuedCommandItem,
  normalizeQueueState,
} from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';

const reference = `x-science-host-grant://file?${new URLSearchParams({
  grant: '/fixture/granted root',
  path: 'nested/sample + 测量%20.csv',
})}`;

describe('ordinary composer host-grant file references', () => {
  it('retains the exact URI in the ordinary files marker instead of prefixing the project workspace', () => {
    expect(buildDisplayMessage('Inspect this snapshot.', [reference], 'x-science://project')).toBe(
      `Inspect this snapshot.\n\n[[X_SCIENCE_FILES]]\n${reference}`
    );
  });

  it('presents the decoded filename without the granted root or double-decoding a percent literal', () => {
    expect(getCleanFileName(reference)).toBe('sample + 测量%20.csv');
    expect(getCleanFileName(reference)).not.toContain('/fixture');
  });

  it('freezes the original URI in queued files and retains it on owner-scoped queue restoration', () => {
    const files = [reference];
    const item = createQueuedCommandItem({ input: 'Inspect this snapshot.', files });
    files[0] = '/changed-after-queueing';
    expect(item.files).toEqual([reference]);
    const restored = normalizeQueueState(JSON.parse(JSON.stringify({ items: [item], isPaused: true })));
    expect(restored.items[0].files).toEqual([reference]);
    expect(restored.items[0].id).toBe(item.id);
  });
});

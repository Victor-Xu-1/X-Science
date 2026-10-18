import { describe, expect, it } from 'vitest';
import {
  createQueuedCommandItem,
  normalizeQueueState,
} from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import {
  normalizeComposerContextItems,
  buildComposerCapabilityPayload,
} from '@/renderer/components/chat/SendBox/composerCompositionModel';
import { pdfSelection } from './pdfReadingFixtures';

describe('C12 PDF draft and queued selection', () => {
  it('keeps normalized immutable queued selection and marks corrupt persisted selections as failed, not ordinary sends', () => {
    const selection = pdfSelection();
    const item = createQueuedCommandItem({ input: 'read this', files: [], readingContext: selection });
    selection.expected_revision = 20;
    expect(item.readingContext).toEqual(pdfSelection());
    const state = normalizeQueueState({
      items: [{ ...item, readingContext: { ...item.readingContext, expected_revision: 'bad' } }],
      isPaused: false,
    });
    expect(state.items).toHaveLength(1);
    expect(state.items[0]).toMatchObject({
      id: item.id,
      input: 'read this',
      readingContextInvalid: true,
      pausedReason: 'send_failed',
    });
    expect(state.items[0].readingContext).toBeUndefined();
  });
  it('retains draft reading intent without treating it as accepted backend attachment authority', () => {
    const items = normalizeComposerContextItems([
      {
        kind: 'artifact',
        artifactId: 'a',
        versionId: 'v',
        projectId: 'p',
        label: 'paper.pdf',
        readingSelected: true,
        readingPosition: { pageNumber: 3, pageCount: 9 },
      },
    ]);
    expect(items[0]).toMatchObject({ readingSelected: true, readingPosition: { pageNumber: 3, pageCount: 9 } });
    expect(buildComposerCapabilityPayload(items).artifactRefs).toEqual([
      { artifact_id: 'a', version_id: 'v', relation: 'attached', filename: 'paper.pdf' },
    ]);
  });
});

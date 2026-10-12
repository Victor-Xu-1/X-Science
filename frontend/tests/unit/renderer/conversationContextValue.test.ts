/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import type { ConversationContextValue } from '@/renderer/hooks/context/ConversationContext';
import { areConversationContextValuesEqual } from '@/renderer/hooks/context/ConversationContext';
import { describe, expect, it } from 'vitest';

const base: ConversationContextValue = {
  conversation_id: 'conversation-1',
  workspace: 'x-science://workspace-1',
  type: 'acp',
  projectId: 'project-1',
  currentFrameId: 'frame-1',
  loadedSkills: ['literature-review'],
  loadedMcpServers: ['pubmed'],
  loadedMcpStatuses: [{ id: 'pubmed', name: 'PubMed', status: 'loaded' }],
  assistantId: 'assistant-1',
};

describe('areConversationContextValuesEqual', () => {
  it('treats a rebuilt object with the same snapshot as equal', () => {
    expect(
      areConversationContextValuesEqual(base, {
        ...base,
        loadedSkills: [...base.loadedSkills!],
        loadedMcpServers: [...base.loadedMcpServers!],
        loadedMcpStatuses: [{ ...base.loadedMcpStatuses![0] }],
      })
    ).toBe(true);
  });

  it('invalidates when conversation identity or loaded skills change', () => {
    expect(areConversationContextValuesEqual(base, { ...base, conversation_id: 'conversation-2' })).toBe(false);
    expect(areConversationContextValuesEqual(base, { ...base, loadedSkills: ['protocol-draft'] })).toBe(false);
  });
});

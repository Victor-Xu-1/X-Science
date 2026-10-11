/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import React from 'react';
import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import {
  ConversationProvider,
  useConversationContext,
  type ConversationContextValue,
} from '@/renderer/hooks/context/ConversationContext';

const snapshot: ConversationContextValue = {
  conversation_id: 'conversation-1',
  workspace: 'synonbiomed://workspace-1',
  type: 'acp',
};

describe('ConversationProvider', () => {
  it('does not rerender memoized consumers when only the value object identity changes', () => {
    let renders = 0;
    const Consumer = React.memo(() => {
      useConversationContext();
      renders += 1;
      return null;
    });

    const { rerender } = render(
      <ConversationProvider value={{ ...snapshot }}>
        <Consumer />
      </ConversationProvider>
    );
    expect(renders).toBe(1);

    rerender(
      <ConversationProvider value={{ ...snapshot }}>
        <Consumer />
      </ConversationProvider>
    );
    expect(renders).toBe(1);

    rerender(
      <ConversationProvider value={{ ...snapshot, conversation_id: 'conversation-2' }}>
        <Consumer />
      </ConversationProvider>
    );
    expect(renders).toBe(2);
  });
});

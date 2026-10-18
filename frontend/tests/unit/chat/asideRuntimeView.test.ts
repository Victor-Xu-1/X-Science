import { describe, expect, it } from 'vitest';
import type { TMessage } from '@/common/chat/chatLib';
import { projectAsideEntries } from '@/renderer/components/chat/BtwOverlay/useAsideRuntimeView';

describe('aside canonical presentation', () => {
  it('shows real partial assistant text and tool progress without requiring a final output', () => {
    const messages: TMessage[] = [
      {
        id: 'seed:0',
        msg_id: 'seed',
        conversation_id: 'child',
        type: 'text',
        position: 'left',
        content: { content: 'Parent snapshot' },
      },
      {
        id: 'input:0',
        msg_id: 'input',
        conversation_id: 'child',
        type: 'text',
        position: 'right',
        content: { content: 'Independent question' },
      },
      {
        id: 'assistant:0',
        msg_id: 'assistant',
        conversation_id: 'child',
        type: 'text',
        position: 'left',
        status: 'work',
        content: { content: 'Live partial answer' },
      },
      {
        id: 'tools',
        msg_id: 'assistant',
        conversation_id: 'child',
        type: 'tool_group',
        content: [
          {
            call_id: 'call-1',
            name: 'web_search',
            description: 'Search sources',
            status: 'Executing',
            render_output_as_markdown: false,
          },
        ],
      },
    ];
    const entries = projectAsideEntries(messages, 'child', 'input');
    expect(entries).toEqual([
      { id: 'input:0', kind: 'message', role: 'user', text: 'Independent question' },
      { id: 'assistant:0', kind: 'message', role: 'assistant', text: 'Live partial answer' },
      { id: 'call-1', kind: 'tool', title: 'Search sources', status: 'Executing' },
    ]);
  });

  it('does not expose another frame or an unproved inherited prefix as side-chat activity', () => {
    const messages: TMessage[] = [
      {
        id: 'input:0',
        msg_id: 'input',
        conversation_id: 'other',
        type: 'text',
        position: 'right',
        content: { content: 'Not this child' },
      },
      {
        id: 'seed:0',
        msg_id: 'seed',
        conversation_id: 'child',
        type: 'text',
        position: 'left',
        content: { content: 'Parent snapshot' },
      },
    ];
    expect(projectAsideEntries(messages, 'child', 'input')).toBeNull();
  });
});

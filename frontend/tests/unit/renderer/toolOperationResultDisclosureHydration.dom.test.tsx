import React from 'react';
import { act, cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ipcBridge } from '@/common';
import type { ToolMessage } from '@/common/chat/normalizeToolCall';
import MessageToolGroupSummary from '@/renderer/pages/conversation/Messages/components/MessageToolGroupSummary';
import { MessagePaginationProvider } from '@/renderer/pages/conversation/Messages/hooks';
import { conversationDisclosureStore } from '@/renderer/services/runtime/conversationDisclosureStore';
import { resetXScienceBranchSelection } from '@/renderer/services/xScienceConversationBranches';

vi.mock('@/common', () => ({ ipcBridge: { database: { getConversationMessage: { invoke: vi.fn() } } } }));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }),
}));
vi.mock('@arco-design/web-react', () => ({
  Button: ({ children }: React.PropsWithChildren) => <button>{children}</button>,
  Tooltip: ({ children }: React.PropsWithChildren) => <>{children}</>,
  Message: { useMessage: () => [{ success: vi.fn(), error: vi.fn() }, null] },
}));
vi.mock('@icon-park/react', () => ({ Download: () => null, CheckSmall: () => null, CloseSmall: () => null }));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: React.PropsWithChildren) => <div>{children}</div>,
}));
vi.mock('@/renderer/components/media/LocalImageView', () => ({ default: () => null }));
vi.mock('@/renderer/utils/file/download', () => ({ downloadFileFromPath: vi.fn() }));
vi.mock('@/renderer/pages/conversation/Messages/components/ToolOperationDiffs', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/Messages/components/MessageWebResearchSources', () => ({ default: () => null }));

describe('tool result disclosure during full-message hydration', () => {
  beforeEach(() => {
    conversationDisclosureStore.resetForTests();
    resetXScienceBranchSelection('frame');
    vi.mocked(ipcBridge.database.getConversationMessage.invoke).mockReset();
  });
  afterEach(cleanup);

  it.each(['pointer', 'keyboard'] as const)(
    'preserves the user output expansion with %s when the full message omits its window branch',
    async (activation) => {
      const user = userEvent.setup();
      const activate = async (element: HTMLElement) => {
        if (activation === 'pointer') {
          await user.click(element);
        } else {
          element.focus();
          await user.keyboard('{Enter}');
        }
      };
      const branchId = 'br_deadbeef';
      const fullMessage = {
        id: 'message',
        conversation_id: 'frame',
        type: 'tool_call',
        content: {
          call_id: 'call',
          name: 'bash',
          status: 'completed',
          revision: 1,
          args: { command: 'printf fixture' },
          output: JSON.stringify({ stdout: 'hydrated fixture output', exit_code: 0 }),
        },
      };
      let resolve!: (message: unknown) => void;
      const invoke = vi.mocked(ipcBridge.database.getConversationMessage.invoke);
      invoke.mockReturnValue(
        new Promise((accept) => {
          resolve = accept;
        }) as never
      );
      const compact = {
        ...fullMessage,
        content: {
          ...fullMessage.content,
          output: JSON.stringify({ stdout: 'compact fixture output', exit_code: 0 }),
          _compact: { truncated: true },
        },
      } as unknown as ToolMessage;
      render(
        <MessagePaginationProvider
          value={{
            conversationId: 'frame',
            branchId,
            hasMoreBefore: false,
            hasMoreAfter: false,
            isLoadingBefore: false,
            isLoadingAfter: false,
            isLoadingAnchor: false,
          }}
        >
          <MessageToolGroupSummary messages={[compact]} />
        </MessagePaginationProvider>
      );

      await activate(screen.getByTestId('tool-chip'));
      await activate(screen.getByTestId('show-output-toggle'));
      expect(screen.getByTestId('show-output-toggle')).toHaveAttribute('aria-expanded', 'true');
      expect(invoke).toHaveBeenCalledExactlyOnceWith({
        conversation_id: 'frame',
        message_id: 'message',
        branch_id: branchId,
      });
      await act(async () => resolve(fullMessage));

      expect(screen.queryByRole('alert')).toBeNull();
      expect(screen.getByTestId('tool-chip')).toHaveAttribute('aria-expanded', 'true');
      expect(screen.getByTestId('show-output-toggle')).toHaveAttribute('aria-expanded', 'true');
      expect(screen.getByTestId('tool-public-output')).toHaveTextContent('hydrated fixture output');
      if (activation === 'keyboard') expect(screen.getByTestId('show-output-toggle')).toHaveFocus();
    }
  );
});

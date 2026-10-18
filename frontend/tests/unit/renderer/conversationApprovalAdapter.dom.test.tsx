import type { IMessageAcpPermission, IMessagePermission } from '@/common/chat/chatLib';
import MessageAcpPermission from '@/renderer/pages/conversation/Messages/acp/MessageAcpPermission';
import MessagePermission from '@/renderer/pages/conversation/Messages/components/MessagePermission';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const { confirmInvoke } = vi.hoisted(() => ({ confirmInvoke: vi.fn() }));

vi.mock('@/common', () => ({
  ipcBridge: {
    conversation: {
      confirmation: {
        confirm: { invoke: confirmInvoke },
      },
    },
  },
}));

describe('conversation approval adapter', () => {
  beforeEach(() => {
    confirmInvoke.mockReset();
    confirmInvoke.mockResolvedValue(undefined);
  });

  it('submits compatibility permissions through the canonical confirmation endpoint', async () => {
    const message = {
      id: 'permission-message',
      msg_id: 'permission-message',
      type: 'permission',
      position: 'left',
      conversation_id: 'conversation-1',
      created_at: Date.now(),
      content: {
        id: 'approval-1',
        call_id: 'approval-1',
        title: 'File access',
        description: 'Allow file access?',
        options: [
          { label: 'Allow once', value: 'proceed_once' },
          { label: 'Always allow', value: 'proceed_always' },
        ],
      },
    } as IMessagePermission;

    await renderWithI18n(<MessagePermission message={message} />, 'en-US');
    fireEvent.click(screen.getByRole('radio', { name: 'Always allow' }));
    fireEvent.click(screen.getByTestId('message-permission-confirm'));

    await waitFor(() =>
      expect(confirmInvoke).toHaveBeenCalledWith({
        conversation_id: 'conversation-1',
        call_id: 'approval-1',
        msg_id: 'permission-message',
        data: { value: 'proceed_always' },
        always_allow: true,
      })
    );
  });

  it('adapts ACP permission options to the same card and endpoint without a fallback transport', async () => {
    const message = {
      id: 'acp-permission-message',
      msg_id: 'acp-permission-message',
      type: 'acp_permission',
      position: 'left',
      conversation_id: 'conversation-2',
      created_at: Date.now(),
      content: {
        session_id: 'session-2',
        options: [{ option_id: 'allow-always-custom', name: 'Always allow', kind: 'allow_always' }],
        tool_call: {
          tool_call_id: 'tool-call-2',
          title: 'Run command',
          kind: 'execute',
          raw_input: { command: 'echo ok' },
        },
      },
    } as IMessageAcpPermission;

    await renderWithI18n(<MessageAcpPermission message={message} />, 'en-US');
    expect(screen.getByTestId('message-acp-permission-card')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('radio', { name: 'Always allow' }));
    fireEvent.click(screen.getByTestId('message-acp-permission-confirm'));

    await waitFor(() =>
      expect(confirmInvoke).toHaveBeenCalledWith({
        conversation_id: 'conversation-2',
        call_id: 'tool-call-2',
        msg_id: 'acp-permission-message',
        data: 'allow-always-custom',
        always_allow: true,
      })
    );
  });
  it.each(
    (
      [
        ['en-US', ['Allow once', 'Allow this session', 'Always allow', 'Deny']],
        ['zh-CN', ['仅允许本次', '允许本会话', '始终允许', '拒绝']],
      ] as const
    ).flatMap(([language, labels]) =>
      ['proceed_once', 'proceed_conversation', 'proceed_always', 'deny'].map((value, index) => ({
        language,
        labels,
        label: labels[index],
        value,
      }))
    )
  )(
    'C6 translates every advertised label and forwards exact $value in $language',
    async ({ language, labels, label, value }) => {
      const message = {
        id: 'grant-choice',
        msg_id: 'grant-choice',
        type: 'permission',
        position: 'left',
        conversation_id: 'grant-child',
        created_at: 1,
        content: {
          id: 'local-exec',
          call_id: 'local-exec',
          title: 'Python',
          options: [
            { label: 'Allow once', value: 'proceed_once' },
            { label: 'Allow this session', value: 'proceed_conversation' },
            { label: 'Always allow', value: 'proceed_always' },
            { label: 'Deny', value: 'deny' },
          ],
        },
      } as IMessagePermission;
      await renderWithI18n(<MessagePermission message={message} />, language);
      expect(confirmInvoke).not.toHaveBeenCalled();
      expect(screen.getAllByRole('radio')).toHaveLength(4);
      for (const advertisedLabel of labels) {
        expect(screen.getByRole('radio', { name: advertisedLabel })).toBeInTheDocument();
      }
      fireEvent.click(screen.getByRole('radio', { name: label }));
      fireEvent.click(screen.getByTestId('message-permission-confirm'));
      await waitFor(() =>
        expect(confirmInvoke).toHaveBeenCalledWith({
          conversation_id: 'grant-child',
          call_id: 'local-exec',
          msg_id: 'grant-choice',
          data: { value },
          always_allow: value === 'proceed_always',
        })
      );
    }
  );
  it('C6 never adds a session grant choice that the producer did not advertise', async () => {
    const message = {
      id: 'once-only',
      msg_id: 'once-only',
      type: 'permission',
      position: 'left',
      conversation_id: 'grant-child',
      created_at: 1,
      content: {
        id: 'software-runtime',
        call_id: 'software-runtime',
        title: 'Software runtime',
        options: [{ label: 'Allow once', value: 'proceed_once' }],
      },
    } as IMessagePermission;
    await renderWithI18n(<MessagePermission message={message} />, 'en-US');
    expect(screen.queryByRole('radio', { name: 'Allow this session' })).not.toBeInTheDocument();
    expect(screen.getAllByRole('radio')).toHaveLength(1);
    expect(confirmInvoke).not.toHaveBeenCalled();
  });
});

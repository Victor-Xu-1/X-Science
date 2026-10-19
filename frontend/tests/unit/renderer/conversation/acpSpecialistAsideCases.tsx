import React from 'react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { expect, it, vi, type Mock } from 'vitest';
import AcpSendBox from '@/renderer/pages/conversation/platforms/acp/AcpSendBox';
import type { ConversationCommandQueueItem } from '@/renderer/pages/conversation/platforms/useConversationCommandQueue';
import type { SpecialistSendAuthority } from '@/common/adapter/specialistBinding';
import type { UseAcpMessageReturn } from '@/renderer/pages/conversation/platforms/acp/useAcpMessage';
import { specialistSnapshot } from '../../xscience/sessionSpecialistFixtures';

export type SpecialistAsideFixture = {
  active: boolean;
  item: ConversationCommandQueueItem | null;
  draft: Record<string, unknown>;
  capture: Mock<(branch?: string | null) => SpecialistSendAuthority>;
  warning: Mock;
  remove: Mock;
};
const snapshot = specialistSnapshot(1, 'AGENT_B');
const authority: SpecialistSendAuthority = {
  reference: {
    scope: snapshot.scope,
    branch: snapshot.prepared_branch,
    revision: snapshot.revision,
    snapshot_id: snapshot.desired.snapshot_id,
  },
  targetAgent: 'AGENT_B',
};
const item = (binding: SpecialistSendAuthority = authority): ConversationCommandQueueItem => ({
  id: 'frozen-item',
  input: 'synthetic frozen draft',
  files: [],
  contextItems: [],
  created_at: 1,
  specialistAuthority: binding,
});

/** Registers only direct Acp button consumers; the existing file owns its ordinary fixture setup. */
export function registerSpecialistAsideCases(args: {
  fixture: SpecialistAsideFixture;
  render: (element: React.ReactElement) => Promise<unknown>;
  messageState: () => UseAcpMessageReturn;
  prepare: () => void;
  asideAsk: Mock;
  send: Mock;
}) {
  const { fixture } = args;
  const mount = async (queued?: ConversationCommandQueueItem) => {
    args.prepare();
    fixture.active = true;
    fixture.item = queued ?? null;
    await args.render(<AcpSendBox conversation_id='conv-1' backend='xscience' messageState={args.messageState()} />);
    if (queued) fireEvent.click(await screen.findByRole('button', { name: 'edit frozen queue' }));
  };
  const choose = async (label: string) => {
    fireEvent.click(screen.getByTestId('x-science-more-send-options'));
    fireEvent.click(await screen.findByRole('menuitem', { name: label }));
    await act(async () => {
      await Promise.resolve();
    });
  };
  const expectBlocked = () => {
    expect(args.asideAsk).not.toHaveBeenCalled();
    expect(args.send).not.toHaveBeenCalled();
    expect(fixture.warning).toHaveBeenCalledWith('文件、Skill 和 MCP 组合内容请使用普通发送或先制定计划。');
    expect(screen.getByTestId('sendbox-draft')).toHaveTextContent('synthetic frozen draft');
  };
  it.each(['侧聊', '在新会话中分支'])(
    'C404 alternate blocks an edited typed queue from %s without losing its draft or reference',
    async (label) => {
      await mount(item());
      await choose(label);
      expectBlocked();
      expect(fixture.remove).toHaveBeenCalledWith('frozen-item');
      expect(fixture.draft.specialistQueuedAuthority).toEqual(authority);
    }
  );
  it('C404 alternate captures an ordinary prepared selection before starting unsupported side-chat', async () => {
    fixture.capture.mockReturnValue(authority);
    await mount();
    await choose('侧聊');
    expectBlocked();
    expect(fixture.capture).toHaveBeenCalledWith(null);
  });
  it('C404 alternate rejects invalid recovered authority and preserves the recovery marker', async () => {
    await mount({ ...item(), specialistAuthority: undefined, specialistAuthorityInvalid: true });
    await choose('在新会话中分支');
    expectBlocked();
    expect(fixture.draft.specialistQueuedInvalid).toBe(true);
    expect(fixture.capture).not.toHaveBeenCalled();
  });
  it('C404 alternate preserves an explicitly captured legacy queue authority over the current prepared selector', async () => {
    fixture.capture.mockReturnValue(authority);
    await mount(item({}));
    await choose('侧聊');
    expect(args.asideAsk).toHaveBeenCalledWith('synthetic frozen draft', expect.objectContaining({ kind: 'draft' }));
    expect(fixture.capture).not.toHaveBeenCalled();
    expect(fixture.warning).not.toHaveBeenCalled();
  });
  it('C404 alternate keeps plan-first on ordinary send with the exact edited queue binding', async () => {
    fixture.capture.mockImplementation(() => {
      throw new Error('must retain original queue reference');
    });
    args.send.mockResolvedValue({
      msg_id: 'ordinary',
      turn_id: 'turn',
      runtime: { state: 'running', is_processing: true },
    });
    await mount(item());
    await choose('计划优先');
    await waitFor(() =>
      expect(args.send).toHaveBeenCalledWith(
        expect.objectContaining({
          session_options: expect.objectContaining({
            plan_mode: true,
            specialist_binding: authority.reference,
            target_agent: 'AGENT_B',
          }),
        })
      )
    );
    expect(args.asideAsk).not.toHaveBeenCalled();
    expect(fixture.capture).not.toHaveBeenCalled();
  });
  it('C404 alternate refuses an unconfirmed current binding without starting a second request path', async () => {
    fixture.capture.mockImplementation(() => {
      throw new Error('unconfirmed selection');
    });
    await mount();
    await choose('侧聊');
    expectBlocked();
  });
}

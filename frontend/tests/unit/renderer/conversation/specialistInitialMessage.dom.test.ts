import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { useAcpInitialMessage } from '@/renderer/pages/conversation/platforms/acp/useAcpInitialMessage';
import { specialistSnapshot } from '../../xscience/sessionSpecialistFixtures';
const { send } = vi.hoisted(() => ({ send: vi.fn() }));
vi.mock('@/common', () => ({ ipcBridge: { acpConversation: { sendMessage: { invoke: send } } } }));
vi.mock('@/renderer/services/xScienceCompute', () => ({ setXScienceSessionComputeProvider: vi.fn() }));
vi.mock('@/renderer/utils/file/messageFiles', () => ({ buildDisplayMessage: (input: string) => input }));
vi.mock('@/renderer/utils/emitter', () => ({ emitter: { emit: vi.fn() } }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key }) }));
const reference = (() => {
  const current = specialistSnapshot(1, 'AGENT_B');
  return {
    scope: current.scope,
    branch: current.prepared_branch,
    revision: 1,
    snapshot_id: current.desired.snapshot_id,
  };
})();
const params = (id: string) => ({
  conversation_id: id,
  backend: 'xscience',
  streamReady: true,
  setAiProcessing: vi.fn(),
  resetState: vi.fn(),
  markSendStarted: vi.fn(),
  markSendAccepted: vi.fn(),
  markSendFailed: vi.fn(),
  checkAndUpdateTitle: vi.fn(),
  addOrUpdateMessage: vi.fn(),
});
beforeEach(() => {
  sessionStorage.clear();
  send.mockReset();
  send.mockResolvedValue({ turn_id: 'turn', msg_id: 'message', runtime: {} });
});
afterEach(cleanup);
it('keeps the exact first-message specialist reference and nonce on a lost ACK retry', async () => {
  const key = 'acp_initial_message_specialist-initial';
  sessionStorage.setItem(
    key,
    JSON.stringify({
      input: 'synthetic work',
      loading_id: 'original-nonce',
      session_options: { ultra_mode: true, target_agent: 'AGENT_B', specialist_binding: reference },
    })
  );
  send.mockRejectedValueOnce(new TypeError('lost ACK'));
  const { result } = renderHook(() => useAcpInitialMessage(params('specialist-initial')));
  await vi.waitFor(() => expect(result.current.failed).toBe(true));
  const original = structuredClone(send.mock.calls[0][0]);
  expect(original).toMatchObject({
    loading_id: 'original-nonce',
    session_options: { target_agent: 'AGENT_B', specialist_binding: reference },
  });
  expect(sessionStorage.getItem(key)).not.toBeNull();
  act(() => result.current.retry());
  await vi.waitFor(() => expect(send).toHaveBeenCalledTimes(2));
  expect(send.mock.calls[1][0]).toEqual(original);
  await vi.waitFor(() => expect(sessionStorage.getItem(key)).toBeNull());
});
it('does not silently remove a malformed initial binding and send under a different selection', async () => {
  const key = 'acp_initial_message_specialist-invalid',
    original = JSON.stringify({
      input: 'retained original',
      loading_id: 'retained-nonce',
      session_options: { specialist_binding: { ...reference, snapshot_id: '' } },
    });
  sessionStorage.setItem(key, original);
  const { result } = renderHook(() => useAcpInitialMessage(params('specialist-invalid')));
  await vi.waitFor(() => expect(result.current.failed).toBe(true));
  expect(send).not.toHaveBeenCalled();
  expect(sessionStorage.getItem(key)).toBe(original);
});
it('stages a frozen selection into the existing durable draft and never auto-sends it', async () => {
  const onDraftPrefill = vi.fn(),
    key = 'acp_initial_message_specialist-draft';
  sessionStorage.setItem(
    key,
    JSON.stringify({
      input: 'editable work',
      draft_only: true,
      session_options: { target_agent: 'AGENT_B', specialist_binding: reference },
    })
  );
  renderHook(() => useAcpInitialMessage({ ...params('specialist-draft'), onDraftPrefill }));
  await vi.waitFor(() =>
    expect(onDraftPrefill).toHaveBeenCalledWith(
      expect.objectContaining({ input: 'editable work', specialistAuthority: { reference, targetAgent: 'AGENT_B' } })
    )
  );
  expect(send).not.toHaveBeenCalled();
  expect(sessionStorage.getItem(key)).toBeNull();
});

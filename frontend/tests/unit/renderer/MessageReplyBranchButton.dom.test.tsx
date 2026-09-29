import React from 'react';
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';
import MessageReplyBranchButton from '@/renderer/pages/conversation/Messages/components/MessageReplyBranchButton';

const state = vi.hoisted(() => ({
  runtime: { hydrated: true, isProcessing: false },
  navigate: vi.fn(),
  request: vi.fn(),
  error: vi.fn(),
  account: 'account',
}));
vi.mock('react-router', () => ({ useNavigate: () => state.navigate }));
vi.mock('@/renderer/pages/conversation/runtime/useConversationRuntimeView', () => ({
  useConversationRuntimeView: () => state.runtime,
}));
vi.mock('@/renderer/services/synonBiomedReplyBranch', () => ({ branchSynonBiomedReply: state.request }));
vi.mock('@/renderer/services/rendererAccountScope', () => ({ getRendererAccountScopeToken: () => state.account }));
vi.mock('@/renderer/services/synonBiomedConversationBranches', () => ({
  createSynonBiomedBranchMutationId: () => 'stable-intent',
}));
vi.mock('@arco-design/web-react', async (original) => ({
  ...(await original<object>()),
  Message: { error: state.error },
}));
const props = { conversationId: 'source', throughAttempt: 3, sourceBranchId: 'br_12345678' };
beforeEach(() => {
  vi.clearAllMocks();
  state.runtime = { hydrated: true, isProcessing: false };
  state.account = 'account';
});

describe('reply branch action', () => {
  it('sends the exact completed boundary once and opens returned conversation', async () => {
    let resolve!: (id: string) => void;
    state.request.mockImplementation(
      () =>
        new Promise<string>((done) => {
          resolve = done;
        })
    );
    await renderWithI18n(<MessageReplyBranchButton {...props} />);
    const button = screen.getByRole('button', { name: /分支|Branch/ });
    fireEvent.click(button);
    fireEvent.click(button);
    await waitFor(() => expect(screen.getByRole('button')).toBeDisabled());
    expect(screen.getByRole('button')).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('button').querySelector('.arco-icon-loading')).toHaveAttribute('aria-hidden', 'true');
    expect(state.request).toHaveBeenCalledExactlyOnceWith({ ...props, intentId: 'stable-intent' });
    await act(async () => resolve('new-conversation'));
    expect(state.navigate).toHaveBeenCalledWith('/conversation/new-conversation');
  });
  it('keeps one intent for uncertain failure and explicit retry', async () => {
    state.request.mockRejectedValueOnce(new Error('lost response')).mockResolvedValueOnce('new');
    await renderWithI18n(<MessageReplyBranchButton {...props} />);
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => expect(state.error).toHaveBeenCalledOnce());
    expect(state.navigate).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button'));
    await waitFor(() => expect(state.navigate).toHaveBeenCalledWith('/conversation/new'));
    expect(state.request.mock.calls[0][0]).toEqual(state.request.mock.calls[1][0]);
  });
  it.each([
    { hydrated: false, isProcessing: false },
    { hydrated: true, isProcessing: true },
  ])('disables while unavailable %o', async (runtime) => {
    state.runtime = runtime;
    await renderWithI18n(<MessageReplyBranchButton {...props} />);
    expect(screen.getByRole('button')).toBeDisabled();
    fireEvent.click(screen.getByRole('button'));
    expect(state.request).not.toHaveBeenCalled();
  });
  it('does not navigate on a late response after unmount', async () => {
    let resolve!: (id: string) => void;
    state.request.mockImplementation(
      () =>
        new Promise<string>((done) => {
          resolve = done;
        })
    );
    const result = await renderWithI18n(<MessageReplyBranchButton {...props} />);
    fireEvent.click(screen.getByRole('button'));
    result.unmount();
    await act(async () => resolve('late'));
    expect(state.navigate).not.toHaveBeenCalled();
  });
});

import BtwOverlay from '@/renderer/components/chat/BtwOverlay';
import type { useBtwCommand } from '@/renderer/components/chat/BtwOverlay/useBtwCommand';
import { cleanup, fireEvent, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { shouldDismissBtwOverlay } from '@/renderer/components/chat/BtwOverlay/btwEscapePolicy';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: string }) => <p>{children}</p>,
}));
afterEach(cleanup);
type Controller = ReturnType<typeof useBtwCommand>;
const makeController = (overrides: Partial<Controller> = {}): Controller => ({
  owner: 'owner',
  generation: 1,
  operation: null,
  isOpen: true,
  starting: false,
  question: 'A concise question',
  draft: '',
  action: null,
  errorKey: null,
  entries: [{ id: 'partial', kind: 'message', role: 'assistant', text: 'A concise answer' }],
  snapshot: null,
  observationError: false,
  truncated: false,
  receipt: null,
  frameId: 'child-1',
  isLoading: false,
  error: null,
  persistenceError: false,
  closeRequested: false,
  acceptedConsumer: null,
  pendingFollowupText: null,
  canRetryFollowup: false,
  canSend: true,
  canCancel: false,
  canResume: false,
  ask: vi.fn().mockResolvedValue(true),
  retry: vi.fn().mockResolvedValue(true),
  close: vi.fn().mockResolvedValue(undefined),
  cancel: vi.fn().mockResolvedValue(undefined),
  send: vi.fn().mockResolvedValue(true),
  setDraft: vi.fn(),
  reconnect: vi.fn().mockResolvedValue(undefined),
  resume: vi.fn().mockResolvedValue(undefined),
  getReceipt: vi.fn().mockReturnValue(null),
  getAcceptedConsumer: vi.fn().mockReturnValue(null),
  acknowledgeConsumer: vi.fn(),
  acknowledgeBranch: vi.fn(),
  ...overrides,
});

describe('integrated side chat panel', () => {
  it('dismisses only Escape inside the panel and yields to open dialogs', () => {
    const panel = document.createElement('div');
    const input = document.createElement('input');
    panel.appendChild(input);
    document.body.appendChild(panel);
    expect(shouldDismissBtwOverlay(new KeyboardEvent('keydown', { key: 'Enter' }), panel)).toBe(false);
    expect(shouldDismissBtwOverlay(new KeyboardEvent('keydown', { key: 'Escape' }), panel)).toBe(true);
    expect(
      shouldDismissBtwOverlay({ key: 'Escape', defaultPrevented: false, isComposing: false, target: input }, panel)
    ).toBe(true);
    const dialog = document.createElement('div');
    dialog.setAttribute('role', 'dialog');
    document.body.appendChild(dialog);
    expect(
      shouldDismissBtwOverlay({ key: 'Escape', defaultPrevented: false, isComposing: false, target: input }, panel)
    ).toBe(false);
    dialog.remove();
    panel.remove();
  });

  it.each([
    ['en-US', 'Side chat', 'Close side chat'],
    ['zh-CN', '侧聊', '关闭侧聊'],
  ] as const)(
    'renders an in-flow labelled, resizable panel with no modal or dark backdrop in %s',
    async (language, title, closeLabel) => {
      const controller = makeController();
      const view = await renderWithI18n(<BtwOverlay controller={controller} />, language);
      const panel = screen.getByRole('region', { name: title });
      expect(view.container).toContainElement(panel);
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
      expect(panel).not.toHaveAttribute('aria-modal');
      expect(document.querySelector('[data-testid="btw-backdrop"]')).toBeNull();
      const add = screen.getByRole('button', { name: view.i18n.t('conversation.sideQuestion.addAttachment') });
      expect(add).toHaveAttribute('aria-disabled', 'true');
      expect(add).toHaveAttribute('title', view.i18n.t('conversation.sideQuestion.attachmentsUnavailable'));
      expect(add.querySelector('svg')).toHaveClass('lucide-plus');
      fireEvent.click(add);
      expect(controller.send).not.toHaveBeenCalled();
      expect(controller.setDraft).not.toHaveBeenCalled();
      const separator = screen.getByRole('separator');
      fireEvent.keyDown(separator, { key: 'ArrowUp' });
      expect(panel).toHaveStyle({ height: '288px' });
      fireEvent.click(screen.getByRole('button', { name: closeLabel }));
      expect(controller.close).toHaveBeenCalledOnce();
      expect(controller.cancel).not.toHaveBeenCalled();
    }
  );

  it('shows actual partial text and running tool status, and uses Cancel while execution is active', async () => {
    const controller = makeController({
      isLoading: true,
      canCancel: true,
      entries: [
        { id: 'partial', kind: 'message', role: 'assistant', text: 'Live partial answer' },
        { id: 'call-1', kind: 'tool', title: 'Search evidence', status: 'Executing' },
      ],
    });
    await renderWithI18n(<BtwOverlay controller={controller} parentTaskRunning />, 'en-US');
    expect(screen.getByText('Live partial answer')).toBeVisible();
    expect(screen.getByTestId('side-chat-tool')).toHaveTextContent('Search evidence · Running');
    expect(screen.getByText('Main task still running in the background.')).toBeVisible();
    const cancel = screen.getByRole('button', { name: 'Cancel current run' });
    expect(cancel.querySelector('svg')).toHaveClass('lucide-square');
    fireEvent.click(cancel);
    expect(controller.cancel).toHaveBeenCalledOnce();
    expect(controller.close).not.toHaveBeenCalled();
  });

  it('allows same-child followups, preserves Shift+Enter, and does not close on ordinary typing keys', async () => {
    const controller = makeController({ draft: 'Followup question' });
    await renderWithI18n(<BtwOverlay controller={controller} />, 'en-US');
    const input = screen.getByRole('textbox', { name: 'Side chat message' });
    fireEvent.change(input, { target: { value: 'New draft' } });
    expect(controller.setDraft).toHaveBeenCalledWith('New draft');
    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true });
    fireEvent.keyDown(input, { key: ' ' });
    expect(controller.send).not.toHaveBeenCalled();
    expect(controller.close).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
    expect(controller.send).toHaveBeenCalledWith('Followup question');
    fireEvent.keyDown(input, { key: 'Escape' });
    expect(controller.close).toHaveBeenCalledOnce();
  });

  it('keeps unknown Close and followup outcomes actionable without pretending they stopped', async () => {
    const controller = makeController({ error: 'Close is not yet confirmed', closeRequested: true, canSend: false });
    await renderWithI18n(<BtwOverlay controller={controller} />, 'en-US');
    expect(screen.getByRole('alert')).toHaveTextContent('Close is not yet confirmed');
    fireEvent.click(screen.getByRole('button', { name: 'Reconnect' }));
    fireEvent.click(screen.getByRole('button', { name: 'Retry close' }));
    expect(controller.reconnect).toHaveBeenCalledOnce();
    expect(controller.close).toHaveBeenCalledOnce();
    expect(controller.send).not.toHaveBeenCalled();
  });
});

import SendBox from '@/renderer/components/chat/SendBox';
import type { useBtwCommand } from '@/renderer/components/chat/BtwOverlay/useBtwCommand';
import type { DomSnippet } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import { cleanup, fireEvent, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../../i18nTestUtils';

const previewContext = {
  setSendBoxHandler: vi.fn(),
  domSnippets: [] as DomSnippet[],
  removeDomSnippet: vi.fn(),
  clearDomSnippets: vi.fn(),
};
const asideMock = vi.hoisted(() => ({ useHook: vi.fn(), controller: null as unknown }));
const layoutMock = vi.hoisted(() => ({ isMobile: false }));
const conversationMock = vi.hoisted(() => ({ id: 'parent-1', projectId: undefined as string | undefined }));
vi.mock('@/renderer/hooks/context/LayoutContext', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/renderer/hooks/context/LayoutContext')>();
  return {
    ...actual,
    useLayoutContext: () => ({ isMobile: layoutMock.isMobile, siderCollapsed: false, setSiderCollapsed: () => {} }),
  };
});
vi.mock('@/renderer/hooks/context/ConversationContext', () => ({
  useConversationContextSafe: () => ({ conversation_id: conversationMock.id, projectId: conversationMock.projectId }),
}));
vi.mock('@/renderer/components/chat/BtwOverlay/useBtwCommand', () => ({
  useBtwCommand: (id: string, enabled: boolean) => {
    asideMock.useHook(id, enabled);
    return asideMock.controller;
  },
}));

const makeController = (): ReturnType<typeof useBtwCommand> => {
  const controller: ReturnType<typeof useBtwCommand> = {
    owner: 'owner',
    generation: 1,
    operation: null,
    isOpen: false,
    starting: false,
    question: '',
    draft: '',
    action: null,
    errorKey: null,
    closingRecoveries: [],
    acceptedConsumers: [],
    entries: [],
    snapshot: null,
    observationError: false,
    truncated: false,
    receipt: null,
    frameId: null,
    isLoading: false,
    error: null,
    persistenceError: false,
    closeRequested: false,
    pendingFollowupText: null,
    canRetryFollowup: false,
    canSend: false,
    canCancel: false,
    canResume: false,
    ask: vi.fn().mockResolvedValue(false),
    retry: vi.fn().mockResolvedValue(false),
    close: vi.fn().mockResolvedValue(undefined),
    retryClose: vi.fn().mockResolvedValue(undefined),
    reconnectClose: vi.fn().mockResolvedValue(undefined),
    cancel: vi.fn().mockResolvedValue(undefined),
    send: vi.fn().mockResolvedValue(false),
    setDraft: vi.fn(),
    reconnect: vi.fn().mockResolvedValue(undefined),
    resume: vi.fn().mockResolvedValue(undefined),
    getReceipt: () => controller.receipt,
    getAcceptedConsumers: () => controller.acceptedConsumers,
    getUnfrozenConsumer: () => null,
    acknowledgeExactConsumer: vi.fn(() => {
      controller.acceptedConsumers = [];
    }),
    acknowledgeBranch: vi.fn(),
  };
  return controller;
};

vi.mock('@/renderer/pages/conversation/Preview/context/PreviewContext', () => ({
  usePreviewContext: () => previewContext,
}));

vi.mock('@/common/config/configService', () => ({
  configService: {
    get: vi.fn(),
    set: vi.fn().mockResolvedValue(undefined),
    setLocal: vi.fn(),
    whenReady: vi.fn().mockResolvedValue(undefined),
    subscribe: vi.fn(() => vi.fn()),
  },
}));

afterEach(cleanup);
beforeEach(() => {
  vi.clearAllMocks();
  layoutMock.isMobile = false;
  conversationMock.id = 'parent-1';
  conversationMock.projectId = undefined;
  asideMock.controller = makeController();
});

describe('SendBox input help', () => {
  it('shows project-only syntax when available and closes the old help on a conversation switch', async () => {
    const props = { value: 'Keep this draft', onChange: vi.fn(), onSend: vi.fn() };
    const view = await renderWithI18n(<SendBox {...props} />, 'en-US');
    const editor = screen.getByTestId('sendbox-input');
    const help = screen.getByRole('button', { name: 'Input help' });
    const description = () => document.getElementById(editor.getAttribute('aria-describedby')!)!;
    expect(description()).not.toHaveTextContent('Type #');
    conversationMock.projectId = 'project-1';
    view.rerender(<SendBox {...props} />);
    expect(description()).toHaveTextContent('Type # to reference sessions in this project.');
    fireEvent.click(help);
    expect(screen.getByRole('dialog', { name: 'Input help' })).toBeInTheDocument();
    conversationMock.id = 'parent-2';
    conversationMock.projectId = undefined;
    view.rerender(<SendBox {...props} />);
    expect(help).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Input help' })).not.toBeInTheDocument());
    expect(description()).not.toHaveTextContent('Type #');
    expect(editor).toHaveValue(props.value);
    expect(props.onChange).not.toHaveBeenCalled();
    expect(props.onSend).not.toHaveBeenCalled();
  });

  it.each([
    ['en-US', 'Input help', 'Enter to send', 'Shift+Enter for a new line', 'Type / for commands, @ to reference files'],
    ['zh-CN', '输入帮助', '按 Enter 发送', '按 Shift+Enter 换行', '输入 / 唤起命令，@ 引用文件'],
  ] as const)(
    'keeps complete help keyboard-accessible with a nonempty draft in %s',
    async (language, label, sendHint, lineHint, syntaxHint) => {
      const user = userEvent.setup();
      const change = vi.fn();
      const send = vi.fn().mockResolvedValue(undefined);
      const draft = 'Keep these results. '.repeat(40);
      await renderWithI18n(
        <SendBox value={draft} onChange={change} onSend={send} placeholder='Research prompt' bottomHint='' />,
        language
      );
      const editor = screen.getByTestId('sendbox-input');
      const help = screen.getByRole('button', { name: label });
      const descriptionId = editor.getAttribute('aria-describedby');
      expect(descriptionId).toBeTruthy();
      expect(document.getElementById(descriptionId!)?.textContent).toContain(sendHint);
      expect(document.getElementById(descriptionId!)?.textContent).toContain(lineHint);
      expect(document.getElementById(descriptionId!)?.textContent).toContain('Research prompt');
      expect(document.getElementById(descriptionId!)?.textContent).toContain(syntaxHint);

      editor.focus();
      await user.tab();
      expect(help).toHaveFocus();
      await user.keyboard('{Enter}');
      expect(screen.getByRole('dialog', { name: label })).toHaveTextContent(sendHint);
      expect(screen.getByRole('dialog', { name: label })).toHaveTextContent(lineHint);
      expect(screen.getByRole('dialog', { name: label })).toHaveTextContent('Research prompt');
      expect(screen.getByRole('dialog', { name: label })).toHaveTextContent(syntaxHint);
      await user.keyboard('{Escape}');
      expect(help).toHaveAttribute('aria-expanded', 'false');
      await waitFor(() => expect(screen.queryByRole('dialog', { name: label })).not.toBeInTheDocument());
      expect(help).toHaveFocus();
      expect(editor).toHaveValue(draft);
      expect(change).not.toHaveBeenCalled();
      expect(send).not.toHaveBeenCalled();
    }
  );

  it('retains the same help entry across composition, scrolling and a controlled pasted draft', async () => {
    const change = vi.fn();
    const send = vi.fn().mockResolvedValue(undefined);
    const props = { onChange: change, onSend: send };
    const view = await renderWithI18n(<SendBox {...props} value='' />, 'en-US');
    const editor = screen.getByTestId('sendbox-input');
    const help = screen.getByRole('button', { name: 'Input help' });
    fireEvent.compositionStart(editor);
    expect(screen.getByRole('button', { name: 'Input help' })).toBe(help);
    fireEvent.keyDown(editor, { key: 'Enter', isComposing: true });
    fireEvent.compositionEnd(editor);
    const draft = 'Pasted results\n'.repeat(40);
    view.rerender(<SendBox {...props} value={draft} />);
    fireEvent.scroll(editor, { target: { scrollTop: 200 } });
    expect(screen.getByRole('button', { name: 'Input help' })).toBe(help);
    fireEvent.click(help);
    expect(screen.getByRole('dialog', { name: 'Input help' })).toBeInTheDocument();
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(help).toHaveAttribute('aria-expanded', 'false');
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Input help' })).not.toBeInTheDocument());
    expect(editor).toHaveValue(draft);
    expect(change).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
  });
});

describe('SendBox right-side tools', () => {
  it('M462 compact mobile excludes the right-tools owner and passes only the connected native More opener', async () => {
    layoutMock.isMobile = true;
    const mountedOwner = vi.fn();
    const unmountedOwner = vi.fn();
    const NativeOwner = () => {
      React.useEffect(() => {
        mountedOwner();
        return () => unmountedOwner();
      }, []);
      return <button type='button'>Native right-tools owner</button>;
    };
    const change = vi.fn();
    const send = vi.fn().mockResolvedValue(undefined);
    const openMore = vi.fn();
    const props = {
      value: 'Keep this draft',
      onChange: change,
      onSend: send,
      rightTools: <NativeOwner />,
    };
    const view = await renderWithI18n(<SendBox {...props} onMobilePlusClick={openMore} />, 'en-US');
    expect(screen.queryByRole('button', { name: 'Native right-tools owner' })).not.toBeInTheDocument();
    expect(mountedOwner).not.toHaveBeenCalled();
    const firstOpener = screen.getByTestId('sendbox-mobile-plus-btn');
    expect(firstOpener).toBeInstanceOf(HTMLButtonElement);
    expect(firstOpener.isConnected).toBe(true);
    fireEvent.click(firstOpener);
    expect(openMore).toHaveBeenCalledOnce();
    expect(openMore).toHaveBeenCalledWith(firstOpener);
    expect(change).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
    expect(screen.getByTestId('sendbox-input')).toHaveValue('Keep this draft');

    layoutMock.isMobile = false;
    view.rerender(<SendBox {...props} onMobilePlusClick={openMore} />);
    expect(screen.getByRole('button', { name: 'Native right-tools owner' })).toBeVisible();
    expect(mountedOwner).toHaveBeenCalled();
    expect(screen.queryByTestId('sendbox-mobile-plus-btn')).not.toBeInTheDocument();

    layoutMock.isMobile = true;
    view.rerender(<SendBox {...props} />);
    expect(screen.getByRole('button', { name: 'Native right-tools owner' })).toBeVisible();
    expect(mountedOwner).toHaveBeenCalled();
    expect(screen.queryByTestId('sendbox-mobile-plus-btn')).not.toBeInTheDocument();

    const mountsBeforeCompact = mountedOwner.mock.calls.length;
    const unmountsBeforeCompact = unmountedOwner.mock.calls.length;
    view.rerender(<SendBox {...props} onMobilePlusClick={openMore} />);
    expect(screen.queryByRole('button', { name: 'Native right-tools owner' })).not.toBeInTheDocument();
    expect(mountedOwner).toHaveBeenCalledTimes(mountsBeforeCompact);
    expect(unmountedOwner).toHaveBeenCalledTimes(unmountsBeforeCompact + 1);
    const currentOpener = screen.getByTestId('sendbox-mobile-plus-btn');
    expect(firstOpener.isConnected).toBe(false);
    expect(currentOpener).not.toBe(firstOpener);
    expect(currentOpener).toBeInstanceOf(HTMLButtonElement);
    expect(currentOpener.isConnected).toBe(true);
    fireEvent.click(currentOpener);
    expect(openMore).toHaveBeenLastCalledWith(currentOpener);
    expect(openMore).toHaveBeenCalledTimes(2);
    expect(change).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
    expect(screen.getByTestId('sendbox-input')).toHaveValue('Keep this draft');
  });

  it('keeps the model/action slot visible in the single-line layout', async () => {
    await renderWithI18n(
      <SendBox
        value='Draft remains editable'
        onChange={vi.fn()}
        onSend={vi.fn().mockResolvedValue(undefined)}
        rightTools={<button type='button'>Current model</button>}
      />
    );

    expect(screen.getByRole('button', { name: 'Current model' })).toBeVisible();
    expect(screen.getByTestId('sendbox-input')).toHaveValue('Draft remains editable');
  });

  it('uses the external ACP owner for /btw without starting a local owner or clearing an unaccepted draft', async () => {
    const external = makeController();
    const change = vi.fn();
    const send = vi.fn().mockResolvedValue(undefined);
    await renderWithI18n(
      <SendBox value='/btw Independent question' onChange={change} onSend={send} enableBtw btwController={external} />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('sendbox-send-btn'));
    expect(asideMock.useHook).toHaveBeenCalledWith('parent-1', false);
    expect(external.ask).toHaveBeenCalledWith('Independent question', {
      kind: 'draft',
      id: 'parent-1',
      content: '/btw Independent question',
    });
    expect((asideMock.controller as ReturnType<typeof useBtwCommand>).ask).not.toHaveBeenCalled();
    expect(change).not.toHaveBeenCalled();
    expect(send).not.toHaveBeenCalled();
    expect(screen.queryByTestId('side-chat-panel')).not.toBeInTheDocument();
  });

  it('consumes a verified local /btw draft only when it still equals the original submission', async () => {
    const local = asideMock.controller as ReturnType<typeof useBtwCommand>;
    const change = vi.fn();
    const send = vi.fn().mockResolvedValue(undefined);
    const view = await renderWithI18n(
      <SendBox value='/btw Original question' onChange={change} onSend={send} enableBtw />,
      'en-US'
    );
    fireEvent.click(screen.getByTestId('sendbox-send-btn'));
    expect(change).not.toHaveBeenCalled();
    local.acceptedConsumers = [
      {
        intentId: 'draft-intent',
        consumer: { kind: 'draft', id: 'parent-1', content: '/btw Original question' },
        ownsDraft: true,
      },
    ];
    view.rerender(<SendBox value='New draft typed during admission' onChange={change} onSend={send} enableBtw />);
    expect(change).not.toHaveBeenCalled();
    expect(local.acknowledgeExactConsumer).toHaveBeenCalledExactlyOnceWith('draft-intent');
    expect(screen.getByTestId('sendbox-input')).toHaveValue('New draft typed during admission');
    expect(send).not.toHaveBeenCalled();
  });
});

import React from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { decodeMessageStreamPayload } from '@/common/adapter/messageStreamProtocol';
import { transformMessage, type IMessageToolCall } from '@/common/chat/chatLib';
import { normalizeToolMessages } from '@/common/chat/normalizeToolCall';
import { createCanonicalToolBranchResolver } from '@/common/chat/canonicalToolBranch';
import {
  MessageListProvider,
  MessagePaginationProvider,
  useMergeLiveMessage,
  useMessageList,
} from '@/renderer/pages/conversation/Messages/hooks';
import MessageToolGroupSummary from '@/renderer/pages/conversation/Messages/components/MessageToolGroupSummary';
import { conversationDisclosureStore } from '@/renderer/services/runtime/conversationDisclosureStore';

vi.mock('@/common/config/configService', () => ({
  configService: {
    whenReady: () => Promise.resolve(),
    get: (): undefined => undefined,
    setLocal: vi.fn(),
    subscribe: () => () => {},
    reset: vi.fn(),
  },
}));
vi.mock('@/common', () => ({
  ipcBridge: { theme: { setActive: { invoke: vi.fn() } }, database: { getConversationMessage: { invoke: vi.fn() } } },
}));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en-US' } }) }));
afterEach(() => {
  cleanup();
  conversationDisclosureStore.resetForTests();
  vi.restoreAllMocks();
});

// Exact producer fields: transcriptWebToolStreamPayload emits the original
// transcriptToolHistoryMessage content and its durable identity as msg_id.
// Progress observations may omit input and branch; neither is invented here.
function canonicalToolPair(liveAttempt = 3, liveStatus: 'running' | 'completed' = 'running', includeInput = false) {
  const identity = `transcript-tool:frame:branch-fixture:${liveAttempt}:call-fixture`;
  const content = {
    call_id: 'call-fixture',
    name: 'bash',
    status: liveStatus,
    attempt: liveAttempt,
    operation_id: 'call-fixture',
    revision: 22,
    phase: liveStatus === 'completed' ? 'completed' : 'progress-000002',
    progress: { phase: 'waiting_for_resources', indeterminate: true, elapsedMs: 2000 },
    ...(includeInput ? { input: { command: 'printf synthetic' }, args: { command: 'printf synthetic' } } : {}),
  };
  const decoded = decodeMessageStreamPayload({
    type: 'message.stream',
    stream_type: 'tool_call',
    data: content,
    msg_id: identity,
    turn_id: 'branch-fixture',
    conversation_id: 'branch-fixture',
    created_at: 1,
    position: 'left',
    status: liveStatus === 'completed' ? 'finish' : 'work',
    source_publication_sequence: 22,
    publication_boundary_id: 'publication-fixture',
  });
  expect(decoded).toBeDefined();
  const live = transformMessage(decoded!) as IMessageToolCall;
  expect(live.id).toBe(identity);
  expect(live.content.attempt).toBe(liveAttempt);
  expect(live.content.operation_id).toBe('call-fixture');
  expect(live.content.xScience).toBeUndefined();
  expect(live.content.input).toEqual(includeInput ? { command: 'printf synthetic' } : undefined);
  const history: IMessageToolCall = {
    id: 'transcript-tool:frame:branch-fixture:3:call-fixture',
    msg_id: 'canonical-start-checkpoint',
    conversation_id: 'branch-fixture',
    type: 'tool_call',
    created_at: 1,
    content: {
      ...content,
      attempt: 3,
      status: 'running',
      phase: 'progress-000001',
      revision: 21,
      input: { command: 'printf synthetic' },
      args: { command: 'printf synthetic' },
      xScience: { branchId: 'br_12345678', messageIndex: 2, blockIndex: 0 },
    },
  };
  return { live, history };
}

it('renders one operation for the same durable tool publication before branch normalization', () => {
  const { history, live } = canonicalToolPair();
  render(
    <MessagePaginationProvider
      value={{
        conversationId: 'branch-fixture',
        branchId: 'br_12345678',
        hasMoreBefore: false,
        hasMoreAfter: false,
        isLoadingBefore: false,
        isLoadingAfter: false,
        isLoadingAnchor: false,
      }}
    >
      <MessageToolGroupSummary messages={[history, live]} />
    </MessagePaginationProvider>
  );
  expect(screen.getAllByTestId('tool-chip')).toHaveLength(1);
  expect(screen.queryByTestId('tool-group-header')).toBeNull();
  expect(live.content.xScience).toBeUndefined();
  expect(normalizeToolMessages([live, history])).toHaveLength(1);
});

it.each(['id', 'conversation', 'name', 'call', 'operation', 'attempt', 'input', 'source coordinates'] as const)(
  'does not acquire a branch across a conflicting %s',
  (field) => {
    const { history, live } = canonicalToolPair();
    const changed: IMessageToolCall = { ...live, content: { ...live.content } };
    switch (field) {
      case 'id':
        changed.id = changed.msg_id = 'another-durable-publication';
        break;
      case 'conversation':
        changed.conversation_id = 'another-conversation';
        break;
      case 'name':
        changed.content.name = 'python';
        break;
      case 'call':
        changed.content.call_id = 'another-call';
        break;
      case 'operation':
        changed.content.operation_id = 'another-operation';
        break;
      case 'attempt':
        changed.content.attempt = 4;
        break;
      case 'input':
        changed.content.input = { command: 'another immutable request' };
        break;
      case 'source coordinates':
        changed.content.xScience = { branchId: null, messageIndex: 99, blockIndex: 0 };
        break;
    }
    const normalized = normalizeToolMessages([history, changed]);
    expect(normalized).toHaveLength(2);
    expect(normalized.filter((tool) => tool.branchId === undefined)).toHaveLength(1);
    expect(changed.content.xScience?.branchId).not.toBe('br_12345678');
  }
);

it('leaves an ambiguous branch unbound and preserves every explicit branch', () => {
  const { history, live } = canonicalToolPair();
  const another = {
    ...history,
    content: { ...history.content, xScience: { ...history.content.xScience!, branchId: 'br_abcdef12' } },
  };
  const normalized = normalizeToolMessages([history, another, live]);
  expect(normalized).toHaveLength(3);
  expect(normalized.map((tool) => tool.branchId)).toEqual(['br_12345678', 'br_abcdef12', undefined]);
});

it('does not infer an unknown or unbound source from a selected window or a prior resolver', () => {
  const { history, live } = canonicalToolPair();
  expect(createCanonicalToolBranchResolver([history])(live).content.xScience?.branchId).toBe('br_12345678');
  // A fresh account-owned list has no previous account's counterpart/cache.
  expect(createCanonicalToolBranchResolver([])(live)).toBe(live);
  const unknown = {
    ...history,
    content: { ...history.content, input: undefined, args: undefined },
  } as unknown as IMessageToolCall;
  expect(createCanonicalToolBranchResolver([unknown])(live)).toBe(live);
  const noAttempt: IMessageToolCall = { ...live, content: { ...live.content, attempt: undefined } };
  expect(createCanonicalToolBranchResolver([history])(noAttempt)).toBe(noAttempt);
  render(
    <MessagePaginationProvider
      value={{
        conversationId: 'branch-fixture',
        branchId: 'br_12345678',
        hasMoreBefore: false,
        hasMoreAfter: false,
        isLoadingBefore: false,
        isLoadingAfter: false,
        isLoadingAnchor: false,
      }}
    >
      <MessageToolGroupSummary messages={[live]} />
    </MessagePaginationProvider>
  );
  expect(normalizeToolMessages([live])[0].branchId).toBeUndefined();
});

function LiveWindow({
  message,
  add,
  firstMessage,
}: {
  message: IMessageToolCall;
  add: boolean;
  firstMessage?: IMessageToolCall;
}) {
  const messages = useMessageList();
  const merge = useMergeLiveMessage();
  return (
    <>
      <button
        onClick={() => {
          if (firstMessage) merge(firstMessage);
          merge(message, add);
        }}
      >
        Deliver native publication
      </button>
      <output data-testid='owned-message-count'>{messages.length}</output>
      <output data-testid='owned-terminal-count'>
        {messages.filter((item) => item.type === 'tool_call' && item.content.status === 'completed').length}
      </output>
      <output data-testid='owned-revision'>
        {messages.find((item) => item.type === 'tool_call')?.content.revision}
      </output>
      <MessageToolGroupSummary
        messages={messages.filter((item): item is IMessageToolCall => item.type === 'tool_call')}
      />
    </>
  );
}

it.each(['completed', 'running'] as const)(
  'keeps one raw native owner from an empty window for same-frame %s',
  (status) => {
    const frames: FrameRequestCallback[] = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      frames.push(callback);
      return frames.length;
    });
    const start = canonicalToolPair(3, 'running', true).live;
    const next = canonicalToolPair(3, status).live;
    next.content.revision = 23;
    render(
      <MessageListProvider value={[]}>
        <LiveWindow firstMessage={start} message={next} add={false} />
      </MessageListProvider>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
    expect(frames).toHaveLength(1);
    expect(screen.getByTestId('owned-message-count').textContent).toBe('0');
    act(() => frames.shift()?.(16));
    expect(screen.getByTestId('owned-message-count').textContent).toBe('1');
    expect(screen.getByTestId('owned-revision').textContent).toBe('23');
    expect(screen.getByTestId('owned-terminal-count').textContent).toBe(status === 'completed' ? '1' : '0');
    expect(screen.getAllByTestId('tool-chip')).toHaveLength(1);
  }
);

it.each([false, true])(
  'upserts the native publication in its exact existing message owner with add=%s',
  async (add) => {
    const { history, live } = canonicalToolPair();
    render(
      <MessageListProvider value={[history]}>
        <LiveWindow message={live} add={add} />
      </MessageListProvider>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
    await waitFor(() => expect(screen.getByTestId('owned-message-count').textContent).toBe('1'));
    expect(screen.getAllByTestId('tool-chip')).toHaveLength(1);
    expect(live.content.xScience).toBeUndefined();
  }
);

it.each(['another native attempt', 'unbound input'] as const)(
  'does not lend a branch through terminal recovery fallback for %s',
  async (boundary) => {
    const { history, live } = canonicalToolPair(boundary === 'another native attempt' ? 4 : 3, 'completed');
    const source =
      boundary === 'unbound input'
        ? ({
            ...history,
            content: { ...history.content, input: undefined, args: undefined },
          } as unknown as IMessageToolCall)
        : history;
    render(
      <MessageListProvider value={[source]}>
        <LiveWindow message={live} add={false} />
      </MessageListProvider>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
    await waitFor(() => expect(screen.getByTestId('owned-message-count').textContent).toBe('2'));
    expect(screen.getAllByTestId('tool-chip')).toHaveLength(2);
  }
);

it.each(['different durable id', 'conflicting input', 'conflicting coordinates'] as const)(
  'does not collapse native exact-index collisions with %s',
  async (boundary) => {
    const { history, live } = canonicalToolPair(3, 'completed');
    const source: IMessageToolCall = { ...history, content: { ...history.content, xScience: undefined } };
    const message: IMessageToolCall = { ...live, content: { ...live.content, output: 'synthetic terminal result' } };
    if (boundary === 'different durable id')
      message.id = message.msg_id = 'transcript-tool:other-stream:3:call-fixture';
    if (boundary === 'conflicting input') message.content.input = { command: 'different immutable command' };
    if (boundary === 'conflicting coordinates') {
      source.content.xScience = { branchId: null, messageIndex: 2, blockIndex: 0 };
      message.content.xScience = { branchId: null, messageIndex: 99, blockIndex: 0 };
    }
    render(
      <MessageListProvider value={[source]}>
        <LiveWindow message={message} add={false} />
      </MessageListProvider>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
    await waitFor(() => expect(screen.getByTestId('owned-message-count').textContent).toBe('2'));
    const rows = screen.getAllByTestId('tool-chip');
    expect(rows).toHaveLength(2);
    fireEvent.click(rows[0]);
    // Observe the committed disclosure state after the RAF-owned publication;
    // each click must expand only its own current row.
    await waitFor(() => {
      const currentRows = screen.getAllByTestId('tool-chip');
      expect(currentRows).toHaveLength(2);
      expect(currentRows[0].getAttribute('aria-expanded')).toBe('true');
      expect(currentRows[1].getAttribute('aria-expanded')).toBe('false');
    });
    fireEvent.click(screen.getAllByTestId('tool-chip')[1]);
    await waitFor(() => {
      const currentRows = screen.getAllByTestId('tool-chip');
      expect(currentRows).toHaveLength(2);
      expect(currentRows[0].getAttribute('aria-expanded')).toBe('true');
      expect(currentRows[1].getAttribute('aria-expanded')).toBe('true');
      expect(currentRows[0].getAttribute('aria-controls')).not.toBe(currentRows[1].getAttribute('aria-controls'));
    });
  }
);

it('settles the terminal native publication through its immutable exact owner', async () => {
  const { history, live } = canonicalToolPair(3, 'completed');
  render(
    <MessageListProvider value={[history]}>
      <LiveWindow message={live} add={false} />
    </MessageListProvider>
  );
  fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
  await waitFor(() => expect(screen.getByTestId('owned-terminal-count').textContent).toBe('1'));
  expect(screen.getByTestId('owned-message-count').textContent).toBe('1');
  expect(screen.getAllByTestId('tool-chip')).toHaveLength(1);
});

it('keeps an exact native reack in one unknown-branch row without inventing a branch', async () => {
  const { history, live } = canonicalToolPair();
  const source: IMessageToolCall = { ...history, content: { ...history.content, xScience: undefined } };
  render(
    <MessageListProvider value={[source]}>
      <LiveWindow message={live} add />
    </MessageListProvider>
  );
  fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
  await waitFor(() => expect(screen.getByTestId('owned-message-count').textContent).toBe('1'));
  expect(screen.getAllByTestId('tool-chip')).toHaveLength(1);
  expect(live.content.xScience).toBeUndefined();
});

it('keeps the exact native row mounted and expanded across newer progress revisions', async () => {
  const { history, live } = canonicalToolPair();
  const owned = [history];
  const view = render(
    <MessageListProvider value={owned}>
      <LiveWindow message={live} add={false} />
    </MessageListProvider>
  );
  fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
  await waitFor(() => expect(screen.getByTestId('owned-revision').textContent).toBe('22'));
  const row = screen.getByTestId('tool-chip');
  fireEvent.click(row);
  expect(row.getAttribute('aria-expanded')).toBe('true');
  const next: IMessageToolCall = {
    ...live,
    content: {
      ...live.content,
      revision: 23,
      phase: 'progress-000003',
      progress: { phase: 'waiting_for_resources', indeterminate: true, elapsedMs: 3000 },
    },
  };
  view.rerender(
    <MessageListProvider value={owned}>
      <LiveWindow message={next} add={false} />
    </MessageListProvider>
  );
  fireEvent.click(screen.getByRole('button', { name: 'Deliver native publication' }));
  await waitFor(() => expect(screen.getByTestId('owned-revision').textContent).toBe('23'));
  expect(screen.getAllByTestId('tool-chip')).toHaveLength(1);
  expect(screen.getByTestId('tool-chip')).toBe(row);
  expect(row.getAttribute('aria-expanded')).toBe('true');
});

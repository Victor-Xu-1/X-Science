import React from 'react';
import { act, cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ipcBridge } from '@/common';
import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import ToolOperationDetail from '@/renderer/pages/conversation/Messages/components/ToolOperationDetail';
import { conversationDisclosureStore } from '@/renderer/services/runtime/conversationDisclosureStore';

const locale = vi.hoisted(() => ({ language: 'en-US' }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: (key: string) => key, i18n: locale }) }));
vi.mock('@/common', () => ({ ipcBridge: { database: { getConversationMessage: { invoke: vi.fn() } } } }));
vi.mock('@arco-design/web-react', () => ({
  Button: ({ children }: React.PropsWithChildren) => <button>{children}</button>,
  Tooltip: ({ children }: React.PropsWithChildren) => <>{children}</>,
  Message: { useMessage: () => [{ success: vi.fn(), error: vi.fn() }, null] },
}));
vi.mock('@icon-park/react', () => ({ Download: () => null, CheckSmall: () => null, CloseSmall: () => null }));
vi.mock('@/renderer/components/media/LocalImageView', () => ({ default: () => null }));
vi.mock('@/renderer/utils/file/download', () => ({ downloadFileFromPath: vi.fn() }));
vi.mock('@/renderer/pages/conversation/Messages/components/ToolOperationDiffs', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/Messages/components/MessageWebResearchSources', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/Messages/components/MessageToolPublicDetail', () => ({
  default: ({ item, resultSummary }: { item: NormalizedToolCall; resultSummary: string | null }) => (
    <div>
      <span data-testid='detail-result-summary'>{resultSummary}</span>
      <pre data-testid='detail-result-output'>{item.output}</pre>
    </div>
  ),
}));

const item: NormalizedToolCall = {
  key: 'call',
  name: 'mcp__chembl__target_search',
  status: 'completed',
  conversationId: 'frame',
  messageId: 'message',
  revision: 1,
  input: '{"query":"fixture"}',
  output: '{"truncated":true}',
  truncated: true,
};

const resultOutput = (count: number) =>
  JSON.stringify({ targets: Array.from({ length: count }, (_, index) => ({ id: index })) });
const fullMessage = (revision = 1, count = 2) => ({
  id: 'message',
  conversation_id: 'frame',
  type: 'tool_call',
  content: {
    call_id: 'call',
    name: item.name,
    status: 'completed',
    revision,
    input: { query: 'fixture' },
    output: resultOutput(count),
  },
});

function deferredMessage() {
  let resolve!: (value: unknown) => void;
  const promise = new Promise<unknown>((accept) => {
    resolve = accept;
  });
  return { promise, resolve };
}

describe('tool operation hydrated result presentation', () => {
  beforeEach(() => {
    locale.language = 'en-US';
    conversationDisclosureStore.resetForTests();
    vi.mocked(ipcBridge.database.getConversationMessage.invoke).mockReset();
  });
  afterEach(cleanup);

  it.each([
    ['en-US', 'Search details pending', '2 results'],
    ['zh-CN', '检索详情待加载', '2 条结果'],
  ])('replaces the row and detail placeholder after real hydration in %s', async (language, pending, complete) => {
    locale.language = language;
    const response = deferredMessage();
    const invoke = vi.mocked(ipcBridge.database.getConversationMessage.invoke);
    invoke.mockReturnValue(response.promise as never);
    render(<ToolOperationDetail item={item} />);
    const row = screen.getByTestId('tool-chip');
    expect(within(row).getByText(pending)).toBeTruthy();
    fireEvent.click(row);
    expect(screen.getByTestId('detail-result-summary').textContent).toBe(pending);
    expect(screen.getByText('tools.labels.loadingFullOutput')).toBeTruthy();
    await act(async () => response.resolve(fullMessage()));
    expect(invoke).toHaveBeenCalledTimes(1);
    expect(within(row).getByText(complete)).toBeTruthy();
    expect(screen.getByTestId('detail-result-summary').textContent).toBe(complete);
    expect(screen.getByTestId('detail-result-output').textContent).toBe(resultOutput(2));
    expect(screen.queryByText(pending)).toBeNull();
  });

  it('keeps failed hydration bounded and uses live status and newer revision over old detail', async () => {
    const invoke = vi.mocked(ipcBridge.database.getConversationMessage.invoke);
    invoke.mockRejectedValue(new Error('fixture unavailable'));
    const view = render(<ToolOperationDetail item={item} />);
    fireEvent.click(screen.getByTestId('tool-chip'));
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(within(screen.getByTestId('tool-chip')).getByText('Search details pending')).toBeTruthy();
    expect(invoke).toHaveBeenCalledTimes(1);

    invoke.mockResolvedValue(fullMessage() as never);
    fireEvent.click(within(screen.getByRole('alert')).getByRole('button'));
    expect(await within(screen.getByTestId('tool-chip')).findByText('2 results')).toBeTruthy();
    expect(invoke).toHaveBeenCalledTimes(2);

    view.rerender(<ToolOperationDetail item={{ ...item, status: 'running' }} />);
    expect(within(screen.getByTestId('tool-chip')).getByText('Running')).toBeTruthy();
    expect(screen.getByTestId('detail-result-summary').textContent).toBe('Running');

    view.rerender(<ToolOperationDetail item={{ ...item, revision: 2, truncated: false, output: resultOutput(3) }} />);
    expect(within(screen.getByTestId('tool-chip')).getByText('3 results')).toBeTruthy();
    expect(screen.getByTestId('detail-result-summary').textContent).toBe('3 results');
    expect(screen.getByTestId('detail-result-output').textContent).toBe(resultOutput(3));
    expect(invoke).toHaveBeenCalledTimes(2);
  });

  it('rejects delayed older detail after a live revision update', async () => {
    const response = deferredMessage();
    vi.mocked(ipcBridge.database.getConversationMessage.invoke).mockReturnValue(response.promise as never);
    const view = render(<ToolOperationDetail item={item} />);
    fireEvent.click(screen.getByTestId('tool-chip'));
    view.rerender(<ToolOperationDetail item={{ ...item, revision: 2, truncated: false, output: resultOutput(3) }} />);
    await act(async () => response.resolve(fullMessage()));
    expect(within(screen.getByTestId('tool-chip')).getByText('3 results')).toBeTruthy();
    expect(screen.getByTestId('detail-result-summary').textContent).toBe('3 results');
    expect(screen.getByTestId('detail-result-output').textContent).toBe(resultOutput(3));
  });
});

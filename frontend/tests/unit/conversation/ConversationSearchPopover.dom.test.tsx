import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import React from 'react';
import { MemoryRouter, useLocation } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithI18n } from '../i18nTestUtils';

const mocks = vi.hoisted(() => ({
  search: vi.fn(),
}));

vi.mock('@/common', () => ({
  ipcBridge: {
    database: {
      searchConversationMessages: {
        invoke: mocks.search,
      },
    },
  },
}));

vi.mock('@/renderer/components/base/SynonModal', () => ({
  default: ({
    visible,
    children,
    modalRender,
  }: {
    visible: boolean;
    children: React.ReactNode;
    modalRender?: (node: React.ReactElement) => React.ReactNode;
  }) => {
    const node = <div role='dialog'>{children}</div>;
    return visible ? (modalRender ? modalRender(node) : node) : null;
  },
}));

vi.mock('@/renderer/hooks/synonBiomed/runtime/usePresetAssistantInfo', () => ({
  usePresetAssistantInfo: () => ({ info: null }),
}));
vi.mock('@/renderer/utils/synonBiomed/runtime/runtimeLogo', () => ({
  useAgentLogos: () => ({}),
}));
vi.mock('@/renderer/pages/conversation/utils/conversationAssistantIdentity', () => ({
  resolveConversationLeadingMark: () => ({ kind: 'none' }),
}));
vi.mock('@/renderer/utils/ui/focus', () => ({
  blockMobileInputFocus: vi.fn(),
  blurActiveElement: vi.fn(),
}));
vi.mock('@/renderer/components/synonBiomed/SynonBiomedAvatar', () => ({
  default: () => <span data-testid='assistant-avatar' />,
}));
vi.mock('@arco-design/web-react', () => ({
  Spin: () => <span data-testid='spinner' />,
}));
vi.mock('@icon-park/react', () => ({
  Close: () => <span />,
  CloseSmall: () => <span />,
  MessageOne: () => <span />,
  Search: () => <span />,
}));

import ConversationSearchPopover from '@/renderer/pages/conversation/GroupedHistory/ConversationSearchPopover';

const makeItem = (id: string, name: string, messageId = '') => ({
  conversation: {
    id,
    name,
    desc: '',
    type: 'acp',
    created_at: 1,
    modified_at: 2,
    status: 'finished',
    model: { provider_id: 'provider', model: 'model' },
    extra: { backend: 'synonbiomed', project_name: 'Discovery' },
  },
  message_id: messageId,
  message_type: 'text',
  message_created_at: 1787557580781,
  preview_text: messageId ? `Matched message ${name}` : '',
  project_name: 'Discovery',
  match_kind: messageId ? 'message' : 'recent',
  match_count: messageId ? 1 : 0,
  relevance: messageId ? 700 : 0,
});

const LocationProbe = () => {
  const location = useLocation();
  return (
    <output data-testid='location'>
      {location.pathname}|{JSON.stringify(location.state)}
    </output>
  );
};

const RouterWrapper = ({ children }: { children: React.ReactNode }) => (
  <MemoryRouter initialEntries={['/guid']}>
    {children}
    <LocationProbe />
  </MemoryRouter>
);

describe('ConversationSearchPopover', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
      configurable: true,
      value: vi.fn(),
    });
  });

  it('loads recent conversations, searches messages, supports keyboard selection, and preserves target navigation', async () => {
    mocks.search.mockImplementation(({ keyword }: { keyword: string }) =>
      Promise.resolve({
        items: keyword
          ? [makeItem('conversation-a', 'Alpha', 'message-a'), makeItem('conversation-b', 'Beta', 'message-b')]
          : [makeItem('conversation-recent', 'Recent conversation')],
        total: keyword ? 2 : 1,
        page: 0,
        page_size: 30,
        has_more: false,
      })
    );

    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );

    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    expect(await screen.findByText('Recent conversation')).toBeInTheDocument();
    expect(mocks.search).toHaveBeenCalledWith({ keyword: '', page: 0, page_size: 30 });

    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'Matched' } });
    expect(await screen.findByText('Alpha')).toBeInTheDocument();
    expect(await screen.findByText('Beta')).toBeInTheDocument();
    await waitFor(() => expect(mocks.search).toHaveBeenLastCalledWith({ keyword: 'Matched', page: 0, page_size: 30 }));

    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() =>
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/conversation/conversation-b|{"targetMessageId":"message-b","fromConversationSearch":true}'
      )
    );
  });

  it('shows a compact retry state and recovers through the same search request', async () => {
    let shouldFail = true;
    mocks.search.mockImplementation(() => {
      if (shouldFail) return Promise.reject(new Error('search unavailable'));
      return Promise.resolve({
        items: [makeItem('conversation-recovered', 'Recovered conversation')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      });
    });
    vi.spyOn(console, 'error').mockImplementation(() => undefined);

    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    expect(await screen.findByText('Conversation search is temporarily unavailable')).toBeInTheDocument();

    shouldFail = false;
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('Recovered conversation')).toBeInTheDocument();
  });

  it('ignores an older response that arrives after the current query', async () => {
    let resolveInitial:
      | ((value: {
          items: ReturnType<typeof makeItem>[];
          total: number;
          page: number;
          page_size: number;
          has_more: boolean;
        }) => void)
      | undefined;
    mocks.search.mockImplementation(({ keyword }: { keyword: string }) => {
      if (!keyword) {
        return new Promise((resolve) => {
          resolveInitial = resolve;
        });
      }
      return Promise.resolve({
        items: [makeItem('conversation-current', 'Current result', 'message-current')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      });
    });

    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await waitFor(() => expect(mocks.search).toHaveBeenCalledWith({ keyword: '', page: 0, page_size: 30 }));

    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'current' } });
    expect(await screen.findByRole('option', { name: /Current result/ })).toBeInTheDocument();
    resolveInitial?.({
      items: [makeItem('conversation-stale', 'Stale result')],
      total: 1,
      page: 0,
      page_size: 30,
      has_more: false,
    });

    await waitFor(() => expect(screen.queryByText('Stale result')).toBeNull());
    expect(screen.getByRole('option', { name: /Current result/ })).toBeInTheDocument();
  });

  it('names the dialog and retains its combobox result target in the empty state', async () => {
    mocks.search.mockResolvedValue({ items: [], total: 0, page: 0, page_size: 30, has_more: false });
    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await screen.findByText('No matching conversations');
    expect(screen.getByRole('dialog', { name: 'Search conversation content' })).toBeInTheDocument();
    const input = screen.getByRole('combobox');
    expect(input).toHaveAccessibleName('Search conversations or messages...');
    const target = document.getElementById(input.getAttribute('aria-controls')!);
    expect(target).toBe(screen.getByRole('listbox', { name: 'Conversation search results' }));
    expect(input).not.toHaveAttribute('aria-activedescendant');
  });

  it('does not navigate to the old result while a replacement query is debouncing or loading', async () => {
    let resolveQuery!: (result: object) => void;
    mocks.search.mockImplementation(({ keyword }: { keyword: string }) =>
      keyword
        ? new Promise((resolve) => {
            resolveQuery = resolve;
          })
        : Promise.resolve({
            items: [makeItem('recent', 'Recent result')],
            total: 1,
            page: 0,
            page_size: 30,
            has_more: false,
          })
    );
    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await screen.findByRole('option', { name: /Recent result/ });
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'replacement' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent('/guid|null');
    expect(screen.queryByRole('option', { name: /Recent result/ })).not.toBeInTheDocument();
    expect(input).not.toHaveAttribute('aria-activedescendant');
    await waitFor(() =>
      expect(mocks.search).toHaveBeenLastCalledWith({ keyword: 'replacement', page: 0, page_size: 30 })
    );
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent('/guid|null');
    await act(async () =>
      resolveQuery({
        items: [makeItem('current', 'Replacement result', 'message-new')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      })
    );
    await screen.findByRole('option', { name: /Replacement result/ });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(screen.getByTestId('location')).toHaveTextContent(
      '/conversation/current|{"targetMessageId":"message-new","fromConversationSearch":true}'
    );
  });

  it('invalidates an old in-flight response as soon as input changes, before debounce expires', async () => {
    let resolveInitial!: (result: object) => void;
    mocks.search.mockImplementation(({ keyword }: { keyword: string }) =>
      keyword
        ? Promise.resolve({
            items: [makeItem('current', 'Current result')],
            total: 1,
            page: 0,
            page_size: 30,
            has_more: false,
          })
        : new Promise((resolve) => {
            resolveInitial = resolve;
          })
    );
    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await waitFor(() => expect(mocks.search).toHaveBeenCalledTimes(1));
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'current' } });
    await act(async () =>
      resolveInitial({
        items: [makeItem('stale', 'Stale during debounce')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      })
    );
    expect(screen.queryByRole('option', { name: /Stale during debounce/ })).not.toBeInTheDocument();
    expect(await screen.findByRole('option', { name: /Current result/ })).toBeInTheDocument();
  });

  it('clears back to recent results without a stuck loading state or losing input focus', async () => {
    mocks.search.mockImplementation(({ keyword }: { keyword: string }) =>
      Promise.resolve({
        items: [makeItem(keyword ? 'matched' : 'recent', keyword ? 'Matched result' : 'Recent result')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      })
    );
    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await screen.findByRole('option', { name: /Recent result/ });
    const input = screen.getByRole('combobox');
    fireEvent.change(input, { target: { value: 'temporary' } });
    fireEvent.change(input, { target: { value: '' } });
    await screen.findByRole('option', { name: /Recent result/ });
    fireEvent.change(input, { target: { value: 'matched' } });
    const result = await screen.findByRole('option', { name: /Matched result/ });
    expect(result).toHaveAttribute('tabindex', '-1');
    const clear = screen.getByRole('button', { name: 'Clear search' });
    clear.focus();
    fireEvent.click(clear);
    expect(input).toHaveFocus();
    expect(input).toHaveValue('');
    await screen.findByRole('option', { name: /Recent result/ });
    expect(screen.getByRole('listbox')).toHaveAttribute('aria-busy', 'false');
  });

  it('does not let a closed search or its late response replace a newly opened query', async () => {
    let resolveInitial!: (result: object) => void;
    mocks.search
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveInitial = resolve;
          })
      )
      .mockResolvedValue({
        items: [makeItem('fresh', 'Fresh recent result')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      });
    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await waitFor(() => expect(mocks.search).toHaveBeenCalledTimes(1));
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await screen.findByRole('option', { name: /Fresh recent result/ });
    await act(async () =>
      resolveInitial({
        items: [makeItem('stale', 'Closed search result')],
        total: 1,
        page: 0,
        page_size: 30,
        has_more: false,
      })
    );
    expect(screen.queryByRole('option', { name: /Closed search result/ })).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: /Fresh recent result/ })).toBeInTheDocument();
  });

  it('appends one page at a time and never appends an older query page to replacement results', async () => {
    let resolvePage!: (result: object) => void;
    mocks.search.mockImplementation(({ keyword, page }: { keyword: string; page: number }) => {
      if (keyword)
        return Promise.resolve({
          items: [makeItem('current', 'Replacement result')],
          total: 1,
          page: 0,
          page_size: 30,
          has_more: false,
        });
      if (page)
        return new Promise((resolve) => {
          resolvePage = resolve;
        });
      return Promise.resolve({
        items: [makeItem('first', 'First page result')],
        total: 3,
        page: 0,
        page_size: 30,
        has_more: true,
      });
    });
    await renderWithI18n(
      <ConversationSearchPopover renderTrigger={({ onClick }) => <button onClick={onClick}>open search</button>} />,
      'en-US',
      { wrapper: RouterWrapper }
    );
    fireEvent.click(screen.getByRole('button', { name: 'open search' }));
    await screen.findByRole('option', { name: /First page result/ });
    const scroller = screen
      .getByRole('listbox')
      .querySelector<HTMLElement>('.conversation-search-modal__result-scroll')!;
    Object.defineProperties(scroller, {
      scrollHeight: { value: 600 },
      clientHeight: { value: 400 },
      scrollTop: { value: 200 },
    });
    fireEvent.scroll(scroller);
    fireEvent.scroll(scroller);
    expect(mocks.search.mock.calls.filter(([request]) => request.page === 1)).toHaveLength(1);
    await act(async () =>
      resolvePage({
        items: [makeItem('second', 'Second page result')],
        total: 3,
        page: 1,
        page_size: 30,
        has_more: true,
      })
    );
    expect(screen.getByRole('option', { name: /First page result/ })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: /Second page result/ })).toBeInTheDocument();
    fireEvent.scroll(scroller);
    expect(mocks.search).toHaveBeenLastCalledWith({ keyword: '', page: 2, page_size: 30 });
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'replacement' } });
    await screen.findByRole('option', { name: /Replacement result/ });
    await act(async () =>
      resolvePage({ items: [makeItem('stale', 'Old paged result')], total: 3, page: 2, page_size: 30, has_more: false })
    );
    expect(screen.queryByRole('option', { name: /Old paged result/ })).not.toBeInTheDocument();
    expect(screen.queryByRole('option', { name: /First page result/ })).not.toBeInTheDocument();
    expect(screen.getByRole('option', { name: /Replacement result/ })).toBeInTheDocument();
  });
});

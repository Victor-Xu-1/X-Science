import { readFileSync } from 'node:fs';
import { fireEvent, screen, within } from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { IMessageToolCall } from '@/common/chat/chatLib';
import MessageToolCall from '@/renderer/pages/conversation/Messages/components/MessageToolCall';
import { conversationDisclosureStore } from '@/renderer/services/runtime/conversationDisclosureStore';
import { renderWithI18n } from '../i18nTestUtils';

const toolDetailCss = readFileSync(
  'packages/desktop/src/renderer/pages/conversation/Messages/components/MessageToolGroupSummary.css',
  'utf8'
);

vi.mock('@/renderer/components/base/FileChangesPanel', () => ({ default: () => null }));
vi.mock('@/renderer/components/Markdown', () => ({
  default: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
vi.mock('@/renderer/components/media/LocalImageView', () => ({
  default: ({ src, alt }: { src: string; alt: string }) => <img src={src} alt={alt} />,
}));
vi.mock('@/renderer/hooks/file/useDiffPreviewHandlers', () => ({
  useDiffPreviewHandlers: () => ({ handleFileClick: vi.fn(), handleDiffClick: vi.fn() }),
}));
vi.mock('@/renderer/components/xScience/runtime/AskUserHistoryCard', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/Messages/components/MessageSubagentTurn', () => ({ default: () => null }));
vi.mock('@/renderer/pages/conversation/Messages/components/MessageSubagentEvents', () => ({ default: () => null }));

const message = {
  id: 'tool-message-1',
  msg_id: 'tool-message-1',
  conversation_id: 'conversation-1',
  type: 'tool_call',
  position: 'left',
  content: {
    call_id: 'call-1',
    name: 'search_records',
    args: { query: 'STAT6' },
    status: 'running',
    output: '3 records found',
  },
} as IMessageToolCall;

describe('MessageToolCall', () => {
  beforeEach(() => conversationDisclosureStore.resetForTests());

  it.each(['en-US', 'zh-CN'] as const)('Skill353 discloses a historical native guide once in %s', async (language) => {
    const body = [
      '# Fixture method',
      ...Array.from({ length: 80 }, (_, index) => 'Instruction ' + (index + 1) + ': Keep controls paired.'),
    ].join('\n\n');
    const output = JSON.stringify('<skill-metadata name="example-guide" loaded="true" executed="false" />\n\n' + body);
    await renderWithI18n(
      <MessageToolCall
        message={{
          ...message,
          content: {
            call_id: 'history-guide',
            name: 'load_skill',
            args: { skill: 'example-guide' },
            status: 'completed',
            output,
          },
        }}
      />,
      language
    );
    fireEvent.click(screen.getByTestId('tool-chip'));
    expect(screen.getAllByText(body, { normalizer: (value) => value })).toHaveLength(1);
    expect(
      screen.getByText(
        language === 'en-US'
          ? 'Loading guidance does not mean the workflow was executed.'
          : '加载指南不代表工作流已执行。'
      )
    ).toBeInTheDocument();
    expect(screen.queryByText(/skill-metadata/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('show-output-toggle')).not.toBeInTheDocument();

    // Check the actual stylesheet and rendered wrappers, not mock Markdown geometry.
    // JSDOM proves scroll constraints here; browser layout remains a separate check.
    const style = document.createElement('style');
    style.textContent = toolDetailCss;
    document.head.append(style);
    try {
      expect(style.sheet?.cssRules.length).toBeGreaterThan(0);
      const surface = screen.getByTestId('tool-public-detail').querySelector('.tool-public-detail__surface--input')!;
      const markdown = surface.querySelector('.tool-public-detail__block-markdown')!;
      const boundedScrollOwners = [surface, markdown].filter((element) => {
        const computed = getComputedStyle(element);
        return computed.maxHeight !== 'none' && /auto|scroll/.test(computed.overflowY || computed.overflow);
      });
      expect(boundedScrollOwners).toEqual([markdown]);
      expect(getComputedStyle(surface).maxHeight).toBe('none');
      expect(getComputedStyle(surface).overflow).toBe('visible');
      expect(getComputedStyle(markdown).maxHeight).toBe('320px');
    } finally {
      style.remove();
    }
  });

  it.each([
    ['en-US', { result: '[]' }, true],
    ['zh-CN', { result: '[]' }, true],
    ['en-US', { content: [{ type: 'text', text: '[]' }] }, true],
    ['zh-CN', { content: [{ type: 'text', text: '[]' }] }, true],
    ['en-US', { n_records_returned: 0 }, true],
    ['zh-CN', { n_records_returned: 0 }, true],
    ['en-US', { rows_retrieved: 0 }, true],
    ['zh-CN', { rows_retrieved: 0 }, true],
    ['en-US', { content: [] }, false],
    ['zh-CN', { content: [] }, false],
  ] as const)('keeps the %s summary and expanded empty-page state aligned for %j', async (language, output, empty) => {
    await renderWithI18n(
      <MessageToolCall
        message={{
          ...message,
          content: { ...message.content, status: 'completed', output: JSON.stringify(output) },
        }}
      />,
      language
    );
    const toggle = screen.getByTestId('tool-chip');
    expect(toggle).toHaveTextContent(
      empty
        ? language === 'en-US'
          ? 'No usable sources'
          : '未发现可用来源'
        : language === 'en-US'
          ? 'Returned data needs review'
          : '返回数据待查看'
    );
    fireEvent.click(toggle);
    const sources = screen.getByRole('region', { name: language === 'en-US' ? 'Sources' : '来源' });
    expect(sources).toHaveTextContent(
      empty
        ? language === 'en-US'
          ? 'No usable sources were found.'
          : '本次检索未发现可用来源'
        : language === 'en-US'
          ? 'This response could not be presented as source records.'
          : '当前返回内容尚不能展示为来源记录'
    );
    expect(within(sources).queryByRole('link')).not.toBeInTheDocument();
  });

  it.each(['en-US', 'zh-CN'] as const)('uses one compound count and expanded source in %s', async (language) => {
    await renderWithI18n(
      <MessageToolCall
        message={{
          ...message,
          content: {
            call_id: 'compound-call',
            name: 'mcp__chemistry__pubchem_search_compounds',
            status: 'completed',
            args: { query: 'public fixture' },
            output: JSON.stringify({
              cids: [12345, 12345],
              properties: [{ CID: 12345, title: 'Public compound' }],
              n_cids_total: 900,
            }),
          },
        }}
      />,
      language
    );
    const toggle = screen.getByTestId('tool-chip');
    expect(toggle).toHaveTextContent(language === 'en-US' ? '1 result' : '1 条结果');
    fireEvent.click(toggle);
    const source = screen.getByRole('link', { name: 'Public compound' });
    expect(source).toHaveAttribute('href', 'https://pubchem.ncbi.nlm.nih.gov/compound/12345');
    expect(screen.getAllByRole('link', { name: 'Public compound' })).toHaveLength(1);
    expect(screen.queryByText(/No usable sources were found|本次检索未发现可用来源/)).not.toBeInTheDocument();
    expect(toggle).not.toHaveTextContent('900');
  });

  it.each(['en-US', 'zh-CN'] as const)(
    'does not label an unrecognized nonempty response as empty in %s',
    async (language) => {
      await renderWithI18n(
        <MessageToolCall
          message={{
            ...message,
            content: {
              ...message.content,
              status: 'completed',
              output: JSON.stringify({ custom_data: [{ record: 'fixture' }] }),
            },
          }}
        />,
        language
      );
      const toggle = screen.getByTestId('tool-chip');
      expect(toggle).toHaveTextContent(language === 'en-US' ? 'Returned data needs review' : '返回数据待查看');
      fireEvent.click(toggle);
      expect(
        screen.getByText(
          language === 'en-US'
            ? 'This response could not be presented as source records. Expand the returned output to inspect it; this does not mean the search found no results.'
            : '当前返回内容尚不能展示为来源记录。可展开输出查看；这不代表检索没有结果。'
        )
      ).toBeInTheDocument();
      expect(screen.queryByText(/No usable sources were found|本次检索未发现可用来源/)).not.toBeInTheDocument();
    }
  );

  it('routes an inferred search call through the canonical research summary', async () => {
    await renderWithI18n(<MessageToolCall message={message} />, 'en-US');

    const toggle = screen.getByTestId('tool-chip');
    expect(toggle).not.toBeDisabled();
    expect(toggle).toHaveAccessibleName(/Search sources.*Running/i);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('region', { name: 'Sources' })).not.toBeInTheDocument();
    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'true');
    expect(toggle).not.toHaveTextContent('search_records');
    expect(screen.queryByTestId('tool-technical-detail')).not.toBeInTheDocument();

    const sources = screen.getByRole('region', { name: 'Sources' });
    expect(within(sources).getByText('query')).toBeInTheDocument();
    expect(within(sources).getByText('STAT6')).toBeInTheDocument();
    expect(within(sources).getByText(/This response could not be presented as source records/)).toBeInTheDocument();
    expect(screen.getByTestId('show-output-toggle')).toHaveAttribute('aria-expanded', 'false');
    fireEvent.click(screen.getByTestId('show-output-toggle'));
    expect(screen.getByText('3 records found')).toBeInTheDocument();

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(screen.queryByRole('region', { name: 'Sources' })).not.toBeInTheDocument();
  });

  it('announces a canceled transcript tool without presenting it as pending', async () => {
    await renderWithI18n(
      <MessageToolCall
        message={{ ...message, content: { ...message.content, status: 'canceled', output: undefined } }}
      />,
      'en-US'
    );

    expect(screen.getByTestId('tool-chip')).toHaveTextContent('Stopped');
    expect(screen.queryByText('Pending')).not.toBeInTheDocument();
  });
});

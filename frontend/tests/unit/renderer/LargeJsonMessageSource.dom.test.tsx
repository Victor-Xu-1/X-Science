import React from 'react';
import { createHash } from 'node:crypto';
import { act, cleanup, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { IMessageText } from '@/common/chat/chatLib';
import MessageText from '@/renderer/pages/conversation/Messages/components/MessageText';
import MarkdownView from '@/renderer/components/Markdown';
import { renderWithI18n } from '../i18nTestUtils';

const clipboard = vi.hoisted(() => vi.fn().mockResolvedValue(undefined));
vi.mock('@/renderer/utils/ui/clipboard', () => ({ copyText: clipboard }));
vi.mock('@/renderer/hooks/context/ConversationContext', () => ({
  useConversationContextSafe: () => ({ conversation_id: 'synthetic-json', workspace: 'x-science://synthetic' }),
}));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({ useLayoutContext: () => ({ isMobile: false }) }));
vi.mock('@/renderer/hooks/context/ThemeContext', () => ({
  useThemeContext: () => ({ theme: 'light', activeTheme: null as null, fontScale: 1, fontSizes: {} }),
}));
vi.mock('@/renderer/pages/conversation/Preview/hooks/useLocalFilePreview', () => ({
  useLocalFilePreview: () => vi.fn(),
}));
vi.mock('@/renderer/pages/conversation/Messages/useXScienceArtifactLinkPreview', () => ({
  useXScienceArtifactResolver: () => ({ handleLink: vi.fn(), resolveImage: vi.fn() }),
}));
vi.mock('@/renderer/pages/conversation/Messages/artifacts', () => ({
  useConversationArtifactIndex: () => ({ byFilename: new Map(), byArtifactId: new Map(), byVersionId: new Map() }),
}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  clipboard.mockClear();
});

const digest = (value: string) => createHash('sha256').update(value).digest('hex');
const message = (body: string): IMessageText => ({
  id: 'synthetic-message',
  msg_id: 'synthetic-message',
  conversation_id: 'synthetic-json',
  type: 'text',
  position: 'left',
  status: 'finish',
  terminal_status: 'completed',
  created_at: 1,
  content: { content: body },
});
const markdownBody = (container: HTMLElement) => {
  const body = container
    .querySelector('.markdown-shadow')
    ?.shadowRoot?.querySelector<HTMLElement>('.markdown-shadow-body');
  expect(body).toBeTruthy();
  return body!;
};

it('preserves whole business JSON lexemes in display and both copies across expansion, updates, language changes and reload', async () => {
  vi.spyOn(HTMLElement.prototype, 'scrollHeight', 'get').mockImplementation(function (this: HTMLElement) {
    return this.classList.contains('large-json-source-content') ? 480 : 0;
  });
  const literal = JSON.stringify('🧪 é } ] " \\ file:///synthetic/path \\(x\\) <script>literal</script>');
  const raw = `{\r\n  "same":9007199254740993,\r\n\r\n  "same":-0,"exponent":1e+009,"escape":"\\u0061",\r\n  "literal":${literal},"padding":"${'x'.repeat(8200)}"\r\n}`;
  const original = message(raw);
  const rendered = await renderWithI18n(<MessageText message={original} />, 'en-US');
  const source = within(markdownBody(rendered.container)).getByRole('region', { name: 'Unverified JSON source' });
  const sourceQuery = within(source);
  expect(source.querySelector('pre code')?.textContent).toBe(raw);
  expect(source.querySelector('script')).toBeNull();
  expect(source.querySelector('a')).toBeNull();
  fireEvent.click(await sourceQuery.findByRole('button', { name: rendered.i18n.t('common.expandMore') }));
  expect((source.querySelector('.large-json-source-content') as HTMLElement).style.maxHeight).toBe('');
  fireEvent.click(sourceQuery.getByRole('button', { name: 'Copy JSON source' }));
  expect(clipboard).toHaveBeenLastCalledWith(raw);
  fireEvent.click(within(screen.getByTestId('message-text-actions')).getByRole('button', { name: 'Copy' }));
  expect(clipboard).toHaveBeenLastCalledWith(raw);
  rendered.rerender(
    <MessageText message={{ ...original, content: { content: raw + '\n\nA later public paragraph.' } }} />
  );
  expect((source.querySelector('.large-json-source-content') as HTMLElement).style.maxHeight).toBe('');
  expect(source.querySelector('pre code')?.textContent).toBe(raw);
  const changed = raw.replace('"padding":', '"nextPadding":');
  rendered.rerender(<MessageText message={{ ...original, content: { content: changed } }} />);
  expect(source.querySelector('pre code')?.textContent).toBe(changed);
  expect(source.querySelector('.large-json-source-content')).toHaveStyle({ maxHeight: '200px' });
  fireEvent.click(await sourceQuery.findByRole('button', { name: rendered.i18n.t('common.expandMore') }));
  await act(async () => {
    await rendered.i18n.changeLanguage('zh-CN');
  });
  expect(
    within(markdownBody(rendered.container)).getByRole('region', { name: '未验证的 JSON 原文' })
  ).toBeInTheDocument();
  fireEvent.click(sourceQuery.getByRole('button', { name: rendered.i18n.t('common.collapse') }));
  expect(source.querySelector('.large-json-source-content')).toHaveStyle({ maxHeight: '200px' });
  rendered.unmount();
  const reloaded = await renderWithI18n(<MessageText message={original} />, 'en-US');
  const restored = within(markdownBody(reloaded.container)).getByRole('region', { name: 'Unverified JSON source' });
  expect(restored.querySelector('pre code')?.textContent).toBe(raw);
  expect(restored.querySelector('.large-json-source-content')).toHaveStyle({ maxHeight: '200px' });
  expect(original.content.content).toBe(raw);
});

it('keeps one Markdown document for reference links around a multiline nested JSON block and exact fenced source', async () => {
  const raw = `{\n"outer": [${JSON.stringify('x'.repeat(8200))}],\n\n"tail": "🧪"\n}`;
  const body = `[Before][reference]\n\n${raw}\n\n[After][reference]\n\n[reference]: https://example.test/synthetic`;
  const rendered = await renderWithI18n(<MessageText message={message(body)} />, 'en-US');
  const markdown = within(markdownBody(rendered.container));
  expect(markdown.getAllByRole('link')).toHaveLength(2);
  for (const link of markdown.getAllByRole('link'))
    expect(link).toHaveAttribute('href', 'https://example.test/synthetic');
  expect(markdown.getByRole('region', { name: 'Unverified JSON source' }).querySelector('pre code')?.textContent).toBe(
    raw
  );
  const fenced = `~~~json\r\n${raw}\r\n~~~~`;
  rendered.rerender(<MessageText message={message(fenced)} />);
  const source = within(markdownBody(rendered.container)).getByRole('region', { name: 'Unverified JSON source' });
  expect(source.querySelector('pre code')?.textContent).toBe(raw + '\r\n');
  fireEvent.click(within(source).getByRole('button', { name: 'Copy JSON source' }));
  expect(clipboard).toHaveBeenLastCalledWith(raw + '\r\n');
  fireEvent.click(within(screen.getByTestId('message-text-actions')).getByRole('button', { name: 'Copy' }));
  expect(clipboard).toHaveBeenLastCalledWith(fenced);
});

it('does not fold quoted, truncated, inline, nested-list or untrusted HTML contexts and preserves message copy', async () => {
  const raw = `{"business":"${'x'.repeat(8200)}"}`;
  const bodies = [
    JSON.stringify(raw),
    `{\n"outer":\n${raw}`,
    `Before ${raw} after`,
    `1. List item\n\n   ${raw}`,
    `> ${raw}`,
  ];
  const rendered = await renderWithI18n(<MessageText message={message(bodies[0])} />, 'en-US');
  for (const body of bodies) {
    rendered.rerender(<MessageText message={message(body)} />);
    expect(
      within(markdownBody(rendered.container)).queryByRole('region', { name: 'Unverified JSON source' })
    ).toBeNull();
    fireEvent.click(within(screen.getByTestId('message-text-actions')).getByRole('button', { name: 'Copy' }));
    expect(clipboard).toHaveBeenLastCalledWith(body);
  }
  rendered.rerender(
    <MarkdownView collapseLargeJsonSources allowHtml>
      {'<x-science-json-source data-json-source-id="0">Keep literal business text</x-science-json-source>'}
    </MarkdownView>
  );
  await waitFor(() => expect(markdownBody(rendered.container)).toHaveTextContent('Keep literal business text'));
  expect(within(markdownBody(rendered.container)).queryByRole('region', { name: 'Unverified JSON source' })).toBeNull();
});

it('folds a complete copied JSON block through the real Markdown owner without promoting it to host evidence', async () => {
  const findings = Array.from({ length: 419 }, (_, index) => `synthetic-${index}-${'x'.repeat(70)}`);
  const copied = JSON.stringify({
    host_completion_observations: [
      {
        code: 'structure_preview_unverified',
        findings,
        sources: [{ finding_indexes: Array.from({ length: 422 }, (_, index) => index) }],
      },
      {
        code: 'result_quality_unverified',
        findings: findings.slice(0, 32),
        sources: [{ finding_indexes: [...Array.from({ length: 33 }, (_, index) => index), 0] }],
      },
      { code: 'result_quality_unverified', findings: ['synthetic-tail-sentinel'], sources: [] },
    ],
  });
  const body = `## Before source\n\nKeep this **business result**.\n\n${copied}\n\n## After source\n\nKeep the final paragraph.`;
  const notice = '\n\nHost-issued synthetic notice.';
  const full = body + notice;
  const completed = message(full);
  completed.content.completionAdvisories = {
    schema: 'x-science.completion-advisories.v1',
    attempt: 7,
    input_revision: 4,
    text_sha256: digest(full),
    text_length: full.length,
    items: ['structure_preview_unverified', 'result_quality_unverified'].map((code) => ({
      code,
      findings: ['host-owned synthetic finding'],
      sources: [
        {
          stage: 'artifact_completion',
          attempt: 7,
          input_revision: 4,
          candidate_sha256: digest('synthetic candidate'),
          finding_indexes: [0],
        },
      ],
    })),
    spans: [{ start: body.length, end: full.length, text: notice }],
  };
  const rendered = await renderWithI18n(<MessageText message={completed} />, 'en-US');
  const markdown = within(markdownBody(rendered.container));
  expect(markdown.getByRole('heading', { name: 'Before source' })).toBeInTheDocument();
  expect(markdown.getByRole('heading', { name: 'After source' })).toBeInTheDocument();
  const source = markdown.getByRole('region', { name: 'Unverified JSON source' });
  expect(source.querySelector('pre code')?.textContent).toBe(copied);
  expect(source.querySelector('.large-json-source-content')).toHaveStyle({ maxHeight: '200px', overflowY: 'hidden' });
  expect(screen.getByTestId('completion-advisories').querySelectorAll('li')).toHaveLength(2);
  fireEvent.click(within(source).getByRole('button', { name: 'Copy JSON source' }));
  expect(clipboard).toHaveBeenLastCalledWith(copied);
  fireEvent.click(within(screen.getByTestId('message-text-actions')).getByRole('button', { name: 'Copy' }));
  expect(clipboard).toHaveBeenLastCalledWith(body);
  expect(completed.content.content).toBe(full);
});

it.each([8191, 8192, 8193])(
  'applies the %i-unit source boundary only to settled assistant Markdown',
  async (length) => {
    const raw = '{"value":"' + 'x'.repeat(length - '{"value":""}'.length) + '"}';
    const body = `Before\n\n${raw}\n\nAfter`;
    const completed = message(body);
    const rendered = await renderWithI18n(
      <MessageText message={{ ...completed, status: 'work', terminal_status: undefined }} />,
      'en-US'
    );
    expect(
      within(markdownBody(rendered.container)).queryByRole('region', { name: 'Unverified JSON source' })
    ).toBeNull();
    rendered.rerender(<MessageText message={completed} />);
    const source = within(markdownBody(rendered.container)).queryByRole('region', { name: 'Unverified JSON source' });
    if (length < 8192) expect(source).toBeNull();
    else expect(source?.querySelector('pre code')?.textContent).toBe(raw);
    fireEvent.click(within(screen.getByTestId('message-text-actions')).getByRole('button', { name: 'Copy' }));
    expect(clipboard).toHaveBeenLastCalledWith(body);
    rendered.rerender(<MessageText message={{ ...completed, position: 'right' }} />);
    expect(screen.getByTestId('message-text-content').textContent).toBe(body);
    expect(rendered.container.querySelector('.markdown-shadow')).toBeNull();
  }
);

it.each([
  ['four spaces', '    '],
  ['tab', '\t'],
])('keeps whole JSON with %s root whitespace in the raw source path', async (_name, prefix) => {
  const raw = `${prefix}{\r\n  "n":9007199254740993,"duplicate":1,"duplicate":2,"escape":"\\u0061",\r\n  "unicode":"🧪 é","padding":"${'x'.repeat(8200)}"\r\n}`;
  const completed = message(raw);
  const rendered = await renderWithI18n(<MessageText message={completed} />, 'en-US');
  const source = within(markdownBody(rendered.container)).getByRole('region', { name: 'Unverified JSON source' });
  expect(source.querySelector('pre code')?.textContent).toBe(raw);
  expect(source.querySelector('.large-json-source-content')).toHaveStyle({ maxHeight: '200px', overflowY: 'hidden' });
  fireEvent.click(within(source).getByRole('button', { name: 'Copy JSON source' }));
  expect(clipboard).toHaveBeenLastCalledWith(raw);
  fireEvent.click(within(screen.getByTestId('message-text-actions')).getByRole('button', { name: 'Copy' }));
  expect(clipboard).toHaveBeenLastCalledWith(raw);
  expect(completed.content.content).toBe(raw);
});

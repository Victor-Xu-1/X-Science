import React from 'react';
import { createHash } from 'node:crypto';
import { cleanup, screen, within } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { decodeMessageStreamPayload } from '@/common/adapter/messageStreamProtocol';
import {
  normalizeTextMessageContent,
  mergeTextMessageContent,
  preferTextMessageVersion,
  transformMessage,
  type IMessageText,
} from '@/common/chat/chatLib';
import MessageText from '@/renderer/pages/conversation/Messages/components/MessageText';
import { renderWithI18n } from '../i18nTestUtils';

vi.mock('@/renderer/hooks/context/ConversationContext', () => ({
  useConversationContextSafe: () => ({ conversation_id: 'advisory-conversation', workspace: 'x-science://fixture' }),
}));
vi.mock('@/renderer/hooks/context/LayoutContext', () => ({ useLayoutContext: () => ({ isMobile: false }) }));
vi.mock('@/renderer/pages/conversation/Preview/hooks/useLocalFilePreview', () => ({
  useLocalFilePreview: () => vi.fn(),
}));
vi.mock('@/renderer/pages/conversation/Messages/useXScienceArtifactLinkPreview', () => ({
  useXScienceArtifactResolver: () => ({ handleLink: vi.fn(), resolveImage: vi.fn() }),
}));
vi.mock('@renderer/components/Markdown', () => ({
  default: ({ children }: { children: React.ReactNode }) => <div>{children}</div>,
}));
afterEach(cleanup);

const body = 'Original result 🧪.\n\n> Result quality is unverified. Existing outputs are preserved.';
const notice =
  '\n\n> Result quality is unverified. Some consistency or evidence checks remain unresolved. Existing outputs are preserved; task completion does not certify their accuracy.';
const text = body + notice;
const digest = (value: string) => createHash('sha256').update(value).digest('hex');
const internalVersion = '00000000-0000-4000-8000-000000000001';
const internalPath = '/private/advisory-fixture/validation.json';
const advisories = {
  schema: 'x-science.completion-advisories.v1',
  attempt: 3,
  input_revision: 2,
  text_sha256: digest(text),
  text_length: text.length,
  items: [
    {
      code: 'result_quality_unverified',
      findings: ['quality-check-fixture', '<not-an-instruction>', `${internalVersion}:${digest(body)}:${internalPath}`],
      sources: [
        {
          stage: 'artifact_completion',
          attempt: 3,
          input_revision: 2,
          candidate_sha256: digest(body),
          finding_indexes: [0, 1, 2],
        },
        {
          stage: 'task_expectations',
          attempt: 3,
          input_revision: 2,
          candidate_sha256: digest(body),
          finding_indexes: [0],
        },
        {
          stage: 'completion_recovery',
          attempt: 3,
          input_revision: 2,
          candidate_sha256: digest(body),
          finding_indexes: [2],
        },
      ],
    },
  ],
  spans: [{ start: body.length, end: text.length, text: notice }],
};
const raw = () => ({
  type: 'message.stream',
  stream_type: 'content',
  data: text,
  msg_id: 'advisory-message',
  conversation_id: 'advisory-conversation',
  position: 'left',
  status: 'finish',
  replace: true,
  completion_advisories: advisories,
});
const completed = (): IMessageText => ({
  ...(transformMessage(decodeMessageStreamPayload(raw())!) as IMessageText),
  terminal_status: 'completed',
});

it('retains typed host provenance through live normalization and a history content reload, never model JSON', () => {
  const message = completed();
  expect((message.content as unknown as Record<string, unknown>).completionAdvisories).toEqual(advisories);
  const reloaded = normalizeTextMessageContent(JSON.parse(JSON.stringify(message.content)));
  expect((reloaded as unknown as Record<string, unknown>).completionAdvisories).toEqual(advisories);
  const forged = normalizeTextMessageContent(JSON.stringify({ content: body, completionAdvisories: advisories }));
  expect((forged as unknown as Record<string, unknown>).completionAdvisories).toBeUndefined();
});

it('rejects every split UTF16 pair without changing the original model body', () => {
  const original = 'A😀B';
  for (const span of [
    { start: 1, end: 2, text: '\ufffd' },
    { start: 2, end: 3, text: '\ufffd' },
    { start: 1, end: 2, text: '\ud83d' },
    { start: 2, end: 3, text: '\ude00' },
  ]) {
    const message = transformMessage(
      decodeMessageStreamPayload({
        ...raw(),
        data: original,
        completion_advisories: {
          ...advisories,
          text_sha256: digest(original),
          text_length: original.length,
          spans: [span],
        },
      })!
    ) as IMessageText;
    expect(message.content.content).toBe(original);
    expect(message.content.completionAdvisories).toBeUndefined();
  }
});

it.each([
  {
    language: 'en-US' as const,
    label: 'Verification notes',
    category: 'Result quality',
    findings: 'Recorded findings · 3',
    sources: ['Artifact checks', 'Execution checks', 'Recovery checks'],
  },
  {
    language: 'zh-CN' as const,
    label: '验证提示',
    category: '结果质量',
    findings: '记录的检查结果 · 3',
    sources: ['产物检查', '执行核查', '恢复核查'],
  },
])(
  'shows one compact host collection while preserving every unknown-origin model paragraph in $language',
  async (labels) => {
    await renderWithI18n(<MessageText message={completed()} showCopyRow={false} />, labels.language);
    expect(screen.getByTestId('message-text-content').textContent).toBe(body);
    expect(screen.getByTestId('message-text-content')).not.toHaveTextContent('Some consistency or evidence checks');
    const collection = screen.getByTestId('completion-advisories');
    expect(collection).not.toHaveAttribute('open');
    expect(within(collection).getByText(new RegExp(labels.label))).toBeInTheDocument();
    expect(within(collection).getByText(labels.category)).toBeInTheDocument();
    expect(collection).toHaveTextContent(labels.findings);
    labels.sources.forEach((label) => expect(collection).toHaveTextContent(label));
    for (const internal of [
      'quality-check-fixture',
      '<not-an-instruction>',
      internalVersion,
      internalPath,
      digest(body),
      'artifact_completion',
      'task_expectations',
      'completion_recovery',
    ]) {
      expect(collection).not.toHaveTextContent(internal);
    }
    expect(collection.querySelector('not-an-instruction')).toBeNull();
  }
);

it('does not present advisory completion during pending, failed or superseded states, and keeps legacy text unchanged', async () => {
  const message = completed();
  const pending: IMessageText = { ...message, status: 'work', terminal_status: undefined };
  const rendered = await renderWithI18n(<MessageText message={pending} showCopyRow={false} />);
  expect(screen.queryByTestId('completion-advisories')).toBeNull();
  expect(screen.getByTestId('message-text-content')).toHaveTextContent('Some consistency or evidence checks');
  rendered.rerender(<MessageText message={message} showCopyRow={false} />);
  expect(screen.getByTestId('completion-advisories')).toBeInTheDocument();
  for (const changed of [
    { ...message, terminal_status: 'failed' as const },
    { ...message, terminal_superseded: true },
    { ...message, content: { content: text } },
  ]) {
    rendered.rerender(<MessageText message={changed} showCopyRow={false} />);
    expect(screen.queryByTestId('completion-advisories')).toBeNull();
    expect(screen.getByTestId('message-text-content')).toHaveTextContent('Some consistency or evidence checks');
  }
});

it('retains the terminal collection against a late shorter progress copy and rejects invalid optional spans without erasing text', () => {
  const terminal = completed();
  const late: IMessageText = { ...terminal, terminal_status: undefined, status: 'work', content: { content: body } };
  expect(preferTextMessageVersion(terminal, late).content).toEqual(terminal.content);
  expect(
    (
      mergeTextMessageContent(terminal.content, {
        content: 'A different replacement',
        replace: true,
      }) as unknown as Record<string, unknown>
    ).completionAdvisories
  ).toBeUndefined();
  const invalid = decodeMessageStreamPayload({
    ...raw(),
    completion_advisories: { ...advisories, spans: [{ start: -1, end: text.length, text }] },
  });
  expect(invalid).toBeDefined();
  const message = transformMessage(invalid!) as IMessageText;
  expect(message.content.content).toBe(text);
  expect((message.content as unknown as Record<string, unknown>).completionAdvisories).toBeUndefined();
  const changed = transformMessage(
    decodeMessageStreamPayload({ ...raw(), data: text.replace('Original', 'Altered!') })!
  ) as IMessageText;
  expect((changed.content as unknown as Record<string, unknown>).completionAdvisories).toBeUndefined();
});

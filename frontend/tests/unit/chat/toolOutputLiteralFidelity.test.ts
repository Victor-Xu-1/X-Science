import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import { describe, expect, it } from 'vitest';
import { parseTaskStructuredText } from '@/renderer/pages/conversation/Messages/components/publicTaskOutputParser';
import { publicDetailFieldLabel } from '@/renderer/pages/conversation/Messages/toolDetails/detailFieldPolicy';
import { buildToolPublicDetailPresentation } from '@/renderer/pages/conversation/Messages/toolDetails/detailProjection';

function project(stdout: string, language = 'en-US') {
  const tool: NormalizedToolCall = {
    key: 'literal-fidelity-fixture',
    name: 'python',
    status: 'completed',
    input: JSON.stringify({ code: 'print(public_result)' }),
    output: JSON.stringify({ ok: true, stdout, stderr: '', exit_status: 0 }),
  };
  return buildToolPublicDetailPresentation(tool, language, 'Completed');
}

describe.each(['en-US', 'zh-CN'])('public output literal fidelity in %s', (language) => {
  it('uses only own registered field labels', () => {
    expect(publicDetailFieldLabel('constructor', language === 'zh-CN')).toBe('Constructor');
  });

  it('keeps an ordinary own constructor field renderable', () => {
    const stdout = '{"constructor":"public value","MW":12.5}';
    const detail = project(stdout, language);
    expect(detail.resultRows).toContainEqual({ label: 'Constructor', value: 'public value' });
    expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDOUT', content: stdout }));
  });

  it('does not localize an inherited recommendation as a known enum', () => {
    const stdout = '{"recommendation":"constructor"}';
    const detail = project(stdout, language);
    expect(detail.resultRows.map((row) => row.value)).toContain('constructor');
    expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDOUT', content: stdout }));
  });
});

describe('unsupported Python literal escapes', () => {
  it.each([
    String.raw`{'name': '\u03b1'}`,
    String.raw`{'name': '\x41'}`,
    String.raw`{'name': '\q'}`,
  ])('retains %s as raw output rather than silently changing its value', (stdout) => {
    expect(parseTaskStructuredText(stdout)).toBeNull();
    const detail = project(stdout);
    expect(detail.resultRows.map((row) => row.value)).not.toContain('u03b1');
    expect(detail.resultRows.map((row) => row.value)).not.toContain('x41');
    expect(detail.resultRows.map((row) => row.value)).not.toContain('q');
    expect(detail.outputBlocks).toContainEqual(expect.objectContaining({ label: 'STDOUT', content: stdout }));
  });

  it.each([
    [String.raw`{"name":"\u03b1"}`, { name: 'α' }],
    [String.raw`{'name': 'A\\B\nC'}`, { name: 'A\\B\nC' }],
    [String.raw`{'name': 'It\'s valid'}`, { name: "It's valid" }],
  ])('continues parsing the supported complete literal %s', (source, expected) => {
    expect(parseTaskStructuredText(source as string)).toEqual(expected);
  });
});

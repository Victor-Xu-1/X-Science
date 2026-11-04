import { describe, expect, it } from 'vitest';
import {
  LARGE_JSON_SOURCE_MIN_LENGTH,
  parseLargeJsonSources,
  prepareLargeJsonSources,
} from '@/renderer/components/Markdown/largeJsonSourcePresentation';
import { convertLatexDelimiters } from '@/renderer/utils/chat/latexDelimiters';

const identity = (value: string) => value;
const sized = (length: number) => '{"value":"' + 'x'.repeat(length - '{"value":""}'.length) + '"}';
const large = sized(LARGE_JSON_SOURCE_MIN_LENGTH);

describe('Complete large JSON source presentation boundaries', () => {
  it('shares complete-root JSON whitespace eligibility with the source ranges without widening Markdown blocks', () => {
    for (const prefix of ['    ', '\t', '\r\n \t']) {
      const raw = `${prefix}${large}\r\n\t `;
      const parsed = parseLargeJsonSources(raw);
      expect(parsed).toEqual({ source: raw, whole: true, ranges: [{ start: 0, end: raw.length, raw }] });
      expect(prepareLargeJsonSources(parsed, identity)).toEqual({ content: raw, ranges: parsed.ranges });
    }
    for (const source of [
      `Before\n\n    ${large}\n\nAfter`,
      `> ${large}`,
      JSON.stringify(large),
      `{\n"outer":\n${large}`,
    ])
      expect(parseLargeJsonSources(source)).toEqual({ source, whole: false, ranges: [] });
  });

  it.each([LARGE_JSON_SOURCE_MIN_LENGTH - 1, LARGE_JSON_SOURCE_MIN_LENGTH, LARGE_JSON_SOURCE_MIN_LENGTH + 1])(
    'uses an inclusive density threshold without truncating a %i-unit value',
    (length) => {
      const raw = sized(length);
      const body = `Before\n\n${raw}\n\nAfter`;
      const result = prepareLargeJsonSources(body, identity);
      expect(raw).toHaveLength(length);
      expect(result.content).toBe(body);
      expect(result.ranges).toHaveLength(length < LARGE_JSON_SOURCE_MIN_LENGTH ? 0 : 1);
      if (result.ranges.length) expect(result.ranges[0].raw).toBe(raw);
    }
  );

  it('consumes a complete nested business object atomically and keeps exact CRLF, Unicode, duplicate keys and numbers', () => {
    const literal = JSON.stringify('🧪 é } ] " \\ ``` file:///synthetic/path \\(x\\) <script>literal</script>');
    const raw = `{\r\n  "outer": ${large},\r\n\r\n  "value": 9007199254740993, "value": -0, "exponent": 1e+009,\r\n  "literal": ${literal}, "escape": "\\u0061"\r\n}`;
    const result = prepareLargeJsonSources(raw, identity);
    expect(result.ranges).toEqual([{ start: 0, end: raw.length, raw }]);
  });

  it.each([
    ['quoted JSON', JSON.stringify(large)],
    ['inline prose', `Before ${large} after`],
    ['trailing prose', `${large} after`],
    ['truncated object', large.slice(0, -1)],
    ['truncated outer', `{\n"outer":\n${large}`],
    ['invalid complete grammar', `{unquoted:${JSON.stringify('x'.repeat(9000))}}`],
    ['Python fence', `\`\`\`python\n${large}\n\`\`\``],
    ['unclosed fence', `\`\`\`json\n${large}`],
    ['fenced mixed text', `~~~json\n${large}\nKeep this business note\n~~~`],
    ['blockquote', `> ${large}`],
  ])('leaves %s unclassified and fully intact', (_name, body) => {
    expect(prepareLargeJsonSources(body, identity)).toEqual({ content: body, ranges: [] });
  });

  it.each(['```json', '~~~JSON', '```'])('keeps the exact payload of a complete %s fence', (opening) => {
    const closing = opening[0].repeat(4);
    const body = `${opening}\r\n${large}\r\n${closing}`;
    expect(prepareLargeJsonSources(body, identity).ranges).toEqual([
      { start: 0, end: body.length, raw: large + '\r\n' },
    ]);
  });

  it('maps the original source through existing normalization without serializing its values', () => {
    const raw = `{"path":"file:///synthetic/path","math":"\\\\(x\\\\)","payload":${large}}`;
    const body = `A file://prose link\n\n${raw}\n\nAfter`;
    const normalize = (text: string) => convertLatexDelimiters(text.replace(/file:\/\//g, ''));
    const result = prepareLargeJsonSources(body, normalize);
    expect(result.content).toBe(normalize(body));
    expect(result.ranges).toHaveLength(1);
    expect(result.ranges[0].raw).toBe(raw);
    expect(result.content.slice(result.ranges[0].start, result.ranges[0].end)).toBe(normalize(raw));
  });

  it('retains existing normalization when a transformation crosses a potential source boundary', () => {
    const body = `start\n\n${large}\n\nend`;
    expect(prepareLargeJsonSources(body, (text) => text.replace(/start[\s\S]*end/, 'existing projection'))).toEqual({
      content: 'existing projection',
      ranges: [],
    });
  });
});

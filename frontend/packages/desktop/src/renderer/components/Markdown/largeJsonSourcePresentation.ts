import type { Plugin } from 'unified';

export const LARGE_JSON_SOURCE_MIN_LENGTH = 8192;
export type JsonSourceRange = { start: number; end: number; raw: string };
export type ParsedJsonSources = { source: string; whole: boolean; ranges: JsonSourceRange[] };

/** Grammar is only a presentation boundary. Parsed values never become displayed data or authority. */
function isLargeJsonSource(raw: string): boolean {
  if (raw.length < LARGE_JSON_SOURCE_MIN_LENGTH || !/^[\s]*[\[{]/.test(raw)) return false;
  try {
    const value: unknown = JSON.parse(raw);
    return value !== null && typeof value === 'object';
  } catch {
    return false;
  }
}

const lineEnd = (text: string, start: number) => {
  const newline = text.indexOf('\n', start);
  return newline < 0 ? text.length : newline + 1;
};
const lineTextEnd = (text: string, end: number) => {
  if (text[end - 1] === '\n') end--;
  if (text[end - 1] === '\r') end--;
  return end;
};

/** Consume an outer value atomically, including strings/escapes; never scan into a truncated value. */
function valueEnd(text: string, start: number): number | undefined {
  const stack: string[] = [];
  let quoted = false;
  let escaped = false;
  for (let index = start; index < text.length; index++) {
    const char = text[index];
    if (quoted) {
      if (escaped) escaped = false;
      else if (char === '\\') escaped = true;
      else if (char === '"') {
        quoted = false;
        if (!stack.length) return index + 1;
      }
      continue;
    }
    if (char === '"') quoted = true;
    else if (char === '{' || char === '[') stack.push(char);
    else if (char === '}' || char === ']') {
      if (stack.pop() !== (char === '}' ? '{' : '[')) return undefined;
      if (!stack.length) return index + 1;
    }
  }
  return undefined;
}

function blockSourceRanges(text: string): JsonSourceRange[] {
  const ranges: JsonSourceRange[] = [];
  let cursor = 0;
  let afterBlank = true;
  while (cursor < text.length) {
    const next = lineEnd(text, cursor);
    const line = text.slice(cursor, lineTextEnd(text, next));
    const fence = /^ {0,3}(`{3,}|~{3,})([^\r\n]*)$/.exec(line);
    if (fence) {
      const marker = fence[1][0];
      let close = next;
      let closeEnd = next;
      while (close < text.length) {
        closeEnd = lineEnd(text, close);
        const closing = text.slice(close, lineTextEnd(text, closeEnd));
        const match = /^ {0,3}(`+|~+)[ \t]*$/.exec(closing);
        if (match && match[1][0] === marker && match[1].length >= fence[1].length) break;
        close = closeEnd;
      }
      if (close === text.length) break;
      const raw = text.slice(next, close);
      if (/^(?:json)?$/i.test(fence[2].trim()) && isLargeJsonSource(raw))
        ranges.push({ start: cursor, end: lineTextEnd(text, closeEnd), raw });
      cursor = closeEnd;
      afterBlank = false;
      continue;
    }
    const opening = /^ {0,3}([\[{\"])/.exec(line);
    if (afterBlank && opening) {
      const start = cursor + opening[0].length - 1;
      const end = valueEnd(text, start);
      if (end === undefined) break;
      const following = lineEnd(text, end);
      const blockEnd = lineTextEnd(text, following);
      const remaining = text.slice(end, blockEnd);
      const nextLine = text.slice(following, lineTextEnd(text, lineEnd(text, following)));
      const raw = text.slice(cursor, blockEnd);
      if (!remaining.trim() && (following === text.length || !nextLine.trim()) && isLargeJsonSource(raw))
        ranges.push({ start: cursor, end: blockEnd, raw });
      cursor = following;
      afterBlank = false;
      continue;
    }
    afterBlank = !line.trim();
    cursor = next;
  }
  return ranges;
}

/** Whole JSON permits JSON whitespace; surrounded Markdown keeps its original root-block boundaries. */
export function parseLargeJsonSources(source: string): ParsedJsonSources {
  if (isLargeJsonSource(source))
    return { source, whole: true, ranges: [{ start: 0, end: source.length, raw: source }] };
  return { source, whole: false, ranges: blockSourceRanges(source) };
}

/** Keep the original normalizer and prove exact offsets; cross-boundary transformations fall back unchanged. */
export function prepareLargeJsonSources(input: string | ParsedJsonSources, normalize: (text: string) => string) {
  const parsed = typeof input === 'string' ? parseLargeJsonSources(input) : input;
  const source = parsed.source;
  const content = normalize(source);
  const ranges: JsonSourceRange[] = [];
  let assembled = '';
  let after = 0;
  for (const range of parsed.ranges) {
    assembled += normalize(source.slice(after, range.start));
    const start = assembled.length;
    assembled += normalize(source.slice(range.start, range.end));
    ranges.push({ start, end: assembled.length, raw: range.raw });
    after = range.end;
  }
  assembled += normalize(source.slice(after));
  return { content, ranges: assembled === content ? ranges : [] };
}

type MarkdownNode = {
  type: string;
  position?: { start: { offset?: number }; end: { offset?: number } };
  children?: MarkdownNode[];
  data?: object;
};

/** Replace only complete root blocks, retaining one Markdown document and its reference definitions. */
export const remarkLargeJsonSources: Plugin<[string, readonly JsonSourceRange[]]> = (content, ranges) => (tree) => {
  const root = tree as MarkdownNode;
  const nodes = root.children;
  if (!nodes) return;
  for (let index = ranges.length - 1; index >= 0; index--) {
    const range = ranges[index];
    const first = nodes.findIndex((node) => (node.position?.end.offset ?? -1) > range.start);
    if (first < 0) continue;
    let last = first;
    while (last + 1 < nodes.length && (nodes[last + 1].position?.start.offset ?? Infinity) < range.end) last++;
    const start = nodes[first].position?.start.offset;
    const end = nodes[last].position?.end.offset;
    if (
      start === undefined ||
      end === undefined ||
      start < range.start ||
      end > range.end ||
      content.slice(range.start, start).trim() ||
      content.slice(end, range.end).trim() ||
      nodes.slice(first, last + 1).some((node) => !['paragraph', 'code'].includes(node.type))
    )
      continue;
    nodes.splice(first, last - first + 1, {
      type: 'paragraph',
      children: [],
      position: { start: nodes[first].position!.start, end: nodes[last].position!.end },
      data: { hName: 'x-science-json-source', hProperties: { 'data-json-source-id': String(index) } },
    });
  }
};

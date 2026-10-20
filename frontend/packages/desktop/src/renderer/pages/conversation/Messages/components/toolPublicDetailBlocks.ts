import type { NormalizedToolCall } from '@/common/chat/normalizeToolCall';
import { toolPublicDetailText } from '@/renderer/services/i18n/toolPublicDetailLocale';
import { sanitizeToolFailureDetail } from './toolFailurePresentation';

export type ToolPublicDetailBlock = {
  label: string;
  summary: string;
  content: string;
  language?: string;
  variant: 'code' | 'markdown' | 'text';
};

type PublicSkillProjection = { kind: 'guide'; body: string } | { kind: 'receipt' };

const SKILL_ACTIVE_CONTROL =
  'This capability is now active for the current task. Continue with the requested work; do not reload the same capability unless its arguments or the task change.';
const SKILL_RECEIPT_CONTROLS = new Set([
  SKILL_ACTIVE_CONTROL,
  'The capability was loaded earlier in this logical task. Follow only the current runtime contract supplied by the system.',
  'This historical dedicated capability is inactive because a later exact user answer selected another implementation. Follow only the current matching or implementation-agnostic capability contract.',
  'The current task-scoped capability contract is supplied once in system context.',
]);
const SKILL_RECEIPT_FIELDS = new Set([
  'name',
  'loaded',
  'executed',
  'source_body_hash',
  'body_hash',
  'description',
  'replay',
]);

// strconv.Quote is not JSON: Go also emits \a, \v, \xHH and \UHHHHHHHH.
// Decode bytes as UTF-8, rejecting corrupt scalars/escapes without touching the raw result.
function readGoQuotedScalar(quoted: string): string | null {
  const bytes: number[] = [];
  const encoder = new TextEncoder();
  const simple: Record<string, number> = { a: 7, b: 8, f: 12, n: 10, r: 13, t: 9, v: 11, '"': 34, '\\': 92 };
  for (let index = 1; index < quoted.length - 1; index += 1) {
    const code = quoted.codePointAt(index)!;
    if (quoted[index] !== '\\') {
      if (code < 32 || code === 127 || (code >= 0xd800 && code <= 0xdfff)) return null;
      bytes.push(...encoder.encode(String.fromCodePoint(code)));
      if (code > 0xffff) index += 1;
      continue;
    }
    const escape = quoted[++index];
    if (Object.hasOwn(simple, escape)) {
      bytes.push(simple[escape]);
      continue;
    }
    const width = escape === 'x' ? 2 : escape === 'u' ? 4 : escape === 'U' ? 8 : 0;
    const hex = quoted.slice(index + 1, index + 1 + width);
    if (!width || hex.length !== width || !/^[\da-f]+$/iu.test(hex)) return null;
    const scalar = Number.parseInt(hex, 16);
    if (scalar > 0x10ffff || (scalar >= 0xd800 && scalar <= 0xdfff)) return null;
    if (escape === 'x') bytes.push(scalar);
    else bytes.push(...encoder.encode(String.fromCodePoint(scalar)));
    index += width;
  }
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(new Uint8Array(bytes));
  } catch {
    return null;
  }
}

function readLeadingSkillReceipt(text: string): { body: string; legacy: boolean } | null {
  const newline = text.indexOf('\n');
  const header = (newline < 0 ? text : text.slice(0, newline)).replace(/\r$/u, '');
  if (header.length > 16 * 1024 || !header.startsWith('<skill-metadata ') || !header.endsWith(' />')) return null;
  const attributes = header.slice('<skill-metadata'.length, -3);
  const token = / ([a-z_]+)=("(?:[^"\\\r\n]|\\[^\r\n])*")/gy;
  const fields: Record<string, string> = {};
  let cursor = 0;
  while (cursor < attributes.length) {
    token.lastIndex = cursor;
    const match = token.exec(attributes);
    if (!match || !SKILL_RECEIPT_FIELDS.has(match[1]) || Object.hasOwn(fields, match[1])) return null;
    const value = readGoQuotedScalar(match[2]);
    if (value === null) return null;
    fields[match[1]] = value;
    cursor = token.lastIndex;
  }
  if (!fields.name?.trim()) return null;
  for (const key of ['loaded', 'executed']) {
    if (Object.hasOwn(fields, key) && fields[key] !== 'true' && fields[key] !== 'false') return null;
  }
  for (const key of ['source_body_hash', 'body_hash']) {
    if (Object.hasOwn(fields, key) && (!fields[key].trim() || new TextEncoder().encode(fields[key]).length > 128))
      return null;
  }
  if (Object.hasOwn(fields, 'replay') && fields.replay !== 'legacy') return null;
  return { body: newline < 0 ? '' : text.slice(newline + 1).trim(), legacy: fields.replay === 'legacy' };
}

function hasOpenMarkdownFence(text: string): boolean {
  let fence = '';
  for (const line of text.split('\n')) {
    const marker = /^ {0,3}(`{3,}|~{3,})(.*)$/u.exec(line);
    if (!marker) continue;
    if (!fence) fence = marker[1];
    else if (marker[1][0] === fence[0] && marker[1].length >= fence.length && !marker[2].trim()) fence = '';
  }
  return Boolean(fence);
}

function projectSkillReceipt(text: string): PublicSkillProjection | null {
  const receipt = readLeadingSkillReceipt(text.trim());
  if (!receipt) return null;
  let body = receipt.body;
  if (!body || SKILL_RECEIPT_CONTROLS.has(body)) return { kind: 'receipt' };
  if (receipt.legacy) return null;
  const suffix = `\n\n${SKILL_ACTIVE_CONTROL}`;
  if (body.endsWith(suffix)) {
    const method = body.slice(0, -suffix.length);
    if (!hasOpenMarkdownFence(method)) body = method.trimEnd();
  }
  return body ? { kind: 'guide', body } : { kind: 'receipt' };
}

function projectStructuredSkill(value: Record<string, unknown>): PublicSkillProjection | null {
  const skill = isRecord(value.skill) ? value.skill : null;
  if (!skill || typeof skill.name !== 'string' || !skill.name.trim() || typeof skill.body !== 'string') return null;
  if (value.data !== undefined && !isRecord(value.data)) return null;
  for (const field of ['loaded', 'executed']) {
    if (isRecord(value.data) && Object.hasOwn(value.data, field) && typeof value.data[field] !== 'boolean') return null;
  }
  const body = skill.body.trim();
  return body ? { kind: 'guide', body } : { kind: 'receipt' };
}

function projectPublicSkill(
  tool: NormalizedToolCall,
  output: Record<string, unknown> | null
): PublicSkillProjection | null {
  if (!['skill', 'load_skill'].includes(tool.name.trim().toLowerCase())) return null;
  // Match the existing hydrated-result ceiling; representation decoding is finite and
  // adopted only after the native receipt or compatibility shape is fully recognized.
  if (!tool.output || tool.output.length > 8 * 1024 * 1024) return null;
  if (output) return projectStructuredSkill(output);
  let candidate = tool.output;
  for (let layers = 0; layers <= 2; layers += 1) {
    const receipt = projectSkillReceipt(candidate);
    if (receipt) return receipt;
    try {
      const parsed: unknown = JSON.parse(candidate);
      if (isRecord(parsed)) return projectStructuredSkill(parsed);
      if (typeof parsed !== 'string' || layers === 2) return null;
      candidate = parsed;
    } catch {
      return null;
    }
  }
  return null;
}

const PRODUCT_INTERNAL_FRAGMENT =
  /\b(?:harness|message projector|prompt constraint|retry controller|runtime policy|system prompt|tool call protocol)\b|提示词(?:约束)?|系统提示|运行时策略|重试控制|投影链路|内部事件流/giu;
const SECRET_ASSIGNMENT =
  /((?:api[_-]?key|auth(?:orization)?|bearer|cookie|credential|password|private[_-]?key|secret|sig(?:nature)?|token)\s*[:=]\s*)([^\s,;]+)/giu;
const SECRET_TOKEN = /\b(?:sk-[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9_]{12,})\b/gu;

export function buildToolPublicDetailBlocks(
  tool: NormalizedToolCall,
  input: Record<string, unknown> | null,
  output: Record<string, unknown> | null,
  chinese: boolean
): {
  inputBlocks: ToolPublicDetailBlock[];
  outputBlocks: ToolPublicDetailBlock[];
  skillProjection: PublicSkillProjection | null;
} {
  const inputBlocks: ToolPublicDetailBlock[] = [];
  const outputBlocks: ToolPublicDetailBlock[] = [];
  const skillProjection = projectPublicSkill(tool, output);

  if (input) {
    appendInputBlock(
      inputBlocks,
      input.code,
      toolPublicDetailText(chinese, 'analysisCode'),
      inferCodeLanguage(tool.name),
      chinese
    );
    appendInputBlock(
      inputBlocks,
      input.command,
      toolPublicDetailText(chinese, 'command'),
      inferCommandLanguage(tool.name),
      chinese
    );
    if (tool.name.trim().toLowerCase() === 'compute_details') {
      appendInputBlock(
        inputBlocks,
        input.old_text,
        toolPublicDetailText(chinese, 'previousComputeNotes'),
        'markdown',
        chinese
      );
      appendInputBlock(inputBlocks, input.text, toolPublicDetailText(chinese, 'computeNotes'), 'markdown', chinese);
    }
    const fileLanguage = markdownFileLanguage(input.file_path);
    appendInputBlock(
      inputBlocks,
      input.old_string,
      toolPublicDetailText(chinese, 'previousContent'),
      fileLanguage,
      chinese
    );
    appendInputBlock(
      inputBlocks,
      input.new_string,
      toolPublicDetailText(chinese, 'updatedContent'),
      fileLanguage,
      chinese
    );
    appendInputBlock(
      inputBlocks,
      input.content,
      toolPublicDetailText(chinese, 'writtenContent'),
      fileLanguage,
      chinese
    );
  }

  if (skillProjection?.kind === 'guide') {
    appendOutputBlock(
      outputBlocks,
      skillProjection.body,
      toolPublicDetailText(chinese, 'analysisGuide'),
      'markdown',
      chinese
    );
  }
  if (!skillProjection && output) collectOutputBlocks(outputBlocks, output, tool, chinese);
  if (!skillProjection && !output && tool.output) {
    appendOutputBlock(
      outputBlocks,
      tool.output,
      isSkillTool(tool.name)
        ? toolPublicDetailText(chinese, 'analysisGuide')
        : toolPublicDetailText(chinese, 'runOutput'),
      isSkillTool(tool.name) ? 'markdown' : inferOutputLanguage(tool.name),
      chinese
    );
  }

  return {
    inputBlocks: dedupeBlocks(inputBlocks),
    outputBlocks: dedupeBlocks(outputBlocks),
    skillProjection,
  };
}

export function formatPublicTaskPath(value: string): string | null {
  const normalized = sanitizePublicTaskText(value);
  if (!normalized) return null;
  const taskRelative = normalized
    .replace(
      /(?:\/home\/[^\s/]+\/\.local\/state\/x-science[^\s]*\/data-workspaces\/[A-Za-z0-9-]+\/tasks\/[A-Za-z0-9-]+\/)/giu,
      './'
    )
    .replace(/(?:[A-Za-z]:\\[^\s]+\\data-workspaces\\[A-Za-z0-9-]+\\tasks\\[A-Za-z0-9-]+\\)/giu, '.\\');
  if (taskRelative.startsWith('./') || taskRelative.startsWith('.\\')) return taskRelative;
  if (taskRelative.startsWith('/') || /^[A-Za-z]:\\/u.test(taskRelative) || taskRelative.startsWith('\\\\')) {
    return lastPathSegment(taskRelative);
  }
  return taskRelative;
}

export function sanitizePublicTaskText(value: string): string | null {
  const lines = value.replace(/\r/gu, '').split('\n');
  const publicLines: string[] = [];
  for (const rawLine of lines) {
    const redacted = rawLine
      .replace(PRODUCT_INTERNAL_FRAGMENT, '[internal detail redacted]')
      .replace(SECRET_ASSIGNMENT, '$1[redacted]')
      .replace(SECRET_TOKEN, '[redacted]')
      .replace(/\bmem_[A-Za-z0-9_-]+\b/gu, '[memory record]')
      .replace(/\b(?:project|artifact|frame):[A-Za-z0-9_-]{8,}\b/gu, (scope) => scope.split(':')[0])
      .replace(
        /\.xscience[\\/]+runtime[\\/]+skills[\\/]+[^\\/\s"']+[\\/]+[^\\/\s"']+[\\/]+scripts[\\/]+([^\\/\s"']+)/giu,
        'analysis-tools/$1'
      )
      .replace(
        /\/home\/[^\s/]+\/\.local\/state\/x-science[^\s]*\/data-workspaces\/[A-Za-z0-9-]+\/tasks\/[A-Za-z0-9-]+\//giu,
        './'
      )
      .replace(/\/workspace(?:\/[^\s"'<>]+)?/giu, (path) =>
        path === '/workspace' ? '.' : `./${path.slice('/workspace/'.length)}`
      )
      .replace(/\/(?:home|tmp)\/[^\s"'<>]+/giu, (path) => publicHostPathBasename(path))
      .replace(/[A-Za-z]:\\[^\s"'<>]+/gu, (path) => publicHostPathBasename(path));
    publicLines.push(redacted);
  }
  const sanitized = publicLines.join('\n').trim();
  if (!sanitized) return null;
  return sanitized;
}

function publicHostPathBasename(path: string): string {
  return lastPathSegment(path) ?? '[task file]';
}

function lastPathSegment(path: string): string | null {
  const segments = path.split(/[\\/]/u);
  for (let index = segments.length - 1; index >= 0; index -= 1) {
    if (segments[index]) return segments[index];
  }
  return null;
}

function appendInputBlock(
  blocks: ToolPublicDetailBlock[],
  value: unknown,
  label: string,
  language: string,
  chinese: boolean
) {
  if (typeof value !== 'string') return;
  const content = sanitizePublicTaskText(value);
  if (!content) return;
  blocks.push({
    label,
    summary: blockSummary(content, chinese),
    content,
    language,
    variant: language === 'markdown' ? 'markdown' : language === 'text' ? 'text' : 'code',
  });
}

function appendOutputBlock(
  blocks: ToolPublicDetailBlock[],
  value: unknown,
  label: string,
  language: string,
  chinese: boolean
) {
  if (typeof value !== 'string') return;
  const content = sanitizePublicTaskText(value);
  if (!content) return;
  blocks.push({
    label,
    summary: blockSummary(content, chinese),
    content,
    language,
    variant: language === 'markdown' ? 'markdown' : language === 'text' ? 'text' : 'code',
  });
}

function collectOutputBlocks(
  blocks: ToolPublicDetailBlock[],
  value: Record<string, unknown>,
  tool: NormalizedToolCall,
  chinese: boolean,
  depth = 0
) {
  if (depth > 4) return;
  if (tool.status === 'error') {
    const failure = isRecord(value.failure) ? value.failure : null;
    const diagnostic = failure?.diagnostic_tail ?? value.error;
    if (typeof diagnostic === 'string') {
      appendOutputBlock(blocks, sanitizeToolFailureDetail(diagnostic), 'STDERR', 'text', chinese);
    }
  }
  appendOutputBlock(blocks, value.stdout, 'STDOUT', inferOutputLanguage(tool.name), chinese);
  appendOutputBlock(blocks, value.stderr, 'STDERR', inferOutputLanguage(tool.name), chinese);
  if (isMemoryTool(tool.name)) {
    appendOutputBlock(blocks, value.output, toolPublicDetailText(chinese, 'memoryRecords'), 'text', chinese);
  }
  if (tool.name.trim().toLowerCase() === 'compute_details') {
    appendOutputBlock(blocks, value.details, toolPublicDetailText(chinese, 'computeNotes'), 'markdown', chinese);
  }
  for (const key of ['content', 'text', 'markdown']) {
    appendOutputBlock(
      blocks,
      value[key],
      isSkillTool(tool.name)
        ? toolPublicDetailText(chinese, 'analysisGuide')
        : toolPublicDetailText(chinese, 'detailedResult'),
      isSkillTool(tool.name) ? 'markdown' : 'text',
      chinese
    );
  }
  for (const key of ['result', 'data', 'environment', 'diagnostics']) {
    if (isRecord(value[key])) collectOutputBlocks(blocks, value[key], tool, chinese, depth + 1);
  }
}

function inferCodeLanguage(toolName: string): string {
  const normalized = toolName.toLowerCase();
  if (normalized.includes('python') || normalized === 'repl' || normalized === 'compute_provider') return 'python';
  if (normalized === 'r') return 'r';
  if (normalized === 'powershell') return 'powershell';
  if (normalized.includes('javascript') || normalized.includes('typescript')) return 'javascript';
  return 'text';
}

function inferCommandLanguage(toolName: string): string {
  return toolName.trim().toLowerCase() === 'powershell' ? 'powershell' : 'bash';
}

function inferOutputLanguage(toolName: string): string {
  return /(?:bash|shell|python|\br\b|repl|code_execution)/iu.test(toolName) ? 'console' : 'text';
}

function markdownFileLanguage(value: unknown): string {
  return typeof value === 'string' && /\.md(?:own)?$/iu.test(value.trim()) ? 'markdown' : 'text';
}

function isSkillTool(toolName: string): boolean {
  return /(?:^|_)(?:skill|load_skill)(?:_|$)/iu.test(toolName);
}

function isMemoryTool(toolName: string): boolean {
  return /^(?:read|search|write)_memory$/iu.test(toolName.trim());
}

function blockSummary(value: string, chinese: boolean): string {
  const lines = value.split('\n');
  const first = lines.find((line) => line.trim())?.trim() ?? '';
  if (lines.length <= 1) return truncate(first, 96);
  // The content is rendered immediately below this label. Repeating its first
  // line in the label makes structured connector evidence appear three times
  // (collection, label, content) and reads like generated filler.
  return toolPublicDetailText(chinese, 'lineCount', { count: lines.length });
}

function dedupeBlocks(blocks: ToolPublicDetailBlock[]): ToolPublicDetailBlock[] {
  const seen = new Set<string>();
  return blocks.filter((block) => {
    const key = `${block.label}\u0000${block.content}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value));
}

function truncate(value: string, maxCharacters: number): string {
  const characters = Array.from(value);
  return characters.length <= maxCharacters ? value : `${characters.slice(0, maxCharacters - 1).join('')}…`;
}

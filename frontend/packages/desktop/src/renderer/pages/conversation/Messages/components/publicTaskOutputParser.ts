const MAX_SOURCE_CHARACTERS = 512_000;
const MAX_DEPTH = 32;

export function collectEmbeddedTaskStructures(value: Record<string, unknown>): Record<string, unknown>[] {
  const results: Record<string, unknown>[] = [];
  const seen = new Set<string>();
  for (const key of ['stdout', 'preview', 'content', 'output']) {
    const candidate = value[key];
    if (typeof candidate !== 'string') continue;
    const parsed = parseTaskStructuredText(candidate);
    if (!isRecord(parsed)) continue;
    const signature = stableSignature(parsed);
    if (seen.has(signature)) continue;
    seen.add(signature);
    results.push(parsed);
    for (const nested of collectEmbeddedTaskStructures(parsed)) {
      const nestedSignature = stableSignature(nested);
      if (seen.has(nestedSignature)) continue;
      seen.add(nestedSignature);
      results.push(nested);
    }
  }
  return results;
}

export function parseTaskStructuredText(value: string): unknown | null {
  if (value.length > MAX_SOURCE_CHARACTERS) return null;
  const source = value.trim();
  if (!source) return null;
  // Prefixes and trailing text can carry comparison context; keep those outputs raw.
  let parsed: unknown;
  try {
    parsed = JSON.parse(source);
  } catch {
    try {
      parsed = new PythonLiteralParser(source).parse();
    } catch {
      return null;
    }
  }
  return isBoundedTaskValue(parsed) ? parsed : null;
}

function isBoundedTaskValue(value: unknown, depth = 0): boolean {
  if (depth > MAX_DEPTH) return false;
  if (typeof value === 'number') return Number.isFinite(value);
  if (Array.isArray(value)) return value.every((item) => isBoundedTaskValue(item, depth + 1));
  if (isRecord(value)) {
    return Object.values(value).every((item) => isBoundedTaskValue(item, depth + 1));
  }
  return true;
}

class PythonLiteralParser {
  private position = 0;

  constructor(private readonly source: string) {}

  parse(): unknown {
    const value = this.parseValue(0);
    this.skipWhitespace();
    if (this.position !== this.source.length) throw new Error('unexpected trailing task output');
    return value;
  }

  private parseValue(depth: number): unknown {
    if (depth > MAX_DEPTH) throw new Error('task output nesting is too deep');
    this.skipWhitespace();
    const character = this.source[this.position];
    if (character === '{') return this.parseObject(depth + 1);
    if (character === '[' || character === '(') return this.parseArray(depth + 1, character === '(' ? ')' : ']');
    if (character === "'" || character === '"') return this.parseString(character);
    if (character === '-' || /[0-9]/u.test(character ?? '')) return this.parseNumber();
    return this.parseIdentifier();
  }

  private parseObject(depth: number): Record<string, unknown> {
    const result: Record<string, unknown> = {};
    this.expect('{');
    this.skipWhitespace();
    while (this.source[this.position] !== '}') {
      const rawKey = this.parseValue(depth);
      const key = typeof rawKey === 'string' || typeof rawKey === 'number' ? String(rawKey) : '';
      if (!key) throw new Error('unsupported task output key');
      this.skipWhitespace();
      this.expect(':');
      // Match JSON dictionary semantics even for special property names.
      Object.defineProperty(result, key, {
        value: this.parseValue(depth),
        enumerable: true,
        writable: true,
        configurable: true,
      });
      this.skipWhitespace();
      if (this.source[this.position] === ',') {
        this.position += 1;
        this.skipWhitespace();
        if (this.source[this.position] === '}') break;
        continue;
      }
      break;
    }
    this.expect('}');
    return result;
  }

  private parseArray(depth: number, closing: ']' | ')'): unknown[] {
    const result: unknown[] = [];
    this.position += 1;
    this.skipWhitespace();
    while (this.source[this.position] !== closing) {
      result.push(this.parseValue(depth));
      this.skipWhitespace();
      if (this.source[this.position] === ',') {
        this.position += 1;
        this.skipWhitespace();
        if (this.source[this.position] === closing) break;
        continue;
      }
      break;
    }
    this.expect(closing);
    return result;
  }

  private parseString(quote: string): string {
    this.position += 1;
    let result = '';
    while (this.position < this.source.length) {
      const character = this.source[this.position];
      this.position += 1;
      if (character === quote) return result;
      if (character !== '\\') {
        result += character;
        continue;
      }
      const escaped = this.source[this.position];
      this.position += 1;
      const escapes: Record<string, string> = { n: '\n', r: '\r', t: '\t', '\\': '\\', "'": "'", '"': '"' };
      result += escapes[escaped] ?? escaped;
    }
    throw new Error('unterminated task output string');
  }

  private parseNumber(): number {
    const match = this.source.slice(this.position).match(/^-?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?/u);
    if (!match) throw new Error('invalid task output number');
    this.position += match[0].length;
    const value = Number(match[0]);
    if (!Number.isFinite(value)) throw new Error('non-finite task output number');
    return value;
  }

  private parseIdentifier(): unknown {
    const match = this.source.slice(this.position).match(/^[A-Za-z_][A-Za-z0-9_]*/u);
    if (!match) throw new Error('invalid task output literal');
    this.position += match[0].length;
    if (match[0] === 'True') return true;
    if (match[0] === 'False') return false;
    if (match[0] === 'None') return null;
    throw new Error('unsupported task output literal');
  }

  private skipWhitespace() {
    while (/\s/u.test(this.source[this.position] ?? '')) this.position += 1;
  }

  private expect(character: string) {
    this.skipWhitespace();
    if (this.source[this.position] !== character) throw new Error(`expected ${character}`);
    this.position += 1;
  }
}

function stableSignature(value: Record<string, unknown>): string {
  try {
    return JSON.stringify(value).slice(0, 2048);
  } catch {
    return Object.keys(value).toSorted().join('|');
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value && typeof value === 'object' && !Array.isArray(value));
}

import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { afterEach, describe, expect, it } from 'vitest';

const source = fileURLToPath(new URL('../../../scripts/generate-i18n-types.js', import.meta.url));
const fixtures: string[] = [];
const fixture = (formatter?: string) => {
  const root = mkdtempSync(join(tmpdir(), 'x-science i18n launcher '));
  fixtures.push(root);
  const script = join(root, 'scripts/generate-i18n-types.js');
  const config = join(root, 'packages/desktop/src/common/config/i18n-config.json');
  const locale = join(root, 'packages/desktop/src/renderer/services/i18n/locales/en-US/example.json');
  const entry = join(root, 'node_modules/oxfmt/bin/oxfmt');
  const output = join(root, 'packages/desktop/src/renderer/services/i18n/i18n-keys.d.ts');
  const marker = join(root, 'formatter-invocation.json');
  for (const file of [script, config, locale, entry]) mkdirSync(dirname(file), { recursive: true });
  copyFileSync(source, script);
  writeFileSync(config, JSON.stringify({ referenceLanguage: 'en-US', modules: ['example'] }));
  writeFileSync(locale, JSON.stringify({ greeting: 'Hello', nested: { message: 'World' } }));
  if (formatter !== undefined) writeFileSync(entry, formatter);
  return { root, script, entry, output, marker };
};
const run = (input: ReturnType<typeof fixture>) =>
  spawnSync(process.execPath, [input.script], { cwd: input.root, encoding: 'utf8' });

afterEach(() => {
  for (const root of fixtures.splice(0)) {
    // Only directories returned by mkdtemp above are eligible for fixture cleanup.
    rmSync(root, { recursive: true, force: true });
  }
});

describe('i18n generator formatter launcher', () => {
  it('runs the installed JavaScript entry with Node and preserves paths containing spaces', () => {
    const input = fixture(String.raw`const fs = require('node:fs');
const path = require('node:path');
fs.writeFileSync(path.join(process.cwd(), 'formatter-invocation.json'), JSON.stringify({ node: process.execPath, args: process.argv.slice(2) }));
fs.appendFileSync(process.argv[2], '\n// formatted by isolated fixture\n');
`);
    const result = run(input);
    expect(result.error).toBeUndefined();
    expect(result.status).toBe(0);
    expect(JSON.parse(readFileSync(input.marker, 'utf8'))).toEqual({ node: process.execPath, args: [input.output] });
    const output = readFileSync(input.output, 'utf8');
    expect(output).toContain("'example.greeting'");
    expect(output).toContain("'example.nested.message'");
    expect(output).toContain('// formatted by isolated fixture');
  });

  it('fails clearly when the installed formatter entry is missing', () => {
    const input = fixture();
    const result = run(input);
    expect(result.error).toBeUndefined();
    expect(result.status).not.toBe(0);
    expect(result.stderr).toContain('Required formatter is missing:');
    expect(result.stderr).toContain(input.entry);
    expect(existsSync(input.marker)).toBe(false);
  });

  it('propagates a formatter failure instead of reporting generated output as successful', () => {
    const input = fixture(String.raw`process.stderr.write('isolated formatter refused output\n'); process.exit(23);`);
    const result = run(input);
    expect(result.error).toBeUndefined();
    expect(result.status).not.toBe(0);
    expect(result.stderr).toContain('isolated formatter refused output');
    expect(result.stdout).not.toContain('i18n key types generated:');
  });
});

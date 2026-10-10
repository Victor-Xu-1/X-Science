import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

const rendererRoot = path.resolve(import.meta.dirname, '../../../packages/desktop/src/renderer');

describe('CodeMirror language catalog boundary', () => {
  it('loads @codemirror/language-data only when resolving a language', () => {
    const source = readFileSync(path.join(rendererRoot, 'pages/conversation/Preview/theme/languageLoader.ts'), 'utf8');

    expect(source).not.toMatch(/^import .*from ['"]@codemirror\/language-data['"]/m);
    expect(source).toContain("import('@codemirror/language-data')");
  });
});

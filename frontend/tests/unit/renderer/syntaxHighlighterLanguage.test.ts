import { describe, expect, it } from 'vitest';
import { normalizeSyntaxHighlighterLanguage } from '@/renderer/components/Markdown/syntaxHighlighterLanguage';

describe('normalizeSyntaxHighlighterLanguage', () => {
  it('keeps LightAsync loader keys and common fence aliases', () => {
    expect(normalizeSyntaxHighlighterLanguage('python')).toBe('python');
    expect(normalizeSyntaxHighlighterLanguage('Python')).toBe('python');
    expect(normalizeSyntaxHighlighterLanguage('py')).toBe('python');
    expect(normalizeSyntaxHighlighterLanguage('js')).toBe('javascript');
    expect(normalizeSyntaxHighlighterLanguage('jsx')).toBe('javascript');
    expect(normalizeSyntaxHighlighterLanguage('ts')).toBe('typescript');
    expect(normalizeSyntaxHighlighterLanguage('tsx')).toBe('typescript');
    expect(normalizeSyntaxHighlighterLanguage('sh')).toBe('bash');
    expect(normalizeSyntaxHighlighterLanguage('yml')).toBe('yaml');
    expect(normalizeSyntaxHighlighterLanguage('html')).toBe('xml');
    expect(normalizeSyntaxHighlighterLanguage('c++')).toBe('cpp');
    expect(normalizeSyntaxHighlighterLanguage('diff')).toBe('diff');
    expect(normalizeSyntaxHighlighterLanguage('r')).toBe('r');
    expect(normalizeSyntaxHighlighterLanguage('matlab')).toBe('matlab');
    expect(normalizeSyntaxHighlighterLanguage('julia')).toBe('julia');
  });

  it('maps hyphenated highlight.js names onto LightAsync camelCase loaders', () => {
    expect(normalizeSyntaxHighlighterLanguage('python-repl')).toBe('pythonRepl');
    expect(normalizeSyntaxHighlighterLanguage('1c')).toBe('oneC');
  });

  it('leaves the reserved text language unhighlighted', () => {
    expect(normalizeSyntaxHighlighterLanguage('text')).toBe('text');
    expect(normalizeSyntaxHighlighterLanguage(' TEXT ')).toBe('text');
    expect(normalizeSyntaxHighlighterLanguage('')).toBe('text');
  });
});

import { readFileSync } from 'node:fs';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const styles = postcss.parse(
  readFileSync(new URL('../../../packages/desktop/src/renderer/styles/workspace-theme.css', import.meta.url), 'utf8')
);

function outlineRules(fragment: string) {
  const matches: { selector: string; outline: string }[] = [];
  styles.walkRules((rule) => {
    if (!rule.selector.includes(fragment)) return;
    rule.walkDecls('outline', (declaration) => matches.push({ selector: rule.selector, outline: declaration.value }));
  });
  return matches;
}

describe('Conversation search composite keyboard indicator', () => {
  it('keeps the existing high-contrast indicator on the enclosing search surface', () => {
    expect(outlineRules('.conversation-search-modal__searchbar:has(input:focus-visible)')).toEqual([
      expect.objectContaining({ outline: '2px solid var(--workspace-text)' }),
    ]);
  });

  it('excludes only the inner search field without changing generic ring specificity', () => {
    const rules = outlineRules(':focus-visible:where(:not(.conversation-search-modal__searchbar input))');
    expect(rules).toHaveLength(1);
    expect(rules[0]).toEqual(expect.objectContaining({ outline: '2px solid var(--workspace-text)' }));
    expect(rules[0].selector).toContain("input:not([type='hidden'])");
    expect(rules[0].selector).toContain('textarea');
    expect(rules[0].selector).toContain('select');
  });
});

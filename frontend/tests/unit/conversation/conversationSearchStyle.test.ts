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

describe('Composite search keyboard indicators', () => {
  it('uses the shared thin indicator on the enclosing search surface', () => {
    expect(outlineRules('.conversation-search-modal__searchbar:has(input:focus-visible)')).toEqual([
      expect.objectContaining({ outline: 'var(--ui-focus-width) solid var(--workspace-focus-ring)' }),
    ]);
    expect(outlineRules('.project-command-palette .arco-input-inner-wrapper:has(input:focus-visible)')).toEqual([
      expect.objectContaining({ outline: 'var(--ui-focus-width) solid var(--workspace-focus-ring)' }),
    ]);
  });

  it('excludes only the inner search field without changing generic ring specificity', () => {
    const rules = outlineRules(':focus-visible:where(');
    expect(rules).toHaveLength(1);
    expect(rules[0]).toEqual(
      expect.objectContaining({ outline: 'var(--ui-focus-width) solid var(--workspace-focus-ring)' })
    );
    expect(rules[0].selector).toContain("input:not([type='hidden'])");
    expect(rules[0].selector).toContain('textarea');
    expect(rules[0].selector).toContain('select');
    expect(rules[0].selector).toContain('.conversation-search-modal__searchbar input');
    expect(rules[0].selector).toContain('.project-command-palette .arco-input-inner-wrapper input');
    expect(rules[0].selector).toContain('.guid-input-card-shell textarea');
    expect(rules[0].selector).toContain('.sendbox-panel textarea');
    expect(rules[0].selector).toContain('.settings-library-search input');
    expect(rules[0].selector).toContain('.environment-search input');
  });

  it('does not flatten semantic options with the ordinary dialog-row reset', () => {
    const resets: string[] = [];
    styles.walkRules((rule) => {
      if (!rule.selector.includes('.arco-modal button.text-left')) return;
      rule.walkDecls('background', (declaration) => {
        if (declaration.value === 'transparent') resets.push(rule.selector);
      });
    });
    expect(resets).toHaveLength(1);
    expect(resets[0]).toContain(".arco-modal button.text-left:where(:not([role='option']))");
    const selection: string[] = [];
    styles.walkRules((rule) => {
      if (!rule.selector.includes("[role='listbox'] [aria-selected='true']")) return;
      rule.walkDecls('background', (declaration) => selection.push(declaration.value));
    });
    expect(selection).toContain('var(--workspace-overlay-selected)');
  });
});

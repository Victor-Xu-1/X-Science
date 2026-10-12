import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath, URL as NodeURL } from 'node:url';
import postcss, { type Rule } from 'postcss';
import { afterEach, describe, expect, it } from 'vitest';

const rendererRoot = fileURLToPath(new NodeURL('../../packages/desktop/src/renderer/', import.meta.url));
const read = (relative: string) => readFileSync(path.join(rendererRoot, relative), 'utf8');
const shell = postcss.parse(read('styles/workspace-theme.css'));
const tokens = postcss.parse(read('styles/tokens.css'));
const sharedOutline = 'var(--ui-focus-width) solid var(--workspace-focus-ring)';
const declarations = (rule: Rule) =>
  Object.fromEntries(rule.nodes.filter((node) => node.type === 'decl').map((node) => [node.prop, node.value]));
const rules: Rule[] = [];
shell.walkRules((rule) => {
  rules.push(rule);
});

// Replace only the keyboard-state pseudo-class, not selector structure or
// specificity. This checks owner/descendant exclusions independently of a
// browser's input-modality heuristics; rendered focus is checked separately.
const keyboardSelector = (selector: string) => selector.replaceAll(':focus-visible', '[data-keyboard-focus]');
const outlinedRulesFor = (element: Element) =>
  rules.filter((rule) => {
    const outline = declarations(rule).outline;
    return outline && !['0', 'none'].includes(outline) && element.matches(keyboardSelector(rule.selector));
  });
const cssFiles = (directory: string): string[] =>
  readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(directory, entry.name);
    if (entry.isDirectory()) return cssFiles(target);
    return entry.name.endsWith('.css') ? [target] : [];
  });
const palettes = ['warm', 'cool', 'white'].map((family) =>
  postcss.parse(read(`pages/settings/AppearanceSettings/presets/${family}.css`))
);
const variables = (rule: Rule) => declarations(rule);
const luminance = (hex: string) => {
  const channels = [1, 3, 5].map((offset) => {
    const value = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return channels[0]! * 0.2126 + channels[1]! * 0.7152 + channels[2]! * 0.0722;
};
const contrast = (foreground: string, background: string) => {
  const values = [luminance(foreground), luminance(background)].toSorted((a, b) => b - a);
  return (values[0]! + 0.05) / (values[1]! + 0.05);
};

afterEach(() => {
  document.body.replaceChildren();
});

describe('lightweight, non-black keyboard focus', () => {
  it('uses one-pixel geometry and independent contrasting colors in every family', () => {
    let width: string | undefined;
    tokens.walkDecls('--ui-focus-width', (declaration) => {
      width = declaration.value;
    });
    expect(width).toBe('1px');
    const semanticPalettes = rules.filter(
      (rule) => rule.parent?.type === 'root' && declarations(rule)['--workspace-focus-ring']
    );
    expect(semanticPalettes).toHaveLength(2);
    const light = variables(semanticPalettes[0]!)['--workspace-focus-ring']!;
    const dark = variables(semanticPalettes[1]!)['--workspace-focus-ring']!;
    for (const color of [light, dark]) {
      expect(color).toMatch(/^#[\da-f]{6}$/i);
      const channels = [1, 3, 5].map((offset) => Number.parseInt(color.slice(offset, offset + 2), 16));
      expect(channels[2]! - channels[0]!).toBeGreaterThan(25);
    }
    for (const palette of palettes) {
      palette.walkRules((rule) => {
        const values = variables(rule);
        if (!values['--workspace-canvas']) return;
        const color = rule.selector.includes("[data-theme='dark']") ? dark : light;
        for (const surface of ['canvas', 'sidebar', 'subtle', 'hover', 'selected']) {
          expect(contrast(color, values[`--workspace-${surface}`]!), rule.selector).toBeGreaterThanOrEqual(3);
        }
        expect(values['--composer-shadow-active']).toBe('var(--composer-shadow)');
      });
    }
  });

  it('never uses text, neutral primary, brand, or literal black as a first-party focus frame', () => {
    const violations: string[] = [];
    const files = cssFiles(rendererRoot);
    expect(files.length).toBeGreaterThan(50);
    for (const file of files) {
      // The embedded native structure viewer has its own explicitly blue
      // toolkit indicators. Its scientific rendering styles are not changed.
      if (file.endsWith('XScienceStructureViewer.css')) continue;
      postcss.parse(readFileSync(file, 'utf8')).walkRules((rule) => {
        if (!rule.selector.includes(':focus')) return;
        rule.walkDecls((declaration) => {
          if (!/^(outline(?:-color)?|box-shadow)$/.test(declaration.prop)) return;
          if (
            /var\(--(?:[^)]*text[^)]*|[^)]*accent[^)]*|(?:color-)?primary[^)]*|brand[^)]*|workspace-focus)\)/i.test(
              declaration.value
            ) ||
            /\bblack\b|#(?:000(?:000)?|111(?:111)?|2b2a28|3b3a37)\b|rgba?\(\s*0[ ,]+0[ ,]+0\b/i.test(declaration.value)
          ) {
            violations.push(`${path.relative(rendererRoot, file)}:${declaration.source?.start?.line}`);
          }
        });
      });
    }
    expect(violations).toEqual([]);
  });

  it.each([
    ['guid-input-card-shell', '<textarea class="arco-textarea" data-keyboard-focus></textarea>'],
    ['sendbox-panel', '<textarea class="arco-textarea" data-keyboard-focus></textarea>'],
    ['conversation-search-modal__searchbar', '<input type="search" data-keyboard-focus>'],
    ['project-command-palette', '<div class="arco-input-inner-wrapper"><input data-keyboard-focus></div>'],
    ['settings-library-search', '<div class="arco-input-inner-wrapper"><input data-keyboard-focus></div>'],
    ['environment-search', '<input type="search" data-keyboard-focus>'],
    ['x-science-ask-user-card', '<input class="arco-input" data-keyboard-focus>'],
  ])('gives %s exactly one owner indicator, never a competing inner ring', (ownerClass, content) => {
    document.body.innerHTML =
      ownerClass === 'x-science-ask-user-card'
        ? `<div data-testid="x-science-ask-user-card">${content}</div>`
        : `<div class="${ownerClass}">${content}</div>`;
    const field = document.querySelector('input, textarea')!;
    const owner = document.querySelector('.arco-input-inner-wrapper') ?? field.parentElement!;
    expect(outlinedRulesFor(field)).toHaveLength(0);
    const matches = outlinedRulesFor(owner);
    expect(matches).toHaveLength(1);
    expect(declarations(matches[0]!).outline).toBe(sharedOutline);
    expect(matches[0]!.selector).toContain(':has(');
  });

  it('retains a visible standalone input indicator without lowering generic specificity', () => {
    document.body.innerHTML = '<input type="search" data-keyboard-focus>';
    const matches = outlinedRulesFor(document.querySelector('input')!);
    expect(matches).toHaveLength(1);
    expect(matches[0]!.selector).toContain("input:not([type='hidden']):not([type='checkbox'])");
    expect(matches[0]!.selector).toContain(':focus-visible:where(');
    expect(declarations(matches[0]!).outline).toBe(sharedOutline);
  });

  it('focuses a composite search clear action without also framing its inactive field', () => {
    document.body.innerHTML =
      '<div class="settings-library-search"><div class="arco-input-inner-wrapper"><input></div><button data-keyboard-focus>Clear</button></div>';
    const button = document.querySelector('button')!;
    expect(outlinedRulesFor(button)).toHaveLength(1);
    expect(outlinedRulesFor(document.querySelector('.arco-input-inner-wrapper')!)).toHaveLength(0);
    expect(outlinedRulesFor(document.querySelector('input')!)).toHaveLength(0);
  });

  it('does not outline programmatically focused dialog shells or suppress their actions', () => {
    const shellReset = rules.find(
      (rule) => rule.selector === "html body :is([role='dialog'], [role='alertdialog']):focus"
    );
    expect(shellReset).toBeDefined();
    expect(declarations(shellReset!).outline).toBe('none');
    document.body.innerHTML = '<div role="dialog" tabindex="-1"><button data-keyboard-focus>Close</button></div>';
    expect(outlinedRulesFor(document.querySelector('[role="dialog"]')!)).toHaveLength(0);
    expect(outlinedRulesFor(document.querySelector('button')!)).toHaveLength(1);
  });

  it('preserves system high-contrast focus instead of forcing the normal palette', () => {
    const systemFocus: Record<string, string> = {};
    shell.walkAtRules('media', (rule) => {
      if (rule.params !== '(forced-colors: active)') return;
      rule.walkDecls((declaration) => {
        systemFocus[declaration.prop] = declaration.value;
      });
    });
    expect(systemFocus).toEqual({ '--workspace-focus-ring': 'Highlight', '--ui-focus-width': '2px' });
    expect(read('styles/workspace-theme.css')).not.toContain('forced-color-adjust: none');
  });
});

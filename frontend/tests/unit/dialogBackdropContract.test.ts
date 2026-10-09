import { readFileSync } from 'node:fs';
import postcss, { type Rule } from 'postcss';
import { describe, expect, it } from 'vitest';

const rendererRoot = new URL('../../packages/desktop/src/renderer/', import.meta.url);
const read = (path: string) => readFileSync(new URL(path, rendererRoot), 'utf8');
const sheet = postcss.parse(read('styles/workspace-theme.css'));
const declarations = (rule: Rule) => {
  const values: Record<string, string> = {};
  rule.walkDecls((declaration) => {
    values[declaration.prop] = declaration.value;
  });
  return values;
};
const rulesFor = (selector: string) => {
  const rules: Rule[] = [];
  sheet.walkRules((rule) => {
    if (rule.selectors.includes(selector)) rules.push(rule);
  });
  return rules;
};

describe('all-dialog non-dimming backdrop contract', () => {
  it.each([
    'html body .arco-modal-mask',
    'html body .arco-drawer-mask',
    'html body .arco-image-preview-mask',
    'html body .accessible-content-dialog__overlay',
    'html body .accessible-action-dialog__overlay',
    'html body .workbench-modal-backdrop',
    'html body dialog::backdrop',
  ])('uses the same clear, lightweight backdrop for %s', (selector) => {
    const rules = rulesFor(selector);
    expect(rules).toHaveLength(1);
    expect(declarations(rules[0]!)).toMatchObject({
      background: 'var(--workspace-modal-backdrop)',
      'backdrop-filter': 'none',
      '-webkit-backdrop-filter': 'none',
    });
  });

  it('never darkens any light or dark family canvas', () => {
    const backdrops: number[] = [];
    sheet.walkDecls('--workspace-modal-backdrop', (declaration) => {
      const match = /^rgb\(255 255 255 \/ ([\d.]+)%\)$/.exec(declaration.value);
      expect(match, declaration.value).not.toBeNull();
      backdrops.push(Number(match![1]) / 100);
    });
    expect(backdrops).toHaveLength(2);
    for (const alpha of backdrops) {
      expect(alpha).toBeGreaterThan(0);
      expect(alpha).toBeLessThanOrEqual(0.12);
      for (const family of ['warm', 'cool', 'white']) {
        const palette = read(`pages/settings/AppearanceSettings/presets/${family}.css`);
        for (const [, hex] of palette.matchAll(/--workspace-canvas:\s*#([\da-f]{6});/gi)) {
          for (const offset of [0, 2, 4]) {
            const channel = Number.parseInt(hex!.slice(offset, offset + 2), 16);
            expect(channel * (1 - alpha) + 255 * alpha).toBeGreaterThanOrEqual(channel);
          }
        }
      }
    }
  });

  it.each([
    ['components/chat/BtwOverlay/BtwOverlay.module.css', '.backdrop'],
    ['components/chat/MobileActionSheet/MobileActionSheet.module.css', '.mask'],
  ])('removes the independent dark backdrop in %s', (path, selector) => {
    let background: string | undefined;
    postcss.parse(read(path)).walkRules(selector, (rule) => {
      background = declarations(rule).background;
    });
    expect(background).toBe('var(--workspace-modal-backdrop)');
  });

  it.each(['components/layout/Layout.tsx', 'pages/conversation/components/ChatLayout/MobileWorkspaceOverlay.tsx'])(
    'keeps mobile overlay hit targets without black utilities in %s',
    (path) => {
      expect(read(path).includes('workbench-modal-backdrop fixed inset-0')).toBe(true);
      expect(read(path).includes('bg-black/30')).toBe(false);
    }
  );

  it('keeps image-preview controls readable without a dark screen', () => {
    for (const selector of [
      'html body .arco-image-preview-close-btn',
      'html body .arco-image-preview-arrow-left',
      'html body .arco-image-preview-arrow-right',
      'html body .arco-image-preview-toolbar',
      'html body .arco-image-preview-scale-value',
      'html body .arco-image-preview-loading',
    ]) {
      const rules = rulesFor(selector);
      expect(rules).toHaveLength(1);
      expect(declarations(rules[0]!)).toMatchObject({
        color: 'var(--workspace-text)',
        background: 'var(--workspace-overlay-surface)',
      });
    }
    expect(read('styles/workspace-theme.css')).toContain('.arco-image-preview-arrow-disabled');
    expect(read('styles/workspace-theme.css')).toContain("[role='button']):focus-visible");
  });
});

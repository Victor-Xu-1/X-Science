import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { fileURLToPath } from 'node:url';
import { resolveDesignTokens } from './_helpers/designTokens';

/**
 * Guards the canonical design primitives in styles/tokens.css: one scale for
 * radius, weight and stacking, and no module re-stating raw literals.
 */
const rendererDir = fileURLToPath(new URL('../../packages/desktop/src/renderer/', import.meta.url));
const tokensCss = readFileSync(path.join(rendererDir, 'styles/tokens.css'), 'utf8');
const themeIndexCss = readFileSync(path.join(rendererDir, 'styles/themes/index.css'), 'utf8');
const shellCss = readFileSync(path.join(rendererDir, 'styles/workspace-theme.css'), 'utf8');

const collectCss = (dir: string): string[] =>
  readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const target = path.join(dir, entry.name);
    if (entry.isDirectory()) return entry.name === 'out' ? [] : collectCss(target);
    return entry.name.endsWith('.css') ? [target] : [];
  });

// Palette sources and the third-party structure widget own their own numbers.
const EXEMPT_FILE = [
  /styles[\\/]tokens\.css$/,
  /styles[\\/]themes[\\/]default-color-scheme\.css$/,
  /styles[\\/]markdown\.css$/,
  /AppearanceSettings[\\/]presets[\\/]/,
  /XScienceStructureViewer\.css$/,
];

const moduleCss = collectCss(rendererDir)
  .filter((file) => !EXEMPT_FILE.some((pattern) => pattern.test(file)))
  .map((file) => [path.relative(rendererDir, file), readFileSync(file, 'utf8')] as const);

// Ultra-small badge sizes are still pending migration.
const PENDING_FONT_SIZE = /font-size:\s*(?:8|9|10)px/;

describe('design primitive scale', () => {
  it('does not leak compact-settings tokens into root or default-settings consumers', () => {
    const probe =
      'font-size: var(--ui-font-title); line-height: var(--ui-line-height-section); radius: var(--settings-card-radius); title: var(--settings-page-title-size)/var(--settings-page-title-line);';
    expect(resolveDesignTokens(probe)).toBe(
      'font-size: 15px; line-height: 24px; radius: var(--settings-card-radius); title: var(--settings-page-title-size)/var(--settings-page-title-line);'
    );
    expect(resolveDesignTokens(probe, 'settings')).toBe(
      'font-size: 15px; line-height: 24px; radius: 16px; title: 26px/34px;'
    );
    expect(resolveDesignTokens(probe, 'settings-compact')).toBe(
      'font-size: 14px; line-height: 22px; radius: 12px; title: 22px/30px;'
    );
    // A compact resolution must not mutate later root resolutions.
    expect(resolveDesignTokens('font-size: var(--ui-font-title);')).toBe('font-size: 15px;');
  });

  it('preserves the scoped conversation menu and model geometry through canonical primitives', () => {
    const menu = resolveDesignTokens(
      readFileSync(path.join(rendererDir, 'components/media/conversationControlMenu.css'), 'utf8')
    );
    // OpenScience 6d869cd components/ui/dropdown-menu.tsx:44/64 uses 15px
    // surfaces and 8px rows; ConversationPanel.tsx:2377 pins Save to 13/500/20.
    expect(menu).toContain('border-radius: calc(16px - 1px) !important;');
    expect(menu).toMatch(
      /\.composer-control-menu__row\s*\{[^}]*min-height:\s*32px[^}]*padding:\s*6px 8px[^}]*gap:\s*8px[^}]*border-radius:\s*8px[^}]*font-size:\s*13px[^}]*font-weight:\s*500[^}]*line-height:\s*20px/s
    );
    expect(menu).toMatch(
      /\.conversation-skill-choice__heading\s*\{[^}]*font-size:\s*14px[^}]*font-weight:\s*500[^}]*line-height:\s*20px/s
    );
    const models = resolveDesignTokens(
      readFileSync(path.join(rendererDir, 'pages/settings/components/settings-models.css'), 'utf8'),
      'settings'
    );
    const editor = resolveDesignTokens(
      readFileSync(path.join(rendererDir, 'pages/settings/models/model-profile-editor.css'), 'utf8'),
      'settings'
    );
    expect(models).toContain('border-radius: calc(16px + 4px);');
    expect(editor).toContain('border-radius: calc(16px - 2px);');
    expect(editor).toContain('border-radius: calc(10px - 1px);');
    for (const file of [
      'components/media/conversationControlMenu.css',
      'pages/settings/components/settings-models.css',
      'pages/settings/models/model-profile-editor.css',
    ]) {
      expect(readFileSync(path.join(rendererDir, file), 'utf8')).not.toMatch(
        /border-radius:\s*\d+px\s*(?:!important)?;/
      );
    }
  });

  it('covers every module stylesheet in the renderer', () => {
    expect(moduleCss.length).toBeGreaterThan(50);
  });

  it('defines one complete primitive scale', () => {
    for (const step of [1, 2, 3, 4, 5, 6, 7, 8]) {
      expect(tokensCss).toMatch(new RegExp(`--ui-space-${step}:\\s*\\d+px;`));
    }
    for (const step of ['xs', 'sm', 'md', 'lg', 'xl', '2xl', '3xl', '4xl', 'pill', 'circle']) {
      expect(tokensCss).toMatch(new RegExp(`--ui-radius-${step}:`));
    }
    for (const step of [
      'micro',
      'meta',
      'body',
      'body-lg',
      'subtitle',
      'title',
      'section',
      'lead',
      'display',
      'document-title',
      'hero',
      'hero-lg',
      'headline',
      'headline-lg',
      'metric',
    ]) {
      expect(tokensCss).toMatch(new RegExp(`--ui-font-${step}:\\s*\\d+px;`));
      expect(tokensCss).toMatch(new RegExp(`--ui-line-height-${step}:\\s*\\d+px;`));
    }
    for (const weight of ['regular', 'medium', 'semibold', 'bold']) {
      expect(tokensCss).toMatch(new RegExp(`--ui-weight-${weight}:\\s*\\d+;`));
    }
    for (const layer of [
      'below',
      'base',
      'raise',
      'layer',
      'layer-top',
      'inline',
      'raised',
      'float',
      'pinned',
      'sticky',
      'header',
      'overlay',
      'modal',
      'modal-top',
      'toast',
      'top',
      'max',
    ]) {
      expect(tokensCss).toMatch(new RegExp(`--ui-z-${layer}:\\s*-?\\d+;`));
    }
    for (const size of ['sm', 'md', 'lg']) {
      expect(tokensCss).toMatch(new RegExp(`--ui-control-${size}:\\s*\\d+px;`));
    }
  });

  it('is loaded before the theme layers that consume it', () => {
    const imported = themeIndexCss.indexOf("'../tokens.css'");
    expect(imported).toBeGreaterThan(-1);
    expect(imported).toBeLessThan(themeIndexCss.indexOf("'./base.css'"));
  });

  it('routes the pre-existing font and radius tokens through the primitives', () => {
    expect(shellCss).toMatch(/--workspace-ui-font-size:\s*var\(--ui-font-body\)/);
    expect(shellCss).toMatch(/--workspace-meta-font-size:\s*var\(--ui-font-meta\)/);
    expect(shellCss).toMatch(/--conversation-font-size:\s*var\(--ui-font-title\)/);
    expect(shellCss).toMatch(/--conversation-ui-font-size:\s*var\(--ui-font-body\)/);
    expect(shellCss).toMatch(/--workspace-overlay-radius:\s*var\(--ui-radius-xl\)/);
    expect(shellCss).toMatch(/--workspace-overlay-radius-sm:\s*var\(--ui-radius-md\)/);
  });
});

describe.each(moduleCss)('%s uses the shared scale', (_name, css) => {
  it('does not append arithmetic to a token outside calc()', () => {
    expect(css).not.toMatch(/:\s*var\(--ui-[^)]+\)\s*[+-]\s*\d/);
  });

  it('declares radius through the radius scale', () => {
    for (const match of css.matchAll(/border-radius:\s*([0-9]+px);/g)) {
      throw new Error(`raw radius ${match[1]} - use a --ui-radius-* token`);
    }
  });

  it('declares weight through the weight scale', () => {
    for (const match of css.matchAll(/font-weight:\s*([0-9]+)\s*(!important)?;/g)) {
      throw new Error(`raw weight ${match[1]} - use a --ui-weight-* token`);
    }
  });

  it('declares stacking through the z scale', () => {
    for (const match of css.matchAll(/z-index:\s*(-?[0-9]+)\s*(!important)?;/g)) {
      throw new Error(`raw z-index ${match[1]} - use a --ui-z-* token`);
    }
  });

  it('declares type through the type scale', () => {
    for (const match of css.matchAll(/font-size:\s*([0-9]+px)\s*(!important)?;/g)) {
      if (PENDING_FONT_SIZE.test(match[0])) continue;
      throw new Error(`raw font-size ${match[1]} - use a --ui-font-* token`);
    }
  });
});

import { existsSync, readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  SETTINGS_LAYOUT_REVISION,
  SETTINGS_VISUAL_SYSTEM_ID,
  SETTINGS_VISUAL_CONTRACTS,
  SETTINGS_VISUAL_ROUTE_IDS,
  SETTINGS_DESKTOP_VIEWPORT,
  SETTINGS_MOBILE_VIEWPORT,
} from '@/renderer/pages/settings/components/settingsVisualContract';
import {
  SETTINGS_GENERATED_ASSET_REVISION,
  SETTINGS_GENERATED_ARTWORK_SRC,
  SETTINGS_GENERATED_EMPTY_SRC,
  SETTINGS_GENERATED_ICON_SRC,
  SETTINGS_GENERATED_NAV_SRC,
  settingsGeneratedWebpSrc,
} from '@/renderer/pages/settings/components/SettingsGeneratedAsset';

// These are source/contract guards. Actual geometry, focus, contrast and visual
// review also require the browser; regexes are never a visual acceptance score.
const root = new URL('../../../packages/desktop/src/renderer/pages/settings/', import.meta.url);
const read = (name: string) => readFileSync(new URL(name, root), 'utf8');
const css = (name: string) => read(`components/${name}.css`);
const layout = css('settings-layout');
const core = css('settings-core');
const cards = css('settings-card-density');
const surfaces = css('settings-card-surfaces');
const manifest = css('settings');

describe('intrinsic settings presentation contract', () => {
  it('lets current-model summary values use the available space without truncating identity', () => {
    expect(read('XScienceModelsSettings.tsx')).not.toContain("font-600 truncate'>{value}");
    expect(css('settings-skills')).not.toMatch(/\.settings-summary-item strong\s*\{[^}]*max-width:\s*70%/);
  });
  it('lets GPU and job sections grow naturally rather than reserve fixed screenshot heights', () => {
    expect(css('settings-compute-components')).not.toMatch(/height:\s*(?:200|206|352)px/);
    expect(css('settings-compute-components')).not.toMatch(/\.compute-section\s*\{[^}]*overflow:\s*hidden/);
  });
  it('leaves the main landmark to the workspace instead of nesting it in content modules', () => {
    expect(read('ComputeSettings.tsx')).not.toMatch(/<main\b/);
    for (const relative of [
      '../artifact/ArtifactPreview.tsx',
      '../conversation/Preview/components/viewers/XScienceHdf5Viewer.tsx',
    ])
      expect(read(relative)).not.toMatch(/<main\b/);
  });
  it('covers all thirteen shipped settings routes with honest reference and state coverage', () => {
    expect(SETTINGS_VISUAL_SYSTEM_ID).toBe('scientific-connectors-v3');
    expect(SETTINGS_LAYOUT_REVISION).toBe('intrinsic-responsive-v1');
    expect(SETTINGS_VISUAL_ROUTE_IDS).toHaveLength(13);
    expect(new Set(SETTINGS_VISUAL_ROUTE_IDS).size).toBe(13);
    for (const route of SETTINGS_VISUAL_ROUTE_IDS) {
      const entry = SETTINGS_VISUAL_CONTRACTS[route];
      expect(entry.route).toBe(route);
      expect(entry.reference).toEqual({ desktop: null, mobile: null });
      expect(entry.rootTestId).toBeTruthy();
      expect(entry.primarySelectors.length).toBeGreaterThan(0);
      expect(entry.interactionSelectors.length).toBeGreaterThan(0);
      expect(entry.dynamicStates.length).toBeGreaterThan(0);
    }
    expect(SETTINGS_DESKTOP_VIEWPORT).toEqual({ width: 1536, height: 1024 });
    expect(SETTINGS_MOBILE_VIEWPORT).toEqual({ width: 390, height: 844 });
  });

  it('loads each visual module once and retires competing screenshot geometry', () => {
    const imports = [...manifest.matchAll(/@import\s+'([^']+)'\s*;/g)].map((match) => match[1]);
    expect(new Set(imports).size).toBe(imports.length);
    for (const retired of ['settings-compact-desktop', 'settings-reference-rails']) {
      expect(manifest).not.toContain(retired);
      expect(existsSync(new URL(`components/${retired}.css`, root))).toBe(false);
    }
    const activeStyles = imports.map((name) => read(`components/${name}`)).join('\n');
    expect(activeStyles).not.toMatch(/transform:\s*scale\(0\.(?:68|72)\)/);
    expect(activeStyles).not.toMatch(/width:\s*(?:138\.889|147\.059)%/);
    expect(imports.indexOf('./settings-connectors.css')).toBeLessThan(imports.indexOf('./settings-card-surfaces.css'));
  });

  it('keeps route loading/stale-content guards and a single centered page rail', () => {
    const wrapper = read('components/SettingsPageWrapper.tsx');
    expect(read('SettingsRoute.tsx')).toContain("import './components/settings.css'");
    expect(wrapper).toContain('data-settings-route={contentRoute}');
    expect(wrapper).toContain('data-settings-layout-revision={SETTINGS_LAYOUT_REVISION}');
    expect(wrapper).toContain('settings-page-wrapper--transitioning');
    expect(layout).toMatch(/--settings-content-max:\s*1280px/);
    expect(layout).toMatch(/max-width:\s*min\(100%,\s*var\(--settings-content-max\)\)/);
    expect(layout).toMatch(/margin-inline:\s*auto/);
    expect(layout).toMatch(/overflow-y:\s*auto/);
    expect(layout).not.toContain('x-science-brand img');
  });

  it('takes palette, control boundaries and surface elevation from the active theme', () => {
    for (const token of ['accent', 'accent-contrast', 'canvas', 'border', 'text', 'text-secondary']) {
      expect(layout).toContain(`var(--workspace-${token})`);
    }
    expect(core).toContain('--settings-control-border: color-mix(');
    expect(core).toContain('border: 1px solid var(--settings-control-border)');
    expect(cards).toMatch(/\.settings-library-filter-select\s*\{[^}]*var\(--settings-control-border\)/);
    expect(core).not.toContain('prefers-color-scheme');
    expect(surfaces).toContain('var(--ui-shadow-sm)');
    expect(surfaces).toContain('var(--settings-surface)');
    expect(surfaces).not.toContain('linear-gradient');
    expect(read('MessageChannelsSettings.css')).toMatch(/qr-frame[\s\S]*?background:\s*#fff/);
  });

  it('gives four catalogs one responsive grid and readable, growing card anatomy', () => {
    expect(cards).toContain('repeat(auto-fill, minmax(min(100%, 260px), 1fr))');
    expect(cards).toContain('grid-auto-rows: auto');
    expect(cards).toMatch(/\.settings-library-card\s*\{[^}]*height:\s*auto/);
    expect(cards).toMatch(/\.settings-library-card__meta\s*\{[^}]*flex-direction:\s*column/);
    expect(cards).toMatch(/\.settings-library-card__title\s*\{[^}]*overflow-wrap:\s*anywhere/);
    expect(cards).not.toMatch(/\.settings-library-card__meta\s*\{[^}]*white-space:\s*nowrap/);
    expect(cards).toMatch(/\.environment-grid:has\(details\[open\]\)\s*\{[^}]*align-items:\s*start/);
    expect(cards).toMatch(
      /\.settings-library-tab-header \+ \.environment-library\s*\{[^}]*margin-top:\s*var\(--ui-space-6\)/
    );
    expect(cards).toMatch(/settings-library-card__control > :is\(button, \.arco-btn\)[^}]*min-height:\s*32px/);
    for (const route of ['experts', 'skills', 'tools', 'environments'] as const) {
      expect(SETTINGS_VISUAL_CONTRACTS[route].grid).toEqual({
        desktopColumns: 4,
        mobileColumns: 1,
        cardMinHeight: 192,
      });
    }
    for (const source of [
      'XScienceExpertsSettings/ExpertCatalogGroup.tsx',
      'skills/SkillLibraryCard.tsx',
      'ToolsSettings/McpConnectorCard.tsx',
      'environments/EnvironmentCard.tsx',
    ]) {
      // Guard the common anatomy without pinning a screenshot-specific height.
      const file = read(source);
      for (const part of ['settings-library-card', '__heading', '__description', '__footer', '__control']) {
        expect(file, `${source} ${part}`).toContain(part);
      }
    }
    const expertRoute = read('XScienceExpertsSettings/ExpertWorkbench.tsx');
    expect(expertRoute).toContain("import ExpertCatalogGroup from './ExpertCatalogGroup'");
    expect(expertRoute).toContain('<ExpertCatalogGroup');
  });

  it('preserves hover/focus affordances and reduced motion without permanent GPU layers', () => {
    expect(surfaces).toContain('.settings-library-card:focus-visible');
    expect(surfaces).toContain(':has(> .settings-skill-card__open:focus-visible)');
    expect(surfaces).toMatch(/\.settings-skill-card__open:focus-visible\s*\{[^}]*outline:\s*none !important/);
    expect(surfaces).not.toContain('.settings-library-card:focus-within');
    expect(surfaces).toContain('outline: var(--ui-focus-width) solid var(--workspace-focus-ring)');
    expect(surfaces).toContain('@media (hover: hover) and (pointer: fine)');
    expect(surfaces).toContain('transform: translateY(-1px)');
    expect(surfaces).toContain('@media (prefers-reduced-motion: reduce)');
    expect(surfaces).toContain('transform: none');
    expect(surfaces).not.toContain('backface-visibility');
    expect(surfaces).not.toContain('translate3d');
  });

  it('keeps natural route-specific content and discoverable disclosures', () => {
    expect(css('settings-models')).toContain('@container (max-width: 800px)');
    expect(css('settings-models')).not.toContain('195px 132px 178px 96px');
    expect(css('settings-network')).not.toMatch(/(?:max-)?height:\s*(?:340|420)px/);
    expect(css('settings-network')).toContain('network-group-row__chevron');
    expect(css('settings-general')).not.toMatch(/width:\s*1176px/);
    expect(css('settings-compute')).not.toContain('position: absolute');
    expect(read('account/AccountInsightsPanels.css')).toMatch(/\.account-insight-panel\s*\{[^}]*height:\s*auto/);
    expect(read('account/AccountMetricsStrip.css')).toContain('repeat(5, minmax(0, 1fr))');
    expect(read('account/AccountMetricsStrip.css')).toContain('@container (max-width: 840px)');
    expect(read('account/AccountMetricsStrip.css')).toContain('@container (max-width: 440px)');
    expect(read('account/AccountMetricsStrip.css')).not.toContain('text-overflow: ellipsis');
    expect(css('settings-governance')).toMatch(/\.memory-manager__workspace\s*\{[^}]*height:\s*auto/);
    const environment = read('environments/EnvironmentCard.tsx');
    expect(environment).not.toContain('title={inventory');
    expect(environment).toContain("<details className='environment-inventory'");
    expect(environment).toContain('aria-labelledby={titleId}');
  });

  it('keeps real connected/attention colours distinct and draft billing explicit', () => {
    const connectors = css('settings-connectors');
    expect(connectors).toMatch(/data-state='connected'[\s\S]*?var\(--settings-status-ready\)/);
    expect(connectors).toMatch(/data-state='attention'[\s\S]*?var\(--settings-status-warning\)/);
    const billing = read('PlansUsageSettings.tsx');
    expect(billing.indexOf("data-testid='billing-draft-notice'")).toBeLessThan(
      billing.indexOf("className='plans-usage-balance-content")
    );
    expect(billing).toContain('getXScienceBillingOverview()');
    expect(billing).toContain("t('settings.plansUsageSettings.draftExplanation')");
    expect(read('AccountSettings.tsx')).not.toContain('AccountManagementWorkbench');
  });
});

describe('active artwork and reference retirement', () => {
  it('does not publish retired reference sheets or retain a competing reference map', () => {
    expect(existsSync(new URL('../../../public/branding/settings-generated-v3/references', import.meta.url))).toBe(
      false
    );
    expect(read('components/settingsVisualContract.ts')).not.toContain('SETTINGS_LOCKED_DESKTOP_REFERENCE_SRC');
    for (const entry of Object.values(SETTINGS_VISUAL_CONTRACTS)) {
      expect(entry.reference).toEqual({ desktop: null, mobile: null });
    }
  });
  it('keeps versioned real artwork/icon assets available', () => {
    expect(SETTINGS_GENERATED_ASSET_REVISION).toMatch(/^\d{8}[a-z]$/);
    for (const source of [
      ...Object.values(SETTINGS_GENERATED_ICON_SRC),
      ...Object.values(SETTINGS_GENERATED_ARTWORK_SRC),
      ...Object.values(SETTINGS_GENERATED_EMPTY_SRC),
      ...Object.values(SETTINGS_GENERATED_NAV_SRC),
    ]) {
      expect(source).toContain(`?v=${SETTINGS_GENERATED_ASSET_REVISION}`);
      const relative = source.split('?')[0].replace(/^\.?\//, '');
      expect(existsSync(new URL(`../../../public/${relative}`, import.meta.url)), source).toBe(true);
    }
  });
  it('keeps lossless WebP siblings for every live generated asset', () => {
    const sources = [
      ...Object.values(SETTINGS_GENERATED_ICON_SRC),
      ...Object.values(SETTINGS_GENERATED_ARTWORK_SRC),
      ...Object.values(SETTINGS_GENERATED_EMPTY_SRC),
      ...Object.values(SETTINGS_GENERATED_NAV_SRC),
    ];
    for (const source of sources) {
      const webpSource = settingsGeneratedWebpSrc(source);
      expect(webpSource).toContain('.webp?');
      const pngRelative = source.split('?')[0].replace(/^\.?\//, '');
      const webpRelative = webpSource.split('?')[0].replace(/^\.?\//, '');
      const png = new URL(`../../../public/${pngRelative}`, import.meta.url);
      const webp = new URL(`../../../public/${webpRelative}`, import.meta.url);
      expect(existsSync(webp), webpSource).toBe(true);
      expect(readFileSync(webp).byteLength).toBeLessThan(readFileSync(png).byteLength);
    }
  });

  it('keeps generated picture as the containing-block/flex box without dropping PNG fallbacks', () => {
    const asset = read('components/SettingsGeneratedAsset.tsx');
    expect(asset).toContain('className={className ??');
    expect(asset).toContain('settings-generated-picture--passthrough');
    expect(asset).toMatch(/<img\s+src=\{src\}/);
    expect(asset).not.toMatch(/<img\b[^>]*className=\{className\}/);
    expect(layout).toMatch(/\.settings-generated-picture--passthrough\s*\{\s*display:\s*contents;/);
    expect(core).toMatch(
      /picture:not\(\.settings-generated-picture--passthrough\)\s*>\s*img\[data-settings-generated-asset\]/
    );
    expect(css('settings-experts')).toContain('.expert-card > :not(.expert-card__artwork)');
    expect(css('settings-route-layouts')).toContain('.network-section--allowed > :not(.network-section__artwork)');
    expect(core).toContain('flex: 0 0 36px');
  });
});

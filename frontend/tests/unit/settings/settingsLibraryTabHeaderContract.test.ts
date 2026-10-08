import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const root = new URL('../../../packages/desktop/src/renderer/pages/settings/', import.meta.url);
const read = (name: string) => readFileSync(new URL(name, root), 'utf8');
const catalogs = {
  experts: read('SynonBiomedExpertsSettings/ExpertWorkbench.tsx'),
  skills: read('SynonBiomedSkillsSettings.tsx'),
  tools: read('ToolsSettings/McpLibraryToolbar.tsx'),
  environments: read('ScientificEnvironmentSettings.tsx'),
};

describe('shared catalog discovery contract', () => {
  it.each(Object.entries(catalogs))(
    '%s exposes one category filter and its existing search authority',
    (_name, source) => {
      expect(source).toContain('<SettingsLibraryTabHeader');
      expect(source.split('<SettingsLibraryFilterSelect')).toHaveLength(2);
      expect(source).toContain('<SettingsLibrarySearch');
      expect(source).not.toContain('<SettingsLibraryFilterToggle');
    }
  );
  it('uses a single accessible clearable search primitive', () => {
    const search = read('components/SettingsLibrarySearch.tsx');
    expect(search).toContain("type='search'");
    expect(search).toContain('aria-label={label}');
    expect(search).toContain("t('common.clear')");
    expect(search).toContain("event.key !== 'Escape'");
    expect(search).toContain("querySelector('input')?.focus()");
    expect(search).toContain('onChange={onChange}');
    expect(search).not.toContain('useState');
  });
  it('announces actual visible counts and retains one search/filter/action header', () => {
    expect(read('components/SettingsLibraryTabHeader.tsx')).toContain("aria-live='polite'");
    expect(catalogs.skills).toContain('count={filteredSkills.length + filteredDrafts.length}');
    expect(catalogs.experts).toContain('count={visibleProfiles.length}');
    expect(catalogs.environments).toContain('count={filtered.length}');
    expect(catalogs.tools).toContain('count={props.visibleCount}');
  });
  it('uses real navigation links for independent catalog URLs, not incomplete ARIA tabs', () => {
    const source = read('LibrarySettingsPage.tsx');
    expect(source).toContain('<nav');
    expect(source).toContain('href={`#/settings/${tab.key}`}');
    expect(source).toContain("aria-current={isActive ? 'page' : undefined}");
    expect(source).not.toContain("role='tablist'");
    expect(source).toContain('event.ctrlKey');
  });
  it('keeps small-rail controls able to wrap instead of clipping', () => {
    const css = read('components/settings-card-density.css');
    expect(css).toContain('flex-wrap: wrap');
    expect(css).toContain('@container (max-width: 640px)');
    expect(css).toContain('max-width: 100%');
  });
});

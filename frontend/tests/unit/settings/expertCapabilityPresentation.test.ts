import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const renderer = new URL('../../../packages/desktop/src/renderer/', import.meta.url);

describe('expert capability reading presentation', () => {
  it('gives selected lists a bounded native scroll surface without truncating labels', () => {
    const css = readFileSync(new URL('pages/settings/components/settings-experts.css', renderer), 'utf8');
    expect(css).toContain('.expert-capability-picker__viewport');
    expect(css).toMatch(/max-height:\s*min\(/);
    expect(css).toContain('overflow-y: auto');
    expect(css).toContain('overflow-wrap: anywhere');
    expect(css).not.toContain('text-overflow: ellipsis');
  });

  it('keeps search focus on one whole input surface and the clear button independent', () => {
    const css = readFileSync(new URL('styles/workspace-theme.css', renderer), 'utf8');
    expect(css).toContain('.settings-library-search .arco-input-inner-wrapper:has(input:focus-visible)');
    expect(css).toContain('.settings-library-search :is(.arco-input-group-wrapper, .arco-input-group)');
    expect(css).toContain('.settings-library-search input:focus-visible');
    expect(css).not.toContain('.settings-library-search *:focus-visible');
  });

  it('excludes library search inputs from the stronger generic control-focus rule', () => {
    const css = readFileSync(new URL('styles/workspace-theme.css', renderer), 'utf8');
    const inputRuleExclusions = css.match(/:focus-visible:where\(\s*:not\(([\s\S]*?)\)\s*\)/)?.[1];
    expect(inputRuleExclusions).toContain('.settings-library-search input');
  });
});

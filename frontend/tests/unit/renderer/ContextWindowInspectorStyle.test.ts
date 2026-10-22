import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe('Context inspector style contract, not rendered layout acceptance', () => {
  it('provides matching English and Chinese keys through the existing conversation resource', () => {
    const read = (language: string) =>
      JSON.parse(
        readFileSync(
          resolve(process.cwd(), `packages/desktop/src/renderer/services/i18n/locales/${language}/conversation.json`),
          'utf8'
        )
      ).contextInspector;
    const english = read('en-US');
    const chinese = read('zh-CN');
    expect(Object.keys(english).sort()).toEqual(Object.keys(chinese).sort());
    expect(Object.values(english).every((value) => typeof value === 'string' && value.length > 0)).toBe(true);
    expect(Object.values(chinese).every((value) => typeof value === 'string' && value.length > 0)).toBe(true);
  });
  it('bounds the viewport with scroll surfaces and retains a transparent overlay', () => {
    const css = readFileSync(
      resolve(
        process.cwd(),
        'packages/desktop/src/renderer/components/xScience/runtime/ContextWindowInspector.module.css'
      ),
      'utf8'
    );
    const overlay = css.match(/\.overlay\s*\{([^}]+)\}/s)?.[1];
    expect(overlay).toMatch(/background:\s*transparent/);
    expect(css).toMatch(/max-height:\s*min\(820px,\s*calc\(100dvh - 24px\)\)/);
    expect(css).toMatch(/overflow-y:\s*auto/);
    expect(css).toMatch(/overflow-x:\s*auto/);
  });
});

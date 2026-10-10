import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

describe.each(['en-US', 'zh-CN'])('interaction diagram state copy in %s', (locale) => {
  const copy = JSON.parse(
    readFileSync(
      resolve(process.cwd(), `packages/desktop/src/renderer/services/i18n/locales/${locale}/preview.json`),
      'utf8'
    )
  ).scientific.structure.quickActions;

  it.each([
    'interactionDiagramDescription',
    'interactionDiagramUnavailable',
    'loadingInteractionDiagram',
    'downloadInteractionDiagram',
    'downloadInteractionDiagramPng',
  ])('keeps %s free of stale branding or publication claims', (key) => {
    expect(copy[key]).toEqual(expect.any(String));
    expect(copy[key]).not.toMatch(/Synon|publication|出版级/i);
  });
});

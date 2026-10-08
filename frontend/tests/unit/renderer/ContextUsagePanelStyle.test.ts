/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { readFileSync } from 'node:fs';
import postcss from 'postcss';
import { describe, expect, it } from 'vitest';

const sheet = postcss.parse(
  readFileSync(
    new URL(
      '../../../packages/desktop/src/renderer/components/synonBiomed/runtime/ContextUsagePanel.module.css',
      import.meta.url
    ),
    'utf8'
  )
);
function declarations(selector: string) {
  const values: Record<string, string> = {};
  sheet.walkRules((rule) => {
    if (rule.selector === selector && rule.parent?.type !== 'atrule')
      rule.walkDecls((declaration) => {
        values[declaration.prop] = declaration.value;
      });
  });
  return values;
}
describe('Context card and retained-history control surfaces', () => {
  it('keeps the compact card and larger hit regions without enlarging its ring artwork', () => {
    expect(declarations('.root').width).toBe('min(260px, calc(100vw - 24px))');
    expect(declarations('.trigger')).toMatchObject({ 'min-width': '28px', 'min-height': '28px' });
    expect(declarations('.closeButton')).toMatchObject({ width: '28px', height: '28px' });
    expect(declarations('.header')).toMatchObject({ position: 'sticky', top: '0' });
  });
  it('does not shrink retained records to invisible columns or discard them for density', () => {
    expect(declarations('.historyPoint')).toMatchObject({ flex: '0 0 28px', width: '28px', height: '44px' });
    expect(declarations('.historyChart')).toMatchObject({ 'overflow-x': 'auto', 'overflow-y': 'hidden' });
  });
  it('keeps only the genuinely different narrow-viewport colour overrides', () => {
    const narrow: string[] = [];
    sheet.walkAtRules('media', (rule) => {
      if (rule.params.includes('520px')) rule.walkDecls((declaration) => narrow.push(declaration.prop));
    });
    expect(narrow).toEqual(['color']);
  });
});

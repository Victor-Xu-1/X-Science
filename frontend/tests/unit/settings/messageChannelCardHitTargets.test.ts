/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { readFile as readFileRaw } from 'node:fs/promises';
import { resolveDesignTokens } from '../_helpers/designTokens';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const stylesPath = fileURLToPath(
  new URL('../../../packages/desktop/src/renderer/pages/settings/MessageChannelsSettings.css', import.meta.url)
);

function ruleBody(styles: string, selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return styles.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`, 's'))?.[1] ?? '';
}

/**
 * Natural card flow replaces the former header/body/footer overlays. Protect
 * the actual cause of unreachable actions, not the retired pointer-events
 * workaround or screenshot-specific coordinates.
 */
describe('message channel card hit targets', () => {
  it('keeps the header and body in normal flow instead of overlapping the actions', async () => {
    const styles = resolveDesignTokens(await readFileRaw(stylesPath, 'utf8'));
    for (const name of ['header', 'body', 'footer']) {
      const rule = ruleBody(styles, `.message-channel-card__${name}`);
      expect(rule).toContain('display:');
      expect(rule).not.toContain('position: absolute;');
      expect(rule).not.toContain('pointer-events: none;');
    }
  });

  it('keeps footer actions wrapping and large enough without clipping their focus outline', async () => {
    const styles = resolveDesignTokens(await readFileRaw(stylesPath, 'utf8'));
    const footer = ruleBody(styles, '.message-channel-card__footer');
    expect(footer).toContain('flex-wrap: wrap;');
    expect(styles).toContain('min-height: 34px;');
    expect(ruleBody(styles, '.message-channel-card')).not.toContain('overflow: hidden;');
  });
});

/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const previewStylesPath = fileURLToPath(
  new URL(
    '../../../packages/desktop/src/renderer/pages/conversation/Preview/components/PreviewPanel/preview.css',
    import.meta.url
  )
);

describe('file board layout styles', () => {
  it('uses one adaptive grid with the selected column count as its maximum', async () => {
    const styles = await readFile(previewStylesPath, 'utf8');
    const boardGrid = styles.match(/^\.preview-board__grid\s*\{([^}]*)\}/ms)?.[1] ?? '';
    const boardColumnDeclarations = [...styles.matchAll(/grid-template-columns\s*:/g)];

    expect(boardGrid).toContain('auto-fit');
    expect(boardGrid).toContain('var(--preview-board-columns, 2)');
    expect(boardGrid).toContain('260px');
    expect(boardColumnDeclarations).toHaveLength(1);
  });

  it('keeps fullscreen above workbench chrome but below dialogs and their controls', async () => {
    const styles = await readFile(previewStylesPath, 'utf8');
    const tokens = await readFile(
      fileURLToPath(new URL('../../../packages/desktop/src/renderer/styles/tokens.css', import.meta.url)),
      'utf8'
    );
    const layer = styles.match(/\.preview-board__fullscreen-layer\s*\{[^}]*z-index:\s*var\((--ui-z-[a-z-]+)\)/s);
    expect(layer).not.toBeNull();
    const value = (name: string) => {
      const token = tokens.match(new RegExp(`${name}:\\s*(\\d+);`));
      expect(token, `declared layer ${name}`).not.toBeNull();
      return Number(token![1]);
    };
    expect(value(layer![1])).toBeGreaterThan(value('--ui-z-header'));
    expect(value(layer![1])).toBeLessThan(value('--ui-z-modal'));
  });
});

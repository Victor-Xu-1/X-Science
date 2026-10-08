import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const stylesPath = fileURLToPath(
  new URL('../../../packages/desktop/src/renderer/pages/artifact/ArchiveArtifactPreview.css', import.meta.url)
);

describe('archive detail layout contracts', () => {
  it('adapts to the owned container rather than scaling the page or depending on viewport width', async () => {
    const styles = await readFile(stylesPath, 'utf8');
    expect(styles).toContain('container-type: inline-size');
    expect(styles).toContain('@container (min-width: 720px)');
    expect(styles).not.toMatch(/transform:|\b(?:vw|vh)\b|@media/);
  });

  it('keeps both narrow stacked reading areas and wider side-by-side areas reachable', async () => {
    const styles = await readFile(stylesPath, 'utf8');
    expect(styles).toContain('grid-template-rows: minmax(180px, 0.6fr) minmax(280px, 1fr)');
    expect(styles).toContain('grid-template-columns: minmax(240px, 0.7fr) minmax(0, 1.3fr)');
    expect(styles.match(/\.artifact-archive\s*\{([^}]+)\}/)?.[1]).toContain('overflow: auto');
    expect(styles).toContain('min-width: 0');
    expect(styles).toContain('min-height: 0');
  });
});

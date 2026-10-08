import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it } from 'vitest';

const read = (path: string) => readFileSync(resolve(process.cwd(), path));

describe('X-Science current product identity', () => {
  it('keeps the product authority and public repository policy aligned', () => {
    const identity = JSON.parse(read('../product-identity.json').toString());
    const release = JSON.parse(read('../docs/governance/release-policy.json').toString());
    expect(identity.display_name).toBe('X-Science');
    expect(identity.machine_slug).toBe('x-science');
    expect(release.repository).toBe('Victor-Xu-1/X-Science');
  });

  it('ships the original approved mark and browser icons without pixel changes', () => {
    for (const [filename, sha256, size] of [
      ['x-science-mark.png', '627c51b57fe4f46f91a3d1effcd87d2698c006da82e8770ffee6404edaeb50fa', 660],
      ['x-science-favicon.png', '84d52d7f9db7c5b818b8090d9f6d4f12ac35fde064301c3a63910f6ed73bb14a', 32],
      ['x-science-touch-icon.png', '28a7436844353a1b2f728c2b46a6205ca1c73c0f5a5d1a9135181922ad79793c', 180],
    ] as const) {
      const png = read(`public/branding/${filename}`);
      expect(createHash('sha256').update(png).digest('hex')).toBe(sha256);
      expect(png.readUInt32BE(16)).toBe(size);
      expect(png.readUInt32BE(20)).toBe(size);
    }
  });

  it('uses the new identity in browser metadata and installable application icons', () => {
    const html = read('packages/desktop/src/renderer/index.html').toString();
    const manifest = JSON.parse(read('public/manifest.webmanifest').toString());
    expect(html).toContain('<title>X-Science</title>');
    expect(html).toContain('./branding/x-science-favicon.png');
    expect(html).toContain('./branding/x-science-touch-icon.png');
    expect(manifest.name).toBe('X-Science');
    expect(manifest.short_name).toBe('X-Science');
    expect(manifest.icons).toContainEqual(
      expect.objectContaining({
        src: './branding/x-science-mark.png',
        sizes: '660x660',
        type: 'image/png',
      })
    );
    expect(html).not.toMatch(/Synon[- ]Biomed|SYNON-Biomed/);
  });
});

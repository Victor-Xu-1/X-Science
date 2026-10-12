import { createHash } from 'node:crypto';
import { readFileSync, readdirSync } from 'node:fs';
import { resolve } from 'node:path';
import { describe, expect, it, vi } from 'vitest';

const read = (path: string) => readFileSync(resolve(process.cwd(), path));

const filesUnder = (directory: string): string[] =>
  readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = resolve(directory, entry.name);
    return entry.isDirectory() ? filesUnder(path) : [path];
  });

const displayValues = (value: unknown): string[] => {
  if (typeof value === 'string') return [value];
  if (Array.isArray(value)) return value.flatMap(displayValues);
  if (value && typeof value === 'object') return Object.values(value).flatMap(displayValues);
  return [];
};

describe('X-Science current product identity', () => {
  it('uses the machine identity only as the Node package-name fallback', async () => {
    const identity = JSON.parse(read('../product-identity.json').toString());
    try {
      for (const [contents, expectedName, expectedVersion] of [
        [null, identity.machine_slug, '0.0.0'],
        [{ version: '1.2.3' }, identity.machine_slug, '1.2.3'],
        [{ name: 'operator-package', version: '2.3.4' }, 'operator-package', '2.3.4'],
      ] as const) {
        vi.resetModules();
        vi.doMock('fs', () => ({
          readFileSync: () => {
            if (contents === null) throw new Error('owned missing package fixture');
            return JSON.stringify(contents);
          },
        }));
        const { NodePlatformServices } = await import('@/common/platform/NodePlatformServices');
        const { paths } = new NodePlatformServices();
        expect(paths.getName()).toBe(expectedName);
        expect(paths.getVersion()).toBe(expectedVersion);
      }
    } finally {
      vi.doUnmock('fs');
      vi.resetModules();
    }
  });

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

  it('keeps every shipped translation value on the current brand without changing technical keys', () => {
    const locales = resolve(process.cwd(), 'packages/desktop/src/renderer/services/i18n/locales');
    expect(readdirSync(locales)).toEqual(expect.arrayContaining(['en-US', 'zh-CN']));
    for (const file of filesUnder(locales).filter((path) => path.endsWith('.json'))) {
      const document = JSON.parse(readFileSync(file, 'utf8'));
      for (const value of displayValues(document))
        expect(value, file).not.toMatch(/\bsynon(?:[-_ ]?(?:biomed|ai))?\b/i);
    }
    for (const locale of ['en-US', 'zh-CN']) {
      const conversation = JSON.parse(readFileSync(resolve(locales, locale, 'conversation.json'), 'utf8'));
      expect(conversation.mcp.descriptions['synon-research']).toContain('X-Science');
      expect(conversation.synonRuntime).toBeDefined();
    }
  });

  it('keeps diagnostic display strings on the shared product identity', () => {
    const source = resolve(process.cwd(), 'packages/desktop/src');
    for (const file of filesUnder(source).filter((path) => /\.tsx?$/.test(path))) {
      const text = readFileSync(file, 'utf8');
      expect(text, file).not.toMatch(/['"`]\[Synon[^\r\n]*['"`]/);
      expect(text, file).not.toContain('Synon LLM Core');
    }
    const service = read('packages/desktop/src/renderer/services/synonBiomedLlm.ts').toString();
    expect(service).toContain("import { PRODUCT_NAME } from '@/common/config/productIdentity'");
    expect(service).toContain('${PRODUCT_NAME} LLM Core');
  });
});

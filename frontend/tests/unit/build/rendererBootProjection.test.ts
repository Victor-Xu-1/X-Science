import { describe, expect, it, vi } from 'vitest';

const identityProbe = vi.hoisted(() => ({ name: 'Changed product name' }));
vi.mock('node:fs', async (importOriginal) => {
  const original = await importOriginal<typeof import('node:fs')>();
  return {
    ...original,
    readFileSync: (pathname: Parameters<typeof original.readFileSync>[0], options?: unknown) => {
      const result = original.readFileSync(pathname, options as never);
      if (!String(pathname).endsWith('/product-identity.json')) return result;
      return JSON.stringify({ ...JSON.parse(String(result)), display_name: identityProbe.name });
    },
  };
});

import { readFileSync } from 'node:fs';
import path from 'node:path';
import viteConfig from '../../../vite.config';
import i18nConfig from '../../../packages/desktop/src/common/config/i18n-config.json';

describe('actual Vite boot identity and language projection', () => {
  it('reads the root authority instead of freezing the current product name in the boot label', async () => {
    const configuration =
      typeof viteConfig === 'function' ? await viteConfig({ command: 'build', mode: 'production' }) : viteConfig;
    const plugin = (configuration.plugins ?? [])
      .flat()
      .find(
        (candidate) =>
          candidate && typeof candidate === 'object' && 'name' in candidate && candidate.name === 'renderer-boot-shell'
      );
    if (!plugin || typeof plugin !== 'object' || !('transformIndexHtml' in plugin))
      throw new Error('Missing boot projection');
    const hook = plugin.transformIndexHtml;
    if (!hook || typeof hook !== 'object' || !('handler' in hook)) throw new Error('Missing boot handler');
    const html = readFileSync(path.resolve(process.cwd(), 'packages/desktop/src/renderer/index.html'), 'utf8');
    const projected = await hook.handler(html, {} as never);
    expect(typeof projected).toBe('string');
    expect(String(projected)).toContain(`lang="${i18nConfig.fallbackLanguage}"`);
    const payload = String(projected).match(
      /<script id="app-boot-config" type="application\/json">([^<]+)<\/script>/
    )?.[1];
    expect(payload).toBeDefined();
    expect(JSON.parse(payload!).productName).toBe(identityProbe.name);
    expect(String(projected).match(/data-app-boot-label>([^<]+)/)?.[1]).toContain(identityProbe.name);
    expect(String(projected)).not.toContain('__APP_BOOT_LOADING__');
  });
});

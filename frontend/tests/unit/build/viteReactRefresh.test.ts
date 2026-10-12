import { describe, expect, it } from 'vitest';
import viteConfig, { createRendererPlugins } from '../../../vite.config';

function pluginNames(value: unknown): string[] {
  if (Array.isArray(value)) return value.flatMap(pluginNames);
  if (value && typeof value === 'object' && 'name' in value && typeof value.name === 'string') {
    return [value.name];
  }
  return [];
}

describe('Vite React development boundary', () => {
  it('keeps the icon pre-transform ahead of the official React refresh plugins', () => {
    const names = pluginNames(createRendererPlugins());

    expect(names).toContain('x-science-icon-park');
    expect(names).not.toContain('x-science-renderer-state-full-reload');
    expect(names).toContain('vite:react-babel');
    expect(names).toContain('vite:react-refresh');
    expect(names.indexOf('x-science-icon-park')).toBeLessThan(names.indexOf('vite:react-babel'));
  });

  it('does not eagerly transform the authenticated conversation graph at startup', async () => {
    if (typeof viteConfig !== 'function') throw new Error('expected a mode-aware Vite configuration');
    const config = await viteConfig({ command: 'serve', mode: 'development', isSsrBuild: false, isPreview: false });

    expect(config.server?.warmup).toBeUndefined();
  });
});

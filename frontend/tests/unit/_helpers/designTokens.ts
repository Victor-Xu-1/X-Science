import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

/** Resolve the declared token context, not every selector as one global map.
 * This deliberately supports the three owned contexts below rather than
 * pretending to implement a browser cascade for arbitrary CSS selectors. */
const rendererDir = fileURLToPath(new URL('../../../packages/desktop/src/renderer/', import.meta.url));

export type DesignTokenScope = 'root' | 'settings' | 'settings-compact';
const tokensCss = readFileSync(path.join(rendererDir, 'styles/tokens.css'), 'utf8');
const settingsCss = readFileSync(path.join(rendererDir, 'pages/settings/components/settings-core.css'), 'utf8');
const declarations = (css: string, selector: string): Map<string, string> => {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const block = css.match(new RegExp(`${escaped}\\s*\\{([^}]*)\\}`))?.[1];
  if (block === undefined) throw new Error(`Design token scope is missing: ${selector}`);
  return new Map(
    [...block.matchAll(/--(ui-[a-z0-9-]+|settings-[a-z0-9-]+):\s*([^;]+);/g)].map((match) => [
      match[1]!,
      match[2]!.trim(),
    ])
  );
};
const rootValues = declarations(tokensCss, ':root');
const settingsValues = declarations(
  settingsCss,
  ".settings-page-wrapper[data-settings-visual-system='scientific-connectors-v3']"
);
const compactValues = declarations(settingsCss, ".settings-page-wrapper[data-settings-density='compact']");

export const resolveDesignTokens = (css: string, scope: DesignTokenScope = 'root'): string => {
  const values = new Map(rootValues);
  if (scope !== 'root') for (const [name, value] of settingsValues) values.set(name, value);
  if (scope === 'settings-compact') for (const [name, value] of compactValues) values.set(name, value);
  const resolveOnce = (source: string): string =>
    source.replace(/var\(--(ui-[a-z0-9-]+|settings-[a-z0-9-]+)\)/g, (whole, name: string) => values.get(name) ?? whole);
  // Settings aliases can refer to other aliases and inherited UI primitives.
  let resolved = resolveOnce(css);
  for (let pass = 0; pass < 3 && /var\(--(?:ui|settings)-/.test(resolved); pass += 1) resolved = resolveOnce(resolved);
  return resolved;
};

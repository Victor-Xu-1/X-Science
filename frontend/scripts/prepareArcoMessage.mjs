import { readFileSync, realpathSync, renameSync, unlinkSync, writeFileSync } from 'node:fs';
import { dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { ARCO_MESSAGE_VERSION, patchArcoMessageSource, sourceSha256 } from './arcoMessageIdentityPatch.mjs';

/**
 * Require a real installation in this frontend, never a linked shared SDK.
 * Checking both package files before writing also rejects a partially unknown
 * installation without changing its other module.
 * @param {string} frontendRoot
 * @param {boolean} [checkOnly]
 */
export function prepareArcoMessage(frontendRoot, checkOnly = false) {
  const frontend = realpathSync(frontendRoot);
  const requestedModules = join(frontend, 'node_modules');
  const modules = realpathSync(requestedModules);
  if (modules !== requestedModules) throw new Error('Refusing linked/shared node_modules; use an owned installation.');

  const requestedPackage = join(modules, '@arco-design', 'web-react');
  const arco = realpathSync(requestedPackage);
  if (arco !== requestedPackage) throw new Error('Refusing linked/shared Arco package; use an owned installation.');
  const metadata = JSON.parse(readFileSync(join(arco, 'package.json'), 'utf8'));
  if (metadata.name !== '@arco-design/web-react' || metadata.version !== ARCO_MESSAGE_VERSION) {
    throw new Error('Arco package identity changed; review the locked compatibility patch.');
  }

  const plan = /** @type {Array<{path: string, before: string, after: string, source: string}>} */ ([]);
  for (const format of /** @type {const} */ (['es', 'lib'])) {
    const requested = join(arco, format, 'Message', 'useMessage.js');
    const path = realpathSync(requested);
    const inside = relative(arco, path);
    if (path !== requested || inside.startsWith('..') || isAbsolute(inside)) {
      throw new Error('Refusing linked/shared Arco Message source.');
    }
    const source = readFileSync(path, 'utf8');
    const prepared = patchArcoMessageSource(source, format);
    const before = sourceSha256(source);
    const after = sourceSha256(prepared);
    if (checkOnly && before !== after) {
      throw new Error('Arco Message is unprepared; run npm run dependencies:prepare after npm ci.');
    }
    plan.push({ path, before, after, source: prepared });
  }

  for (const entry of plan) {
    if (entry.before === entry.after) continue;
    const temporary = entry.path + '.x-science-prepare';
    // wx never replaces someone else's temporary file. Rename replaces this
    // owned path, rather than modifying a possibly hardlinked npm cache inode.
    writeFileSync(temporary, entry.source, { encoding: 'utf8', flag: 'wx' });
    try {
      renameSync(temporary, entry.path);
    } catch (error) {
      unlinkSync(temporary);
      throw error;
    }
  }
  return {
    package: metadata.name,
    version: metadata.version,
    files: plan.map(({ path, before, after }) => ({ path: relative(frontend, path), before, after })),
  };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const args = process.argv.slice(2);
    if (args.length > 1 || (args.length === 1 && args[0] !== '--check')) {
      throw new Error('Usage: node scripts/prepareArcoMessage.mjs [--check]');
    }
    const frontend = dirname(dirname(fileURLToPath(import.meta.url)));
    console.log(JSON.stringify(prepareArcoMessage(frontend, args[0] === '--check')));
  } catch (error) {
    console.error(error instanceof Error ? error.message : String(error));
    process.exitCode = 1;
  }
}

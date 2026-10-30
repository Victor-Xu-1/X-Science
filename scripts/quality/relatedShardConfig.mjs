import { createHash } from 'node:crypto';
import { lstatSync, readFileSync } from 'node:fs';
import { isAbsolute, relative, resolve, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { createRequire } from 'node:module';

// Configuration and custom reporters use different Vitest module loaders.
// Read the reviewed constructor through Node's shared native module cache.
// An unsupported synchronous ESM graph rejects optimization; never compare names.
const StableSequencer = createRequire(import.meta.url)('./relatedShardSequencer.mjs').default;
import { BaseSequencer } from '../../frontend/node_modules/vitest/dist/node.js';

// An optimization profile, never a test waiver. Changed/unknown configuration
// retains the original related command rather than expanding this allowance.
const supportedConfig = '5d47dd708f9373ddb369320b48afbfb9f6f82b8605fabc2e7b94964f5577a790';
const digest = (value) => createHash('sha256').update(value).digest('hex');

function canonical(value, root, seen = new Set()) {
  if (value === undefined) return { type: 'undefined' };
  if (value === null || typeof value === 'boolean') return value;
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) throw new Error('Non-finite configuration value');
    return value;
  }
  if (typeof value === 'string') return value.split(root).join('<frontend>');
  if (value === BaseSequencer) return { type: 'VitestBaseSequencer' };
  if (value instanceof RegExp) return { type: 'RegExp', source: value.source, flags: value.flags };
  if (typeof value !== 'object' || seen.has(value)) throw new Error('Unsupported configuration identity');
  seen.add(value);
  try {
    if (Array.isArray(value)) return value.map((item) => canonical(item, root, seen));
    if (Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null) {
      throw new Error('Unsupported configuration object');
    }
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, canonical(value[key], root, seen)]));
  } finally {
    seen.delete(value);
  }
}

function defaultMiddlewareApi(config) {
  const api = config.api;
  return !config.ui && api && JSON.stringify(Object.keys(api).sort()) === '["allowExec","allowWrite","middlewareMode","token"]' &&
    api.middlewareMode === true && api.allowExec === true && api.allowWrite === true &&
    typeof api.token === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(api.token);
}

function supported(config, sharded = false) {
  const sequence = config.sequence ?? {};
  return defaultMiddlewareApi(config) && config.isolate === true && ['forks', 'threads'].includes(config.pool) &&
    config.watch === false && config.allowOnly === false && !config.coverage?.enabled && !config.bail &&
    !config.browser?.enabled && !config.testNamePattern &&
    !config.globalSetup?.length && config.fileParallelism !== false &&
    (sequence.sequencer === BaseSequencer || sharded && sequence.sequencer === StableSequencer) &&
    !sequence.shuffle && !sequence.concurrent && !sequence.groupOrder;
}

export function requireSupportedSource(root) {
  if (digest(readFileSync(resolve(root, 'vitest.config.ts'))) !== supportedConfig) {
    throw new Error('Configuration is outside the reviewed optimization profile');
  }
  const packageMetadata = JSON.parse(readFileSync(resolve(root, 'node_modules/vitest/package.json')));
  if (packageMetadata.version !== '4.1.11') throw new Error('Unsupported Vitest version');
}

export function shardConfigurationSource(root) {
  const base = JSON.stringify(resolve(root, 'vitest.config.ts'));
  const sequencer = JSON.stringify(pathToFileURL(resolve(root, '../scripts/quality/relatedShardSequencer.mjs')).href);
  return 'import base from ' + base + ';\n' +
    'const sequenceModule = ' + sequencer + ';\n' +
    'const { default: Stable } = await import(sequenceModule);\n' +
    'export default { ...base, test: { ...base.test, sequence: { ...(base.test?.sequence ?? {}), sequencer: Stable } } };\n';
}

function requireActualWrapper(context, root) {
  const file = process.env.X_SCIENCE_RELATED_CONFIG;
  if (!file || !isAbsolute(file) || !relative(root, file).startsWith('..' + sep) ||
      lstatSync(file).isSymbolicLink() || !lstatSync(file).isFile() ||
      context.vite.config.configFile !== file || context.config.config !== file ||
      readFileSync(file, 'utf8') !== shardConfigurationSource(root)) {
    throw new Error('Actual shard configuration is not the generated original-profile wrapper');
  }
}

function comparable(config, root, sharded, activeReporters) {
  const copy = { ...config, sequence: { ...config.sequence } };
  delete copy.shard;
  delete copy.reporters;
  delete copy.sequence.sequencer;
  const original = resolve(root, 'vitest.config.ts');
  const active = sharded ? process.env.X_SCIENCE_RELATED_CONFIG : original;
  // resolveConfig generates a fresh API secret for its middleware-only default.
  // Permission flags remain exact. Do not persist the generated nonce or
  // compare secrets belonging to different processes.
  if (!defaultMiddlewareApi(config)) throw new Error('Active or unknown API configuration');
  copy.api = { ...config.api, token: '<per-process-default-middleware-api-nonce>' };
  if (copy['--'] !== undefined && (!Array.isArray(copy['--']) || copy['--'].length !== 0)) {
    throw new Error('Unexpected CLI positional aliases');
  }
  delete copy['--'];
  if (copy.c !== undefined && copy.c !== active) throw new Error('CLI config alias differs');
  delete copy.c;
  if (copy.reporter !== undefined && JSON.stringify(copy.reporter) !== JSON.stringify(activeReporters)) {
    throw new Error('CLI reporter alias differs');
  }
  delete copy.reporter;
  if (config.coverage?.enabled !== false || !Array.isArray(config.coverage.exclude) ||
      config.coverage.exclude.filter((path) => path === active).length !== 1) {
    throw new Error('Unknown automatic coverage config exclusion');
  }
  // Coverage is disabled; only the automatically excluded active config file
  // changes with the exact reviewed wrapper. Preserve every other exclusion.
  copy.coverage = { ...config.coverage, exclude: config.coverage.exclude.map((path) => path === active ? original : path) };
  if (sharded && Object.hasOwn(copy, 'config')) copy.config = original;
  return copy;
}

export function resolvedRelatedConfiguration(context, root, shard) {
  requireSupportedSource(root);
  const sharded = shard !== undefined;
  if (!supported(context.config, sharded) || context.projects.length !== 2 ||
      context.projects.some((project) => !['node', 'dom'].includes(project.name) || !supported(project.config, sharded))) {
    throw new Error('Configuration cannot establish independent file execution');
  }
  if (sharded) {
    requireActualWrapper(context, root);
    if (context.config.sequence.sequencer !== StableSequencer ||
        context.config.shard?.index !== shard.index || context.config.shard?.count !== shard.count) {
      throw new Error('Actual worker sequencer or shard differs');
    }
  } else if (context.config.shard !== undefined) {
    throw new Error('Original selection is already sharded');
  }
  const reporters = context.config.reporters.map((item) => {
    if (!Array.isArray(item) || Object.keys(item[1] ?? {}).length) throw new Error('Custom reporter options');
    return item[0];
  });
  const defaults = process.env.GITHUB_ACTIONS === 'true' ? ['default', 'github-actions'] : ['default'];
  const expected = sharded ? [...defaults, resolve(root, '../scripts/quality/relatedShardReporter.mjs')] : defaults;
  if (JSON.stringify(reporters) !== JSON.stringify(expected)) throw new Error('Unsupported active reporters');
  const projects = [...context.projects].sort((left, right) => left.name.localeCompare(right.name));
  const record = canonical({
    test: comparable(context.config, root, sharded, expected),
    projects: projects.map((project) => ({
      name: project.name,
      test: comparable(project.config, root, sharded, expected),
      define: project.vite.config.define,
      alias: project.vite.config.resolve?.alias,
    })),
    runtime: { node: process.versions, platform: process.platform, arch: process.arch },
    environment: Object.fromEntries(['CI', 'GITHUB_ACTIONS', 'NODE_OPTIONS', 'NODE_ENV', 'TZ', 'LANG', 'ImageOS', 'ImageVersion']
      .map((name) => [name, process.env[name]])),
  }, root);
  const encoded = JSON.stringify(record);
  return { record, sha256: digest(encoded), default_reporters: defaults };
}

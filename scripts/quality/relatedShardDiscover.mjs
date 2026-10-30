import { readFile, writeFile } from 'node:fs/promises';
import { resolve, isAbsolute, relative, sep } from 'node:path';
import { createVitest } from '../../frontend/node_modules/vitest/dist/node.js';
import { relatedShardIdentity, compareRelatedIdentities } from './relatedShardIdentity.mjs';
import { requireSupportedSource, resolvedRelatedConfiguration, shardConfigurationSource } from './relatedShardConfig.mjs';

const [rootValue, inputFile, outputFile] = process.argv.slice(2);
const root = resolve(rootValue ?? '');
if (![inputFile, outputFile].every((path) => path && isAbsolute(path))) throw new Error('Absolute discovery files required');
const outputRelative = relative(root, outputFile);
if (!outputRelative.startsWith('..' + sep) && !isAbsolute(outputRelative)) throw new Error('Discovery output must be external');
const input = JSON.parse(await readFile(inputFile, 'utf8'));
if (!Array.isArray(input) || input.some((value) => typeof value !== 'string' || !value || value.includes('\0'))) {
  throw new Error('Invalid original related input');
}
process.env.TEST = 'true';
process.env.VITEST = 'true';
process.env.NODE_ENV ??= 'test';
let context;
let output;
try {
  requireSupportedSource(root);
  context = await createVitest('test', {
    root, run: true, watch: false, related: input, project: ['node', 'dom'], passWithNoTests: true,
  });
  const specifications = await context.getRelevantTestSpecifications();
  const configuration = resolvedRelatedConfiguration(context, root);
  const specs = specifications.map((spec) => relatedShardIdentity(spec, root)).sort(compareRelatedIdentities);
  for (let index = 1; index < specs.length; index++) {
    if (compareRelatedIdentities(specs[index - 1], specs[index]) === 0) throw new Error('Duplicate specification identity');
  }
  output = { eligible: true, specs, configuration, wrapper_source: shardConfigurationSource(root) };
} catch {
  // The supervising process runs the original command on an unsupported plan.
  // A worker already bound to a sharded plan fails instead of changing coverage.
  output = { eligible: false, reason: 'unproved configuration or discovery' };
} finally {
  await context?.close();
}
await writeFile(outputFile, JSON.stringify(output), { flag: 'wx' });

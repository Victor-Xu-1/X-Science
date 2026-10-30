import { writeFile } from 'node:fs/promises';
import { realpathSync } from 'node:fs';
import { basename, dirname, isAbsolute, join, relative, sep } from 'node:path';
import { relatedShardIdentity } from './relatedShardIdentity.mjs';
import { resolvedRelatedConfiguration } from './relatedShardConfig.mjs';

/** Observe Vitest only; the supervising process binds inputs and exit status. */
export default class RelatedShardReporter {
  planned = [];
  queued = [];
  ended = [];

  onInit(context) {
    this.root = context.config.root;
    const index = Number(process.env.X_SCIENCE_RELATED_INDEX);
    const count = Number(process.env.X_SCIENCE_RELATED_COUNT);
    if (!Number.isInteger(index) || !Number.isInteger(count) || index < 1 || count < index) {
      throw new Error('Actual shard assignment unavailable');
    }
    this.context = context;
    this.assignment = { index, count };
    this.configuration = resolvedRelatedConfiguration(context, this.root, this.assignment).sha256;
    const output = process.env.X_SCIENCE_RELATED_REPORT;
    if (!output || !isAbsolute(output)) throw new Error('An absolute related shard report path is required');
    const parent = realpathSync(dirname(output));
    const root = realpathSync(this.root);
    const within = relative(root, parent);
    if (!within || (within !== '..' && !within.startsWith('..' + sep) && !isAbsolute(within))) {
      throw new Error('Related shard report must be outside the frontend source');
    }
    this.output = join(parent, basename(output));
  }

  onTestRunStart(specifications) {
    if (resolvedRelatedConfiguration(this.context, this.root, this.assignment).sha256 !== this.configuration) {
      throw new Error('Actual shard configuration changed after initialization');
    }
    this.planned = specifications.map((specification) => relatedShardIdentity(specification, this.root));
  }

  onTestModuleQueued(module) {
    this.queued.push(relatedShardIdentity(module.toTestSpecification(), this.root));
  }

  onTestModuleEnd(module) {
    this.ended.push({ ...relatedShardIdentity(module.toTestSpecification(), this.root), state: module.state(), ok: module.ok() });
  }

  async onTestRunEnd(_modules, errors, reason) {
    await writeFile(this.output, JSON.stringify({
      configuration_sha256: this.configuration,
      planned: this.planned,
      queued: this.queued,
      ended: this.ended,
      reason,
      unhandled_errors: errors.length,
    }), { encoding: 'utf8', flag: 'wx' });
  }
}

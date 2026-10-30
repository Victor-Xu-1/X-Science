import { BaseSequencer } from '../../frontend/node_modules/vitest/dist/node.js';
import { compareRelatedIdentities, relatedShardIdentity } from './relatedShardIdentity.mjs';

/** Override only shard assignment. Normal Vitest sort and execution remain native. */
export default class RelatedShardSequencer extends BaseSequencer {
  async shard(specifications) {
    const { index, count } = this.ctx.config.shard ?? {};
    if (!Number.isInteger(index) || !Number.isInteger(count) || index < 1 || count < 1 || index > count) {
      throw new Error('Invalid related shard selection');
    }
    const sorted = specifications
      .map((specification) => ({ specification, identity: relatedShardIdentity(specification, this.ctx.config.root) }))
      .sort((left, right) => compareRelatedIdentities(left.identity, right.identity));
    for (let i = 1; i < sorted.length; i++) {
      if (compareRelatedIdentities(sorted[i - 1].identity, sorted[i].identity) === 0) {
        throw new Error('Duplicate related shard specification');
      }
    }
    const size = Math.floor(sorted.length / count);
    const extra = sorted.length % count;
    const start = (index - 1) * size + Math.min(index - 1, extra);
    const end = start + size + (index <= extra ? 1 : 0);
    return sorted.slice(start, end).map((entry) => entry.specification);
  }
}

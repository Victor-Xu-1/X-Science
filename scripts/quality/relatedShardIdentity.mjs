import { isAbsolute, relative, sep } from 'node:path';

/** Convert a Vitest specification to the stable identity used by every shard. */
export function relatedShardIdentity(specification, root) {
  const project = specification.project?.name;
  const pool = specification.pool;
  const moduleId = specification.moduleId;
  if (![project, pool, moduleId, root].every((value) => typeof value === 'string' && value && !value.includes('\0')) ||
      !isAbsolute(moduleId) || !isAbsolute(root)) {
    throw new Error('Related shard specification has no valid identity');
  }
  const path = relative(root, moduleId).split(sep).join('/');
  if (!path || path === '..' || path.startsWith('../') || isAbsolute(path)) {
    throw new Error('Related shard specification is outside the frontend root');
  }
  return { project, path, pool };
}

/** Byte ordering agrees across workers without depending on locale. */
export function compareRelatedIdentities(left, right) {
  for (const field of ['project', 'path', 'pool']) {
    const difference = Buffer.compare(Buffer.from(left[field], 'utf8'), Buffer.from(right[field], 'utf8'));
    if (difference) return difference;
  }
  return 0;
}

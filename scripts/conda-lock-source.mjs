import { createHash } from 'node:crypto';
import { readFile, stat } from 'node:fs/promises';
import { dirname, join } from 'node:path';

const digest = (text) => createHash('sha256').update(text).digest('hex');

// Reuse authenticated pinned metadata without inventing a solver receipt or
// re-solving unrelated packages. Removal is safe only for unused leaves.
export async function readLockedRecords(path, { name, platform, exclude = '' }) {
  const info = await stat(path);
  if (!info.isFile() || info.size > 32 * 1024 * 1024) {
    throw new Error('locked manifest is outside the supported size');
  }
  const { manifestSHA256, ...body } = JSON.parse(await readFile(path, 'utf8'));
  if (
    digest(`${JSON.stringify(body, null, 2)}\n`) !== manifestSHA256 ||
    body.schemaVersion !== 2 || body.name !== name || body.platform !== platform ||
    !Array.isArray(body.packages) || body.packages.length !== body.packageCount
  ) {
    throw new Error('locked manifest identity or digest does not match');
  }
  for (const [file, expected] of [
    ['explicit.txt', body.explicitSHA256], ['licenses.json', body.licenseInventorySHA256],
  ]) {
    if (digest(await readFile(join(dirname(path), file))) !== expected) {
      throw new Error(`locked ${file} digest does not match`);
    }
  }
  const excluded = new Set(exclude ? exclude.split(',') : []);
  const names = new Set(body.packages.map((item) => item.name));
  for (const name of excluded) {
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/u.test(name) || !names.has(name)) {
      throw new Error(`excluded package is not in the verified lock: ${name}`);
    }
  }
  const retained = body.packages.filter((item) => !excluded.has(item.name));
  for (const item of retained) {
    for (const dependency of item.depends) {
      const name = dependency.split(/\s|[<>=!~]/u, 1)[0];
      if (excluded.has(name)) {
        throw new Error(`cannot remove ${name}: required by ${item.name}`);
      }
    }
  }
  return retained.map((item) => ({
    entry: `locked-${item.name}`,
    decoded: { ...item, build_number: item.buildNumber },
  }));
}

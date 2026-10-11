import { readFileSync } from 'node:fs';
import type { Plugin as EsbuildPlugin } from 'esbuild';
import type { Plugin } from 'vite';

const pickHelperPath = /[/\\]molstar[/\\]lib[/\\]mol-canvas3d[/\\]helper[/\\]pick-helper\.js$/;
const identifyStart = '    identify(x, y, camera) {';
const identifyEnd = '    asyncIdentify(x, y, camera) {';
const returnPick = '        return this.getPickData(x, y, camera);';

/**
 * Mol* 5.11 starts an asynchronous hover read and clears `dirty` before the
 * GPU read has completed. A click in that interval incorrectly returns no
 * object. Complete that read synchronously only for an explicit click.
 * Keep hover asynchronous and preserve the upstream picking/selection logic.
 * Remove this compatibility patch when the locked upstream fixes identify().
 */
export function patchMolstarPicking(source: string): string {
  const start = source.indexOf(identifyStart);
  const end = source.indexOf(identifyEnd, start);
  const method = source.slice(start, end);
  if (start < 0 || end < 0 || method.split(returnPick).length !== 2) {
    throw new Error('Mol* synchronous picking changed; review the picking compatibility patch.');
  }
  const patched = method.replace(
    returnPick,
    `        if (this.buffers.check() !== AsyncPickStatus.Resolved) {
            this.buffers.read();
        }
${returnPick}`
  );
  return source.slice(0, start) + patched + source.slice(end);
}

export function molstarPickingBuildPlugin(): Plugin {
  return {
    name: 'molstar-picking-readiness',
    enforce: 'pre',
    transform(source, id) {
      return pickHelperPath.test(id) ? { code: patchMolstarPicking(source), map: null } : null;
    },
  };
}

/** Vite development prebundling must use the same fix as production builds. */
export function molstarPickingPrebundlePlugin(): EsbuildPlugin {
  return {
    name: 'molstar-picking-readiness',
    setup(build) {
      build.onLoad({ filter: pickHelperPath }, ({ path }) => ({
        contents: patchMolstarPicking(readFileSync(path, 'utf8')),
        loader: 'js',
      }));
    },
  };
}

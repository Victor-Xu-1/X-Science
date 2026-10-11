import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { runInNewContext } from 'node:vm';
import { describe, expect, it, vi } from 'vitest';
import {
  molstarPickingBuildPlugin,
  molstarPickingPrebundlePlugin,
  patchMolstarPicking,
} from '../../scripts/molstarPickingPatch';

const require = createRequire(import.meta.url);
const sourcePath = require.resolve('molstar/lib/mol-canvas3d/helper/pick-helper.js');
const upstream = readFileSync(sourcePath, 'utf8');
const status = { Pending: 0, Resolved: 1, Failed: 2 };

function createPicker(source: string, initialStatus: number, dirty = false) {
  const PickHelper = runInNewContext(
    source.replace(/^import .+;\r?$/gm, '').replace('export class PickHelper', 'class PickHelper') + '\nPickHelper;',
    { isTimingMode: false, AsyncPickStatus: status }
  );
  let readiness = initialStatus;
  const picked = { id: { objectId: 3, instanceId: 0, groupId: 2 } };
  const picker = Object.assign(Object.create(PickHelper.prototype), {
    dirty,
    prepare: vi.fn(),
    render: vi.fn(() => {
      picker.dirty = false;
    }),
    webgl: { resources: { finalizePrograms: vi.fn() } },
    buffers: {
      check: vi.fn(() => readiness),
      read: vi.fn(() => {
        readiness = status.Resolved;
      }),
      asyncRead: vi.fn(() => {
        readiness = status.Pending;
      }),
    },
    getPickData: vi.fn(() => (readiness === status.Resolved ? picked : undefined)),
  });
  return { picker, picked };
}

describe('Mol* click picking while a hover GPU read is pending', () => {
  it('reproduces the locked upstream miss and completes the pending read for the same click', () => {
    const original = createPicker(upstream, status.Pending);
    expect(original.picker.identify(12, 34, {})).toBeUndefined();
    const { picker, picked } = createPicker(patchMolstarPicking(upstream), status.Pending);
    expect(picker.identify(12, 34, {})).toBe(picked);
    expect(picker.buffers.read).toHaveBeenCalledTimes(1);
    expect(picker.render).not.toHaveBeenCalled();
    expect(picker.getPickData).toHaveBeenCalledWith(12, 34, {});
  });

  it.each([status.Resolved, status.Pending, status.Failed])(
    'does not duplicate a dirty-scene read (status %s)',
    (readiness) => {
      const { picker, picked } = createPicker(patchMolstarPicking(upstream), readiness, true);
      expect(picker.identify(12, 34, {})).toBe(picked);
      expect(picker.buffers.read).toHaveBeenCalledTimes(1);
      expect(picker.render).toHaveBeenCalledTimes(1);
    }
  );

  it('reuses resolved data without another GPU read and leaves empty positions empty', () => {
    const { picker, picked } = createPicker(patchMolstarPicking(upstream), status.Resolved);
    expect(picker.identify(12, 34, {})).toBe(picked);
    picker.getPickData.mockReturnValue(undefined);
    expect(picker.identify(0, 0, {})).toBeUndefined();
    expect(picker.buffers.read).not.toHaveBeenCalled();
  });

  it('keeps hover asynchronous', () => {
    const { picker } = createPicker(patchMolstarPicking(upstream), status.Resolved, true);
    expect(picker.asyncIdentify(12, 34, {}).tryGet()).toBe('pending');
    expect(picker.buffers.asyncRead).toHaveBeenCalledTimes(1);
    expect(picker.buffers.read).not.toHaveBeenCalled();
  });

  it('applies identical code in production and development dependency prebundling', () => {
    const build = molstarPickingBuildPlugin();
    const transform = build.transform as (source: string, id: string) => { code: string } | null;
    let load: ((args: { path: string }) => { contents: string }) | undefined;
    molstarPickingPrebundlePlugin().setup({
      onLoad: (_options: unknown, callback: typeof load) => {
        load = callback;
      },
    } as never);
    expect(transform(upstream, sourcePath)?.code).toBe(load!({ path: sourcePath }).contents);
    expect(transform(upstream, '/unrelated/pick-helper.js')).toBeNull();
    expect(transform(upstream, sourcePath.replaceAll('/', '\\'))?.code).toBe(patchMolstarPicking(upstream));
  });

  it('fails clearly when the locked dependency no longer has the reviewed method', () => {
    expect(() => patchMolstarPicking(upstream.replace('identify(x, y, camera)', 'identify(point)'))).toThrow(/review/);
  });
});

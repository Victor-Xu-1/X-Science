import { describe, expect, it } from 'vitest';
import type { Structure } from 'molstar/lib/mol-model/structure';
import { getQualityProps } from 'molstar/lib/mol-repr/util';
import {
  resolveSurfaceGeometryParams,
  STANDARD_PROTEIN_SURFACE_TYPE_PARAMS,
} from '@/renderer/pages/conversation/Preview/components/viewers/molstarSurfaceGeometry';

const component = (elementCount: number) =>
  Object.freeze({
    elementCount,
    isCoarse: false,
    isCoarseGrained: false,
  }) as unknown as Structure;

describe('native surface geometry', () => {
  it.each(['pocket', 'ligand'] as const)('uses the actual small %s component for finer tessellation', (scope) => {
    const structure = component(48);
    const params = resolveSurfaceGeometryParams(scope, structure);
    expect(getQualityProps(params).resolution).toBeLessThan(
      getQualityProps(STANDARD_PROTEIN_SURFACE_TYPE_PARAMS).resolution
    );
    expect(params.probeRadius).toBe(1.4);
    expect(params.alpha).toBe(0.7);
    expect(structure.elementCount).toBe(48);
  });
  it.each(['pocket', 'ligand'] as const)('retains native size-aware limits for a large %s component', (scope) => {
    const small = getQualityProps(resolveSurfaceGeometryParams(scope, component(48)));
    const large = getQualityProps(resolveSurfaceGeometryParams(scope, component(2_000_000)));
    expect(large.resolution).toBeGreaterThan(small.resolution);
  });
  it('retains the existing full-protein budget and physical surface contract', () => {
    expect(resolveSurfaceGeometryParams('protein', component(48))).toBe(STANDARD_PROTEIN_SURFACE_TYPE_PARAMS);
    expect(resolveSurfaceGeometryParams('protein', component(2_000_000))).toBe(STANDARD_PROTEIN_SURFACE_TYPE_PARAMS);
    expect(resolveSurfaceGeometryParams('ligand').probeRadius).toBe(STANDARD_PROTEIN_SURFACE_TYPE_PARAMS.probeRadius);
  });
});

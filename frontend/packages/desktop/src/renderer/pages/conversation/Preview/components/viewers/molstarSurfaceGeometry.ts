import type { Structure } from 'molstar/lib/mol-model/structure';
import { getStructureQuality } from 'molstar/lib/mol-repr/util';

export type MolstarSurfaceScope = 'protein' | 'pocket' | 'ligand';

/** Physical surface definition and opacity do not depend on rendering quality. */
export const STANDARD_PROTEIN_SURFACE_TYPE_PARAMS = {
  probeRadius: 1.4,
  alpha: 0.7,
  quality: 'medium',
} as const;

export const ELECTROSTATIC_SURFACE_TYPE_PARAMS = STANDARD_PROTEIN_SURFACE_TYPE_PARAMS;
export const POCKET_SURFACE_TYPE_PARAMS = {
  ...STANDARD_PROTEIN_SURFACE_TYPE_PARAMS,
  quality: 'higher',
} as const;

/** Use native size-aware detail for the represented component, not its full parent. */
export function resolveSurfaceGeometryParams(scope: MolstarSurfaceScope, structure?: Structure) {
  if (scope === 'protein') return STANDARD_PROTEIN_SURFACE_TYPE_PARAMS;
  return {
    ...POCKET_SURFACE_TYPE_PARAMS,
    quality: structure ? getStructureQuality(structure) : POCKET_SURFACE_TYPE_PARAMS.quality,
  };
}

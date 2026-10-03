/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */
import { OrderedSet } from 'molstar/lib/mol-data/int';
import { Unit, type StructureElement } from 'molstar/lib/mol-model/structure';
import { BondType } from 'molstar/lib/mol-model/structure/model/types';

/** Explicit molecule tables or complete declared CIF component bonds, never distance guesses. */
export function hasDeclaredLigandTopology(loci: StructureElement.Loci): boolean {
  let edges = 0;
  let completeMoleculeTable = true;
  for (const element of loci.elements) {
    const { unit, indices } = element;
    if (!Unit.isAtomic(unit)) return false;
    const kind = unit.model.sourceData?.kind;
    const moleculeTable = kind === 'mol' || kind === 'sdf' || kind === 'mol2';
    if (!moleculeTable && kind !== 'mmCIF' && kind !== 'CCD') return false;
    completeMoleculeTable &&= moleculeTable;
    let invalid = false;
    OrderedSet.forEach(indices, (index) => {
      for (let edge = unit.bonds.offset[index]; edge < unit.bonds.offset[index + 1]; edge += 1) {
        if (!OrderedSet.has(indices, unit.bonds.b[edge])) continue;
        const order = unit.bonds.edgeProps.order[edge];
        const flags = unit.bonds.edgeProps.flags[edge];
        edges += 1;
        if (!Number.isInteger(order) || order < 1 || order > 4 || (!moleculeTable && flags & BondType.Flag.Computed)) {
          invalid = true;
        }
      }
    });
    if (invalid) return false;
  }
  return completeMoleculeTable || edges > 0;
}

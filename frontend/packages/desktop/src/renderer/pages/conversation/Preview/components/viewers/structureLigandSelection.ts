import { OrderedSet, SortedArray } from 'molstar/lib/mol-data/int';
import { StructureElement, Unit, type Structure } from 'molstar/lib/mol-model/structure';
import { StructureQuery } from 'molstar/lib/mol-model/structure/query/query';
import { StructureSelectionQueries } from 'molstar/lib/mol-plugin-state/helpers/structure-selection-query';

const STANDALONE_FORMATS = new Set(['mol', 'sdf', 'mol2', 'xyz']);
const ADDITIVES = new Set(['ACT', 'DMS', 'EDO', 'EOH', 'GOL', 'MPD', 'PEG', 'PG4', 'PGE']);
export const isDisplayLigandResidueName = (name: string): boolean => !ADDITIVES.has(name.trim().toUpperCase());

export type StructureLigandInstance = {
  id: string;
  label: string;
  residueName: string;
  atomCount: number;
  loci: StructureElement.Loci;
};

export function queryStructureLigands(structure: Structure, format = ''): StructureElement.Loci {
  const ligand = StructureQuery.loci(StructureSelectionQueries.ligand.query, structure);
  if (!StructureElement.Loci.isEmpty(ligand) || !STANDALONE_FORMATS.has(format)) return ligand;
  const protein = StructureQuery.loci(StructureSelectionQueries.protein.query, structure);
  return StructureElement.Loci.isEmpty(protein)
    ? StructureQuery.loci(StructureSelectionQueries.all.query, structure)
    : ligand;
}

const code = (value: string): string => (value === '.' || value === '?' ? '' : value.trim());

/** Atom identity, never distance or residue name alone, defines each selectable instance. */
export function splitStructureLigands(loci: StructureElement.Loci, hasProtein: boolean): StructureLigandInstance[] {
  const result: StructureLigandInstance[] = [];
  for (const { unit, indices } of loci.elements) {
    if (!Unit.isAtomic(unit)) continue;
    const hierarchy = unit.model.atomicHierarchy;
    const groups = new Map<number, number[]>();
    for (let position = 0; position < OrderedSet.size(indices); position += 1) {
      const index = OrderedSet.getAt(indices, position);
      const residue = hierarchy.residueAtomSegments.index[unit.elements[index]];
      const group = groups.get(residue) ?? [];
      group.push(index);
      groups.set(residue, group);
    }
    for (const [residue, atomIndices] of groups) {
      const atom = unit.elements[atomIndices[0]];
      const residueName =
        code(hierarchy.atoms.auth_comp_id.value(atom)) || code(hierarchy.atoms.label_comp_id.value(atom));
      if (hasProtein && !isDisplayLigandResidueName(residueName)) continue;
      const chain = hierarchy.chainAtomSegments.index[atom];
      const chainName =
        code(hierarchy.chains.auth_asym_id.value(chain)) || code(hierarchy.chains.label_asym_id.value(chain));
      const sequence = hierarchy.residues.auth_seq_id.value(residue);
      const insertion = code(hierarchy.residues.pdbx_PDB_ins_code.value(residue));
      const alternates = [
        ...new Set(
          atomIndices.map((index) => code(hierarchy.atoms.label_alt_id.value(unit.elements[index]))).filter(Boolean)
        ),
      ];
      for (const alternate of alternates.length ? alternates : ['']) {
        const selected = atomIndices.filter((index) => {
          const alt = code(hierarchy.atoms.label_alt_id.value(unit.elements[index]));
          return !alt || alt === alternate;
        });
        const selection = StructureElement.Loci(loci.structure, [
          {
            unit,
            indices: SortedArray.ofSortedArray(selected),
          },
        ]);
        result.push({
          id: 'ligand:' + JSON.stringify([unit.id, unit.model.modelNum, residue, alternate]),
          label: `${residueName || 'Ligand'} · ${chainName || '—'}:${sequence}${insertion}${alternate ? ' · ' + alternate : ''}`,
          residueName,
          atomCount: selected.length,
          loci: selection,
        });
      }
    }
  }
  return result;
}

export function selectStructureLigandLoci(
  structure: Structure,
  instances: readonly StructureLigandInstance[],
  ids: readonly string[]
): StructureElement.Loci {
  const wanted = new Set(ids);
  return instances
    .filter((instance) => wanted.has(instance.id))
    .reduce(
      (selection, instance) => StructureElement.Loci.union(selection, instance.loci),
      StructureElement.Loci.none(structure)
    );
}

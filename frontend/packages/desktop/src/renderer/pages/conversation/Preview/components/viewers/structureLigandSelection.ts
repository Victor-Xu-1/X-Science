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

/** A partial atom pick must belong to exactly one complete native instance. */
export function resolveSingleStructureLigandInstance(
  structure: Structure,
  selection: StructureElement.Loci | undefined,
  format = '',
  fallbackResidueName?: string
): StructureLigandInstance | undefined {
  const protein = StructureQuery.loci(StructureSelectionQueries.protein.query, structure);
  const instances = splitStructureLigands(
    queryStructureLigands(structure, format),
    !StructureElement.Loci.isEmpty(protein)
  );
  const picked = selection && StructureElement.Loci.remap(selection, structure);
  const candidates =
    picked && !StructureElement.Loci.isEmpty(picked)
      ? instances.filter((instance) =>
          StructureElement.Loci.isEmpty(StructureElement.Loci.subtract(picked, instance.loci))
        )
      : fallbackResidueName
        ? instances.filter((instance) => instance.residueName.toUpperCase() === fallbackResidueName.toUpperCase())
        : [];
  return candidates.length === 1 ? candidates[0] : undefined;
}

/** PDB response serials map back to the same native instance authority. */
export function resolveStructureLigandInstanceByAtomSerial(structure: Structure, serial: number, format = '') {
  const protein = StructureQuery.loci(StructureSelectionQueries.protein.query, structure);
  const candidates = splitStructureLigands(
    queryStructureLigands(structure, format),
    !StructureElement.Loci.isEmpty(protein)
  ).filter((instance) =>
    instance.loci.elements.some(({ unit, indices }) => {
      if (!Unit.isAtomic(unit)) return false;
      for (let i = 0; i < OrderedSet.size(indices); i += 1) {
        if (unit.model.atomicConformation.atomId.value(unit.elements[OrderedSet.getAt(indices, i)]) === serial)
          return true;
      }
      return false;
    })
  );
  return candidates.length === 1 ? candidates[0] : undefined;
}

/** A preset consumes native selected atom identities; an explicit empty selection stays empty. */
export function resolveStructureViewLigandLoci(
  structure: Structure,
  selected?: StructureElement.Loci,
  format = ''
): StructureElement.Loci {
  const ligands = queryStructureLigands(structure, format);
  if (selected !== undefined) {
    return StructureElement.Loci.intersect(ligands, StructureElement.Loci.remap(selected, structure));
  }
  const protein = StructureQuery.loci(StructureSelectionQueries.protein.query, structure);
  return (
    splitStructureLigands(ligands, !StructureElement.Loci.isEmpty(protein))[0]?.loci ??
    StructureElement.Loci.none(structure)
  );
}

/** Stable API keys derive from the complete native instance order, never residue names or selection order. */
export function resolveStructureViewLigandInstances(
  structure: Structure,
  selected?: StructureElement.Loci,
  format = ''
): Array<StructureLigandInstance & { key: string }> {
  const displayed = resolveStructureViewLigandLoci(structure, selected, format);
  const protein = StructureQuery.loci(StructureSelectionQueries.protein.query, structure);
  return splitStructureLigands(queryStructureLigands(structure, format), !StructureElement.Loci.isEmpty(protein))
    .map((instance, index) => Object.assign({ key: `LIGAND_${index + 1}` }, instance))
    .filter((instance) => StructureElement.Loci.isEmpty(StructureElement.Loci.subtract(instance.loci, displayed)));
}

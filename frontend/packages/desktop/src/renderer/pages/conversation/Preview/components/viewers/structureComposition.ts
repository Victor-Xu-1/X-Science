import { OrderedSet } from 'molstar/lib/mol-data/int';
import { StructureElement, Unit, type Structure } from 'molstar/lib/mol-model/structure';
import { StructureQuery } from 'molstar/lib/mol-model/structure/query/query';
import { StructureSelectionQueries } from 'molstar/lib/mol-plugin-state/helpers/structure-selection-query';
import { queryStructureLigands, splitStructureLigands } from './structureLigandSelection';
export { isDisplayLigandResidueName } from './structureLigandSelection';

export type StructureObjectKind = 'protein' | 'ligand';

export type StructureObjectSummary = {
  id: string;
  kind: StructureObjectKind;
  label?: string;
  atomCount: number;
  residueNames: string[];
};

export type MolstarStructureComposition = {
  objects: StructureObjectSummary[];
  atomCount: number;
  hasProtein: boolean;
  hasLigand: boolean;
};

const residueNames = (loci: StructureElement.Loci): string[] => {
  const names = new Set<string>();
  for (const { unit, indices } of loci.elements) {
    if (!Unit.isAtomic(unit)) continue;
    for (let index = 0; index < OrderedSet.size(indices); index += 1) {
      const unitIndex = OrderedSet.getAt(indices, index);
      const atom = unit.elements[unitIndex];
      const name =
        unit.model.atomicHierarchy.atoms.auth_comp_id.value(atom).trim() ||
        unit.model.atomicHierarchy.atoms.label_comp_id.value(atom).trim();
      if (name) names.add(name);
    }
  }
  return [...names].toSorted();
};

const selection = (structure: Structure, kind: 'all' | StructureObjectKind): StructureElement.Loci =>
  StructureQuery.loci(StructureSelectionQueries[kind].query, structure);

/** Builds the sidebar model from Mol* semantics after parsing, independent of the source extension. */
export const summarizeStructureComposition = (
  structure: Structure | undefined,
  format = ''
): MolstarStructureComposition => {
  if (!structure) return { objects: [], atomCount: 0, hasProtein: false, hasLigand: false };

  const all = selection(structure, 'all');
  const protein = selection(structure, 'protein');
  const ligand = queryStructureLigands(structure, format);
  const atomCount = StructureElement.Loci.size(all);
  const proteinAtomCount = StructureElement.Loci.size(protein);
  const ligands = splitStructureLigands(ligand, proteinAtomCount > 0);

  const objects: StructureObjectSummary[] = [];
  if (proteinAtomCount > 0) {
    objects.push({ id: 'protein', kind: 'protein', atomCount: proteinAtomCount, residueNames: residueNames(protein) });
  }
  for (const instance of ligands) {
    objects.push({
      id: instance.id,
      kind: 'ligand',
      label: instance.label,
      atomCount: instance.atomCount,
      residueNames: [instance.residueName],
    });
  }

  return {
    objects,
    atomCount,
    hasProtein: proteinAtomCount > 0,
    hasLigand: ligands.length > 0,
  };
};

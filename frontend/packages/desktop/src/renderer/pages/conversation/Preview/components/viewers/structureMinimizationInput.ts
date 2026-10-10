import { formatMolstarPosePdb, type MolstarPoseAtom } from './molstarPose';
import { formatChemicalPoseInput, type ChemicalPoseSource } from './structureChemicalPoseInput';
import { normalizeChemicalElement } from './structureChemicalPoseResult';

/** Keep the selected active model's original records/connectivity, not another frame. */
export function selectCurrentPdbModel(source: string, index: number): string {
  const lines: string[] = source.match(/[^\n]*\n|[^\n]+$/g) ?? [];
  const count = lines.filter((line) => line.startsWith('MODEL ')).length;
  if (!Number.isSafeInteger(index) || index < 0 || index >= Math.max(count, 1)) {
    throw new Error('MOLSTAR_MINIMIZATION_MODEL_INVALID');
  }
  if (count <= 1) return source;
  let current = -1;
  let inModel = false;
  return lines
    .filter((line) => {
      if (line.startsWith('MODEL ')) {
        inModel = true;
        current += 1;
        return current === index;
      }
      if (line.startsWith('ENDMDL')) {
        inModel = false;
        return current === index;
      }
      return !inModel || current === index;
    })
    .join('');
}

export type StructureMinimizationPose = {
  content: string;
  ligandResidueName: string;
} & (
  | {
      format: 'pdb';
      ligandAtomSerial: number;
    }
  | { format: ChemicalPoseSource['format']; atomElements: readonly string[]; ligandAtomSerial?: undefined }
);

/** Native atom identity binds the input, independent of camera/list visibility. */
export function createStructureMinimizationPose(
  atoms: readonly MolstarPoseAtom[],
  keys: ReadonlySet<string>,
  ligandResidueName: string,
  sourcePdb?: string,
  chemicalSource?: ChemicalPoseSource
): StructureMinimizationPose {
  const selected = atoms.filter((atom) => atom.nativeKey && keys.has(atom.nativeKey));
  const atom =
    selected.find(
      (candidate) => candidate.alternateLocation?.trim() && !['.', '?'].includes(candidate.alternateLocation.trim())
    ) ?? selected[0];
  if (!atom) throw new Error('STRUCTURE_MINIMIZATION_LIGAND_NOT_SELECTED');
  if (sourcePdb === undefined && chemicalSource) {
    if (selected.length !== atoms.length) throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_CONTEXT_PARTITION_INVALID');
    return {
      content: formatChemicalPoseInput(chemicalSource, atoms),
      format: chemicalSource.format,
      atomElements: atoms
        .toSorted((a, b) => a.sourceIndex! - b.sourceIndex!)
        .map((candidate) => normalizeChemicalElement(candidate.element ?? '')),
      ligandResidueName,
    };
  }
  const serial = sourcePdb === undefined ? atom.serial : atom.sourceSerial;
  if (!Number.isSafeInteger(serial) || !serial || serial < 1 || serial > 99999)
    throw new Error('MOLSTAR_MINIMIZATION_ATOM_ID_INVALID');
  return {
    content: sourcePdb ?? formatMolstarPosePdb(atoms),
    format: 'pdb',
    ligandResidueName,
    ligandAtomSerial: serial,
  };
}

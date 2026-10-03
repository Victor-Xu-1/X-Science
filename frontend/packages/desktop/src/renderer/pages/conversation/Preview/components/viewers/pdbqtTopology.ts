/**
 * @license
 * Copyright 2026 Synon-AI
 * SPDX-License-Identifier: Apache-2.0
 */

// PDBQT coordinates alone cannot establish bond orders. Meeko's embedded
// SMILES and one-based index maps can: never substitute geometry or filenames.
type PoseAtom = {
  serial: number;
  element: string;
  coordinates: readonly [number, number, number];
};
type Pose = {
  smiles: string;
  atoms: Map<number, PoseAtom>;
  indices: Map<number, number>;
  hydrogens: Map<number, number>;
};
export type PdbqtTopology = { source: string; smiles: readonly string[] };
const MAX_POSES = 128;
const MAX_ATOMS = 999;
const MAX_DISTINCT_SMILES = 16;
const atomTypes: Readonly<Record<string, string>> = {
  A: 'C',
  NA: 'N',
  NS: 'N',
  OA: 'O',
  OS: 'O',
  SA: 'S',
  HD: 'H',
  HS: 'H',
};

function readPairs(payload: string, target: Map<number, number>, reverse: boolean): void {
  if (payload.length > MAX_ATOMS * 12) throw new Error('PDBQT_INDEX_MAP_INVALID');
  const values = payload.trim().split(/\s+/).map(Number);
  if (values.length % 2 || values.some((value) => !Number.isSafeInteger(value) || value < 1 || value > MAX_ATOMS)) {
    throw new Error('PDBQT_INDEX_MAP_INVALID');
  }
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index + (reverse ? 1 : 0)];
    const value = values[index + (reverse ? 0 : 1)];
    if (target.has(key)) throw new Error('PDBQT_INDEX_MAP_DUPLICATE');
    target.set(key, value);
  }
}

function readPose(lines: readonly string[]): Pose {
  const pose: Pose = {
    smiles: '',
    atoms: new Map(),
    indices: new Map(),
    hydrogens: new Map(),
  };
  for (const line of lines) {
    if (line.startsWith('REMARK SMILES IDX ')) {
      readPairs(line.slice(18), pose.indices, false);
    } else if (line.startsWith('REMARK H PARENT ')) {
      readPairs(line.slice(16), pose.hydrogens, true);
    } else if (line.startsWith('REMARK SMILES ')) {
      const smiles = line.slice(14).trim();
      if (pose.smiles || !smiles || smiles.length > 4096 || /\s/.test(smiles)) throw new Error('PDBQT_SMILES_INVALID');
      pose.smiles = smiles;
    } else if (/^(ATOM  |HETATM)/.test(line)) {
      const serial = Number(line.slice(6, 11).trim());
      const type = line.slice(77).trim().split(/\s+/)[0]?.toUpperCase();
      const element = atomTypes[type] ?? type;
      const fields = [line.slice(30, 38), line.slice(38, 46), line.slice(46, 54)];
      const coordinates = fields.map(Number) as [number, number, number];
      if (
        !Number.isSafeInteger(serial) ||
        serial < 1 ||
        pose.atoms.has(serial) ||
        pose.atoms.size >= MAX_ATOMS ||
        !/^[A-Z]{1,2}$/.test(element) ||
        fields.some((field) => !field.trim()) ||
        coordinates.some((value) => !Number.isFinite(value) || value.toFixed(4).length > 10)
      ) {
        throw new Error('PDBQT_ATOM_INVALID');
      }
      pose.atoms.set(serial, { serial, element, coordinates });
    }
  }
  if (!pose.smiles || pose.atoms.size === 0 || pose.indices.size === 0) throw new Error('PDBQT_TOPOLOGY_MISSING');
  return pose;
}

function readPoses(source: string): Pose[] {
  const lines = source.split(/\r?\n/);
  if (!lines.some((line) => /^MODEL(?:\s|$)/.test(line))) return [readPose(lines)];
  const poses: Pose[] = [];
  const prefix: string[] = [];
  let current: string[] | undefined;
  for (const line of lines) {
    if (/^MODEL(?:\s|$)/.test(line)) {
      if (current || poses.length >= MAX_POSES) throw new Error('PDBQT_MODELS_INVALID');
      current = [...prefix];
    } else if (/^ENDMDL(?:\s|$)/.test(line)) {
      if (!current) throw new Error('PDBQT_MODELS_INVALID');
      poses.push(readPose(current));
      current = undefined;
    } else if (current) {
      current.push(line);
    } else if (poses.length === 0 && /^REMARK (SMILES(?: IDX)? |H PARENT )/.test(line)) {
      if (prefix.length > MAX_ATOMS || line.length > MAX_ATOMS * 12) throw new Error('PDBQT_METADATA_TOO_LARGE');
      prefix.push(line);
    } else if (/^(ATOM  |HETATM)/.test(line)) {
      throw new Error('PDBQT_MODELS_INVALID');
    }
  }
  if (current || poses.length === 0) throw new Error('PDBQT_MODELS_INVALID');
  return poses;
}

const integer = (value: number, width = 3): string => String(value).padStart(width, ' ');
const coordinates = (atom: PoseAtom): string =>
  atom.coordinates.map((value) => value.toFixed(4).padStart(10, ' ')).join('');

function restorePose(pose: Pose, template: string, poseIndex: number): string {
  const lines = template.split(/\r?\n/);
  const atomCount = Number(lines[3]?.slice(0, 3));
  const bondCount = Number(lines[3]?.slice(3, 6));
  const end = lines.indexOf('M  END');
  if (
    !lines[3]?.includes('V2000') ||
    !Number.isSafeInteger(atomCount) ||
    atomCount < 1 ||
    atomCount > MAX_ATOMS ||
    !Number.isSafeInteger(bondCount) ||
    bondCount < 0 ||
    bondCount > MAX_ATOMS ||
    end < 4 + atomCount + bondCount ||
    pose.indices.size !== atomCount ||
    new Set(pose.indices.values()).size !== atomCount
  ) {
    throw new Error('PDBQT_TEMPLATE_INVALID');
  }
  const assigned = new Set<number>();
  const atoms = lines.slice(4, 4 + atomCount).map((line, index) => {
    const serial = pose.indices.get(index + 1);
    const atom = serial === undefined ? undefined : pose.atoms.get(serial);
    if (!atom || atom.element !== line.slice(31, 34).trim().toUpperCase()) throw new Error('PDBQT_ATOM_MAP_MISMATCH');
    assigned.add(atom.serial);
    return coordinates(atom) + line.slice(30);
  });
  const bonds = lines.slice(4 + atomCount, 4 + atomCount + bondCount);
  for (const [serial, parentIndex] of pose.hydrogens) {
    const atom = pose.atoms.get(serial);
    const parentSerial = pose.indices.get(parentIndex);
    if (
      !atom ||
      atom.element !== 'H' ||
      assigned.has(serial) ||
      parentSerial === undefined ||
      !assigned.has(parentSerial)
    ) {
      throw new Error('PDBQT_HYDROGEN_MAP_MISMATCH');
    }
    atoms.push(`${coordinates(atom)} H   0  0  0  0  0  0  0  0  0  0  0  0`);
    // Bracket H counts must not be counted again after those hydrogens become
    // explicitly bonded coordinate atoms. Preserve all other atom fields.
    const parentLine = atoms[parentIndex - 1];
    atoms[parentIndex - 1] = parentLine.slice(0, 42) + '  0' + parentLine.slice(45);
    bonds.push(`${integer(parentIndex)}${integer(atoms.length)}  1  0  0  0  0`);
    assigned.add(serial);
  }
  if (assigned.size !== pose.atoms.size || atoms.length > MAX_ATOMS || bonds.length > MAX_ATOMS) {
    throw new Error('PDBQT_ATOM_MAP_INCOMPLETE');
  }
  return [
    `Docking pose ${poseIndex + 1}`,
    lines[1],
    lines[2],
    `${integer(atoms.length)}${integer(bonds.length)}${lines[3].slice(6)}`,
    ...atoms,
    ...bonds,
    ...lines.slice(4 + atomCount + bondCount, end + 1),
    '$$$$',
    '',
  ].join('\n');
}

/** Local-only, bounded, all-or-nothing recovery; original artifact bytes never change. */
export async function restorePdbqtTopology(
  source: string,
  resolveSmilesMolBlock: (smiles: string) => Promise<string | null>
): Promise<PdbqtTopology | undefined> {
  try {
    const poses = readPoses(source);
    const templates = new Map<string, string>();
    const restored: string[] = [];
    for (const [index, pose] of poses.entries()) {
      let template = templates.get(pose.smiles);
      if (!template) {
        if (templates.size >= MAX_DISTINCT_SMILES) return undefined;
        template = (await resolveSmilesMolBlock(pose.smiles)) ?? undefined;
        if (!template) return undefined;
        templates.set(pose.smiles, template);
      }
      restored.push(restorePose(pose, template, index));
    }
    return {
      source: restored.join(''),
      smiles: poses.map((pose) => pose.smiles),
    };
  } catch {
    // Missing or inconsistent authority cannot turn a saturated geometry guess
    // into a chemical result. The coordinate viewer remains available instead.
    return undefined;
  }
}

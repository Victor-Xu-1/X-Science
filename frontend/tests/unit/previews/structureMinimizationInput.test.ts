// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { parsePDB } from 'molstar/lib/mol-io/reader/pdb/parser';
import { trajectoryFromPDB } from 'molstar/lib/mol-model-formats/structure/pdb';
import { Structure, StructureElement } from 'molstar/lib/mol-model/structure';
import { MULTI_LIGAND_PDB, MULTI_LIGAND_MODELS } from '../../fixtures/structureLigands';
import { createPoseAtoms } from '@/renderer/pages/conversation/Preview/components/viewers/molstarStructureEngine';
import {
  queryStructureLigands,
  splitStructureLigands,
  resolveSingleStructureLigandInstance,
  resolveStructureLigandInstanceByAtomSerial,
} from '@/renderer/pages/conversation/Preview/components/viewers/structureLigandSelection';
import {
  createStructureMinimizationPose,
  selectCurrentPdbModel,
} from '@/renderer/pages/conversation/Preview/components/viewers/structureMinimizationInput';

async function nativeFixture() {
  const parsed = await parsePDB(MULTI_LIGAND_PDB, 'native-selection').run();
  if (parsed.isError === true) throw new Error(parsed.message);
  const trajectory = await trajectoryFromPDB(parsed.result).run();
  const structure = Structure.ofModel(trajectory.representative);
  return { structure, instances: splitStructureLigands(queryStructureLigands(structure, 'pdb'), true) };
}

describe('native minimization input authority', () => {
  it('never coalesces same-name native instances or falls back on a foreign atom', async () => {
    const { structure, instances } = await nativeFixture();
    expect(resolveSingleStructureLigandInstance(structure, instances[2].loci, 'pdb')?.id).toBe(instances[2].id);
    expect(
      resolveSingleStructureLigandInstance(
        structure,
        StructureElement.Loci.union(instances[0].loci, instances[2].loci),
        'pdb'
      )
    ).toBeUndefined();
    expect(resolveSingleStructureLigandInstance(structure, undefined, 'pdb', 'UNL')).toBeUndefined();
    expect(resolveStructureLigandInstanceByAtomSerial(structure, 13, 'pdb')?.id).toBe(instances[2].id);
    expect(resolveStructureLigandInstanceByAtomSerial(structure, 99, 'pdb')).toBeUndefined();
  });

  it('binds the non-first source atom and preserves original records and full fixed context', async () => {
    const { structure, instances } = await nativeFixture();
    const atoms = createPoseAtoms(structure, undefined);
    const selected = createPoseAtoms(structure, instances[2].loci);
    const keys = new Set(selected.flatMap((atom) => (atom.nativeKey ? [atom.nativeKey] : [])));
    const original = createStructureMinimizationPose(atoms, keys, 'UNL', MULTI_LIGAND_PDB);
    expect(original).toEqual({
      content: MULTI_LIGAND_PDB,
      format: 'pdb',
      ligandResidueName: 'UNL',
      ligandAtomSerial: 13,
    });
    const projected = createStructureMinimizationPose(atoms, keys, 'UNL');
    expect(projected.content.split('\n').filter((line) => /^(ATOM  |HETATM)/.test(line))).toHaveLength(14);
    const chosen = atoms.find((atom) => atom.nativeKey && keys.has(atom.nativeKey))!;
    expect(projected.ligandAtomSerial).toBe(chosen.serial);
    expect(() => createStructureMinimizationPose(atoms, new Set(['foreign']), 'UNL')).toThrow('NOT_SELECTED');
  });

  it('isolates the actual active PDB model without losing common metadata or connectivity', () => {
    const source = 'HEADER original\r\n' + MULTI_LIGAND_MODELS + 'CONECT    9   10\r\n';
    const active = selectCurrentPdbModel(source, 1);
    expect(active).toContain('MODEL        2');
    expect(active).not.toContain('MODEL        1');
    expect(active).toContain('HEADER original\r\n');
    expect(active).toContain('CONECT    9   10\r\n');
    expect(active.match(/^MODEL /gm)).toHaveLength(1);
    expect(() => selectCurrentPdbModel(source, 2)).toThrow('MODEL_INVALID');
    expect(selectCurrentPdbModel(MULTI_LIGAND_PDB, 0)).toBe(MULTI_LIGAND_PDB);
  });
});

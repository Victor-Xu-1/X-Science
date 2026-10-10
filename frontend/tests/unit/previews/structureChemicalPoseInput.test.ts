import { describe, expect, it } from 'vitest';
import { createStructureMinimizationPose } from '@/renderer/pages/conversation/Preview/components/viewers/structureMinimizationInput';
import type { MolstarPoseAtom } from '@/renderer/pages/conversation/Preview/components/viewers/molstarPose';
import { parseSdf } from 'molstar/lib/mol-io/reader/sdf/parser';
import { trajectoryFromSdf } from 'molstar/lib/mol-model-formats/structure/sdf';
import { Structure } from 'molstar/lib/mol-model/structure';
import { createPoseAtoms } from '@/renderer/pages/conversation/Preview/components/viewers/molstarStructureEngine';
import { resolveStructureLigandInstanceByAtomSerial } from '@/renderer/pages/conversation/Preview/components/viewers/structureLigandSelection';
import { restoreChemicalPoseResult } from '@/renderer/pages/conversation/Preview/components/viewers/structureChemicalPoseResult';
import { DOUBLE_BOND_MOL as mol, DOUBLE_BOND_SDF as sdf } from '../../fixtures/structureLigands';

const atoms: MolstarPoseAtom[] = [1, 2].map((serial) => ({
  nativeKey: `unit:${serial}`,
  sourceIndex: serial - 1,
  sourceSerial: serial,
  serial,
  recordName: 'HETATM',
  atomName: `C${serial}`,
  residueName: 'MOL',
  chainId: 'A',
  residueNumber: 1,
  x: serial + 0.25,
  y: -0.5,
  z: 0.125,
  element: 'C',
}));
const keys = new Set(atoms.map((atom) => atom.nativeKey!));

describe('chemical-source minimization input', () => {
  it('restores source-owned SDF properties instead of accepting replacement chemical declarations', async () => {
    const reply = (mol.replace('    1.3400', '    1.3257') + '$$$$\n').replace('  1  2  2', '  1  2  1');
    const restored = await restoreChemicalPoseResult(
      { content: sdf, format: 'sdf', atomElements: ['C', 'C'] },
      { content: reply, format: 'sdf', filename: 'result.sdf' }
    );
    expect(restored.content).toBe(sdf.replace('    1.3400', '    1.3257'));
    expect(restored.format).toBe('sdf');
  });

  it('keeps MOL2 identities and partial charges after the provider returns SDF coordinates', async () => {
    const mol2 = `@<TRIPOS>MOLECULE\nsource molecule\n2 1 1 0 0\nSMALL\nUSER_CHARGES\n@<TRIPOS>ATOM\n42 C1 0.0 0.0 0.0 C.2 1 LIG -0.2\n77 C2 1.34 0.0 0.0 C.2 1 LIG 0.2\n@<TRIPOS>BOND\n1 42 77 2\n@<TRIPOS>SUBSTRUCTURE\n1 LIG 42\n`;
    const restored = await restoreChemicalPoseResult(
      { content: mol2, format: 'mol2', atomElements: ['C', 'C'] },
      { content: mol + '$$$$\n', format: 'sdf', filename: 'result.sdf' }
    );
    expect(restored.filename).toBe('result.mol2');
    expect(restored.format).toBe('mol2');
    expect(restored.content).toContain(' C.2 1 LIG -0.2\n');
    expect(restored.content).toContain('@<TRIPOS>BOND\n1 42 77 2\n');
  });

  it('rejects reply format, cardinality and element mismatches rather than mapping unrelated coordinates', async () => {
    const input = { content: sdf, format: 'sdf' as const, atomElements: ['C', 'C'] };
    for (const reply of [
      { content: sdf, format: 'pdb', filename: 'bad.pdb' },
      { content: sdf + sdf, format: 'sdf', filename: 'bad.sdf' },
      { content: sdf, format: 'sdf', filename: 'bad.sdf' },
    ]) {
      const source = reply.content === sdf && reply.format === 'sdf' ? { ...input, atomElements: ['C', 'N'] } : input;
      await expect(restoreChemicalPoseResult(source, reply)).rejects.toThrow('RESULT_INVALID');
    }
    await expect(
      restoreChemicalPoseResult({ ...input, atomElements: ['C'] }, { content: sdf, format: 'sdf', filename: 'bad.sdf' })
    ).rejects.toThrow('RESULT_INVALID');
  });
  it('binds real parsed SDF source rows and keeps explicit hidden hydrogens in the complete ligand', async () => {
    const explicit = `Ethene\n  X-Science\n\n  6  5  0  0  0  0            999 V2000\n    0.0000    0.0000    0.0000 C   0  0  0  0  0  0  0  0  0  0  0  0\n    1.3400    0.0000    0.0000 C   0  0  0  0  0  0  0  0  0  0  0  0\n   -0.5000    0.9000    0.0000 H   0  0  0  0  0  0  0  0  0  0  0  0\n   -0.5000   -0.9000    0.0000 H   0  0  0  0  0  0  0  0  0  0  0  0\n    1.8400    0.9000    0.0000 H   0  0  0  0  0  0  0  0  0  0  0  0 0\n    1.8400   -0.9000    0.0000 H   0  0  0  0  0  0  0  0  0  0  0  0\n  1  2  2  0  0  0  0\n  1  3  1  0  0  0  0\n  1  4  1  0  0  0  0\n  2  5  1  0  0  0  0\n  2  6  1  0  0  0  0\nM  END\n$$$$\n`;
    const parsed = await parseSdf(explicit).run();
    if (parsed.isError === true) throw new Error(parsed.message);
    expect(parsed.result.compounds).toHaveLength(1);
    const trajectory = await trajectoryFromSdf(parsed.result.compounds[0]).run();
    const structure = Structure.ofModel(trajectory.representative);
    const instance = resolveStructureLigandInstanceByAtomSerial(structure, 1, 'sdf')!;
    const nativeAtoms = createPoseAtoms(structure, undefined);
    const nativeKeys = new Set(createPoseAtoms(structure, instance.loci).map((atom) => atom.nativeKey!));
    expect(nativeAtoms.map((atom) => atom.sourceIndex).toSorted()).toEqual([0, 1, 2, 3, 4, 5]);
    expect(nativeKeys.size).toBe(6);
    const pose = createStructureMinimizationPose(nativeAtoms, nativeKeys, 'MOL', undefined, {
      format: 'sdf',
      content: explicit,
      modelIndex: 0,
    });
    expect(pose.format).toBe('sdf');
    expect(pose.content).toBe(explicit);
  });
  it('preserves declared SDF bonds, charges and record properties while using native coordinates', () => {
    const pose = createStructureMinimizationPose(atoms, keys, 'MOL', undefined, {
      format: 'sdf',
      content: sdf,
      modelIndex: 0,
    });
    expect(pose.format).toBe('sdf');
    expect(pose.ligandAtomSerial).toBeUndefined();
    const before = sdf.split('\n'),
      after = pose.content.split('\n');
    expect(after).toHaveLength(before.length);
    before.forEach((line, index) => {
      if (index === 4 || index === 5) {
        expect(after[index].slice(30)).toBe(line.slice(30));
        expect(Number(after[index].slice(0, 10))).toBe(atoms[index - 4].x);
      } else expect(after[index]).toBe(line);
    });
  });

  it('maps sorted native atoms by source row rather than display order or author serial', () => {
    const reordered = [
      { ...atoms[1], sourceSerial: 77 },
      { ...atoms[0], sourceSerial: 42 },
    ];
    const pose = createStructureMinimizationPose(reordered, keys, 'MOL', undefined, {
      format: 'mol',
      content: mol.replaceAll('\n', '\r\n'),
      modelIndex: 0,
    });
    expect(pose.content.split('\r\n')[4].slice(0, 10).trim()).toBe('1.2500');
    expect(pose.content).toContain('M  CHG  1   1  -1\r\n');
  });

  it('selects the actual SDF trajectory record without submitting alternative molecules', () => {
    const other = sdf.replace('Declared double bond', 'Second source').replace('  1  2  2', '  1  2  3');
    const pose = createStructureMinimizationPose(atoms, keys, 'MOL', undefined, {
      format: 'sdf',
      content: sdf + other,
      modelIndex: 1,
    });
    expect(pose.content).toContain('Second source');
    expect(pose.content).not.toContain('Declared double bond');
    expect(pose.content).toContain('  1  2  3');
    expect(pose.content.match(/\$\$\$\$/g)).toHaveLength(1);
  });

  it('preserves V3000 atom IDs, bond references, charge, isotope and continuation properties', () => {
    const v3 = `V3000 source\n  X-Science\n\n  0  0  0  0  0  0            999 V3000\nM  V30 BEGIN CTAB\nM  V30 COUNTS 2 1 0 0 0\nM  V30 BEGIN ATOM\nM  V30 42 C 0 0 0 0 CHG=-1 -\nM  V30 MASS=13 CFG=1\nM  V30 77 C 1.34 0 0 0\nM  V30 END ATOM\nM  V30 BEGIN BOND\nM  V30 1 2 42 77 CFG=1\nM  V30 END BOND\nM  V30 END CTAB\nM  END\n`;
    const pose = createStructureMinimizationPose(atoms, keys, 'MOL', undefined, {
      format: 'mol',
      content: v3,
      modelIndex: 0,
    });
    expect(pose.content).toContain(
      'M  V30 42 C 1.25000000000000 -0.500000000000000 0.125000000000000 0 CHG=-1 -\nM  V30 MASS=13 CFG=1'
    );
    expect(pose.content).toContain('M  V30 1 2 42 77 CFG=1');
  });

  it('preserves MOL2 bond types, source IDs and partial charges', () => {
    const mol2 = `@<TRIPOS>MOLECULE\nsource molecule\n2 1 1 0 0\nSMALL\nUSER_CHARGES\n@<TRIPOS>ATOM\n42 C1 0.0 0.0 0.0 C.2 1 LIG -0.2\n77 C2 1.34 0.0 0.0 C.2 1 LIG 0.2\n@<TRIPOS>BOND\n1 42 77 2\n@<TRIPOS>SUBSTRUCTURE\n1 LIG 42\n`;
    const pose = createStructureMinimizationPose(atoms, keys, 'LIG', undefined, {
      format: 'mol2',
      content: mol2,
      modelIndex: 0,
    });
    expect(pose.format).toBe('mol2');
    expect(pose.content).toContain('42 C1 1.25000000000000');
    expect(pose.content).toContain(' C.2 1 LIG -0.2\n');
    expect(pose.content).toContain('@<TRIPOS>BOND\n1 42 77 2\n');
  });

  it('rejects partial selections, duplicate/missing source rows and unrepresentable coordinates', () => {
    const chemical = { format: 'sdf' as const, content: sdf, modelIndex: 0 };
    expect(() => createStructureMinimizationPose(atoms, new Set(['unit:1']), 'MOL', undefined, chemical)).toThrow(
      'PARTITION_INVALID'
    );
    for (const broken of [
      [{ ...atoms[0], sourceIndex: undefined }, atoms[1]],
      [atoms[0], { ...atoms[1], sourceIndex: 0 }],
      [atoms[0], { ...atoms[1], sourceIndex: 8 }],
      [atoms[0], { ...atoms[1], x: Infinity }],
      [atoms[0], { ...atoms[1], x: 1e9 }],
    ])
      expect(() => createStructureMinimizationPose(broken, keys, 'MOL', undefined, chemical)).toThrow(
        'MAPPING_INVALID'
      );
    expect(() =>
      createStructureMinimizationPose(atoms, keys, 'MOL', undefined, { ...chemical, modelIndex: 1 })
    ).toThrow('MAPPING_INVALID');
  });
});

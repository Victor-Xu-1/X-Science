import { createRequire } from 'node:module';
import type { RDKitLoader, RDKitModule } from '@rdkit/rdkit';
import { Color } from 'molstar/lib/mol-util/color';
import { describe, expect, it } from 'vitest';
import {
  formatDockingLigandMolBlock,
  type DockingLigandShapeData,
} from '@/renderer/pages/conversation/Preview/components/viewers/molstarDockingLigandShape';

type ParsedLigand = {
  atoms: { z?: number; chg?: number }[];
  bonds: { atoms: [number, number]; bo?: number }[];
};

const loadRdkit = createRequire(import.meta.url)('@rdkit/rdkit') as RDKitLoader;
let rdkit: Promise<RDKitModule> | undefined;

async function readLigand(molBlock: string): Promise<ParsedLigand> {
  const module = await (rdkit ??= loadRdkit());
  const molecule = module.get_mol(molBlock, JSON.stringify({ removeHs: false }));
  if (!molecule) throw new Error('RDKit could not parse the generated ligand');
  try {
    const result = JSON.parse(molecule.get_json()) as { molecules: [ParsedLigand] };
    return result.molecules[0];
  } finally {
    molecule.delete();
  }
}

const carbonColor = Color(0x0f766e);
const ligandAtom = (index: number, element = 'C', formalCharge = 0): DockingLigandShapeData['atoms'][number] => ({
  position: [index * 1.25, 0, 0],
  element,
  label: `${element}${index + 1}`,
  residueName: 'LIG',
  formalCharge,
  carbonColor,
});

const ligandBond = (atomA: number, atomB: number, order = 1): DockingLigandShapeData['bonds'][number] => ({
  atomA,
  atomB,
  aromatic: false,
  order,
});

function chain(atomCount: number) {
  return {
    atoms: Array.from({ length: atomCount }, (_, index) => ligandAtom(index)),
    bonds: Array.from({ length: atomCount - 1 }, (_, index) => ligandBond(index, index + 1)),
    aromaticCycles: [],
    carbonColor,
  };
}

describe('Molfile format selection for ligand preview', () => {
  it.each([999, 1000])('round-trips a ligand with %i atoms', async (atomCount) => {
    const shape = chain(atomCount);
    const molBlock = formatDockingLigandMolBlock(shape);
    expect(molBlock.split('\n')[3]).toContain(atomCount === 999 ? 'V2000' : 'V3000');
    const ligand = await readLigand(molBlock);
    expect(ligand.atoms).toHaveLength(atomCount);
    expect(ligand.bonds).toHaveLength(atomCount - 1);
    expect(ligand.bonds.at(-1)?.atoms).toEqual([atomCount - 2, atomCount - 1]);
  });

  it.each([999, 1000])('round-trips a ligand with 999 atoms and %i bonds', async (bondCount) => {
    const shape = chain(999);
    shape.bonds.push(ligandBond(0, 2));
    if (bondCount === 1000) shape.bonds.push(ligandBond(3, 5));
    const molBlock = formatDockingLigandMolBlock(shape);
    expect(molBlock.split('\n')[3]).toContain(bondCount === 999 ? 'V2000' : 'V3000');
    const ligand = await readLigand(molBlock);
    expect(ligand.atoms).toHaveLength(999);
    expect(ligand.bonds).toHaveLength(bondCount);
    expect(ligand.bonds.at(-1)?.atoms).toEqual(bondCount === 999 ? [0, 2] : [3, 5]);
  });

  it('counts retained explicit hydrogen and preserves charges and multiple bonds', async () => {
    const shape = chain(999);
    shape.atoms[998] = ligandAtom(998, 'O', -1);
    shape.atoms.push(ligandAtom(999, 'H'), ligandAtom(1000, 'H'));
    shape.bonds[500] = ligandBond(500, 501, 2);
    shape.bonds[800] = ligandBond(800, 801, 3);
    shape.bonds.push(ligandBond(0, 999));
    const molBlock = formatDockingLigandMolBlock(shape);
    expect(molBlock.split('\n')[3]).toContain('V3000');
    const ligand = await readLigand(molBlock);
    expect(ligand.atoms).toHaveLength(1000);
    expect(ligand.bonds).toHaveLength(999);
    expect(ligand.atoms[998]).toMatchObject({ z: 8, chg: -1 });
    expect(ligand.atoms[999]).toMatchObject({ z: 1 });
    expect(ligand.bonds[500]).toMatchObject({ atoms: [500, 501], bo: 2 });
    expect(ligand.bonds[800]).toMatchObject({ atoms: [800, 801], bo: 3 });
    expect(ligand.bonds.at(-1)?.atoms).toEqual([0, 999]);
  });

  it('uses the same declared aromatic bond policy in V3000', async () => {
    const shape = chain(994);
    for (let offset = 0; offset < 6; offset += 1) {
      shape.atoms.push(ligandAtom(994 + offset));
      shape.bonds.push({
        ...ligandBond(994 + offset, 994 + ((offset + 1) % 6), offset % 2 === 0 ? 2 : 1),
        aromatic: true,
        chemicalAromatic: true,
      });
    }
    const aromatic = formatDockingLigandMolBlock(shape);
    const kekule = formatDockingLigandMolBlock(shape, 'Ligand', { preserveAromaticBonds: false });
    expect(aromatic).toContain('M  V30 994 4 995 996\n');
    expect(kekule).toContain('M  V30 994 2 995 996\n');
    expect((await readLigand(aromatic)).bonds).toHaveLength(999);
    expect((await readLigand(kekule)).bonds).toHaveLength(999);
  });
});

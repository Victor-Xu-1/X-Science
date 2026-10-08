import { describe, expect, it } from 'vitest';
import { parseMol } from 'molstar/lib/mol-io/reader/mol/parser';
import { trajectoryFromMol } from 'molstar/lib/mol-model-formats/structure/mol';
import { OrderedSet } from 'molstar/lib/mol-data/int';
import { Structure, StructureElement } from 'molstar/lib/mol-model/structure';
import { Color } from 'molstar/lib/mol-util/color';
import {
  createDockingLigandShapeData,
  formatDockingLigandMolBlock,
} from '@/renderer/pages/conversation/Preview/components/viewers/molstarDockingLigandShape';

const integer = (value: number) => String(value).padStart(3, ' ');
const atomLine = (x: number, y: number, element: string) =>
  `${x.toFixed(4).padStart(10, ' ')}${y.toFixed(4).padStart(10, ' ')}    0.0000 ${element.padEnd(
    3,
    ' '
  )} 0  0  0  0  0  0  0  0  0  0  0  0`;

async function shapeFromMol(atoms: string[], bonds: Array<[number, number, number]>) {
  const source = [
    'Synthetic bond-order regression',
    '  X-Science',
    '',
    `${integer(atoms.length)}${integer(bonds.length)}  0  0  0  0            999 V2000`,
    ...atoms,
    ...bonds.map(([a, b, order]) => `${integer(a)}${integer(b)}${integer(order)}  0  0  0  0`),
    'M  END',
    '',
  ].join('\n');
  const parsed = await parseMol(source).run();
  if (parsed.isError) throw new Error(parsed.message);
  const trajectory = await trajectoryFromMol(parsed.result).run();
  const structure = Structure.ofModel(trajectory.representative);
  const loci = StructureElement.Loci(
    structure,
    structure.units.map((unit) => ({
      unit,
      indices: OrderedSet.ofBounds(0, unit.elements.length),
    }))
  );
  return createDockingLigandShapeData(loci, Color(0x0f766e));
}

function ordersFromMolBlock(source: string) {
  const lines = source.split('\n');
  const atoms = Number(lines[3].slice(0, 3));
  const bonds = Number(lines[3].slice(3, 6));
  return lines.slice(4 + atoms, 4 + atoms + bonds).map((line) => Number(line.slice(6, 9)));
}

describe('ligand chemical bond orders through the real Mol* parser', () => {
  it('does not flatten declared Kekule double bonds when adding a planar-ring display accent', async () => {
    const atoms = Array.from({ length: 6 }, (_, index) => {
      const angle = (index * Math.PI) / 3;
      return atomLine(Math.cos(angle) * 1.4, Math.sin(angle) * 1.4, 'C');
    });
    const bonds = Array.from({ length: 6 }, (_, index): [number, number, number] => [
      index + 1,
      ((index + 1) % 6) + 1,
      index % 2 === 0 ? 2 : 1,
    ]);
    const shape = await shapeFromMol(atoms, bonds);
    expect(shape.aromaticCycles).toHaveLength(0);
    expect(shape.bonds.filter((bond) => bond.displayOrder === 2)).toHaveLength(3);
    expect(ordersFromMolBlock(formatDockingLigandMolBlock(shape)).filter((order) => order === 2)).toHaveLength(3);
  });

  it('keeps declared single bonds even at a geometry-only double-bond-like distance', async () => {
    const shape = await shapeFromMol([atomLine(0, 0, 'C'), atomLine(1.2, 0, 'O')], [[1, 2, 1]]);
    expect(ordersFromMolBlock(formatDockingLigandMolBlock(shape))).toEqual([1]);
    expect(shape.bonds[0].displayOrder).toBe(1);
  });

  it('recognizes explicit MDL aromatic bond type 4 instead of converting it to a triple bond', async () => {
    const atoms = Array.from({ length: 6 }, (_, index) => {
      const angle = (index * Math.PI) / 3;
      return atomLine(Math.cos(angle) * 1.4, Math.sin(angle) * 1.4, 'C');
    });
    const bonds = Array.from({ length: 6 }, (_, index): [number, number, number] => [
      index + 1,
      ((index + 1) % 6) + 1,
      4,
    ]);
    const shape = await shapeFromMol(atoms, bonds);
    expect(ordersFromMolBlock(formatDockingLigandMolBlock(shape))).toEqual(Array(6).fill(4));
  });

  it('preserves carbonyl and triple bond orders without relying on coordinates', async () => {
    const carbonyl = await shapeFromMol([atomLine(0, 0, 'C'), atomLine(1.5, 0, 'O')], [[1, 2, 2]]);
    const triple = await shapeFromMol([atomLine(0, 0, 'C'), atomLine(1.5, 0, 'N')], [[1, 2, 3]]);
    expect(ordersFromMolBlock(formatDockingLigandMolBlock(carbonyl))).toEqual([2]);
    expect(ordersFromMolBlock(formatDockingLigandMolBlock(triple))).toEqual([3]);
  });
});

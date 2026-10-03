import { describe, expect, it, vi } from 'vitest';
import { restorePdbqtTopology } from '@/renderer/pages/conversation/Preview/components/viewers/pdbqtTopology';

const atom = (serial: number, element: string, x: number) =>
  `HETATM${String(serial).padStart(5)} ${element.padEnd(4)} UNL A   1    ${x
    .toFixed(3)
    .padStart(8)}   0.000   0.000  1.00  0.00     0.000 ${element}`;
const template = `Synthetic aldehyde
  RDKit

  3  2  0  0  0  0            999 V2000
    0.0000    0.0000    0.0000 C   0  0  0  0  0  0  0  0  0  0  0  0
    0.0000    0.0000    0.0000 C   0  0  0  0  0  0  0  0  0  0  0  0
    0.0000    0.0000    0.0000 O   0  0  0  0  0  0  0  0  0  0  0  0
  1  2  1  0  0  0  0
  2  3  2  0  0  0  0
M  END
`;
const pose = (index: number, extra = '') =>
  [
    `MODEL ${index}`,
    'REMARK SMILES CC=O',
    'REMARK SMILES IDX 1 3 2 1',
    'REMARK SMILES IDX 3 2',
    extra,
    atom(1, 'C', index + 10),
    atom(2, 'O', index + 20),
    atom(3, 'C', index + 30),
    'ENDMDL',
  ]
    .filter(Boolean)
    .join('\n');

describe('PDBQT embedded topology authority', () => {
  it('restores exact bond orders and atom-map coordinates for every pose with one local template parse', async () => {
    const input = Array.from({ length: 9 }, (_, index) => pose(index + 1)).join('\n');
    const resolver = vi.fn(async () => template);
    const result = await restorePdbqtTopology(input, resolver);
    expect(resolver).toHaveBeenCalledExactlyOnceWith('CC=O');
    expect(result?.smiles).toEqual(Array(9).fill('CC=O'));
    const models = result!.source.split('$$$$\n').filter(Boolean);
    expect(models).toHaveLength(9);
    for (const [index, model] of models.entries()) {
      const lines = model.split('\n');
      expect(Number(lines[4].slice(0, 10))).toBe(index + 31);
      expect(Number(lines[5].slice(0, 10))).toBe(index + 11);
      expect(Number(lines[6].slice(0, 10))).toBe(index + 21);
      expect(lines[8].slice(6, 9)).toBe('  2');
    }
    expect(input).toBe(Array.from({ length: 9 }, (_, index) => pose(index + 1)).join('\n'));
  });

  it('keeps explicitly mapped polar hydrogens and their original positions', async () => {
    const input = pose(1, 'REMARK H PARENT 1 4').replace('ENDMDL', `${atom(4, 'HD', 40)}\nENDMDL`);
    const result = await restorePdbqtTopology(input, async () => template);
    const lines = result!.source.split('\n');
    expect(lines[3].slice(0, 6)).toBe('  4  3');
    expect(Number(lines[7].slice(0, 10))).toBe(40);
    expect(lines[10].slice(0, 9)).toBe('  1  4  1');
  });

  it.each([
    ['missing smiles', pose(1).replace('REMARK SMILES CC=O', '')],
    ['duplicate indices', pose(1, 'REMARK SMILES IDX 1 2')],
    ['noninjective map', pose(1).replace('3 2', '3 1')],
    ['incomplete map', pose(1).replace('REMARK SMILES IDX 3 2', '')],
    ['wrong element', pose(1).replace(atom(2, 'O', 21), atom(2, 'N', 21))],
    ['nonfinite coordinates', pose(1).replace('  11.000', '     NaN')],
    ['unmapped extra atom', pose(1).replace('ENDMDL', `${atom(4, 'C', 40)}\nENDMDL`)],
    ['partial model', pose(1).replace('ENDMDL', '')],
    ['one invalid pose', `${pose(1)}\n${pose(2).replace('REMARK SMILES CC=O', '')}`],
  ])('does not fabricate topology for %s', async (_name, input) => {
    expect(await restorePdbqtTopology(input, async () => template)).toBeUndefined();
  });

  it('leaves coordinate preview available when the local chemistry runtime cannot parse metadata', async () => {
    expect(await restorePdbqtTopology(pose(1), async () => null)).toBeUndefined();
    expect(
      await restorePdbqtTopology(pose(1), async () => {
        throw new Error('unavailable');
      })
    ).toBeUndefined();
  });
});

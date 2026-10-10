import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { StructureElement, type Structure, type Unit } from 'molstar/lib/mol-model/structure';
import { OrderedSet } from 'molstar/lib/mol-data/int';
import {
  POCKET_RESIDUE_LABEL_TYPE_PARAMS,
  resolvePocketReadingFocusRadius,
} from '@/renderer/pages/conversation/Preview/components/viewers/molstarPocketReading';

const positions = [0, 8, 10, 500];
const unit = {
  id: 7,
  elements: [0, 1, 2, 3],
  conformation: {
    position: (index: number, target: number[]) => {
      target[0] = positions[index];
      target[1] = 0;
      target[2] = 0;
      return target;
    },
    r: () => 0,
  },
} as unknown as Unit.Atomic;
const structure = { units: [unit], unitMap: new Map([[7, unit]]), elementCount: 4 } as unknown as Structure;
const locus = (start: number, end: number) =>
  StructureElement.Loci(structure, [{ unit, indices: OrderedSet.ofBounds(start, end) }]);

describe('pocket reading presentation', () => {
  it('frames exactly the ligand and displayed contact atoms without mutating either selection', () => {
    const ligand = locus(0, 1),
      contacts = locus(1, 3);
    const radius = resolvePocketReadingFocusRadius(ligand, contacts, 5.5);
    expect(radius).toBeCloseTo(10);
    expect(StructureElement.Loci.size(ligand)).toBe(1);
    expect(StructureElement.Loci.size(contacts)).toBe(2);
    expect(unit.elements).toEqual([0, 1, 2, 3]);
    expect(positions).toEqual([0, 8, 10, 500]);
  });
  it('retains ligand framing when no contact residues are displayed', () => {
    expect(resolvePocketReadingFocusRadius(locus(0, 1), StructureElement.Loci(structure, []), 5.5)).toBe(5.5);
    expect(resolvePocketReadingFocusRadius(locus(0, 1), locus(1, 3), 20)).toBe(20);
  });
  it('keeps native complete residue labels as compact tethered callouts', () => {
    expect(POCKET_RESIDUE_LABEL_TYPE_PARAMS).toMatchObject({
      level: 'residue',
      tether: true,
      attachment: 'top-center',
    });
    expect(POCKET_RESIDUE_LABEL_TYPE_PARAMS.sizeFactor).toBeLessThan(0.68);
    expect(POCKET_RESIDUE_LABEL_TYPE_PARAMS.sizeFactor).toBeGreaterThan(0);
    expect(POCKET_RESIDUE_LABEL_TYPE_PARAMS.backgroundOpacity).toBeGreaterThan(0.75);
  });
  it('is consumed by the existing native label graph and guarded camera path', () => {
    const source = readFileSync(
      resolve(
        process.cwd(),
        'packages/desktop/src/renderer/pages/conversation/Preview/components/viewers/molstarStructureEngine.ts'
      ),
      'utf8'
    );
    expect(source).toContain('...POCKET_RESIDUE_LABEL_TYPE_PARAMS');
    expect(source).toMatch(
      /if \(shouldFocusPocketCamera\(options\)\)\s*\{\s*plugin\.managers\.camera\.focusLoci\(ligandLoci,/
    );
    expect(source).toContain('minRadius: resolvePocketReadingFocusRadius(');
  });
});

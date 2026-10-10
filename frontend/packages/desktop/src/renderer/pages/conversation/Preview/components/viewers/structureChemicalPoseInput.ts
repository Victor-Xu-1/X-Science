export type ChemicalPoseCoordinates = { sourceIndex?: number; x: number; y: number; z: number };

export type ChemicalPoseFormat = 'mol' | 'sdf' | 'mol2';
export type ChemicalPoseSource = { content: string; format: ChemicalPoseFormat; modelIndex: number };
export const isChemicalPoseFormat = (format: string): format is ChemicalPoseFormat =>
  format === 'mol' || format === 'sdf' || format === 'mol2';

const invalid = (): never => {
  throw new Error('MOLSTAR_MINIMIZATION_CHEMICAL_SOURCE_MAPPING_INVALID');
};
const linesOf = (source: string): string[] => source.match(/[^\n]*\n|[^\n]+$/g) ?? [];

/** Select a parser-owned trajectory record, without interpreting or rewriting its chemical graph. */
function selectRecord(source: ChemicalPoseSource): string[] {
  if (!Number.isSafeInteger(source.modelIndex) || source.modelIndex < 0) return invalid();
  const lines = linesOf(source.content);
  const records: string[][] = [];
  if (source.format === 'sdf') {
    let start = 0;
    lines.forEach((line, index) => {
      if (line.trim() !== '$$$$') return;
      records.push(lines.slice(start, index + 1));
      start = index + 1;
    });
    if (lines.slice(start).join('').trim()) records.push(lines.slice(start));
  } else if (source.format === 'mol2') {
    const starts = lines.flatMap((line, index) => (line.trim() === '@<TRIPOS>MOLECULE' ? [index] : []));
    starts.forEach((start, index) => records.push(lines.slice(start, starts[index + 1])));
  } else records.push(lines);
  return records[source.modelIndex] ?? invalid();
}

/** Change only coordinate columns; native source rows bind atoms independently of sorting or author IDs. */
export function formatChemicalPoseInput(source: ChemicalPoseSource, atoms: readonly ChemicalPoseCoordinates[]): string {
  const lines = selectRecord(source);
  const byRow = new Map<number, ChemicalPoseCoordinates>();
  for (const atom of atoms) {
    if (!Number.isSafeInteger(atom.sourceIndex) || atom.sourceIndex! < 0 || byRow.has(atom.sourceIndex!))
      return invalid();
    if (![atom.x, atom.y, atom.z].every(Number.isFinite)) return invalid();
    byRow.set(atom.sourceIndex!, atom);
  }
  let rows = 0;
  const coordinates = (): number[] => {
    const atom = byRow.get(rows++);
    if (!atom) return invalid();
    return [atom.x, atom.y, atom.z];
  };
  if (source.format === 'mol2') {
    const declared = Number(lines[2]?.trim().split(/\s+/)[0]);
    let inAtoms = false;
    lines.forEach((line, index) => {
      if (line.trim().startsWith('@<TRIPOS>')) {
        inAtoms = line.trim() === '@<TRIPOS>ATOM';
        return;
      }
      if (!inAtoms || !line.trim() || line.trimStart().startsWith('#')) return;
      const match = /^(\s*\d+\s+\S+\s+)(\S+)(\s+)(\S+)(\s+)(\S+)([\s\S]*)$/.exec(line);
      if (!match) return invalid();
      const [x, y, z] = coordinates().map((value) => value.toPrecision(15));
      lines[index] = `${match[1]}${x}${match[3]}${y}${match[5]}${z}${match[7]}`;
    });
    if (rows !== declared) return invalid();
  } else if (lines[3]?.includes('V3000')) {
    const countLine = lines.find((line) => /^M\s+V30\s+COUNTS\s/.test(line));
    const declared = Number(countLine?.trim().split(/\s+/)[3]);
    let inAtoms = false;
    lines.forEach((line, index) => {
      if (/^M\s+V30\s+BEGIN ATOM\s*$/.test(line)) {
        inAtoms = true;
        return;
      }
      if (/^M\s+V30\s+END ATOM\s*$/.test(line)) {
        inAtoms = false;
        return;
      }
      if (!inAtoms || !/^M\s+V30\s+\d+\s/.test(line)) return;
      const match = /^(M\s+V30\s+\d+\s+\S+\s+)(\S+)(\s+)(\S+)(\s+)(\S+)([\s\S]*)$/.exec(line);
      if (!match) return invalid();
      const [x, y, z] = coordinates().map((value) => value.toPrecision(15));
      lines[index] = `${match[1]}${x}${match[3]}${y}${match[5]}${z}${match[7]}`;
    });
    if (rows !== declared) return invalid();
  } else {
    const declared = Number(lines[3]?.slice(0, 3).trim());
    if (!Number.isSafeInteger(declared) || declared < 1 || lines.length < 4 + declared) return invalid();
    for (let index = 4; index < 4 + declared; index++) {
      const fields = coordinates().map((value) => {
        const text = value.toFixed(4);
        if (text.length > 10) return invalid();
        return text.padStart(10, ' ');
      });
      lines[index] = fields.join('') + lines[index].slice(30);
    }
  }
  if (rows !== atoms.length || rows === 0) return invalid();
  return lines.join('');
}

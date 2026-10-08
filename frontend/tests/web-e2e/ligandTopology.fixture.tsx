import '@arco-design/web-react/es/_util/react-19-adapter';
import '@arco-design/web-react/dist/css/arco.css';
import 'uno.css';
import '@/renderer/styles/themes/index.css';
import '@/renderer/styles/workspace-theme.css';
import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import preview from '@/renderer/services/i18n/locales/zh-CN/preview.json';
import common from '@/renderer/services/i18n/locales/zh-CN/common.json';
import SynonBiomedStructureViewer from '@/renderer/pages/conversation/Preview/components/viewers/SynonBiomedStructureViewer';
import { parseSmilesMolBlock, renderMoleculeSvg, validateAndRenderMolBlock } from '@/renderer/services/rdkitBrowser';
import { restorePdbqtTopology } from '@/renderer/pages/conversation/Preview/components/viewers/pdbqtTopology';

const positions = Array.from({ length: 6 }, (_, index) => {
  const angle = (index * Math.PI) / 3;
  return [Math.cos(angle) * 1.4, Math.sin(angle) * 1.4, 0];
}).concat([
  [1.4, -2.4249, 0],
  [2.1, -3.6373, 0],
]);
const elements = Array(7).fill('C').concat('O');
const bonds = Array.from({ length: 6 }, (_, index) => [
  index + 1,
  ((index + 1) % 6) + 1,
  index % 2 === 0 ? 2 : 1,
]).concat([
  [6, 7, 1],
  [7, 8, 2],
]);
const integer = (value: number) => String(value).padStart(3, ' ');
const mol = [
  'Synthetic benzaldehyde',
  '  X-Science',
  '',
  '  8  8  0  0  0  0            999 V2000',
  ...positions.map(
    (position, index) =>
      `${position.map((value) => value.toFixed(4).padStart(10, ' ')).join('')} ${elements[index].padEnd(
        3
      )} 0  0  0  0  0  0  0  0  0  0  0  0`
  ),
  ...bonds.map(([a, b, order]) => `${integer(a)}${integer(b)}${integer(order)}  0  0  0  0`),
  'M  END',
  '',
].join('\n');
function coordinateLines(pose: number, pdbqt: boolean) {
  return positions
    .map((position, index) => {
      const type = index < 6 ? 'A' : index === 7 ? 'OA' : 'C';
      const atom = `HETATM${String(index + 1).padStart(5)} ${(elements[index] + (index + 1)).padEnd(4)} UNL A   1    ${(
        position[0] + pose
      )
        .toFixed(3)
        .padStart(8)}${position[1].toFixed(3).padStart(8)}   0.000  1.00  0.00`;
      return pdbqt ? `${atom}     0.000 ${type}` : `${atom}          ${elements[index].padStart(2)}`;
    })
    .join('\n');
}
const pdbqt = Array.from({ length: 9 }, (_, index) =>
  [
    `MODEL ${index + 1}`,
    'REMARK SMILES c1ccccc1C=O',
    'REMARK SMILES IDX 1 1 2 2 3 3 4 4 5 5 6 6 7 7 8 8',
    coordinateLines(index, true),
    'ENDMDL',
    '',
  ].join('\n')
).join('');
const mol2 = [
  '@<TRIPOS>MOLECULE',
  'Synthetic benzaldehyde',
  '8 8 1 0 0',
  'SMALL',
  'NO_CHARGES',
  '',
  '@<TRIPOS>ATOM',
  ...positions.map(
    (position, index) =>
      `${index + 1} ${elements[index]}${index + 1} ${position.join(' ')} ${
        index < 6 ? 'C.ar' : index === 7 ? 'O.2' : 'C.2'
      } 1 BEN 0`
  ),
  '@<TRIPOS>BOND',
  ...bonds.map(([a, b, order], index) => `${index + 1} ${a} ${b} ${index < 6 ? 'ar' : order}`),
  '@<TRIPOS>SUBSTRUCTURE',
  '1 BEN 1',
  '',
].join('\n');
const cif = [
  'data_BEN',
  '_chem_comp.id BEN',
  '_chem_comp.type NON-POLYMER',
  'loop_',
  '_chem_comp_atom.comp_id',
  '_chem_comp_atom.atom_id',
  '_chem_comp_atom.type_symbol',
  '_chem_comp_atom.charge',
  '_chem_comp_atom.model_Cartn_x',
  '_chem_comp_atom.model_Cartn_y',
  '_chem_comp_atom.model_Cartn_z',
  '_chem_comp_atom.pdbx_model_Cartn_x_ideal',
  '_chem_comp_atom.pdbx_model_Cartn_y_ideal',
  '_chem_comp_atom.pdbx_model_Cartn_z_ideal',
  ...positions.map(
    (position, index) =>
      `BEN ${elements[index]}${index + 1} ${elements[index]} 0 ${position.join(' ')} ${position.join(' ')}`
  ),
  'loop_',
  '_chem_comp_bond.comp_id',
  '_chem_comp_bond.atom_id_1',
  '_chem_comp_bond.atom_id_2',
  '_chem_comp_bond.value_order',
  '_chem_comp_bond.pdbx_aromatic_flag',
  ...bonds.map(
    ([a, b, order], index) =>
      `BEN ${elements[a - 1]}${a} ${elements[b - 1]}${b} ${order === 2 ? 'DOUB' : 'SING'} ${index < 6 ? 'Y' : 'N'}`
  ),
  '',
].join('\n');
const sources: Record<string, string> = {
  sdf: mol + '$$$$\n',
  mol,
  mol2,
  cif,
  pdbqt,
  pdb: coordinateLines(0, false) + '\nEND\n',
};
async function startFixture() {
  await i18n.use(initReactI18next).init({
    lng: 'zh-CN',
    resources: { 'zh-CN': { translation: { preview, common } } },
    interpolation: { escapeValue: false },
  });
  const referenceSvg = await renderMoleculeSvg('c1ccccc1C=O', 276, 189);
  const emptyHeader = await parseSmilesMolBlock('C=O');
  const emptyHeaderValid = Boolean(emptyHeader && (await validateAndRenderMolBlock(emptyHeader, 276, 189)));
  const chemistryValidation = await Promise.all(
    [
      ['c1cc[nH]c1', 4, 1],
      ['C[NH2+]C', 2, 2],
      ['N#CC(=O)O', 0, 0],
    ].map(async ([source, parent, count]) => {
      const smiles = String(source);
      const template = await parseSmilesMolBlock(smiles);
      if (!template) return false;
      const lines = template.split('\n');
      const atoms = Number(lines[3].slice(0, 3));
      const records = lines.slice(4, 4 + atoms).map((line, index) => {
        const element = line.slice(31, 34).trim();
        return `HETATM${String(index + 1).padStart(5)} ${element.padEnd(4)} UNL A   1    ${index.toFixed(3).padStart(8)}   0.000   0.000  1.00  0.00     0.000 ${element}`;
      });
      const hydrogenPairs = Array.from({ length: Number(count) }, (_, index) => `${parent} ${atoms + index + 1}`);
      records.push(
        ...hydrogenPairs.map(
          (_pair, index) =>
            `HETATM${String(atoms + index + 1).padStart(5)} H    UNL A   1       0.000   1.000   0.000  1.00  0.00     0.000 HD`
        )
      );
      const metadata = [
        `REMARK SMILES ${smiles}`,
        `REMARK SMILES IDX ${Array.from({ length: atoms }, (_, index) => `${index + 1} ${index + 1}`).join(' ')}`,
      ];
      if (hydrogenPairs.length) metadata.push(`REMARK H PARENT ${hydrogenPairs.join(' ')}`);
      const recovered = await restorePdbqtTopology([...metadata, ...records].join('\n'), parseSmilesMolBlock);
      const restored = recovered ? await validateAndRenderMolBlock(recovered.source.split('$$$$')[0], 276, 189) : null;
      const expected = await validateAndRenderMolBlock(smiles, 276, 189);
      const normalized = restored ? await validateAndRenderMolBlock(restored.smiles, 276, 189) : null;
      if (!normalized || normalized.smiles !== expected?.smiles)
        console.error('Synthetic chemistry roundtrip', {
          smiles,
          restored: restored?.smiles,
          normalized: normalized?.smiles,
          recovered: Boolean(recovered),
          expected: expected?.smiles,
        });
      return Boolean(normalized && expected && normalized.smiles === expected.smiles);
    })
  );
  function Fixture() {
    const [format, setFormat] = useState('sdf');
    return (
      <main style={{ padding: 20, width: 600, height: 840 }}>
        <nav aria-label='Synthetic formats'>
          {Object.keys(sources).map((value) => (
            <button key={value} onClick={() => setFormat(value)}>
              {value}
            </button>
          ))}
        </nav>
        <div hidden data-testid='reference-svg' dangerouslySetInnerHTML={{ __html: referenceSvg ?? '' }} />
        <output hidden data-testid='chemistry-validation'>
          {JSON.stringify([...chemistryValidation, emptyHeaderValid])}
        </output>
        <section style={{ height: 780 }}>
          <SynonBiomedStructureViewer filename={`synthetic-benzaldehyde.${format}`} content={sources[format]} />
        </section>
      </main>
    );
  }
  createRoot(document.getElementById('root')!).render(<Fixture />);
}
// Worker replies must not depend on a top-level module evaluation completing.
// Match the application's asynchronous post-bootstrap renderer lifecycle.
void startFixture();

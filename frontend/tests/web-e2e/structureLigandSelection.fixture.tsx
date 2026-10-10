import '@arco-design/web-react/es/_util/react-19-adapter';
import '@arco-design/web-react/dist/css/arco.css';
import 'uno.css';
import '@/renderer/styles/themes/index.css';
import '@/renderer/styles/workspace-theme.css';
import React, { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import preview from '@/renderer/services/i18n/locales/zh-CN/preview.json';
import common from '@/renderer/services/i18n/locales/zh-CN/common.json';
import SynonBiomedStructureViewer from '@/renderer/pages/conversation/Preview/components/viewers/SynonBiomedStructureViewer';
import {
  MULTI_LIGAND_PDB,
  MULTI_LIGAND_CIF,
  MULTI_LIGAND_MODELS,
  MULTI_NAMED_LIGAND_PDB,
  MULTI_LIGAND_BONDED_CIF,
} from '../fixtures/structureLigands';
import {
  createMolstarStructureEngine,
  type MolstarStructureEngine,
} from '@/renderer/pages/conversation/Preview/components/viewers/molstarStructureEngine';
import { summarizeStructureComposition } from '@/renderer/pages/conversation/Preview/components/viewers/structureComposition';
import {
  queryStructureLigands,
  splitStructureLigands,
} from '@/renderer/pages/conversation/Preview/components/viewers/structureLigandSelection';

function NativeContractProbe() {
  const host = useRef<HTMLDivElement>(null);
  const engine = useRef<MolstarStructureEngine | undefined>(undefined);
  const [ready, setReady] = useState(false);
  const [output, setOutput] = useState('Preparing native contract probe');
  useEffect(() => {
    let active = true;
    let current: MolstarStructureEngine | undefined;
    void (async () => {
      current = await createMolstarStructureEngine(host.current!);
      if (!active) {
        current.dispose();
        return;
      }
      await current.load(MULTI_LIGAND_BONDED_CIF, 'synthetic-contract.cif', 'mmcif');
      if (active) {
        engine.current = current;
        setReady(true);
      }
    })().catch((error) => {
      if (active) setOutput(String(error));
    });
    return () => {
      active = false;
      engine.current = undefined;
      current?.dispose();
    };
  }, []);
  const inspect = async () => {
    const current = engine.current;
    if (!current) return;
    setReady(false);
    try {
      const instances = current.getStructureComposition().objects.filter((object) => object.kind === 'ligand');
      const root = current.plugin.managers.structure.hierarchy.current.structures[0].cell.obj!.data;
      const native = splitStructureLigands(queryStructureLigands(root, 'mmcif'), true);
      current.plugin.managers.structure.selection.fromLoci('set', native[0].loci);
      const pickedBefore = current.getSelectionSummary().atomCount;
      await current.setStructureLigandSelection([instances[0].id, instances[2].id]);
      const pickedAfter = current.getSelectionSummary().atomCount;
      const source = current.getElectrostaticInputSource();
      if (!source) throw new Error('Native input missing');
      const potentials = Object.fromEntries(
        source.ligands.map((ligand, index) => [
          ligand.key,
          {
            label: `Synthetic field ${ligand.key}`,
            source: [
              'object 1 class gridpositions counts 2 2 2',
              'origin -12 -12 -12',
              'delta 24 0 0',
              'delta 0 24 0',
              'delta 0 0 24',
              'object 2 class gridconnections counts 2 2 2',
              'object 3 class array type double rank 0 items 8 data follows',
              Array(8)
                .fill(index ? -1 : 1)
                .join(' '),
              'attribute "dep" string "positions"',
              'object "field" class field',
              'component "positions" value 1',
              'component "connections" value 2',
              'component "data" value 3',
            ].join('\n'),
          },
        ])
      );
      await current.setElectrostaticPotentials({ ligands: potentials }, [-5, 5]);
      await current.applyRepresentationStyle('ligand-surface');
      const surfaces = [...current.plugin.state.data.cells.values()].flatMap((cell) => {
        const params = cell.transform.params as {
          type?: { name: string };
          colorTheme?: { params: { volume?: { ref: string } } };
        };
        if (params.type?.name !== 'molecular-surface') return [];
        const parent = current.plugin.state.data.cells.get(cell.transform.parent)?.obj?.data;
        return [
          {
            atoms: summarizeStructureComposition(parent, 'mmcif').atomCount,
            field: params.colorTheme?.params.volume?.ref,
          },
        ];
      });
      setOutput(
        JSON.stringify(
          {
            pickedBefore,
            pickedAfter,
            inputs: source.ligands.map(({ key, atomCount, molBlock }) => ({
              key,
              atomCount,
              bonds: Number(molBlock.split('\n')[3].slice(3, 6)),
            })),
            surfaces,
            distinctFields: new Set(surfaces.map((surface) => surface.field)).size,
            originalAtoms: current.getStructureComposition().atomCount,
            backendCalculation: false,
          },
          null,
          2
        )
      );
    } catch (error) {
      setOutput(String(error));
    } finally {
      setReady(true);
    }
  };
  return (
    <section aria-label='Native contract probe' style={{ width: 440 }}>
      <h2>Native contract probe</h2>
      <p>Synthetic fields only; no backend or scientific calculation.</p>
      <button disabled={!ready} onClick={() => void inspect()}>
        Inspect first + third native inputs and fields
      </button>
      <div ref={host} style={{ position: 'relative', height: 350 }} />
      <pre aria-label='Native contract result'>{output}</pre>
    </section>
  );
}

const inputs = {
  pdb: MULTI_LIGAND_PDB,
  cif: MULTI_LIGAND_CIF,
  models: MULTI_LIGAND_MODELS,
  named: MULTI_NAMED_LIGAND_PDB,
};
async function start() {
  await i18n.use(initReactI18next).init({
    lng: 'zh-CN',
    resources: { 'zh-CN': { translation: { preview, common } } },
    interpolation: { escapeValue: false },
  });
  function Fixture() {
    const [selected, setSelected] = useState<keyof typeof inputs>('pdb');
    return (
      <main style={{ padding: 16 }}>
        <h1>合成预览回归：一个受体 + 三个重叠同名配体</h1>
        <p>named uses three distinct residue identities; synthetic behavior fixtures, not scientific results.</p>
        <nav>
          {Object.keys(inputs).map((key) => (
            <button key={key} onClick={() => setSelected(key as keyof typeof inputs)}>
              {key}
            </button>
          ))}
        </nav>
        <div style={{ display: 'flex', gap: 16, height: 740 }}>
          <section aria-label='预览 A' style={{ width: 580 }}>
            <SynonBiomedStructureViewer
              filename={'synthetic.' + (selected === 'cif' ? 'cif' : 'pdb')}
              content={inputs[selected]}
            />
          </section>
          <section aria-label='预览 B' style={{ width: 580 }}>
            <SynonBiomedStructureViewer filename='independent.pdb' content={MULTI_LIGAND_PDB} />
          </section>
          <NativeContractProbe />
        </div>
      </main>
    );
  }
  createRoot(document.getElementById('root')!).render(<Fixture />);
}
void start();

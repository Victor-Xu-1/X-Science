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
import { MULTI_LIGAND_PDB, MULTI_LIGAND_CIF, MULTI_LIGAND_MODELS } from '../fixtures/structureLigands';

const inputs = { pdb: MULTI_LIGAND_PDB, cif: MULTI_LIGAND_CIF, models: MULTI_LIGAND_MODELS };
async function start() {
  await i18n
    .use(initReactI18next)
    .init({
      lng: 'zh-CN',
      resources: { 'zh-CN': { translation: { preview, common } } },
      interpolation: { escapeValue: false },
    });
  function Fixture() {
    const [selected, setSelected] = useState<keyof typeof inputs>('pdb');
    return (
      <main style={{ padding: 16 }}>
        <h1>合成预览回归：一个受体 + 三个重叠同名配体</h1>
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
              filename={'synthetic.' + (selected === 'models' ? 'pdb' : selected)}
              content={inputs[selected]}
            />
          </section>
          <section aria-label='预览 B' style={{ width: 580 }}>
            <SynonBiomedStructureViewer filename='independent.pdb' content={MULTI_LIGAND_PDB} />
          </section>
        </div>
      </main>
    );
  }
  createRoot(document.getElementById('root')!).render(<Fixture />);
}
void start();

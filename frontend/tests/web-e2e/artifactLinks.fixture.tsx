import '@arco-design/web-react/es/_util/react-19-adapter';
import '@arco-design/web-react/dist/css/arco.css';
import 'uno.css';
import '@/renderer/styles/themes/index.css';
import '@/renderer/styles/workspace-theme.css';
import React from 'react';
import { createRoot } from 'react-dom/client';
import { MemoryRouter } from 'react-router';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import conversation from '@/renderer/services/i18n/locales/zh-CN/conversation.json';
import preview from '@/renderer/services/i18n/locales/zh-CN/preview.json';
import MarkdownView from '@/renderer/components/Markdown';
import { useSynonBiomedArtifactResolver } from '@/renderer/pages/conversation/Messages/useSynonBiomedArtifactLinkPreview';
import { PreviewProvider, usePreviewContext } from '@/renderer/pages/conversation/Preview/context/PreviewContext';
import PreviewPanel from '@/renderer/pages/conversation/Preview/components/PreviewPanel/PreviewPanel';
import { ThemeProvider } from '@/renderer/hooks/context/ThemeContext';

await i18n.use(initReactI18next).init({
  lng: 'zh-CN',
  resources: { 'zh-CN': { translation: { conversation, preview } } },
  interpolation: { escapeValue: false },
});
type Identity = { artifact_id: string; version_id: string };
const fixture: { conversation: string; old: Identity; latest: Identity } = await fetch('/__artifact_fixture__').then(
  (r) => r.json()
);

function ArtifactLinks() {
  const resolver = useSynonBiomedArtifactResolver({
    conversationId: fixture.conversation,
    workspace: 'synonbiomed://controlled-project',
    artifactReferences: [fixture.old, fixture.latest],
  });
  const { activeTab, isOpen, closePreview } = usePreviewContext();
  const id = encodeURIComponent(fixture.latest.artifact_id);
  const old = encodeURIComponent(fixture.old.version_id);
  const links = `[Latest report](/#/artifacts/${id})\n\n[Exact old report](http://127.0.0.1:8765/#/artifacts/${id}/versions/${old})\n\n[Unknown artifact](#/artifacts/missing)`;
  return (
    <main style={{ padding: 24 }}>
      <p>Controlled artifact-link regression; not a scientific result.</p>
      <MarkdownView onLink={resolver.handleLink} resolveLinkHref={resolver.resolveLinkHref}>
        {links}
      </MarkdownView>
      <output data-testid='preview-identity'>{isOpen ? JSON.stringify(activeTab?.metadata) : 'closed'}</output>
      <button onClick={closePreview}>Close test preview</button>
      <div style={{ minHeight: 400 }}>
        <PreviewPanel conversationId={fixture.conversation} />
      </div>
    </main>
  );
}
createRoot(document.getElementById('root')!).render(
  <MemoryRouter>
    <ThemeProvider>
      <PreviewProvider>
        <ArtifactLinks />
      </PreviewProvider>
    </ThemeProvider>
  </MemoryRouter>
);

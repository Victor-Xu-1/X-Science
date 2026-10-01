import '@arco-design/web-react/es/_util/react-19-adapter';
import '@arco-design/web-react/dist/css/arco.css';
import 'uno.css';
import '@/renderer/styles/themes/index.css';
import '@/renderer/styles/workspace-theme.css';
import React, { useState } from 'react';
import { createRoot } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import zhCommon from '@/renderer/services/i18n/locales/zh-CN/common.json';
import zhConversation from '@/renderer/services/i18n/locales/zh-CN/conversation.json';
import { AuthContext } from '@/renderer/hooks/context/AuthContext';
import { RealtimeProvider } from '@/renderer/hooks/context/RealtimeContext';
import { LayoutContext } from '@/renderer/hooks/context/LayoutContext';
import { createRealtimeRuntime } from '@/common/adapter/realtimeRuntime';
import SynonBiomedRuntimeOperations from '@/renderer/components/synonBiomed/runtime/SynonBiomedRuntimeOperations';

await i18n.use(initReactI18next).init({
  lng: 'zh-CN',
  resources: { 'zh-CN': { translation: { common: zhCommon, conversation: zhConversation } } },
  interpolation: { escapeValue: false },
});
// Isolate only the surrounding account/WS shell. Frame reads, plan reads and
// decisions use the unmodified transport against real Go HTTP and SQLite.
const runtime = createRealtimeRuntime({
  createClient: ({ onStatus }) => ({
    setIdentity: async () => {
      onStatus('connected');
    },
    dispose: () => {},
  }),
});
const auth = {
  ready: true,
  user: { id: 'local', username: 'controlled-test' },
  status: 'authenticated' as const,
  failure: null,
  login: async () => ({ success: true }),
  register: async () => ({ success: true }),
  logout: async () => {},
  refresh: async () => {},
  clearAuthCache: () => {},
};
function Task() {
  const frame = new URLSearchParams(location.search).get('frame') ?? 'plan-review';
  const [state, setState] = useState('waiting_approval');
  return (
    <main style={{ padding: 20, maxWidth: 880, margin: 'auto' }}>
      <p>Controlled task-attention regression; not a scientific task.</p>
      <SynonBiomedRuntimeOperations
        conversationId={frame}
        runtimeState={state}
        onRuntimeUpdated={(_id, next) => setState(next.state)}
        onResumed={(_id, next) => setState(next.state)}
      />
    </main>
  );
}
createRoot(document.getElementById('root')!).render(
  <AuthContext.Provider value={auth}>
    <RealtimeProvider runtime={runtime}>
      <LayoutContext.Provider
        value={{ isMobile: window.innerWidth < 520, siderCollapsed: true, setSiderCollapsed: () => {} }}
      >
        <Task />
      </LayoutContext.Provider>
    </RealtimeProvider>
  </AuthContext.Provider>
);

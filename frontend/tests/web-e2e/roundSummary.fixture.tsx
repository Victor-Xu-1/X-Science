import '@arco-design/web-react/es/_util/react-19-adapter';
import '@arco-design/web-react/dist/css/arco.css';
import 'uno.css';
import '@/renderer/styles/themes/index.css';
import '@/renderer/styles/workspace-theme.css';
import React from 'react';
import { createRoot } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import zhMessages from '@/renderer/services/i18n/locales/zh-CN/messages.json';
import MessageRoundFooter from '@/renderer/pages/conversation/Messages/components/MessageRoundFooter';
import { decodeRoundSummary } from '@/common/chat/roundSummary';
import { IconCopy, IconBranch } from '@arco-design/web-react/icon';
import styles from '@/renderer/pages/conversation/Messages/components/MessageRoundFooter.module.css';

await i18n.use(initReactI18next).init({
  lng: 'zh-CN',
  resources: { 'zh-CN': { translation: { messages: zhMessages } } },
  interpolation: { escapeValue: false },
});
const summary = decodeRoundSummary(await fetch('/__round_summary_fixture_data__').then((response) => response.json()));
if (!summary) throw new Error('Invalid browser fixture summary');
createRoot(document.getElementById('root')!).render(
  <main style={{ padding: 24, paddingTop: 410 }}>
    <p>Browser regression fixture — completed round, not a scientific result.</p>
    <div className={styles.actions} data-testid='footer-layout'>
      <button className={styles.iconButton} aria-label='复制' type='button'>
        <IconCopy className={styles.icon} />
      </button>
      <button className={styles.iconButton} aria-label='从此回复分支到新会话' type='button'>
        <IconBranch className={styles.icon} />
      </button>
      <MessageRoundFooter summary={summary} />
    </div>
  </main>
);

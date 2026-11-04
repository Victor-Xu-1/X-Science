import React, { useRef } from 'react';
import { Message } from '@arco-design/web-react';
import { useTranslation } from 'react-i18next';
import CollapsibleContent from '@/renderer/components/chat/CollapsibleContent';
import { copyText } from '@/renderer/utils/ui/clipboard';

/** A reversible view of model-provided bytes, never a host observation or a JSON formatter. */
export default function LargeJsonSource({ raw }: { raw: string }) {
  const { t } = useTranslation();
  const identity = useRef({ raw, revision: 0 });
  if (identity.current.raw !== raw) identity.current = { raw, revision: identity.current.revision + 1 };
  return (
    <section aria-label={t('messages.largeJsonSource.title')} className='my-8px max-w-full min-w-0'>
      <div className='flex items-center justify-between gap-8px'>
        <strong>{t('messages.largeJsonSource.title')}</strong>
        <button
          type='button'
          onClick={() => {
            void copyText(raw).catch(() => Message.error(t('common.copyFailed')));
          }}
        >
          {t('messages.largeJsonSource.copy')}
        </button>
      </div>
      <p>{t('messages.largeJsonSource.description')}</p>
      <CollapsibleContent
        key={identity.current.revision}
        maxHeight={200}
        defaultCollapsed
        allowHorizontalScroll
        contentClassName='large-json-source-content'
      >
        <pre style={{ margin: 0, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>
          <code>{raw}</code>
        </pre>
      </CollapsibleContent>
    </section>
  );
}

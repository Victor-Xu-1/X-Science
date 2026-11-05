import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { isBackendHttpError } from '@/common/adapter/httpBridge';
import type {
  LiteratureMetadataDraft,
  LiteratureMetadataLookupScheme,
} from '@/common/adapter/literatureMetadataLookup';
import WorkbenchModal from '@/renderer/components/base/WorkbenchModal';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';
import { lookupLiteratureMetadata } from '@/renderer/services/runtime/literatureMetadataLookup';
import { useLibraryDetailDialog } from './useLibraryDetailDialog';
import './libraryManualCreate.css';

/** This component owns a read only; Save and durable retries belong to the original manual editor. */
export function LibraryMetadataLookup({
  account,
  scheme,
  close,
  resolved,
}: {
  account: string;
  scheme: LiteratureMetadataLookupScheme;
  close: () => void;
  resolved: (draft: LiteratureMetadataDraft) => void;
}) {
  const { t } = useTranslation();
  const addTitle = t(scheme === 'pmid' ? 'conversation.libraryPmid.add' : 'conversation.libraryDoi.add');
  const [input, setInput] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const phase = useRef<'idle' | 'pending' | 'closed'>('idle');
  const request = useRef<AbortController | undefined>(undefined);
  const current = () => phase.current !== 'closed' && account === getRendererAccountScopeToken();
  const cancel = () => {
    phase.current = 'closed';
    request.current?.abort();
    close();
  };
  useEffect(() => {
    phase.current = 'idle';
    return () => {
      phase.current = 'closed';
      request.current?.abort();
    };
  }, []);
  const dialog = useLibraryDetailDialog(account, busy ? 'loading' : 'ready', cancel);
  const search = async () => {
    if (!current() || phase.current !== 'idle' || !input.trim()) return;
    phase.current = 'pending';
    const controller = new AbortController();
    request.current = controller;
    setBusy(true);
    setError(undefined);
    try {
      const draft = await lookupLiteratureMetadata(input, scheme, controller.signal);
      if (!current() || request.current !== controller || controller.signal.aborted) return;
      phase.current = 'closed';
      resolved(draft);
    } catch (failure) {
      if (current() && request.current === controller && !controller.signal.aborted) {
        const code = isBackendHttpError(failure) ? failure.code : '';
        setError(
          code === 'LITERATURE_LOOKUP_INVALID'
            ? 'invalid'
            : code === 'LITERATURE_LOOKUP_NOT_FOUND'
              ? 'notFound'
              : code === 'LITERATURE_LOOKUP_RATE_LIMITED'
                ? 'rateLimited'
                : 'failed'
        );
      }
    } finally {
      if (current() && request.current === controller) {
        phase.current = 'idle';
        setBusy(false);
      }
    }
  };
  return (
    <WorkbenchModal
      visible
      closable
      footer={null}
      focusLock
      autoFocus
      escToExit={false}
      onCancel={cancel}
      maskStyle={{ backgroundColor: 'transparent' }}
      style={{ width: 640, maxWidth: 'calc(100vw - 32px)' }}
      title={addTitle}
      className='library-manual-create'
      modalRender={(node) =>
        React.isValidElement<React.HTMLAttributes<HTMLDivElement>>(node)
          ? React.cloneElement(node, {
              'aria-label': addTitle,
              'aria-busy': busy,
              ...dialog.events,
            })
          : node
      }
    >
      <div ref={dialog.contentRef} className='library-manual-create__body'>
        <p className='library-manual-create__description'>{t('conversation.libraryDoi.description')}</p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void search();
          }}
        >
          <div className='library-manual-create__fields'>
            <label className='library-bibliographic__field'>
              {t(scheme === 'pmid' ? 'conversation.libraryPmid.input' : 'conversation.libraryDoi.input')}
              <input
                value={input}
                maxLength={8192}
                disabled={busy}
                autoCapitalize='none'
                autoCorrect='off'
                spellCheck={false}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && event.nativeEvent.isComposing) event.preventDefault();
                }}
                onChange={(event) => {
                  setInput(event.target.value);
                  setError(undefined);
                }}
              />
            </label>
            {error && (
              <p role='alert'>
                {t(
                  scheme === 'pmid' && (error === 'invalid' || error === 'notFound')
                    ? `conversation.libraryPmid.${error}`
                    : `conversation.libraryDoi.${error}`
                )}
              </p>
            )}
          </div>
          <footer className='library-manual-create__actions'>
            <button type='button' onClick={cancel}>
              {t('common.cancel')}
            </button>
            <button type='submit' disabled={busy || !input.trim()} aria-busy={busy}>
              {t(busy ? 'conversation.libraryDoi.searching' : 'conversation.libraryDoi.find')}
            </button>
          </footer>
        </form>
      </div>
    </WorkbenchModal>
  );
}

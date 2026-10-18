import type { PdfReadingContext, PdfReadingSource } from '@/common/adapter/pdfReadingContext';
import { loadProjectReadingOptions, type PdfReadingOption } from '@/renderer/services/runtime/pdfReadingContext';
import { Trigger } from '@arco-design/web-react';
import { FileText, Loader2, Search } from 'lucide-react';
import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import './readingContext.css';

export function ReadingContextPicker({
  context,
  pending,
  onSelect,
  children,
}: {
  context: PdfReadingContext;
  pending: boolean;
  onSelect: (source: PdfReadingSource) => Promise<void>;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [retry, setRetry] = useState(0);
  const [result, setResult] = useState<{
    status: 'loading' | 'ready' | 'error';
    options: PdfReadingOption[];
    unavailable: number;
  }>({
    status: 'loading',
    options: [],
    unavailable: 0,
  });
  const [selected, setSelected] = useState<string | null>(null);
  const [selectError, setSelectError] = useState(false);
  const scope = JSON.stringify(context.scope);
  const authority = useRef<object>({});
  useEffect(() => {
    const token = {};
    authority.current = token;
    return () => {
      if (authority.current === token) authority.current = {};
    };
  }, [scope]);
  const atLimit = context.bindings.length >= 3;
  useEffect(() => {
    setOpen(false);
    setQuery('');
    setSelected(null);
    setSelectError(false);
  }, [scope]);
  useEffect(() => {
    if (!open || atLimit) return;
    const abort = new AbortController();
    setResult({ status: 'loading', options: [], unavailable: 0 });
    void loadProjectReadingOptions(context, abort.signal).then(
      (loaded) => {
        if (!abort.signal.aborted) setResult({ status: 'ready', ...loaded });
      },
      () => {
        if (!abort.signal.aborted) setResult({ status: 'error', options: [], unavailable: 0 });
      }
    );
    return () => abort.abort();
  }, [open, context, retry, atLimit]);
  const items = useMemo(() => {
    const needle = query.trim().toLocaleLowerCase();
    return result.options.filter(
      (item) =>
        !context.bindings.some((binding) => binding.version_id === item.source.version_id) &&
        (!needle || item.name.toLocaleLowerCase().includes(needle))
    );
  }, [context.bindings, query, result.options]);
  const choose = async (item: PdfReadingOption) => {
    if (selected || pending) return;
    const token = authority.current;
    setSelected(item.source.version_id);
    setSelectError(false);
    try {
      await onSelect(item.source);
      if (authority.current === token) {
        setOpen(false);
        setQuery('');
      }
    } catch {
      if (authority.current === token) setSelectError(true);
    } finally {
      if (authority.current === token) setSelected(null);
    }
  };
  const reload = () => {
    setSelectError(false);
    setRetry((value) => value + 1);
  };
  return (
    <Trigger
      trigger='click'
      position='tl'
      popupVisible={open}
      onVisibleChange={setOpen}
      popupAlign={{ bottom: 8 }}
      unmountOnExit
      popup={() => (
        <section
          className='pdf-reading-picker'
          aria-label={t('conversation.reading.choose')}
          onKeyDown={(event) => {
            if (event.key === 'Escape') {
              event.stopPropagation();
              setOpen(false);
            }
          }}
        >
          <div className='pdf-reading-picker__project'>{t('conversation.reading.project')}</div>
          {!atLimit && (
            <label className='pdf-reading-picker__search'>
              <Search size={14} aria-hidden='true' />
              <input
                autoFocus
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                aria-label={t('conversation.reading.search')}
                placeholder={t('conversation.reading.search')}
              />
            </label>
          )}
          {(selectError || result.unavailable > 0) && (
            <div role='alert' className='pdf-reading-notice'>
              {t(selectError ? 'conversation.reading.mutationFailed' : 'conversation.reading.someUnavailable')}
              {!selectError && (
                <button type='button' onClick={reload}>
                  {t('common.retry')}
                </button>
              )}
            </div>
          )}
          {atLimit ? (
            <p>{t('conversation.reading.limit')}</p>
          ) : result.status === 'loading' ? (
            <p role='status'>
              <Loader2 size={16} className='pdf-reading-spin' />
              {t('conversation.reading.checking')}
            </p>
          ) : result.status === 'error' ? (
            <div role='alert' className='pdf-reading-notice'>
              {t('conversation.reading.loadFailed')}
              <button type='button' onClick={reload}>
                {t('common.retry')}
              </button>
            </div>
          ) : items.length === 0 ? (
            <p>{t('conversation.reading.empty')}</p>
          ) : (
            <ul role='listbox' aria-label={t('conversation.reading.choose')}>
              {items.map((item) => (
                <li key={item.source.version_id}>
                  <button
                    type='button'
                    role='option'
                    aria-selected={false}
                    disabled={pending || selected !== null}
                    onClick={() => void choose(item)}
                  >
                    {selected === item.source.version_id ? (
                      <Loader2 size={16} className='pdf-reading-spin' />
                    ) : (
                      <FileText size={16} />
                    )}
                    <span title={item.name}>{item.name}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
    >
      {children}
    </Trigger>
  );
}

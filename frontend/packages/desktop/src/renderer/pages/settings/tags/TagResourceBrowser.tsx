import React, { useEffect, useMemo, useRef, useState } from 'react';
import { BookOpen, ExternalLink, Search, X } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { LiteratureItem } from '@/common/adapter/literatureLibrary';
import type { ResourceTagSnapshot } from '@/common/adapter/resourceTags';
import { loadLiteratureItem } from '@/renderer/services/runtime/literatureLibrary';
import { getRendererAccountScopeToken } from '@/renderer/services/rendererAccountScope';

type Row = { id: string; item?: LiteratureItem; failed?: boolean };
/** Catalog resolution is read-only and uses the existing authorized literature reader. */
export function TagResourceBrowser({
  account,
  tagId,
  snapshot,
  disabled,
  remove,
  query,
  onQueryChange,
}: {
  account: string;
  tagId: string;
  snapshot: ResourceTagSnapshot;
  disabled: boolean;
  remove(id: string): Promise<void>;
  query: string;
  onQueryChange(value: string): void;
}) {
  const { t } = useTranslation();
  const idsKey = JSON.stringify(
    snapshot.assignments
      .filter((entry) => entry.tag_id === tagId && entry.resource_type === 'literature.item')
      .map((entry) => entry.resource_id)
      .toSorted()
  );
  const key = JSON.stringify([account, tagId, idsKey]);
  const ids = useMemo(() => JSON.parse(idsKey) as string[], [idsKey]);
  const order = useMemo(() => new Map(ids.map((id, index) => [id, index])), [ids]);
  const [attempt, setAttempt] = useState(0);
  const [view, setView] = useState<{ key: string; rows: Row[]; loading: boolean }>({ key, rows: [], loading: true });
  const [removing, setRemoving] = useState<string | null>(null);
  const [error, setError] = useState(false);
  useEffect(() => {
    setRemoving(null);
    setError(false);
  }, [key]);
  const current = useRef(key);
  current.current = key;
  useEffect(() => {
    const abort = new AbortController();
    current.current = key;
    const rows: Row[] = [];
    let index = 0;
    const publish = (loading: boolean) => {
      if (!abort.signal.aborted && current.current === key && account === getRendererAccountScopeToken())
        setView({ key, rows: rows.toSorted((a, b) => order.get(a.id)! - order.get(b.id)!), loading });
    };
    publish(ids.length > 0);
    // A bounded worker pool resolves every selected relationship without flooding the API.
    const worker = async () => {
      while (!abort.signal.aborted && index < ids.length) {
        const id = ids[index++];
        try {
          // Each of the four workers is sequential so the read concurrency stays bounded.
          // eslint-disable-next-line no-await-in-loop
          const item = await loadLiteratureItem(id, abort.signal);
          rows.push({ id, item });
        } catch {
          if (!abort.signal.aborted) rows.push({ id, failed: true });
        }
        publish(true);
      }
    };
    void Promise.all(Array.from({ length: Math.min(4, ids.length) }, worker)).then(() => publish(false));
    return () => {
      abort.abort();
      if (current.current === key) current.current = '';
    };
  }, [account, key, ids, order, attempt]);
  const rows = view.key === key ? view.rows : [];
  const loading = view.key !== key || view.loading;
  const normalized = query.trim().toLowerCase();
  const visible = rows.filter((row) => (row.item?.title ?? row.id).toLowerCase().includes(normalized));
  const [page, setPage] = useState(0);
  useEffect(() => setPage(0), [key, query]);
  const lastPage = Math.max(0, Math.ceil(visible.length / 20) - 1);
  const effectivePage = Math.min(page, lastPage);
  const unlink = async (id: string) => {
    if (disabled || removing) return;
    const original = key;
    setRemoving(id);
    setError(false);
    try {
      await remove(id);
    } catch {
      if (current.current === original) setError(true);
    } finally {
      if (current.current === original) setRemoving(null);
    }
  };
  return (
    <section className='tags-resources' aria-label={t('settings.resourceTags.resources')}>
      <div className='tags-resources__toolbar'>
        <label className='tags-search'>
          <Search size={14} aria-hidden='true' />
          <input
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder={t('settings.resourceTags.searchResources')}
            aria-label={t('settings.resourceTags.searchResources')}
          />
        </label>
        <span className='tags-muted'>
          {t('settings.resourceTags.literature')} · {ids.length}
        </span>
      </div>
      {loading && (
        <p role='status' className='tags-muted'>
          {t('settings.resourceTags.loadingResources')}
        </p>
      )}
      {rows.some((row) => row.failed) && (
        <div role='alert' className='tags-notice'>
          {t('settings.resourceTags.resourceUnavailable')}{' '}
          <button type='button' className='settings-action-button' onClick={() => setAttempt((value) => value + 1)}>
            {t('common.retry')}
          </button>
        </div>
      )}
      {error && (
        <p role='alert' className='tags-notice'>
          {t('conversation.resourceTags.updateFailed')}
        </p>
      )}
      <ul className='tags-resources__list'>
        {visible.slice(effectivePage * 20, effectivePage * 20 + 20).map((row) => (
          <li key={row.id}>
            <BookOpen size={16} aria-hidden='true' />
            {row.item ? (
              <a href={`#/library?item=${encodeURIComponent(row.id)}`}>
                <span>{row.item.title}</span>
                <ExternalLink size={12} aria-hidden='true' />
              </a>
            ) : (
              <span className='tags-muted'>{row.id}</span>
            )}
            <button
              type='button'
              className='tags-icon-button'
              disabled={disabled || removing !== null}
              aria-label={t('settings.resourceTags.removeResource', { name: row.item?.title ?? row.id })}
              onClick={() => void unlink(row.id)}
            >
              <X size={14} aria-hidden='true' />
            </button>
          </li>
        ))}
      </ul>
      {!loading && visible.length === 0 && <p className='tags-empty'>{t('settings.resourceTags.noResources')}</p>}
      {lastPage > 0 && (
        <nav className='tags-pagination' aria-label={t('settings.resourceTags.resourcePages')}>
          <button
            type='button'
            className='settings-action-button'
            disabled={effectivePage === 0}
            onClick={() => setPage(effectivePage - 1)}
          >
            {t('settings.resourceTags.previous')}
          </button>
          <span>
            {effectivePage + 1} / {lastPage + 1}
          </span>
          <button
            type='button'
            className='settings-action-button'
            disabled={effectivePage === lastPage}
            onClick={() => setPage(effectivePage + 1)}
          >
            {t('settings.resourceTags.next')}
          </button>
        </nav>
      )}
    </section>
  );
}

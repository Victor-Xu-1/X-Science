import React, { useEffect, useId } from 'react';
import { Close } from '@icon-park/react';
import { useTranslation } from 'react-i18next';
import { useSessionPermissionGrantsView } from '@/renderer/hooks/xScience/runtime/useSessionPermissionGrants';
import type { SessionPermissionGrantsController } from '@/renderer/services/runtime/sessionPermissionGrantsController';

type Props = { controller: SessionPermissionGrantsController; open: boolean };
const actionClass =
  'border-0 bg-transparent p-0 text-11px text-t-secondary hover:text-t-primary disabled:cursor-not-allowed disabled:opacity-50';

export default function SessionPermissionGrants({ controller, open }: Props) {
  const { t } = useTranslation();
  const boundaryId = useId();
  const view = useSessionPermissionGrantsView(controller);
  useEffect(() => (open ? controller.open() : undefined), [controller, open]);
  if (!open || !controller.isCurrent()) return null;
  const snapshot = view.snapshot;
  const hasDiagnostic =
    view.error ||
    view.retryable ||
    snapshot?.incomplete ||
    snapshot?.legacy_unbound_count ||
    snapshot?.stale_grant_count ||
    view.results.some((result) => result.status === 'changed');
  if (snapshot && snapshot.grants.length === 0 && !view.loading && !view.writing && !hasDiagnostic) return null;
  const locked = view.writing || view.retryable || !view.available || view.error === 'invalid';
  const categories = new Map<string, { label: string; count: number }>();
  for (const grant of snapshot?.grants ?? []) {
    const category = categories.get(grant.category_key);
    categories.set(grant.category_key, {
      label: category?.label ?? (grant.label || grant.key),
      count: (category?.count ?? 0) + 1,
    });
  }
  const errorKeys = {
    read: 'agentMode.sessionGrants.loadFailed',
    uncertain: 'agentMode.sessionGrants.uncertain',
    scope: 'agentMode.sessionGrants.scopeChanged',
    invalid: 'agentMode.sessionGrants.invalid',
  } as const;
  return (
    <section
      className='mx-4px mt-4px border-t border-solid border-[var(--border-base)] pt-4px text-11px'
      aria-label={t('agentMode.sessionGrants.title')}
      aria-describedby={boundaryId}
      data-testid='session-permission-grants'
    >
      <div className='flex items-center justify-between gap-8px px-8px pb-2px'>
        <span className='font-medium text-t-tertiary' title={t('agentMode.sessionGrants.boundary')}>
          {t('agentMode.sessionGrants.title')}
        </span>
        <button
          type='button'
          className={actionClass}
          aria-label={t('agentMode.sessionGrants.clearLabel')}
          disabled={!snapshot?.grants.length || snapshot.incomplete || locked}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            if (snapshot) void controller.revoke(snapshot);
          }}
        >
          {t('agentMode.sessionGrants.clear')}
        </button>
      </div>
      <div className='max-h-160px overflow-y-auto'>
        {[...categories].map(([key, category]) => (
          <div key={key} className='flex items-center gap-8px rounded-6px px-8px py-4px text-11px text-t-secondary'>
            <span className='min-w-0 flex-1 truncate font-mono leading-16px' title={category.label}>
              {category.label}
            </span>
            {category.count > 1 ? <span>{category.count}</span> : null}
            <button
              type='button'
              className='flex h-20px w-20px shrink-0 items-center justify-center border-0 bg-transparent p-0 rounded-4px text-t-tertiary hover:bg-fill-2 hover:text-t-primary disabled:opacity-50'
              aria-label={t('agentMode.sessionGrants.revokeLabel', { label: category.label })}
              disabled={locked}
              onClick={(event) => {
                event.preventDefault();
                event.stopPropagation();
                if (snapshot) void controller.revoke(snapshot, key);
              }}
            >
              <Close size={14} aria-hidden='true' />
            </button>
          </div>
        ))}
        {!view.loading && !view.writing && !view.error && snapshot && !snapshot.grants.length ? (
          <p className='m-0 px-8px py-4px'>{t('agentMode.sessionGrants.empty')}</p>
        ) : null}
      </div>
      {view.loading ? (
        <p role='status' className='m-0 px-8px py-4px'>
          {t('agentMode.sessionGrants.loading')}
        </p>
      ) : null}
      {view.writing ? (
        <p role='status' className='m-0 px-8px py-4px'>
          {t('agentMode.sessionGrants.revoking')}
        </p>
      ) : null}
      {view.error ? (
        <p role='alert' className='m-0 px-8px py-4px'>
          {t(errorKeys[view.error])}
        </p>
      ) : null}
      {view.results.some((result) => result.status === 'changed') ? (
        <p role='status' className='m-0 px-8px py-4px'>
          {t('agentMode.sessionGrants.changed')}
        </p>
      ) : null}
      {view.results.some((result) => result.status === 'already_absent') ? (
        <p role='status' className='m-0 px-8px py-4px'>
          {t('agentMode.sessionGrants.alreadyAbsent')}
        </p>
      ) : null}
      {snapshot?.incomplete ? <p className='m-0 px-8px py-4px'>{t('agentMode.sessionGrants.incomplete')}</p> : null}
      {snapshot?.legacy_unbound_count ? (
        <p className='m-0 px-8px py-4px'>
          {t('agentMode.sessionGrants.legacy', { count: snapshot!.legacy_unbound_count })}
        </p>
      ) : null}
      {snapshot?.stale_grant_count ? (
        <p className='m-0 px-8px py-4px'>
          {t('agentMode.sessionGrants.stale', { count: snapshot!.stale_grant_count })}
        </p>
      ) : null}
      <p id={boundaryId} className='sr-only'>
        {t('agentMode.sessionGrants.boundary')}
      </p>
      <div className='flex gap-12px px-8px py-4px'>
        {view.retryable ? (
          <button
            type='button'
            className={actionClass}
            disabled={view.writing}
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              void controller.retry();
            }}
          >
            {t('agentMode.sessionGrants.retryRevoke')}
          </button>
        ) : null}
        <button
          type='button'
          className={actionClass}
          disabled={view.loading || view.writing || !view.available}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            void controller.refresh();
          }}
        >
          {t('agentMode.sessionGrants.refresh')}
        </button>
      </div>
    </section>
  );
}

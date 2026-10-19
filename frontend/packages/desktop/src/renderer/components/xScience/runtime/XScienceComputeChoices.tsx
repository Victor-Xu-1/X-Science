import { Check } from '@icon-park/react';
import { Spin, Switch } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceComputeProvider } from '@/renderer/services/xScienceCompute';

const rowClass =
  'composer-control-menu__row session-options-menu__row box-border flex h-32px w-full items-center gap-8px border-0 bg-transparent px-8px text-left font-inherit text-13px leading-none text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50';

/** Presentation only. The parent menu retains the single read/mutation owner. */
export default function XScienceComputeChoices({
  providers,
  enabled,
  status,
  hasSnapshot,
  disabled,
  pending,
  retry,
  selectLocal,
  change,
  manage,
}: {
  providers: XScienceComputeProvider[];
  enabled: string[];
  status: 'loading' | 'ready' | 'error';
  hasSnapshot: boolean;
  disabled: boolean;
  pending: string | null;
  retry: () => void;
  selectLocal: () => void;
  change: (name: string, checked: boolean) => void;
  manage: () => void;
}) {
  const { t } = useTranslation();
  const readOnly = disabled || status !== 'ready';
  const local = hasSnapshot && enabled.length === 0;
  return (
    <>
      {status === 'loading' ? (
        <div role='status' className='flex items-center gap-8px px-8px py-6px text-11px text-t-secondary'>
          <Spin size={14} />
          {t('conversation.xscienceRuntime.sessionOptions.computeState.loading')}
        </div>
      ) : status === 'error' ? (
        <div role='alert' className='px-8px py-6px text-11px text-t-secondary'>
          <p className='m-0'>{t('conversation.xscienceRuntime.sessionOptions.computeState.read')}</p>
          <button type='button' role='menuitem' className={`${rowClass} cursor-pointer text-primary`} onClick={retry}>
            {t('conversation.xscienceRuntime.sessionOptions.computeState.retry')}
          </button>
        </div>
      ) : null}
      <button
        type='button'
        role='menuitemradio'
        aria-checked={local}
        className={`${rowClass} cursor-pointer gap-10px`}
        disabled={readOnly || pending !== null}
        onClick={selectLocal}
      >
        <span className='inline-flex w-20px'>{local ? <Check size={14} /> : null}</span>
        <span>{t('conversation.xscienceRuntime.sessionOptions.localMachine')}</span>
      </button>
      {providers.map((provider) => {
        const checked = enabled.includes(provider.name);
        return (
          <div key={provider.name} role='none' className={`${rowClass} cursor-default gap-10px`}>
            <span className='min-w-0 flex-1 truncate'>{provider.displayName}</span>
            <Switch
              size='small'
              role='menuitemcheckbox'
              aria-checked={checked}
              aria-label={t('conversation.xscienceRuntime.sessionOptions.computeNamed', { name: provider.displayName })}
              checked={checked}
              style={checked ? { backgroundColor: 'var(--workspace-accent)' } : undefined}
              loading={pending === provider.name}
              disabled={readOnly || pending !== null}
              onChange={(next) => change(provider.name, next)}
            />
          </div>
        );
      })}
      {status === 'ready' && providers.length === 0 ? (
        <div className='px-12px py-8px text-12px leading-18px text-t-tertiary'>
          {t('conversation.xscienceRuntime.sessionOptions.noCompute')}
        </div>
      ) : null}
      <div role='separator' className='mx-12px my-5px h-1px bg-fill-3' />
      <button type='button' role='menuitem' className={`${rowClass} cursor-pointer`} onClick={manage}>
        {t('conversation.xscienceRuntime.sessionOptions.manageCompute')}
      </button>
    </>
  );
}

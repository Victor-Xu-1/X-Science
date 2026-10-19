import { Spin, Tooltip } from '@arco-design/web-react';
import { GitFork } from 'lucide-react';
import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import {
  delegationControlPresentation,
  type XScienceDelegationControl,
} from '@/renderer/hooks/xScience/runtime/useSessionDelegationPolicy';

export default function XScienceDelegationControlRow({
  control,
  active = true,
}: {
  control?: XScienceDelegationControl;
  active?: boolean;
}) {
  const { t } = useTranslation();
  const presentation = delegationControlPresentation(control);
  const refresh = control?.refresh;
  useEffect(() => {
    if (active) void refresh?.();
  }, [active, refresh]);
  const reason = t(`conversation.xscienceRuntime.sessionOptions.delegationState.${presentation.reason}`);
  return (
    <>
      <Tooltip content={reason} position='left'>
        <button
          type='button'
          role='menuitemcheckbox'
          aria-checked={presentation.checked ?? 'mixed'}
          aria-busy={control?.pending || undefined}
          title={reason}
          data-testid='session-config-row-delegation'
          disabled={presentation.disabled}
          className='composer-control-menu__row session-options-menu__row box-border flex h-32px w-full cursor-pointer items-center gap-8px border-0 bg-transparent px-8px text-left font-inherit text-13px text-t-primary outline-none hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50'
          onClick={() => {
            if (!presentation.disabled) void control?.change(!presentation.checked);
          }}
        >
          <GitFork size={16} strokeWidth={2} aria-hidden='true' className='shrink-0 text-t-secondary' />
          <span className='min-w-0 flex-1 truncate'>{t('conversation.xscienceRuntime.sessionOptions.delegation')}</span>
          <span
            aria-hidden='true'
            className={`relative inline-flex h-20px w-36px shrink-0 items-center rounded-full ${presentation.checked ? 'bg-[var(--workspace-accent)]' : 'bg-fill-4'}`}
          >
            {control?.pending ? (
              <span className='absolute inset-0 flex items-center justify-center'>
                <Spin size={12} />
              </span>
            ) : (
              <span
                className='h-16px w-16px rounded-full'
                style={{
                  transform: `translateX(${presentation.checked === undefined ? 10 : presentation.checked ? 18 : 2}px)`,
                  backgroundColor: presentation.checked
                    ? 'var(--workspace-accent-contrast)'
                    : 'var(--workspace-overlay-surface)',
                }}
              />
            )}
          </span>
        </button>
      </Tooltip>
      {control?.error ? (
        <div className='px-8px py-4px text-11px text-t-secondary' role='status'>
          <span>{reason}</span>{' '}
          {control.available ? (
            <button
              type='button'
              role='menuitem'
              disabled={control.pending}
              className='border-0 bg-transparent p-0 text-primary'
              onClick={() => void control.retry()}
            >
              {t('conversation.xscienceRuntime.sessionOptions.delegationState.retry')}
            </button>
          ) : null}
        </div>
      ) : null}
    </>
  );
}

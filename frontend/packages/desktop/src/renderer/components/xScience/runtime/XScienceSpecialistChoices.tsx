import React, { useEffect } from 'react';
import { Check } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import type { XScienceSpecialistControl } from '@/renderer/hooks/xScience/runtime/useSessionSpecialist';

const rowClass =
  'composer-control-menu__row session-options-menu__row box-border flex min-h-32px w-full items-center gap-8px border-0 bg-transparent px-8px py-6px text-left text-13px text-t-primary hover:bg-[var(--color-fill-2)] focus-visible:bg-[var(--color-fill-2)] disabled:cursor-default disabled:opacity-50';
export function specialistControlLabel(control: XScienceSpecialistControl | undefined, t: TFunction): string {
  if (!control?.current()) return t('conversation.xscienceRuntime.sessionOptions.specialistState.unknown');
  if (control.unavailable) return t('conversation.xscienceRuntime.sessionOptions.specialistState.unavailable');
  const selected = control.applied;
  return selected
    ? selected.is_default
      ? t('conversation.xscienceRuntime.sessionOptions.none')
      : selected.display_name || selected.selection_name
    : t('conversation.xscienceRuntime.sessionOptions.specialistState.unknown');
}
export function specialistChoiceRows(control: XScienceSpecialistControl | undefined, t: TFunction) {
  const selected = control?.desired ?? control?.applied;
  const disabled = !control?.current() || !control.available || control.pending || control.error === 'uncertain';
  return [
    {
      key: 'default',
      name: null as string | null,
      label: t('conversation.xscienceRuntime.sessionOptions.none'),
      active: Boolean(selected?.is_default),
      disabled,
    },
    ...(control?.choices ?? []).map((choice) => ({
      key: `agent:${choice.id}`,
      name: choice.name,
      label: choice.label,
      active: selected?.assistant_id === choice.id,
      disabled,
    })),
  ];
}
export default function XScienceSpecialistChoices({
  control,
  onCreate,
  active,
  statusOnly = false,
}: {
  control?: XScienceSpecialistControl;
  onCreate?: () => void;
  active?: boolean;
  statusOnly?: boolean;
}) {
  const { t } = useTranslation(),
    refresh = control?.refresh;
  useEffect(() => {
    if (active) void refresh?.();
  }, [active, refresh]);
  const selected = control?.desired ?? control?.applied;
  return (
    <>
      {!statusOnly
        ? specialistChoiceRows(control, t).map((choice) => (
            <button
              key={choice.key}
              type='button'
              role='menuitemradio'
              className={rowClass}
              aria-checked={choice.active}
              disabled={choice.disabled}
              aria-label={t('conversation.xscienceRuntime.sessionOptions.selectExpert', { name: choice.label })}
              onClick={() => {
                if (!choice.disabled) void control?.change(choice.name);
              }}
            >
              <span className='inline-flex w-20px'>{choice.active ? <Check size={14} /> : null}</span>
              <span className='truncate'>{choice.label}</span>
            </button>
          ))
        : null}
      {control?.unavailable && control.applied ? (
        <div className={`${rowClass} opacity-50`} aria-disabled='true'>
          <span className='line-through'>{control.applied.display_name || control.applied.selection_name}</span>
          <span className='text-11px'>
            {t('conversation.xscienceRuntime.sessionOptions.specialistState.unavailable')}
          </span>
        </div>
      ) : null}
      {control?.prepared ? (
        <p className='m-0 px-8px py-4px text-11px text-t-secondary' role='status'>
          {t('conversation.xscienceRuntime.sessionOptions.specialistState.prepared', {
            name: selected?.display_name || selected?.selection_name,
          })}
          {control.hasLiveAttempt ? ` ${t('conversation.xscienceRuntime.sessionOptions.specialistState.live')}` : ''}
        </p>
      ) : null}
      {control?.pending || control?.catalogLoading ? (
        <p role='status' className='m-0 px-8px py-4px text-11px text-t-secondary'>
          {t('conversation.xscienceRuntime.sessionOptions.specialistState.loading')}
        </p>
      ) : null}
      {control?.error || control?.catalogError ? (
        <div role='status' className='px-8px py-4px text-11px text-t-secondary'>
          {t(`conversation.xscienceRuntime.sessionOptions.specialistState.${control.error ?? 'read'}`)}{' '}
          <button
            type='button'
            role='menuitem'
            className='border-0 bg-transparent p-0 text-primary'
            disabled={control.pending}
            onClick={() => {
              void control.retry();
              if (control.catalogError) void control.refresh();
            }}
          >
            {t('conversation.xscienceRuntime.sessionOptions.specialistState.retry')}
          </button>
        </div>
      ) : null}
      {!control?.catalogLoading && !control?.choices.length ? (
        <p className='m-0 px-8px py-4px text-11px text-t-secondary'>
          {t('conversation.xscienceRuntime.sessionOptions.noExperts')}
        </p>
      ) : null}
      {onCreate ? (
        <>
          <div role='separator' className='mx-8px my-5px h-1px bg-fill-3' />
          <button type='button' role='menuitem' className={rowClass} onClick={onCreate}>
            {t('conversation.xscienceRuntime.sessionOptions.createExpert')}
          </button>
        </>
      ) : null}
    </>
  );
}

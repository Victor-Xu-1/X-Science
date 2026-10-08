import React, { useId } from 'react';
import { useTranslation } from 'react-i18next';
import type { ScientificRuntimeOption } from '@/renderer/services/scientificRuntimeSettings';
import { scientificRuntimePresentation } from '@/renderer/utils/scientificRuntimePresentation';
import { SettingsGeneratedIcon } from '../components/SettingsGeneratedAsset';
import {
  activeEnvironmentStates,
  environmentCategory,
  environmentIcon,
  environmentStateKeys,
} from './environmentCatalog';

export function EnvironmentCard({
  item,
  disabled,
  onPrepare,
  onPause,
  onUninstall,
}: {
  item: ScientificRuntimeOption;
  disabled: boolean;
  onPrepare: (item: ScientificRuntimeOption) => void;
  onPause: (item: ScientificRuntimeOption) => void;
  onUninstall: (item: ScientificRuntimeOption) => void;
}) {
  const { t } = useTranslation();
  const titleId = useId();
  const text = scientificRuntimePresentation(item.id, t);
  const active = activeEnvironmentStates.has(item.status);
  const uninstalling = item.status === 'uninstalling';
  const actionLabel =
    item.status === 'ready'
      ? t('settings.environments.uninstall')
      : uninstalling
        ? t('settings.storageSettings.softwareUninstalling')
        : active
          ? t('settings.environments.preparing')
          : t(
              item.status === 'failed' || item.status === 'stopped' ? 'common.retry' : 'settings.environments.download'
            );
  return (
    <article
      className='settings-entity-card environment-card settings-library-card'
      role='listitem'
      data-environment-id={item.id}
      aria-labelledby={titleId}
    >
      <div className='environment-card-heading settings-library-card__heading'>
        <span className='environment-card-icon settings-library-card__icon' aria-hidden='true'>
          <SettingsGeneratedIcon id={environmentIcon(item.id)} className='environment-card-icon-image' />
        </span>
        <span id={titleId} className='environment-card__title settings-library-card__title'>
          {text.title}
        </span>
        {item.required && <span className='environment-required'>{t('settings.environments.required')}</span>}
      </div>
      <p className='environment-card__description settings-library-card__description'>{text.description}</p>
      {item.status === 'preparing' && item.phasePercent != null && (
        <progress
          className='environment-card-progress'
          value={item.phasePercent}
          max={100}
          aria-label={t('settings.storageSettings.softwarePhaseProgress')}
        />
      )}
      {item.errorCode && (item.status === 'failed' || item.status === 'stopped') && (
        <p className='environment-error-code'>{t('settings.environments.failureHint')}</p>
      )}
      <div className='environment-card-footer settings-library-card__footer'>
        <span className='environment-card-status settings-library-card__meta'>
          <span className='environment-status' data-status={item.status} role='status'>
            {t('settings.storageSettings.' + (environmentStateKeys[item.status] ?? 'softwareUnknown'))}
          </span>
          <span className='environment-category'>
            {t('settings.environments.categories.' + environmentCategory(item.id))}
          </span>
          <span className='environment-size'>
            {item.required
              ? t('settings.environments.included')
              : t('settings.storageSettings.estimatedSoftwareSize', {
                  value: item.estimatedInstallMB,
                })}
          </span>
        </span>
        <span className='settings-library-card__control environment-card-actions'>
          {!item.required && active && !uninstalling ? (
            <button
              type='button'
              className='settings-action-button environment-action environment-action--pause'
              aria-label={`${t('settings.environments.pause')} · ${text.title}`}
              disabled={disabled || !item.available}
              onClick={() => onPause(item)}
            >
              {t('settings.environments.pause')}
            </button>
          ) : null}
          {item.required ? (
            item.status === 'failed' || item.status === 'stopped' ? (
              <button
                type='button'
                className='settings-action-button environment-action'
                aria-label={`${t('common.retry')} · ${text.title}`}
                disabled={disabled || !item.available}
                onClick={() => onPrepare(item)}
              >
                {t('common.retry')}
              </button>
            ) : null
          ) : (
            <button
              type='button'
              className='settings-action-button environment-action'
              aria-label={`${actionLabel} · ${text.title}`}
              disabled={disabled || !item.available || active}
              onClick={() => {
                if (item.status === 'ready') onUninstall(item);
                else onPrepare(item);
              }}
            >
              {actionLabel}
            </button>
          )}
        </span>
      </div>
      {item.environment || item.packages?.length ? (
        <details className='environment-inventory'>
          <summary>{t('settings.environments.packages', { count: item.packages?.length ?? 0 })}</summary>
          {item.environment ? (
            <p>
              {t('settings.environments.environmentId')}: <code>{item.environment}</code>
            </p>
          ) : null}
          <div
            className='environment-inventory__packages'
            role='region'
            tabIndex={0}
            aria-label={`${t('settings.environments.packages', { count: item.packages?.length ?? 0 })} · ${text.title}`}
          >
            <ul>
              {(item.packages ?? []).map((pkg, index) => (
                <li key={`${index}:${pkg.spec}`}>
                  <code>{pkg.spec}</code>
                </li>
              ))}
            </ul>
          </div>
        </details>
      ) : null}
    </article>
  );
}

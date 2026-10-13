import { Button, Tooltip } from '@arco-design/web-react';
import { CheckOne, Delete, Edit, LinkOne, Lock, PlayOne } from '@icon-park/react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import type { XScienceLlmProfile, XScienceLlmProviderTemplate } from '@/renderer/services/xScienceLlm';
import { modelProviderLabel, numericOptionLabel, TEMPERATURE_OPTIONS } from './modelProfilePresentation';

export type ModelProfileAction = 'test' | 'activate' | 'delete';
export type ModelTestResult = 'success' | 'error';

type Props = {
  profile: XScienceLlmProfile;
  templates: XScienceLlmProviderTemplate[];
  current: boolean;
  disabled: boolean;
  editDisabled: boolean;
  pending?: ModelProfileAction;
  result?: ModelTestResult;
  onTest: () => void;
  onActivate: () => void;
  onEdit: () => void;
  onDelete: () => void;
};

export const ModelProfileCard: React.FC<Props> = ({
  profile,
  templates,
  current,
  disabled,
  editDisabled,
  pending,
  result,
  onTest,
  onActivate,
  onEdit,
  onDelete,
}) => {
  const { t } = useTranslation();
  const titleId = React.useId();
  return (
    <article
      className={`model-profile-card${current ? ' model-profile-card--current' : ''}`}
      data-testid={`x-science-model-profile-${profile.id}`}
      aria-labelledby={titleId}
      aria-busy={Boolean(pending)}
    >
      <header className='model-profile-card__header'>
        <span className='model-profile-card__symbol' aria-hidden='true'>
          <LinkOne size='23' strokeWidth={2} />
        </span>
        <div className='model-profile-card__identity'>
          <h3 id={titleId}>{profile.name}</h3>
          <span>{modelProviderLabel(profile.provider, templates, t)}</span>
        </div>
        {current && (
          <span className='model-profile-card__current'>
            <CheckOne size='14' />
            {t('settings.modelsCurrent')}
          </span>
        )}
      </header>
      <dl className='model-profile-card__details'>
        <div>
          <dt>{t('settings.modelsModelId')}</dt>
          <dd className='model-profile-card__model'>{profile.model || '—'}</dd>
        </div>
        <div>
          <dt>{t('settings.modelsBaseUrl')}</dt>
          <dd className='model-profile-card__endpoint'>{profile.baseUrl || '—'}</dd>
        </div>
        <div>
          <dt>{t('settings.modelsCredential')}</dt>
          <dd className={`model-profile-card__credential${profile.hasApiKey ? '' : ' is-missing'}`}>
            <Lock size='15' aria-hidden='true' />
            {t(profile.hasApiKey ? 'settings.modelsKeySaved' : 'settings.modelsKeyMissing')}
          </dd>
        </div>
      </dl>
      <footer className='model-profile-card__footer'>
        <span className='model-profile-card__runtime'>
          {t('settings.modelsRuntimeParameters', {
            temperature: numericOptionLabel(TEMPERATURE_OPTIONS, profile.temperature ?? 0.2, t),
          })}
        </span>
        <div className='model-profile-card__actions'>
          {!current && (
            <Button
              type='primary'
              aria-label={t('settings.modelsSetCurrentNamed', {
                name: profile.name,
              })}
              disabled={disabled}
              loading={pending === 'activate'}
              onClick={onActivate}
            >
              {t('settings.modelsSetCurrent')}
            </Button>
          )}
          <Button
            type='secondary'
            icon={<PlayOne size='15' />}
            aria-label={t('settings.modelsTestNamed', { name: profile.name })}
            disabled={disabled}
            loading={pending === 'test'}
            onClick={onTest}
          >
            {t('settings.modelsTest')}
          </Button>
          <Button
            type='outline'
            icon={<Edit size='15' />}
            aria-label={t('settings.modelsEditNamed', { name: profile.name })}
            disabled={editDisabled}
            onClick={onEdit}
          >
            {t('common.edit')}
          </Button>
          <Tooltip content={t('common.delete')}>
            <Button
              type='text'
              className='model-profile-card__delete'
              icon={<Delete size='17' />}
              aria-label={t('settings.modelsDeleteNamed', {
                name: profile.name,
              })}
              disabled={disabled}
              loading={pending === 'delete'}
              onClick={onDelete}
            />
          </Tooltip>
        </div>
      </footer>
      {result && (
        <div className={`model-profile-card__result is-${result}`} role={result === 'error' ? 'alert' : 'status'}>
          {t(result === 'success' ? 'settings.modelsTestSuccess' : 'settings.modelsTestFailed', {
            model: profile.model,
          })}
        </div>
      )}
    </article>
  );
};

import type { SynonBiomedExpertProfile } from '@/renderer/services/agents/synonBiomedExpertProfiles';
import { Switch } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import ExpertUsageSummary, { type ExpertUsageView } from './ExpertUsageSummary';
import {
  SettingsGeneratedArtwork,
  SettingsGeneratedIcon,
  type SettingsGeneratedArtworkId,
  type SettingsGeneratedIconId,
} from '../components/SettingsGeneratedAsset';
import { compactSettingsDescription } from '../components/settingsPresentation';

function resolveExpertIcon(
  profile: Pick<SynonBiomedExpertProfile, 'name' | 'displayName' | 'description'>
): SettingsGeneratedIconId {
  const searchable = `${profile.name} ${profile.displayName} ${profile.description}`.toLowerCase();
  if (/variant|genom|dna|gene/.test(searchable)) return 'connector-dna';
  if (/pathway|network|interaction/.test(searchable)) return 'connector-regulation';
  if (/expression|transcript|cell/.test(searchable)) return 'connector-bars';
  if (/literature|research|review|publication/.test(searchable)) return 'connector-book';
  if (/chem|drug|molecule|compound/.test(searchable)) return 'connector-molecule';
  if (/rna/.test(searchable)) return 'connector-rna';
  return 'connector-protein';
}

function resolveExpertArtwork(
  profile: Pick<SynonBiomedExpertProfile, 'name' | 'displayName' | 'description'>
): SettingsGeneratedArtworkId {
  switch (resolveExpertIcon(profile)) {
    case 'connector-dna':
      return 'dna';
    case 'connector-regulation':
      return 'regulation';
    case 'connector-bars':
      return 'expression';
    case 'connector-book':
      return 'book';
    case 'connector-molecule':
      return 'chemistry';
    case 'connector-rna':
      return 'rna';
    default:
      return 'protein';
  }
}

const ExpertCatalogGroup: React.FC<{
  title: string;
  profiles: SynonBiomedExpertProfile[];
  onOpen: (profile: SynonBiomedExpertProfile) => void;
  onToggle: (profile: SynonBiomedExpertProfile, enabled: boolean) => void;
  pendingProfileName: string | null;
  busy: boolean;
  usageByName: ExpertUsageView;
}> = ({ title, profiles, onOpen, onToggle, pendingProfileName, busy, usageByName }) => {
  const { t } = useTranslation();
  return (
    <section className='expert-group pt-18px'>
      <h2 className='mb-6px mt-0 px-8px text-12px font-500 text-t-tertiary'>{title}</h2>
      <div className='expert-grid'>
        {profiles.map((profile) => (
          <div
            key={profile.name}
            data-testid={`expert-card-${profile.name}`}
            className='expert-card settings-library-card group'
          >
            <SettingsGeneratedArtwork id={resolveExpertArtwork(profile)} className='expert-card__artwork' />
            <button
              type='button'
              className='expert-card__main settings-library-card__body border-0 bg-transparent text-left focus-visible:outline-2 focus-visible:outline-offset-2'
              onClick={() => onOpen(profile)}
            >
              <span className='expert-card__heading settings-library-card__heading'>
                <span className='settings-list-icon expert-card__icon settings-library-card__icon'>
                  <SettingsGeneratedIcon
                    id={resolveExpertIcon(profile)}
                    className='settings-list-generated-icon expert-card__icon-image'
                  />
                </span>
                <span className='expert-card__title-row settings-library-card__title-slot'>
                  <span className='expert-card__title settings-library-card__title'>{profile.displayName}</span>
                  {profile.name === 'OPERON' ? (
                    <span className='expert-card__default-badge'>{t('settings.expertsSettings.default')}</span>
                  ) : null}
                </span>
              </span>
              <span className='expert-card__description settings-library-card__description' title={profile.description}>
                {compactSettingsDescription(profile.description, {
                  maxLength: 104,
                  stripPrefixes: [profile.displayName, profile.name],
                })}
              </span>
            </button>
            <div className='expert-card__footer settings-library-card__footer'>
              <ExpertUsageSummary profileName={profile.name} usageByName={usageByName} />
              <span className='settings-library-card__control'>
                {profile.source !== 'user' ? (
                  <span className='settings-library-status' role='status'>
                    {t(profile.enabled ? 'settings.skillsSettings.enabled' : 'settings.skillsSettings.disabled')}
                  </span>
                ) : (
                  <Switch
                    className='expert-card__switch shrink-0'
                    aria-label={t('settings.expertsSettings.enableNamed', {
                      name: profile.displayName,
                    })}
                    checked={profile.enabled}
                    loading={pendingProfileName === profile.name}
                    disabled={busy || pendingProfileName !== null}
                    onChange={(enabled) => onToggle(profile, enabled)}
                  />
                )}
              </span>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
};

export default ExpertCatalogGroup;

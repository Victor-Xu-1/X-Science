import { localizeSynonBiomedExpertProfile } from '@/renderer/services/agents/synonBiomedExpertLocalization';
import { Button, Empty, Input, Select, Spin } from '@arco-design/web-react';
import { Search } from '@icon-park/react';
import React, { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import SettingsPageHeader from '../components/SettingsPageHeader';
import SettingsLibraryTabHeader from '../components/SettingsLibraryTabHeader';
import SettingsLibrarySearch from '../components/SettingsLibrarySearch';
import SettingsLibraryFilterSelect from '../components/SettingsLibraryFilterSelect';
import SynonBiomedExpertProfileModal from './SynonBiomedExpertProfileModal';
import ExpertCatalogGroup from './ExpertCatalogGroup';
import ExpertDetailModal from './ExpertDetailModal';
import useExpertWorkbenchController from './useExpertWorkbenchController';
import { SettingsGeneratedEmptyArtwork } from '../components/SettingsGeneratedAsset';
import { SettingsToolbar } from '../components/SettingsPrimitives';

type ExpertWorkbenchProps = {
  /** When false, renders without the page-level header (used inside the merged library page). */
  withHeader?: boolean;
  /** When true (with withHeader=false), renders the merged-page one-line compact header. */
  compactHeader?: boolean;
};

/** The expert catalog's own category filter: every profile, custom or built-in. */
type ExpertSourceFilter = 'all' | 'personal' | 'builtin';

const ExpertWorkbench: React.FC<ExpertWorkbenchProps> = ({ withHeader = true, compactHeader = false }) => {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<ExpertSourceFilter>('all');
  const {
    messageContext,
    profiles,
    skills,
    connectors,
    expertUsage,
    loading,
    loadError,
    selected,
    draft,
    baseline,
    dirty,
    detailLoading,
    saving,
    pendingProfileName,
    createVisible,
    setCreateVisible,
    closeCreate,
    openProfile,
    toggleEnabled,
    reloadCatalog,
    detailKey,
    savingCurrent,
    updateDraft,
    saveCurrent,
    closeCurrent,
    toggleCurrent,
    deleteCurrent,
  } = useExpertWorkbenchController();

  const localizedProfiles = useMemo(
    () => profiles.map((profile) => localizeSynonBiomedExpertProfile(profile, t)),
    [profiles, t]
  );
  const localizedSelected = useMemo(
    () => (selected ? localizeSynonBiomedExpertProfile(selected, t) : null),
    [selected, t]
  );

  const visibleProfiles = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    return localizedProfiles.filter((profile) => {
      if (filter === 'personal' && profile.source !== 'user') return false;
      if (filter === 'builtin' && profile.source === 'user') return false;
      if (!normalizedQuery) return true;
      return [profile.name, profile.displayName, profile.description].some((value) =>
        value.toLowerCase().includes(normalizedQuery)
      );
    });
  }, [filter, localizedProfiles, query]);

  const groupedProfiles = useMemo(
    () => ({
      personal: visibleProfiles.filter((profile) => profile.source === 'user'),
      builtin: visibleProfiles.filter((profile) => profile.source !== 'user'),
    }),
    [visibleProfiles]
  );
  const detailModal = selected ? (
    <ExpertDetailModal
      key={detailKey}
      profile={selected}
      presentation={localizedSelected ?? selected}
      draft={draft}
      loading={detailLoading || !baseline}
      saving={savingCurrent}
      saveBlocked={saving || !baseline}
      dirty={dirty}
      pendingProfileName={pendingProfileName}
      skills={skills}
      connectors={connectors}
      onDraftChange={updateDraft}
      onSave={saveCurrent}
      onClose={closeCurrent}
      onToggle={toggleCurrent}
      onDelete={deleteCurrent}
    />
  ) : null;

  return (
    <div data-testid='expert-list-page' className='settings-experts-page flex min-h-0 flex-col gap-24px'>
      {messageContext}
      {withHeader ? (
        <SettingsPageHeader
          data-testid='experts-header'
          title={t('settings.expertsSettings.title')}
          description={t('settings.expertsSettings.description')}
          actions={
            <Button type='primary' onClick={() => setCreateVisible(true)}>
              {t('settings.expertsSettings.addExpert')}
            </Button>
          }
        />
      ) : compactHeader ? (
        <SettingsLibraryTabHeader
          title={t('settings.expertsSettings.title')}
          count={visibleProfiles.length}
          search={
            <SettingsLibrarySearch
              label={t('settings.expertsSettings.searchPlaceholder')}
              value={query}
              onChange={setQuery}
              data-testid='experts-search'
            />
          }
          filters={
            <SettingsLibraryFilterSelect
              aria-label={t('settings.expertsSettings.filter')}
              data-testid='experts-category-filter'
              value={filter}
              onChange={(value) => setFilter(value as ExpertSourceFilter)}
            >
              <option value='all'>{t('settings.expertsSettings.filterAll', { count: profiles.length })}</option>
              <option value='personal'>
                {t('settings.expertsSettings.filterPersonal', {
                  count: profiles.filter((profile) => profile.source === 'user').length,
                })}
              </option>
              <option value='builtin'>
                {t('settings.expertsSettings.filterBuiltin', {
                  count: profiles.filter((profile) => profile.source !== 'user').length,
                })}
              </option>
            </SettingsLibraryFilterSelect>
          }
          actions={
            <Button type='primary' onClick={() => setCreateVisible(true)}>
              {t('settings.expertsSettings.addExpert')}
            </Button>
          }
        />
      ) : null}
      {!compactHeader ? (
        <SettingsToolbar className='experts-toolbar'>
          <Select
            aria-label={t('settings.expertsSettings.filter')}
            value={filter}
            onChange={setFilter}
            className='w-170px'
          >
            <Select.Option value='all'>
              {t('settings.expertsSettings.filterAll', { count: profiles.length })}
            </Select.Option>
            <Select.Option value='personal'>
              {t('settings.expertsSettings.filterPersonal', {
                count: profiles.filter((profile) => profile.source === 'user').length,
              })}
            </Select.Option>
            <Select.Option value='builtin'>
              {t('settings.expertsSettings.filterBuiltin', {
                count: profiles.filter((profile) => profile.source !== 'user').length,
              })}
            </Select.Option>
          </Select>
          <Input
            aria-label={t('settings.expertsSettings.search')}
            prefix={<Search size={15} />}
            value={query}
            onChange={setQuery}
            placeholder={t('settings.expertsSettings.searchPlaceholder')}
            className='ml-auto w-260px max-w-full'
            allowClear
          />
        </SettingsToolbar>
      ) : null}
      <div className='min-h-0 flex-1 pb-24px'>
        {loading ? (
          <div className='flex min-h-260px items-center justify-center'>
            <Spin />
          </div>
        ) : null}
        {!loading && loadError ? (
          <div className='settings-load-error-panel flex min-h-260px flex-col items-center justify-center gap-12px'>
            <Empty description={loadError} />
            <Button onClick={() => void reloadCatalog()}>{t('common.retry')}</Button>
          </div>
        ) : null}
        {!loading && !loadError && visibleProfiles.length === 0 ? (
          <div className='settings-empty-artwork-panel'>
            <SettingsGeneratedEmptyArtwork id='experts' className='settings-empty-artwork-illustration' />
            <Empty description={t('settings.expertsSettings.noMatches')} />
          </div>
        ) : null}
        {!loading && !loadError ? (
          <div className='w-full'>
            {groupedProfiles.personal.length ? (
              <ExpertCatalogGroup
                title={t('settings.expertsSettings.yourExperts')}
                profiles={groupedProfiles.personal}
                onOpen={openProfile}
                onToggle={toggleEnabled}
                pendingProfileName={pendingProfileName}
                busy={saving}
                usageByName={expertUsage}
              />
            ) : null}
            {groupedProfiles.builtin.length ? (
              <ExpertCatalogGroup
                title={t('settings.expertsSettings.builtin')}
                profiles={groupedProfiles.builtin}
                onOpen={openProfile}
                onToggle={toggleEnabled}
                pendingProfileName={pendingProfileName}
                busy={saving}
                usageByName={expertUsage}
              />
            ) : null}
          </div>
        ) : null}
      </div>
      {detailModal}
      <SynonBiomedExpertProfileModal
        visible={createVisible}
        profile={null}
        onClose={closeCreate}
        onChanged={() => void reloadCatalog()}
      />
    </div>
  );
};

export default ExpertWorkbench;

/** Header-less, wrapper-less variant used inside the merged library page. */
export const ExpertWorkbenchContent: React.FC = () => <ExpertWorkbench withHeader={false} compactHeader />;

import { Button, Message, Spin, Tag, Tooltip } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { CheckOne, Delete, Edit, Refresh } from '@icon-park/react';
import React, { useCallback, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import SettingsPageHeader from './components/SettingsPageHeader';
import SettingsPageWrapper from './components/SettingsPageWrapper';
import {
  SettingsGeneratedEmptyArtwork,
  SettingsGeneratedIcon,
  type SettingsGeneratedIconId,
} from './components/SettingsGeneratedAsset';
import {
  activateSynonBiomedLlmProfile,
  deleteSynonBiomedLlmProfile,
  saveSynonBiomedLlmProfile,
  testSynonBiomedLlmProfile,
  type SynonBiomedLlmProfile,
} from '@/renderer/services/synonBiomedLlm';
import { ModelProfileEditor, type ModelProfileEditorState } from './models/ModelProfileEditor';
import { modelProviderLabel, numericOptionLabel, TEMPERATURE_OPTIONS } from './models/modelProfilePresentation';
import { useModelProfileSnapshot } from './models/useModelProfileSnapshot';

function resolveModelIcon(
  profile: Pick<SynonBiomedLlmProfile, 'name' | 'provider' | 'model'>
): SettingsGeneratedIconId {
  const searchable = `${profile.name} ${profile.provider} ${profile.model}`.toLowerCase();
  if (/openai|ark/.test(searchable)) return 'connector-database';
  if (/azure/.test(searchable)) return 'connector-clipboard';
  if (/anthropic|claude/.test(searchable)) return 'connector-cube';
  if (/google|gemini/.test(searchable)) return 'connector-dna';
  if (/mistral/.test(searchable)) return 'connector-bars';
  if (/protein|bio|science|chem/.test(searchable)) return 'connector-protein';
  return 'models';
}

export const SynonBiomedModelsSettingsContent: React.FC = () => {
  const { t } = useTranslation();
  const { snapshot, loading, failed, load, isActive } = useModelProfileSnapshot();
  const profiles = snapshot?.profiles ?? [];
  const [editor, setEditor] = useState<ModelProfileEditorState | null>(null);
  const [pendingProfileId, setPendingProfileId] = useState<string | null>(null);

  const activeProfile = useMemo(
    () => snapshot?.profiles.find((profile) => profile.id === snapshot.activeProfileId),
    [snapshot]
  );

  const runProfileAction = useCallback(
    async (profileId: string, action: () => Promise<unknown>, successMessage?: string) => {
      setPendingProfileId(profileId);
      try {
        await action();
        if (!isActive()) return;
        if (successMessage) Message.success(successMessage);
        await load();
      } catch {
        if (isActive()) Message.error(t('settings.modelsActionFailed'));
      } finally {
        if (isActive()) setPendingProfileId(null);
      }
    },
    [isActive, load, t]
  );

  const handleTest = useCallback(
    async (profile: SynonBiomedLlmProfile) => {
      setPendingProfileId(profile.id);
      try {
        await testSynonBiomedLlmProfile(profile.id);
        if (!isActive()) return;
        // Providers may return an internal deployment alias (for example a
        // routed model name). The user needs the configured profile ID here;
        // the alias belongs in diagnostics, not the connection verdict.
        Message.success(t('settings.modelsTestSuccess', { model: profile.model }));
      } catch {
        if (isActive()) Message.error(t('settings.modelsTestFailed'));
      } finally {
        if (isActive()) setPendingProfileId(null);
      }
    },
    [isActive, t]
  );

  const handleDelete = useCallback(
    (profile: SynonBiomedLlmProfile) => {
      Modal.confirm({
        title: t('settings.modelsDeleteTitle'),
        content: t('settings.modelsDeleteConfirm', { name: profile.name }),
        okButtonProps: { status: 'danger' },
        onOk: () =>
          runProfileAction(profile.id, () => deleteSynonBiomedLlmProfile(profile.id), t('settings.modelsDeleted')),
      });
    },
    [runProfileAction, t]
  );

  const headerActions = (
    <Button
      type='secondary'
      icon={
        loading ? (
          <span aria-hidden='true'>
            <Spin size={12} />
          </span>
        ) : (
          <Refresh theme='outline' size='14' />
        )
      }
      onClick={() => void load()}
      aria-busy={loading}
      aria-label={t('common.refresh')}
    >
      {t('common.refresh')}
    </Button>
  );

  return (
    <div className='settings-models-page flex flex-col'>
      <SettingsPageHeader
        data-testid='models-header'
        title={t('settings.models')}
        description={t('settings.modelsDescription')}
        actions={headerActions}
      />

      {failed ? (
        <div className='settings-model-feedback' role='alert'>
          <span>{t(snapshot ? 'settings.modelsRefreshFailedRetained' : 'settings.modelsLoadError')}</span>
          <Button type='outline' onClick={() => void load()}>
            {t('common.retry')}
          </Button>
        </div>
      ) : snapshot && loading ? (
        <div className='settings-model-feedback' role='status'>
          {t('settings.modelsRefreshing')}
        </div>
      ) : null}

      <div className='settings-summary-strip'>
        <StatusItem
          icon='connector-cube'
          label={t('settings.modelsProfiles')}
          value={snapshot ? profiles.length : t('settings.modelsStatusUnavailable')}
        />
        <StatusItem
          icon='connector-bars'
          label={t('settings.modelsActive')}
          value={
            !snapshot || loading || failed
              ? t('settings.modelsStatusUnavailable')
              : activeProfile?.model || t('settings.modelsUnconfigured')
          }
        />
        <SummaryActionItem
          data-testid='synon-biomed-model-add'
          icon='connector-database'
          label={t('settings.modelsAdd')}
          disabled={!snapshot || loading || failed}
          onClick={() => setEditor({})}
        />
      </div>

      {!snapshot ? (
        failed ? null : (
          <div className='h-180px flex items-center justify-center gap-8px' role='status'>
            <Spin />
            <span>{t('common.loading')}</span>
          </div>
        )
      ) : profiles.length === 0 ? (
        <div className='border border-dashed border-arco-2 rd-8px py-36px text-center'>
          <SettingsGeneratedEmptyArtwork id='governance' className='settings-empty-artwork-illustration' />
          <div className='text-14px font-600 text-t-primary'>{t('settings.modelsEmpty')}</div>
          <div className='mt-5px text-12px text-t-secondary'>{t('settings.modelsEmptyHint')}</div>
        </div>
      ) : (
        <div className='settings-list flex flex-col' aria-busy={loading} data-testid='synon-biomed-model-profiles'>
          {profiles.map((profile) => {
            const active = !loading && !failed && profile.id === snapshot.activeProfileId;
            const pending = pendingProfileId === profile.id;
            return (
              <div
                key={profile.id}
                data-testid={`synon-biomed-model-profile-${profile.id}`}
                className='settings-list-row settings-model-profile px-8px py-13px'
              >
                <div className='settings-model-profile__main'>
                  <div className='settings-model-profile__identity'>
                    <SettingsGeneratedIcon id={resolveModelIcon(profile)} className='settings-models-profile__icon' />
                    <div className='settings-model-profile__identity-copy'>
                      <div className='settings-model-profile__identity-name'>{profile.name}</div>
                      <div className='settings-model-profile__identity-model'>{profile.model}</div>
                      <div className='settings-model-profile__identity-tags'>
                        {active && <Tag color='green'>{t('settings.modelsCurrent')}</Tag>}
                        <Tag color='gray'>{modelProviderLabel(profile.provider, snapshot.templates, t)}</Tag>
                      </div>
                    </div>
                  </div>
                  <div className='settings-model-profile__detail settings-model-profile__model-id'>
                    <span className='settings-model-profile__detail-label'>{t('settings.modelsModelId')}</span>
                    <span className='settings-model-profile__detail-value' title={profile.model}>
                      {profile.model || '-'}
                    </span>
                  </div>
                  <div className='settings-model-profile__detail settings-model-profile__base-url'>
                    <span className='settings-model-profile__detail-label'>{t('settings.modelsBaseUrl')}</span>
                    <span className='settings-model-profile__detail-value' title={profile.baseUrl}>
                      {profile.baseUrl || '-'}
                    </span>
                  </div>
                  <div className='settings-model-profile__key'>
                    <span className='settings-model-profile__detail-label'>{t('settings.modelsCredential')}</span>
                    <span
                      className={
                        profile.hasApiKey
                          ? 'settings-model-profile__key-value is-saved'
                          : 'settings-model-profile__key-value'
                      }
                    >
                      <span className='settings-model-profile__key-dot' aria-hidden='true' />
                      {profile.hasApiKey ? t('settings.modelsKeySaved') : t('settings.modelsKeyMissing')}
                    </span>
                    <span className='settings-model-profile__runtime'>
                      {t('settings.modelsRuntimeParameters', {
                        temperature: numericOptionLabel(TEMPERATURE_OPTIONS, profile.temperature ?? 0.2, t),
                      })}
                    </span>
                  </div>
                  <div className='settings-model-profile__actions'>
                    <Tooltip content={t('settings.modelsSetCurrent')}>
                      <Button
                        type='outline'
                        className='settings-model-profile__activate'
                        icon={<CheckOne theme='outline' size='16' />}
                        aria-label={t('settings.modelsSetCurrentNamed', {
                          name: profile.name,
                        })}
                        disabled={active || loading || failed}
                        loading={pending}
                        onClick={() =>
                          void runProfileAction(
                            profile.id,
                            () => activateSynonBiomedLlmProfile(profile),
                            t('settings.modelsActivated')
                          )
                        }
                      >
                        {t('settings.modelsSetCurrent')}
                      </Button>
                    </Tooltip>
                    <Button
                      type='secondary'
                      size='small'
                      aria-label={t('settings.modelsTestNamed', {
                        name: profile.name,
                      })}
                      loading={pending}
                      disabled={loading || failed}
                      onClick={() => void handleTest(profile)}
                    >
                      {t('settings.modelsTest')}
                    </Button>
                    <Tooltip content={t('common.edit')}>
                      <Button
                        type='outline'
                        className='settings-model-profile__edit'
                        icon={<Edit theme='outline' size='16' />}
                        aria-label={t('settings.modelsEditNamed', {
                          name: profile.name,
                        })}
                        disabled={loading || failed}
                        onClick={() => setEditor({ profile })}
                      >
                        {t('common.edit')}
                      </Button>
                    </Tooltip>
                    <Tooltip content={t('common.delete')}>
                      <Button
                        type='outline'
                        status='danger'
                        icon={<Delete theme='outline' size='16' />}
                        aria-label={t('settings.modelsDeleteNamed', {
                          name: profile.name,
                        })}
                        disabled={loading || failed}
                        onClick={() => handleDelete(profile)}
                      >
                        {t('common.delete')}
                      </Button>
                    </Tooltip>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      <ModelProfileEditor
        editor={editor}
        templates={snapshot?.templates ?? []}
        onClose={() => setEditor(null)}
        onSaved={async (input) => {
          await saveSynonBiomedLlmProfile(input);
          if (!isActive()) return;
          setEditor((current) => (current === editor ? null : current));
          Message.success(t('settings.modelsSaved'));
          await load();
        }}
      />
    </div>
  );
};

const SynonBiomedModelsSettings: React.FC = () => (
  <SettingsPageWrapper>
    <SynonBiomedModelsSettingsContent />
  </SettingsPageWrapper>
);

const StatusItem: React.FC<{ icon: SettingsGeneratedIconId; label: string; value: React.ReactNode }> = ({
  icon,
  label,
  value,
}) => (
  <div className='settings-summary-item'>
    <SettingsGeneratedIcon id={icon} className='settings-summary-item__icon' />
    <div className='settings-summary-item__copy min-w-0'>
      <span className='settings-summary-item__label'>{label}</span>
      <strong className='text-t-primary font-600'>{value}</strong>
    </div>
  </div>
);

const SummaryActionItem: React.FC<{
  'data-testid': string;
  icon: SettingsGeneratedIconId;
  label: string;
  onClick: () => void;
  disabled?: boolean;
}> = ({ 'data-testid': dataTestId, icon, label, onClick, disabled }) => (
  <button
    type='button'
    className='settings-summary-item settings-summary-action'
    data-testid={dataTestId}
    aria-label={label}
    onClick={onClick}
    disabled={disabled}
  >
    <SettingsGeneratedIcon id={icon} className='settings-summary-item__icon' />
    <span className='settings-summary-action__label'>{label}</span>
  </button>
);

export default SynonBiomedModelsSettings;

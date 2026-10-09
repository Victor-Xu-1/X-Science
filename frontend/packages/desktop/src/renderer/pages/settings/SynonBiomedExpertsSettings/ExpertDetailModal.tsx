import type { SynonBiomedExpertProfile } from '@/renderer/services/agents/synonBiomedExpertProfiles';
import type { SynonBiomedMcpServer, SynonBiomedSkill } from '@/renderer/services/synonBiomedCapabilities';
import SynonBiomedAvatar from '@/renderer/components/synonBiomed/SynonBiomedAvatar';
import Modal from '@/renderer/components/base/WorkbenchModal';
import Tabs from '@/renderer/components/base/WorkbenchTabs';
import { Button, Input, Spin, Switch } from '@arco-design/web-react';
import React from 'react';
import { useTranslation } from 'react-i18next';
import { ExpertConnectorPicker, ExpertSkillPicker } from './ExpertCapabilities';
import type { ExpertDetailDraft } from './useExpertWorkbenchController';

type Props = {
  profile: SynonBiomedExpertProfile;
  presentation: SynonBiomedExpertProfile;
  draft: ExpertDetailDraft | null;
  loading: boolean;
  saving: boolean;
  saveBlocked: boolean;
  dirty: boolean;
  pendingProfileName: string | null;
  skills: SynonBiomedSkill[];
  connectors: SynonBiomedMcpServer[];
  onDraftChange: (update: (current: ExpertDetailDraft) => ExpertDetailDraft) => void;
  onSave: () => void;
  onClose: () => void;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
};
const { TabPane } = Tabs;

/** Detail composition only; reads, writes and opening ownership stay in the workbench. */
export default function ExpertDetailModal({
  profile,
  presentation,
  draft,
  loading,
  saving,
  saveBlocked,
  dirty,
  pendingProfileName,
  skills,
  connectors,
  onDraftChange,
  onSave,
  onClose,
  onToggle,
  onDelete,
}: Props) {
  const { t } = useTranslation();
  return (
    <Modal
      visible
      title={presentation.displayName}
      onCancel={onClose}
      onOk={onSave}
      okText={t('settings.expertsSettings.saveChanges')}
      cancelText={t('common.cancel')}
      okButtonProps={{ loading: saving, disabled: !dirty || !draft || saveBlocked || pendingProfileName !== null }}
      unmountOnExit
      style={{ width: 'min(760px, 92vw)' }}
    >
      <div data-testid='expert-detail-modal' className='expert-detail-modal__body'>
        {loading || !draft ? (
          <div className='flex min-h-240px items-center justify-center'>
            <Spin />
          </div>
        ) : (
          <>
            <header className='expert-detail-modal__header'>
              <div className='expert-detail-modal__avatar'>
                <SynonBiomedAvatar size={22} />
              </div>
              <div className='expert-detail-modal__identity'>
                <h1>{presentation.displayName}</h1>
                <p>{t('settings.expertsSettings.identifier', { id: profile.name })}</p>
                <p>{presentation.description}</p>
              </div>
              <div className='expert-detail-modal__actions'>
                {profile.source === 'user' ? (
                  <>
                    <Switch
                      aria-label={t('settings.expertsSettings.enableNamed', { name: presentation.displayName })}
                      checked={profile.enabled}
                      loading={pendingProfileName === profile.name}
                      disabled={saveBlocked || pendingProfileName !== null}
                      onChange={onToggle}
                    />
                    <Button
                      status='danger'
                      type='text'
                      disabled={saveBlocked || pendingProfileName !== null}
                      onClick={onDelete}
                    >
                      {t('settings.expertsSettings.deleteExpert')}
                    </Button>
                  </>
                ) : (
                  <span role='status' className='expert-detail-modal__status'>
                    {t(profile.enabled ? 'settings.skillsSettings.enabled' : 'settings.skillsSettings.disabled')}
                  </span>
                )}
              </div>
            </header>
            <div className='expert-detail-modal__coverage'>
              <span>
                {t(profile.source === 'user' ? 'settings.expertsSettings.custom' : 'settings.expertsSettings.builtin')}
              </span>
              <span data-testid='expert-skill-coverage'>
                {t('settings.expertsSettings.capabilityCoverage.skills', {
                  selected: draft.skillNames.length,
                  available: skills.length,
                })}
              </span>
              <span data-testid='expert-connector-coverage'>
                {t('settings.expertsSettings.capabilityCoverage.connectors', {
                  selected: draft.connectorIds.length,
                  available: connectors.filter((connector) => connector.enabled).length,
                })}
              </span>
            </div>
            {saveBlocked && !saving ? (
              <p role='status' className='expert-detail-modal__pending'>
                {t('settings.expertsSettings.otherSavePending')}
              </p>
            ) : null}
            <Tabs
              aria-label={t('settings.expertsSettings.configuration')}
              defaultActiveTab='skills'
              destroyOnHide={false}
            >
              <TabPane
                key='skills'
                title={`${t('settings.skills')}${draft.skillNames.length ? ` ${draft.skillNames.length}` : ''}`}
              >
                <ExpertSkillPicker
                  selectedIds={draft.skillNames}
                  skills={skills}
                  disabled={saving}
                  onChange={(skillNames) => onDraftChange((current) => ({ ...current, skillNames }))}
                />
              </TabPane>
              <TabPane
                key='connectors'
                title={`${t('settings.expertsSettings.connectors')}${draft.connectorIds.length ? ` ${draft.connectorIds.length}` : ''}`}
              >
                <ExpertConnectorPicker
                  selectedIds={draft.connectorIds}
                  connectors={connectors}
                  disabled={saving}
                  onChange={(connectorIds) => onDraftChange((current) => ({ ...current, connectorIds }))}
                />
              </TabPane>
              <TabPane key='instructions' title={t('settings.expertsSettings.instructions')}>
                <section className='expert-detail-modal__form'>
                  <p>{t('settings.expertsSettings.instructionsDescription')}</p>
                  <Input.TextArea
                    aria-label={t('settings.expertsSettings.instructions')}
                    value={draft.instructions}
                    onChange={(instructions) => onDraftChange((current) => ({ ...current, instructions }))}
                    placeholder={t('settings.expertsSettings.instructionsPlaceholder')}
                    autoSize={{ minRows: 6, maxRows: 12 }}
                    maxLength={16000}
                    disabled={saving}
                    showWordLimit
                  />
                </section>
              </TabPane>
              {profile.source === 'user' ? (
                <TabPane key='identity' title={t('settings.expertsSettings.identity')}>
                  <section className='expert-detail-modal__form'>
                    <p>{t('settings.expertsSettings.identityDescription')}</p>
                    <label>
                      {t('settings.expertsSettings.name')}
                      <Input
                        aria-label={t('settings.expertsSettings.name')}
                        disabled={saving}
                        value={draft.displayName}
                        onChange={(displayName) => onDraftChange((current) => ({ ...current, displayName }))}
                      />
                    </label>
                    <label>
                      {t('settings.expertsSettings.descriptionField')}
                      <Input.TextArea
                        aria-label={t('settings.expertsSettings.descriptionField')}
                        disabled={saving}
                        value={draft.description}
                        onChange={(description) => onDraftChange((current) => ({ ...current, description }))}
                        autoSize={{ minRows: 2, maxRows: 4 }}
                      />
                    </label>
                  </section>
                </TabPane>
              ) : null}
            </Tabs>
          </>
        )}
      </div>
    </Modal>
  );
}

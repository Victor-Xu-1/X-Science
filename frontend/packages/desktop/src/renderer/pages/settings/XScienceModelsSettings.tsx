import { Button, Message, Spin } from '@arco-design/web-react';
import Modal from '@/renderer/components/base/WorkbenchModal';
import { Add, LinkOne, Refresh } from '@icon-park/react';
import React, { useCallback, useId, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import SettingsPageHeader from './components/SettingsPageHeader';
import SettingsPageWrapper from './components/SettingsPageWrapper';
import {
  activateXScienceLlmProfile,
  deleteXScienceLlmProfile,
  saveXScienceLlmProfile,
  testXScienceLlmProfile,
  type XScienceLlmProfile,
} from '@/renderer/services/xScienceLlm';
import { ModelProfileEditor, type ModelProfileEditorState } from './models/ModelProfileEditor';
import { ModelProfileCard, type ModelProfileAction, type ModelTestResult } from './models/ModelProfileCard';
import { useModelProfileSnapshot } from './models/useModelProfileSnapshot';

export const XScienceModelsSettingsContent: React.FC = () => {
  const { t } = useTranslation();
  const connectionsId = useId();
  const { snapshot, loading, failed, load, isActive } = useModelProfileSnapshot();
  const [editor, setEditor] = useState<ModelProfileEditorState | null>(null);
  const [pending, setPending] = useState<{
    id: string;
    action: ModelProfileAction;
  } | null>(null);
  const [testResult, setTestResult] = useState<{
    profile: XScienceLlmProfile;
    result: ModelTestResult;
  } | null>(null);
  const actionInFlight = useRef(false);
  const unavailable = !snapshot || loading || failed;

  const runProfileAction = useCallback(
    async (profile: XScienceLlmProfile, action: ModelProfileAction) => {
      if (actionInFlight.current) return;
      actionInFlight.current = true;
      setPending({ id: profile.id, action });
      setTestResult(null);
      try {
        if (action === 'test') {
          await testXScienceLlmProfile(profile.id);
          if (!isActive()) return;
          setTestResult({ profile, result: 'success' });
          // Report the configured model, not a provider's internal routing alias.
          Message.success(t('settings.modelsTestSuccess', { model: profile.model }));
        } else {
          if (action === 'activate') await activateXScienceLlmProfile(profile);
          else await deleteXScienceLlmProfile(profile.id);
          if (!isActive()) return;
          Message.success(t(action === 'activate' ? 'settings.modelsActivated' : 'settings.modelsDeleted'));
          await load();
        }
      } catch {
        if (!isActive()) return;
        if (action === 'test') setTestResult({ profile, result: 'error' });
        Message.error(t(action === 'test' ? 'settings.modelsTestFailed' : 'settings.modelsActionFailed'));
      } finally {
        actionInFlight.current = false;
        if (isActive()) setPending(null);
      }
    },
    [isActive, load, t]
  );

  const handleDelete = (profile: XScienceLlmProfile) => {
    Modal.confirm({
      title: t('settings.modelsDeleteTitle'),
      content: t('settings.modelsDeleteConfirm', { name: profile.name }),
      okButtonProps: { status: 'danger' },
      onOk: () => runProfileAction(profile, 'delete'),
    });
  };

  return (
    <div className='settings-models-page'>
      <SettingsPageHeader
        data-testid='models-header'
        title={t('settings.models')}
        description={t('settings.modelsDescription')}
        actions={
          <>
            <Button
              type='text'
              icon={
                loading ? (
                  <span aria-hidden='true'>
                    <Spin size={14} />
                  </span>
                ) : (
                  <Refresh theme='outline' size='16' />
                )
              }
              onClick={() => {
                setTestResult(null);
                void load();
              }}
              aria-label={t('common.refresh')}
              aria-busy={loading}
            >
              {t('common.refresh')}
            </Button>
            <Button
              type='primary'
              icon={<Add theme='outline' size='17' fill='currentColor' />}
              data-testid='x-science-model-add'
              disabled={unavailable}
              onClick={() => setEditor({})}
              aria-label={t('settings.modelsAdd')}
            >
              {t('settings.modelsAdd')}
            </Button>
          </>
        }
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

      {!snapshot ? (
        failed ? null : (
          <div className='model-profiles-loading' role='status'>
            <Spin />
            <span>{t('common.loading')}</span>
          </div>
        )
      ) : (
        <section aria-labelledby={connectionsId} className='model-connections'>
          <div className='model-connections-heading'>
            <h2 id={connectionsId}>{t('settings.modelsConnections')}</h2>
            <span className='model-connections-count'>{snapshot.profiles.length}</span>
          </div>
          {snapshot.profiles.length === 0 ? (
            <div className='model-connections-empty'>
              <span className='model-connections-empty__icon' aria-hidden='true'>
                <LinkOne size='28' />
              </span>
              <h3>{t('settings.modelsEmpty')}</h3>
              <p>{t('settings.modelsEmptyHint')}</p>
            </div>
          ) : (
            <div className='model-profiles-list' aria-busy={loading} data-testid='x-science-model-profiles'>
              {snapshot.profiles.map((profile) => (
                <ModelProfileCard
                  key={profile.id}
                  profile={profile}
                  templates={snapshot.templates}
                  current={!loading && !failed && profile.id === snapshot.activeProfileId}
                  disabled={unavailable || pending !== null}
                  editDisabled={unavailable}
                  pending={pending?.id === profile.id ? pending.action : undefined}
                  result={testResult?.profile === profile ? testResult.result : undefined}
                  onTest={() => void runProfileAction(profile, 'test')}
                  onActivate={() => void runProfileAction(profile, 'activate')}
                  onEdit={() => setEditor({ profile })}
                  onDelete={() => handleDelete(profile)}
                />
              ))}
            </div>
          )}
        </section>
      )}
      <ModelProfileEditor
        editor={editor}
        templates={snapshot?.templates ?? []}
        onClose={() => setEditor(null)}
        onSaved={async (input) => {
          await saveXScienceLlmProfile(input);
          if (!isActive()) return;
          setTestResult(null);
          setEditor((current) => (current === editor ? null : current));
          Message.success(t('settings.modelsSaved'));
          await load();
        }}
      />
    </div>
  );
};

const XScienceModelsSettings: React.FC = () => (
  <SettingsPageWrapper>
    <XScienceModelsSettingsContent />
  </SettingsPageWrapper>
);

export default XScienceModelsSettings;

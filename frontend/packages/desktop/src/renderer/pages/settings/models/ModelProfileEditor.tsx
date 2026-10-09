import { Input, InputNumber, Message, Select } from '@arco-design/web-react';
import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Modal from '@/renderer/components/base/WorkbenchModal';
import type {
  SynonBiomedLlmProfile,
  SynonBiomedLlmProfileInput,
  SynonBiomedLlmProviderTemplate,
} from '@/renderer/services/synonBiomedLlm';
import { localizeProviderTemplate, TEMPERATURE_OPTIONS, withCurrentNumericOption } from './modelProfilePresentation';

export type ModelProfileEditorState = { profile?: SynonBiomedLlmProfile };

type ProfileDraft = {
  name: string;
  provider: string;
  baseUrl: string;
  model: string;
  apiKey: string;
  temperature: number;
  contextWindow?: number;
};

export const ModelProfileEditor: React.FC<{
  editor: ModelProfileEditorState | null;
  templates: SynonBiomedLlmProviderTemplate[];
  onClose: () => void;
  onSaved: (input: SynonBiomedLlmProfileInput) => Promise<void>;
}> = ({ editor, templates, onClose, onSaved }) => {
  const { t } = useTranslation();
  const defaultTemplate = preferredProviderTemplate(templates);
  const [draft, setDraft] = useState<ProfileDraft>(() => createDraft(editor?.profile, defaultTemplate));
  const [saving, setSaving] = useState(false);
  const initializedOpening = useRef(editor);
  const mounted = useRef(false);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => {
    if (editor && initializedOpening.current !== editor) {
      setDraft(createDraft(editor.profile, preferredProviderTemplate(templates)));
    }
    initializedOpening.current = editor;
  }, [editor, templates]);

  const selectedTemplate = templates.find((template) => template.provider === draft.provider) ?? defaultTemplate;
  const temperatureOptions = withCurrentNumericOption(TEMPERATURE_OPTIONS, draft.temperature);
  const modelOptions = Array.from(
    new Set([draft.model, ...(selectedTemplate?.modelExamples ?? [])].map((model) => model.trim()).filter(Boolean))
  );
  const title = editor?.profile ? t('settings.modelsEdit') : t('settings.modelsAdd');
  const valid = Boolean(draft.name.trim() && draft.provider && draft.baseUrl.trim() && draft.model.trim());

  const submit = async () => {
    if (!editor || !valid || saving) return;
    const submittedOpening = editor;
    setSaving(true);
    try {
      await onSaved({
        id: editor.profile?.id,
        name: draft.name.trim(),
        provider: draft.provider,
        baseUrl: draft.baseUrl.trim(),
        model: draft.model.trim(),
        apiKey: draft.apiKey.trim() || undefined,
        copyApiKeyFrom: editor.profile?.id,
        temperature: draft.temperature,
        contextWindow: draft.contextWindow ?? null,
        ...(editor.profile ? { maxTokens: null } : {}),
      });
    } catch {
      if (mounted.current && initializedOpening.current === submittedOpening) {
        Message.error(t('settings.modelsSaveFailed'));
      }
    } finally {
      if (mounted.current) setSaving(false);
    }
  };

  return (
    <Modal
      visible={Boolean(editor)}
      title={title}
      className='synon-biomed-model-editor-modal'
      onCancel={onClose}
      onOk={() => void submit()}
      okText={t('common.save')}
      cancelText={t('common.cancel')}
      okButtonProps={{ disabled: !valid, loading: saving }}
      unmountOnExit
      style={{ width: 'min(620px, 92vw)' }}
    >
      <div className='grid grid-cols-1 md:grid-cols-2 gap-x-14px gap-y-12px'>
        <Field label={t('settings.modelsName')} className='md:col-span-2'>
          <Input
            aria-label={t('settings.modelsName')}
            value={draft.name}
            onChange={(name) => setDraft((current) => ({ ...current, name }))}
            placeholder={t('settings.modelsNamePlaceholder')}
          />
        </Field>
        <Field label={t('settings.modelsProvider')}>
          <Select
            aria-label={t('settings.modelsProvider')}
            data-testid='synon-biomed-provider-select'
            value={draft.provider}
            getPopupContainer={getEditorPopupContainer}
            onChange={(provider) => {
              const template = templates.find((item) => item.provider === provider);
              setDraft((current) => ({
                ...current,
                provider,
                baseUrl: provider === current.provider ? current.baseUrl : template?.defaultBaseUrl || current.baseUrl,
                model: provider === current.provider ? current.model : template?.modelExamples[0] || '',
                contextWindow: provider === current.provider ? current.contextWindow : undefined,
              }));
            }}
          >
            {templates.map((template) => (
              <Select.Option key={template.provider} value={template.provider}>
                {localizeProviderTemplate(template, t).label}
              </Select.Option>
            ))}
          </Select>
        </Field>
        <Field label={t('settings.modelsModelId')}>
          <Select
            aria-label={t('settings.modelsModelId')}
            data-testid='synon-biomed-model-id-select'
            value={draft.model || undefined}
            getPopupContainer={getEditorPopupContainer}
            onChange={(model) =>
              setDraft((current) => ({
                ...current,
                model: typeof model === 'string' ? model : '',
                contextWindow: model === current.model ? current.contextWindow : undefined,
              }))
            }
            placeholder={t('settings.customModelPlaceholder')}
            showSearch
            allowClear
            allowCreate
          >
            {modelOptions.map((model) => (
              <Select.Option key={model} value={model}>
                {model}
              </Select.Option>
            ))}
          </Select>
        </Field>
        <Field label={t('settings.modelsBaseUrl')} className='md:col-span-2'>
          <Input
            aria-label={t('settings.modelsBaseUrl')}
            value={draft.baseUrl}
            onChange={(baseUrl) =>
              setDraft((current) => ({
                ...current,
                baseUrl,
                contextWindow: baseUrl === current.baseUrl ? current.contextWindow : undefined,
              }))
            }
            placeholder={selectedTemplate?.defaultBaseUrl}
          />
        </Field>
        <Field label={t('settings.modelsApiKey')} className='md:col-span-2'>
          <Input.Password
            aria-label={t('settings.modelsApiKey')}
            value={draft.apiKey}
            onChange={(apiKey) => setDraft((current) => ({ ...current, apiKey }))}
            placeholder={editor?.profile?.hasApiKey ? t('settings.modelsKeepKey') : t('settings.modelsKeyPlaceholder')}
            autoComplete='new-password'
          />
        </Field>
        <Field
          label={t('settings.modelsContextWindow')}
          description={t('settings.modelsContextWindowHint')}
          className='md:col-span-2'
        >
          <InputNumber
            aria-label={t('settings.modelsContextWindow')}
            data-testid='synon-biomed-context-window-input'
            value={draft.contextWindow}
            min={1}
            max={10_000_000}
            step={1}
            precision={0}
            placeholder={t('settings.modelsContextWindowUnknown')}
            onChange={(contextWindow) => setDraft((current) => ({ ...current, contextWindow }))}
          />
        </Field>
        <Field label={t('settings.modelsTemperature')} description={t('settings.modelsTemperatureHint')}>
          <Select
            aria-label={t('settings.modelsTemperature')}
            data-testid='synon-biomed-temperature-select'
            value={String(draft.temperature)}
            getPopupContainer={getEditorPopupContainer}
            onChange={(temperature) => setDraft((current) => ({ ...current, temperature: Number(temperature ?? 0.2) }))}
          >
            {temperatureOptions.map((option) => (
              <Select.Option key={`temperature-${option.value}`} value={String(option.value)}>
                {t(option.labelKey)}
              </Select.Option>
            ))}
          </Select>
        </Field>
      </div>
    </Modal>
  );
};

const Field: React.FC<{
  label: string;
  description?: string;
  className?: string;
  children: React.ReactNode;
}> = ({ label, description, className, children }) => (
  <div className={`flex flex-col gap-5px text-12px text-t-secondary ${className ?? ''}`}>
    <span>{label}</span>
    {children}
    {description ? <span className='text-11px leading-18px text-t-tertiary'>{description}</span> : null}
  </div>
);

function createDraft(profile?: SynonBiomedLlmProfile, template?: SynonBiomedLlmProviderTemplate): ProfileDraft {
  return {
    name: profile?.name ?? '',
    provider: profile?.provider ?? template?.provider ?? '',
    baseUrl: profile?.baseUrl ?? template?.defaultBaseUrl ?? '',
    model: profile?.model ?? template?.modelExamples[0] ?? '',
    apiKey: '',
    temperature: profile?.temperature ?? 0.2,
    contextWindow: profile?.contextWindow,
  };
}

function getEditorPopupContainer(): Element {
  return document.body;
}

function preferredProviderTemplate(templates: SynonBiomedLlmProviderTemplate[]) {
  return (
    templates.find((template) => template.provider.toLowerCase() === 'deepseek') ??
    templates.find((template) => template.modelExamples.length > 0 && template.defaultBaseUrl) ??
    templates[0]
  );
}

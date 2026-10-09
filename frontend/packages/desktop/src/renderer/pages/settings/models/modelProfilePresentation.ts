import type { TFunction } from 'i18next';
import type { SynonBiomedLlmProviderTemplate } from '@/renderer/services/synonBiomedLlm';

export type NumericOption = { value: number; labelKey: string };

export const TEMPERATURE_OPTIONS: NumericOption[] = [
  { value: 0.1, labelKey: 'settings.modelsTemperatureVeryStable' },
  { value: 0.2, labelKey: 'settings.modelsTemperatureStable' },
  { value: 0.5, labelKey: 'settings.modelsTemperatureBalanced' },
  { value: 0.8, labelKey: 'settings.modelsTemperatureFlexible' },
  { value: 1.2, labelKey: 'settings.modelsTemperatureCreative' },
  { value: 2, labelKey: 'settings.modelsTemperatureVeryCreative' },
];

export function withCurrentNumericOption(options: NumericOption[], value: number): NumericOption[] {
  return options.some((option) => option.value === value)
    ? options
    : [...options, { value, labelKey: 'settings.modelsCurrentValue' }];
}

export function numericOptionLabel(options: NumericOption[], value: number, t: TFunction): string {
  const option = withCurrentNumericOption(options, value).find((item) => item.value === value);
  return option ? t(option.labelKey) : t('settings.modelsCurrentValue');
}

const LOCAL_PROVIDER_LABEL_KEYS: Partial<Record<string, string>> = {
  custom: 'settings.modelsProviderCustomCompatible',
  moonshot: 'settings.modelsProviderMoonshotChina',
  'moonshot-global': 'settings.modelsProviderMoonshotGlobal',
  ollama: 'settings.modelsProviderOllamaLocal',
  'lm-studio': 'settings.modelsProviderLmStudioLocal',
  vllm: 'settings.modelsProviderVllmSelfHosted',
};

export function modelProviderLabel(
  provider: string,
  templates: SynonBiomedLlmProviderTemplate[],
  t: TFunction
): string {
  const labelKey = LOCAL_PROVIDER_LABEL_KEYS[provider.toLowerCase()];
  if (labelKey) return t(labelKey);
  return templates.find((template) => template.provider.toLowerCase() === provider.toLowerCase())?.label || provider;
}

export function localizeProviderTemplate(
  template: SynonBiomedLlmProviderTemplate,
  t: TFunction
): SynonBiomedLlmProviderTemplate {
  return { ...template, label: modelProviderLabel(template.provider, [template], t) };
}

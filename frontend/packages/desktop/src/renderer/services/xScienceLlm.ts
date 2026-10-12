import { PRODUCT_NAME } from '@/common/config/productIdentity';
import { MODEL_PLATFORMS } from '@/renderer/utils/model/modelPlatforms';

export type XScienceLlmProviderTemplate = {
  provider: string;
  label: string;
  defaultBaseUrl: string;
  modelExamples: string[];
  protocol: 'openai' | 'gemini' | string;
};

export type XScienceLlmProfile = {
  id: string;
  name: string;
  provider: string;
  baseUrl: string;
  model: string;
  temperature?: number;
  maxTokens?: number;
  contextWindow?: number;
  createdAt?: string;
  updatedAt?: string;
  hasApiKey: boolean;
  apiKeySource: string;
};

export type XScienceLlmProvidersSnapshot = {
  activeProfileId?: string;
  profiles: XScienceLlmProfile[];
  templates: XScienceLlmProviderTemplate[];
};

export type XScienceLlmProfileInput = {
  id?: string;
  name: string;
  provider: string;
  baseUrl: string;
  model: string;
  apiKey?: string;
  copyApiKeyFrom?: string;
  temperature?: number;
  maxTokens?: number | null;
  contextWindow?: number | null;
};

export type XScienceLlmModel = {
  id: string;
  displayName: string;
  description?: string;
  provider?: string;
  profileId?: string;
  model?: string;
  active: boolean;
};

export type XScienceLlmModelsSnapshot = {
  defaultModelId: string;
  hasMore: boolean;
  models: XScienceLlmModel[];
};

export type XScienceLlmTestResult = {
  text: string;
  model: string;
};

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

const PROVIDERS_PATH = '/api/llm/providers';
const TEST_PATH = '/api/llm/test';
const OPTIMIZE_PROMPT_PATH = '/api/llm/optimize-prompt';
const MODELS_PATH = '/v1/models';

const MODEL_EXAMPLES: Record<string, string[]> = {
  OpenAI: ['gpt-5', 'gpt-4.1'],
  Anthropic: ['claude-sonnet-4-5', 'claude-opus-4-1'],
  DeepSeek: ['deepseek-chat', 'deepseek-reasoner'],
  gemini: ['gemini-2.5-pro', 'gemini-2.5-flash'],
  Dashscope: ['qwen-plus', 'qwen-max', 'qwen-turbo'],
  'Dashscope-Coding': ['qwen3-coder-plus'],
  Zhipu: ['glm-4.5', 'glm-4.5-air', 'glm-4-plus'],
  Moonshot: ['kimi-k2-0711-preview', 'moonshot-v1-128k', 'moonshot-v1-32k'],
  'SiliconFlow-CN': ['deepseek-ai/DeepSeek-V3', 'Qwen/Qwen3-235B-A22B', 'moonshotai/Kimi-K2-Instruct'],
  Ark: ['doubao-seed-1-6', 'doubao-1-5-pro', 'deepseek-v3'],
  Qianfan: ['ernie-4.5-turbo-128k', 'ernie-x1-turbo-32k'],
  Hunyuan: ['hunyuan-turbos-latest', 'hunyuan-large'],
  MiniMax: ['MiniMax-M1', 'abab6.5s-chat'],
  ModelScope: ['Qwen/Qwen3-235B-A22B', 'deepseek-ai/DeepSeek-V3'],
  OpenRouter: ['qwen/qwen3-235b-a22b', 'deepseek/deepseek-chat-v3.1', 'moonshotai/kimi-k2'],
};

const FALLBACK_PROVIDER_TEMPLATES: XScienceLlmProviderTemplate[] = [
  ...MODEL_PLATFORMS.filter((item) => item.platform !== 'bedrock' && item.platform !== 'gemini-vertex-ai').map(
    (item) => ({
      provider: item.value,
      label: item.name,
      defaultBaseUrl: item.base_url ?? '',
      modelExamples: MODEL_EXAMPLES[item.value] ?? [],
      protocol: item.platform === 'anthropic' ? 'anthropic' : item.platform === 'gemini' ? 'gemini' : 'openai',
    })
  ),
  {
    provider: 'openai-responses',
    label: 'OpenAI Responses',
    defaultBaseUrl: 'https://api.openai.com/v1/responses',
    modelExamples: ['gpt-5', 'o3'],
    protocol: 'openai-responses',
  },
  {
    provider: 'ollama',
    label: 'Ollama',
    defaultBaseUrl: 'http://127.0.0.1:11434/v1',
    modelExamples: ['qwen3', 'llama3.3'],
    protocol: 'openai',
  },
  {
    provider: 'lm-studio',
    label: 'LM Studio',
    defaultBaseUrl: 'http://127.0.0.1:1234/v1',
    modelExamples: ['local-model'],
    protocol: 'openai',
  },
  {
    provider: 'vllm',
    label: 'vLLM',
    defaultBaseUrl: 'http://127.0.0.1:8000/v1',
    modelExamples: ['served-model'],
    protocol: 'openai',
  },
];

async function requestJson<T>(path: string, init?: RequestInit, fetchImpl: FetchLike = fetch): Promise<T> {
  const response = await fetchImpl(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...init?.headers,
    },
  });
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    throw new Error(`${PRODUCT_NAME} LLM Core request failed: ${response.status}${detail ? ` ${detail}` : ''}`);
  }
  return response.json() as Promise<T>;
}

export async function loadXScienceLlmProviders(fetchImpl: FetchLike = fetch): Promise<XScienceLlmProvidersSnapshot> {
  const payload = await requestJson<{
    activeProfileId?: unknown;
    profiles?: unknown;
    providers?: unknown;
    templates?: unknown;
  }>(PROVIDERS_PATH, undefined, fetchImpl);
  return toProvidersSnapshot(payload);
}

function toProvidersSnapshot(payload: {
  activeProfileId?: unknown;
  profiles?: unknown;
  providers?: unknown;
  templates?: unknown;
}): XScienceLlmProvidersSnapshot {
  const rawProfiles = Array.isArray(payload.profiles)
    ? payload.profiles
    : Array.isArray(payload.providers)
      ? payload.providers
      : [];
  const templates = Array.isArray(payload.templates)
    ? payload.templates.map(toTemplate).filter((template): template is XScienceLlmProviderTemplate => Boolean(template))
    : [];
  return {
    activeProfileId: typeof payload.activeProfileId === 'string' ? payload.activeProfileId : undefined,
    profiles: rawProfiles.map(toProfile).filter((profile): profile is XScienceLlmProfile => Boolean(profile)),
    templates: templates.length > 0 ? templates : FALLBACK_PROVIDER_TEMPLATES,
  };
}

export async function loadXScienceLlmModels(fetchImpl: FetchLike = fetch): Promise<XScienceLlmModelsSnapshot> {
  const payload = await requestJson<{
    default_model_id?: unknown;
    has_more?: unknown;
    data?: unknown;
  }>(MODELS_PATH, undefined, fetchImpl);
  return {
    defaultModelId: stringValue(payload.default_model_id),
    hasMore: payload.has_more === true,
    models: Array.isArray(payload.data)
      ? payload.data.map(toModel).filter((model): model is XScienceLlmModel => Boolean(model))
      : [],
  };
}

export async function saveXScienceLlmProfile(
  input: XScienceLlmProfileInput,
  fetchImpl: FetchLike = fetch
): Promise<XScienceLlmProfile> {
  const payload = await requestJson<{ profile?: unknown }>(
    PROVIDERS_PATH,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(compactProfileInput(input)),
    },
    fetchImpl
  );
  const profile = toProfile(payload.profile);
  if (!profile) throw new Error(`${PRODUCT_NAME} LLM Core returned an invalid profile`);
  return profile;
}

export function activateXScienceLlmProfile(
  profile: XScienceLlmProfile,
  fetchImpl: FetchLike = fetch
): Promise<XScienceLlmProfile> {
  return saveXScienceLlmProfile(
    {
      id: profile.id,
      name: profile.name,
      provider: profile.provider,
      baseUrl: profile.baseUrl,
      model: profile.model,
      temperature: profile.temperature,
      maxTokens: profile.maxTokens,
      contextWindow: profile.contextWindow,
      copyApiKeyFrom: profile.id,
    },
    fetchImpl
  );
}

export async function deleteXScienceLlmProfile(
  profileId: string,
  fetchImpl: FetchLike = fetch
): Promise<XScienceLlmProvidersSnapshot> {
  const payload = await requestJson<{
    activeProfileId?: unknown;
    profiles?: unknown;
    templates?: unknown;
  }>(`${PROVIDERS_PATH}/${encodeURIComponent(profileId)}`, { method: 'DELETE' }, fetchImpl);
  return toProvidersSnapshot(payload);
}

export async function testXScienceLlmProfile(
  profileId: string,
  fetchImpl: FetchLike = fetch
): Promise<XScienceLlmTestResult> {
  const payload = await requestJson<{ result?: unknown }>(
    TEST_PATH,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ profileId }),
    },
    fetchImpl
  );
  const result = isRecord(payload.result) ? payload.result : {};
  return {
    text: stringValue(result.text),
    model: stringValue(result.model),
  };
}

export type XSciencePromptOptimizeResult = {
  text: string;
  model: string;
};

export type XSciencePromptOptimizeInput = {
  text: string;
  conversationId: string;
  signal?: AbortSignal;
};

export async function optimizeXSciencePrompt(
  input: XSciencePromptOptimizeInput,
  fetchImpl: FetchLike = fetch
): Promise<XSciencePromptOptimizeResult> {
  const payload = await requestJson<{ result?: unknown }>(
    OPTIMIZE_PROMPT_PATH,
    {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ text: input.text, conversationId: input.conversationId }),
      signal: input.signal,
    },
    fetchImpl
  );
  const result = isRecord(payload.result) ? payload.result : {};
  return {
    text: stringValue(result.text),
    model: stringValue(result.model),
  };
}

function compactProfileInput(input: XScienceLlmProfileInput): Record<string, unknown> {
  return Object.fromEntries(Object.entries(input).filter(([, value]) => value !== undefined && value !== ''));
}

function toProfile(value: unknown): XScienceLlmProfile | null {
  if (!isRecord(value)) return null;
  const id = stringValue(value.id);
  const provider = stringValue(value.provider) || stringValue(value.type);
  if (!id || !provider) return null;
  return {
    id,
    name: stringValue(value.name) || provider,
    provider,
    baseUrl: stringValue(value.baseUrl),
    model: stringValue(value.model),
    temperature: optionalNumber(value.temperature),
    maxTokens: optionalNumber(value.maxTokens),
    contextWindow: optionalNumber(value.contextWindow),
    createdAt: optionalString(value.createdAt),
    updatedAt: optionalString(value.updatedAt),
    hasApiKey: value.hasApiKey === true || value.credentialConfigured === true,
    apiKeySource: stringValue(value.apiKeySource) || 'missing',
  };
}

function toTemplate(value: unknown): XScienceLlmProviderTemplate | null {
  if (!isRecord(value)) return null;
  const provider = stringValue(value.provider);
  if (!provider) return null;
  return {
    provider,
    label: stringValue(value.label) || provider,
    defaultBaseUrl: stringValue(value.defaultBaseUrl),
    modelExamples: Array.isArray(value.modelExamples) ? value.modelExamples.map(stringValue).filter(Boolean) : [],
    protocol: stringValue(value.protocol) || 'openai',
  };
}

function toModel(value: unknown): XScienceLlmModel | null {
  if (!isRecord(value)) return null;
  const id = stringValue(value.id);
  if (!id) return null;
  return {
    id,
    displayName: stringValue(value.display_name) || id,
    description: optionalString(value.description),
    provider: optionalString(value.provider),
    profileId: optionalString(value.profile_id),
    model: optionalString(value.model),
    active: value.active === true,
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.length > 0 ? value : undefined;
}

function optionalNumber(value: unknown): number | undefined {
  return typeof value === 'number' && Number.isFinite(value) ? value : undefined;
}

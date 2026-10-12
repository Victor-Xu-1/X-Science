import { loadXScienceLlmModels, type XScienceLlmModelsSnapshot } from '@/renderer/services/xScienceLlm';

export type XScienceSessionDefaults = {
  defaultModelId?: string;
  subagentModelId?: string;
  effort?: 'low' | 'medium' | 'high';
};

export type XScienceSessionDefaultsSnapshot = {
  defaults: XScienceSessionDefaults;
  models: XScienceLlmModelsSnapshot;
};

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

const SETTINGS_PATH = '/api/settings/client';
const SETTING_KEYS = {
  defaultModelId: 'xScience.session.defaultModelId',
  subagentModelId: 'xScience.session.subagentModelId',
  effort: 'xScience.session.effort',
} as const;
const EFFORT_VALUES = new Set<XScienceSessionDefaults['effort']>(['low', 'medium', 'high']);

/** Load global session defaults and discard model IDs that are no longer available. */
export async function loadXScienceSessionDefaults(fetchImpl: FetchLike = fetch): Promise<XScienceSessionDefaults> {
  return (await loadXScienceSessionDefaultsSnapshot(fetchImpl)).defaults;
}

export async function loadXScienceSessionDefaultsSnapshot(
  fetchImpl: FetchLike = fetch
): Promise<XScienceSessionDefaultsSnapshot> {
  const settingsPath = `${SETTINGS_PATH}?keys=${encodeURIComponent(Object.values(SETTING_KEYS).join(','))}`;
  const [payload, models] = await Promise.all([
    requestJson<unknown>(settingsPath, undefined, fetchImpl),
    loadXScienceLlmModels(fetchImpl),
  ]);
  const settings = unwrapSettings(payload);
  const availableModelIds = new Set(models.models.map((model) => model.id));
  return {
    defaults: compactDefaults({
      defaultModelId: validModelId(settings[SETTING_KEYS.defaultModelId], availableModelIds),
      subagentModelId: validModelId(settings[SETTING_KEYS.subagentModelId], availableModelIds),
      effort: validEffort(settings[SETTING_KEYS.effort]),
    }),
    models,
  };
}

/** Persist global session defaults; undefined values remove their backend setting. */
export async function saveXScienceSessionDefaults(
  next: XScienceSessionDefaults,
  fetchImpl: FetchLike = fetch
): Promise<void> {
  const normalized = compactDefaults({
    defaultModelId: optionalString(next.defaultModelId),
    subagentModelId: optionalString(next.subagentModelId),
    effort: validEffort(next.effort),
  });

  await requestJson<unknown>(
    SETTINGS_PATH,
    {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        [SETTING_KEYS.defaultModelId]: normalized.defaultModelId ?? null,
        [SETTING_KEYS.subagentModelId]: normalized.subagentModelId ?? null,
        [SETTING_KEYS.effort]: normalized.effort ?? null,
      }),
    },
    fetchImpl
  );
}

async function requestJson<T>(path: string, init: RequestInit | undefined, fetchImpl: FetchLike): Promise<T> {
  const response = await fetchImpl(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...init?.headers,
    },
  });
  if (!response.ok) {
    const detail = await response.text().catch(() => '');
    throw new Error(`Client settings request failed: ${response.status}${detail ? ` ${detail}` : ''}`);
  }
  return response.json() as Promise<T>;
}

function unwrapSettings(payload: unknown): Record<string, unknown> {
  if (!isRecord(payload)) return {};
  return isRecord(payload.data) ? payload.data : payload;
}

function validModelId(value: unknown, availableModelIds: ReadonlySet<string>): string | undefined {
  const modelId = optionalString(value);
  return modelId && availableModelIds.has(modelId) ? modelId : undefined;
}

function validEffort(value: unknown): XScienceSessionDefaults['effort'] {
  return typeof value === 'string' && EFFORT_VALUES.has(value as XScienceSessionDefaults['effort'])
    ? (value as XScienceSessionDefaults['effort'])
    : undefined;
}

function optionalString(value: unknown): string | undefined {
  return typeof value === 'string' && value.trim() ? value.trim() : undefined;
}

function compactDefaults(next: XScienceSessionDefaults): XScienceSessionDefaults {
  return Object.fromEntries(Object.entries(next).filter(([, value]) => value !== undefined));
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

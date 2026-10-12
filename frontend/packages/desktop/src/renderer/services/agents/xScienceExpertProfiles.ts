import { requestXScienceJson, type XScienceGatewayOptions } from '../xScienceHttp';

const EXPERT_PROFILES_PATH = '/api/x-science/expert-profiles';

export type XScienceExpertProfile = {
  id?: string;
  name: string;
  source: 'bundled' | 'user' | string;
  displayName: string;
  description: string;
  systemPrompt: string;
  iconKey: string;
  colorKey: string;
  enabled: boolean;
  userHidden: boolean;
  unrestricted: boolean;
  skillNames: string[];
  connectorIds?: string[];
};

export type XScienceExpertProfileInput = {
  name: string;
  displayName: string;
  description: string;
  systemPrompt: string;
  enabled: boolean;
};

export async function loadXScienceExpertProfiles(
  options: XScienceGatewayOptions = {}
): Promise<XScienceExpertProfile[]> {
  const payload = await requestXScienceJson<unknown[]>(EXPERT_PROFILES_PATH, { method: 'GET' }, options);
  return Array.isArray(payload)
    ? payload
        .map(toExpertProfile)
        .filter(isPresent)
        .filter((profile) => !profile.userHidden)
    : [];
}

export async function loadXScienceExpertRuntimeConnectorIds(
  name: string,
  options: XScienceGatewayOptions = {}
): Promise<string[]> {
  const payload = await requestXScienceJson<unknown>(
    `/api/agents/${encodeURIComponent(name)}/mcp-servers?include_tools=false`,
    { method: 'GET' },
    options
  );
  if (!Array.isArray(payload)) return [];
  return payload
    .map((value) => (value && typeof value === 'object' ? stringValue((value as Record<string, unknown>).id) : ''))
    .filter((id): id is string => Boolean(id));
}

export async function loadXScienceExpertProfilesWithRuntimeConnectors(
  options: XScienceGatewayOptions = {}
): Promise<XScienceExpertProfile[]> {
  const profiles = await loadXScienceExpertProfiles(options);
  return Promise.all(profiles.map((profile) => enrichXScienceExpertProfile(profile, options)));
}

async function enrichXScienceExpertProfile(
  profile: XScienceExpertProfile,
  options: XScienceGatewayOptions
): Promise<XScienceExpertProfile> {
  if (profile.source !== 'user') return profile;
  try {
    return {
      ...profile,
      connectorIds: await loadXScienceExpertRuntimeConnectorIds(profile.name, options),
    };
  } catch (error) {
    console.error(`Failed to load runtime connectors for expert ${profile.name}:`, error);
    return profile;
  }
}

export async function createXScienceExpertProfile(
  input: XScienceExpertProfileInput,
  options: XScienceGatewayOptions = {}
): Promise<XScienceExpertProfile> {
  const payload = await requestXScienceJson<unknown>(
    EXPERT_PROFILES_PATH,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(input),
    },
    options
  );
  const profile = toExpertProfile(payload);
  if (!profile) throw new Error('X-Science returned an invalid expert profile');
  return profile;
}

export async function updateXScienceExpertProfile(
  currentName: string,
  input: XScienceExpertProfileInput,
  options: XScienceGatewayOptions = {}
): Promise<XScienceExpertProfile> {
  const payload = await requestXScienceJson<unknown>(
    `${EXPERT_PROFILES_PATH}/${encodeURIComponent(currentName)}`,
    {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: input.name,
        displayName: input.displayName,
        description: input.description,
        systemPrompt: input.systemPrompt,
      }),
    },
    options
  );
  const profile = toExpertProfile(payload);
  if (!profile) throw new Error('X-Science returned an invalid expert profile');
  if (profile.enabled !== input.enabled) {
    return setXScienceExpertProfileEnabled(profile.name, input.enabled, options);
  }
  return profile;
}

export async function setXScienceExpertProfileEnabled(
  name: string,
  enabled: boolean,
  options: XScienceGatewayOptions = {}
): Promise<XScienceExpertProfile> {
  const payload = await requestXScienceJson<unknown>(
    `${EXPERT_PROFILES_PATH}/${encodeURIComponent(name)}/enabled`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled }),
    },
    options
  );
  const profile = toExpertProfile(payload);
  if (!profile) throw new Error('X-Science returned an invalid expert profile');
  return profile;
}

export async function deleteXScienceExpertProfile(name: string, options: XScienceGatewayOptions = {}): Promise<void> {
  await requestXScienceJson(`${EXPERT_PROFILES_PATH}/${encodeURIComponent(name)}`, { method: 'DELETE' }, options);
}

export async function loadXScienceExpertInstructions(
  profile: XScienceExpertProfile,
  options: XScienceGatewayOptions = {}
): Promise<string> {
  if (profile.source === 'user') return profile.systemPrompt;
  const payload = await requestXScienceJson<unknown>(
    `${EXPERT_PROFILES_PATH}/${encodeURIComponent(profile.name)}/custom-prompt`,
    { method: 'GET' },
    options
  );
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return '';
  return stringValue((payload as Record<string, unknown>).prompt_text);
}

export async function saveXScienceExpertInstructions(
  profile: XScienceExpertProfile,
  instructions: string,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  if (profile.source === 'user') {
    await updateXScienceExpertProfile(
      profile.name,
      {
        name: profile.name,
        displayName: profile.displayName,
        description: profile.description,
        systemPrompt: instructions,
        enabled: profile.enabled,
      },
      options
    );
    return;
  }

  const path = `${EXPERT_PROFILES_PATH}/${encodeURIComponent(profile.name)}/custom-prompt`;
  if (!instructions.trim()) {
    await requestXScienceJson(path, { method: 'DELETE' }, options);
    return;
  }
  await requestXScienceJson(
    path,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ prompt_text: instructions }),
    },
    options
  );
}

export async function updateXScienceExpertSkills(
  name: string,
  currentSkillNames: string[],
  nextSkillNames: string[],
  options: XScienceGatewayOptions = {}
): Promise<XScienceExpertProfile> {
  const current = new Set(currentSkillNames);
  const next = new Set(nextSkillNames);
  const payload = await requestXScienceJson<unknown>(
    `${EXPERT_PROFILES_PATH}/${encodeURIComponent(name)}/skills`,
    {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        attach: nextSkillNames.filter((skillName) => !current.has(skillName)),
        detach: currentSkillNames.filter((skillName) => !next.has(skillName)),
      }),
    },
    options
  );
  const profile = toExpertProfile(payload);
  if (!profile) throw new Error('X-Science returned an invalid expert capability profile');
  return profile;
}

export async function attachXScienceExpertConnector(
  name: string,
  serverId: string,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  await requestXScienceJson(
    `${EXPERT_PROFILES_PATH}/${encodeURIComponent(name)}/connectors`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ server_id: serverId }),
    },
    options
  );
}

export async function detachXScienceExpertConnector(
  name: string,
  serverId: string,
  options: XScienceGatewayOptions = {}
): Promise<void> {
  await requestXScienceJson(
    `${EXPERT_PROFILES_PATH}/${encodeURIComponent(name)}/connectors/${encodeURIComponent(serverId)}`,
    { method: 'DELETE' },
    options
  );
}

export function normalizeXScienceExpertName(value: string): string {
  return value
    .trim()
    .replace(/[^a-zA-Z0-9]+/gu, '_')
    .replace(/^_+|_+$/gu, '')
    .toUpperCase()
    .slice(0, 32);
}

function toExpertProfile(value: unknown): XScienceExpertProfile | null {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return null;
  const record = value as Record<string, unknown>;
  const name = stringValue(record.name);
  if (!name) return null;
  return {
    id: stringValue(record.id) || undefined,
    name,
    source: stringValue(record.source) || 'bundled',
    displayName: stringValue(record.displayName) || name,
    description: stringValue(record.description),
    systemPrompt: stringValue(record.systemPrompt),
    iconKey: stringValue(record.iconKey),
    colorKey: stringValue(record.colorKey),
    enabled: record.enabled !== false,
    userHidden: record.userHidden === true,
    unrestricted: record.unrestricted === true,
    skillNames: Array.isArray(record.skillNames)
      ? record.skillNames.filter((item): item is string => typeof item === 'string')
      : [],
    connectorIds: Array.isArray(record.connectorIds)
      ? record.connectorIds.filter((item): item is string => typeof item === 'string')
      : [],
  };
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function isPresent<T>(value: T | null): value is T {
  return value !== null;
}

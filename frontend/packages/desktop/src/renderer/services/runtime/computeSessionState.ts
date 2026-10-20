import { requestXScienceJson } from '../xScienceHttp';

export type ComputeSoftwareRuntimeKind =
  | 'local_default'
  | 'local_explicit'
  | 'remote_unsupported'
  | 'invalid_selection'
  | 'unavailable';
export type ComputeSoftwareRuntimePolicy = { kind: ComputeSoftwareRuntimeKind; providerName?: string };
export type SessionComputeState = {
  enabled: string[];
  configured: boolean;
  selectionSource: 'inherited' | 'explicit';
  softwareRuntime: ComputeSoftwareRuntimePolicy;
};

const kinds = new Set<ComputeSoftwareRuntimeKind>([
  'local_default',
  'local_explicit',
  'remote_unsupported',
  'invalid_selection',
  'unavailable',
]);
const invalid = () => new Error('X-Science returned an invalid session compute state');
const object = (value: unknown): value is Record<string, unknown> =>
  !!value && typeof value === 'object' && !Array.isArray(value);
const name = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 255 &&
  value.trim() === value &&
  [...value].every((character) => character.charCodeAt(0) >= 32 && character.charCodeAt(0) !== 127);

export function decodeSessionComputeState(value: unknown): SessionComputeState {
  if (
    !object(value) ||
    Object.keys(value).some((key) => !['enabled', 'configured', 'selectionSource', 'softwareRuntime'].includes(key)) ||
    !Array.isArray(value.enabled) ||
    !value.enabled.every(name) ||
    new Set(value.enabled).size !== value.enabled.length ||
    typeof value.configured !== 'boolean' ||
    value.selectionSource !== (value.configured ? 'explicit' : 'inherited') ||
    !object(value.softwareRuntime)
  )
    throw invalid();
  const policy = value.softwareRuntime;
  if (
    Object.keys(policy).some((key) => !['kind', 'providerName'].includes(key)) ||
    !kinds.has(policy.kind as ComputeSoftwareRuntimeKind) ||
    (Object.hasOwn(policy, 'providerName') && !name(policy.providerName)) ||
    (policy.kind === 'remote_unsupported' ? !name(policy.providerName) : Object.hasOwn(policy, 'providerName')) ||
    (value.configured ? policy.kind === 'local_default' : policy.kind !== 'local_default')
  )
    throw invalid();
  return {
    enabled: [...value.enabled],
    configured: value.configured,
    selectionSource: value.configured ? 'explicit' : 'inherited',
    softwareRuntime: {
      kind: policy.kind as ComputeSoftwareRuntimeKind,
      ...(policy.providerName === undefined ? {} : { providerName: policy.providerName as string }),
    },
  };
}

/** One bounded root observation; failure never falls back to the legacy array GET. */
export async function loadXScienceSessionComputeState(
  rootFrameId: string,
  fetchImpl: typeof fetch = fetch
): Promise<SessionComputeState> {
  try {
    const payload = await requestXScienceJson<unknown>(
      `/api/compute/session/${encodeURIComponent(rootFrameId)}/state`,
      {},
      { fetchImpl, timeoutMs: 15_000 }
    );
    return decodeSessionComputeState(payload);
  } catch (error) {
    if (error instanceof SyntaxError) throw invalid();
    throw error;
  }
}

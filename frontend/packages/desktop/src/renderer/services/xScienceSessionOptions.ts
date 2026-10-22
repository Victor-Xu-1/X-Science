import { invalidateXScienceFrameReads, loadCachedXScienceFrame } from './xScienceFrameReads';
import type { SpecialistInputReference } from '@/common/adapter/specialistBinding';
import { getRendererAccountScopeToken } from './rendererAccountScope';

/** Freshness/operation metadata only. The existing session options own the value. */
export type XScienceSessionPreferenceControl = {
  status: 'loading' | 'ready' | 'writing' | 'readback' | 'unknown';
  pending: boolean;
  error?: unknown;
  refresh: () => void;
  update: (value: boolean) => Promise<void>;
  capture: () => boolean | null;
  current: () => boolean;
};

export type XScienceSessionMemoryControl = XScienceSessionPreferenceControl;

export type XScienceSessionOptions = {
  delegation: boolean;
  autoReview: boolean;
  memory: boolean;
  targetAgent: string;
  goalText: string | null;
  asRoutine: boolean;
};

export type XScienceSessionConfigPatch = {
  autoReview?: boolean;
  memory?: boolean;
  goalText?: null;
};

type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;
type RecordValue = Record<string, unknown>;

const SESSION_REQUEST_TIMEOUT_MS = 8_000;

export const X_SCIENCE_SESSION_DEFAULTS: Readonly<XScienceSessionOptions> = Object.freeze({
  delegation: false,
  autoReview: false,
  // The global/user Memory authority remains separate; retain an explicit session opt-out below.
  memory: true,
  targetAgent: 'OPERON',
  goalText: null,
  asRoutine: false,
});

export function createXScienceSessionDefaults(targetAgent = 'OPERON'): XScienceSessionOptions {
  return {
    ...X_SCIENCE_SESSION_DEFAULTS,
    targetAgent: targetAgent.trim() || X_SCIENCE_SESSION_DEFAULTS.targetAgent,
  };
}

/** Observed preferences are not new intent for an immutable command retry. */
export function sessionOptionsRetrySnapshot(
  options: XScienceSessionOptions
): Omit<XScienceSessionOptions, 'targetAgent' | 'memory' | 'autoReview'> &
  Record<'targetAgent' | 'memory' | 'autoReview', undefined> {
  return { ...options, targetAgent: undefined, memory: undefined, autoReview: undefined };
}

export async function loadXScienceSessionOptions(
  rootFrameId: string,
  fetchImpl: FetchLike = fetch
): Promise<XScienceSessionOptions> {
  const framePayload =
    fetchImpl === fetch
      ? await loadCachedXScienceFrame(rootFrameId)
      : await requestJson(`/api/frames/${encodeURIComponent(rootFrameId)}`, { method: 'GET' }, fetchImpl);
  const payloadRecord = asRecord(framePayload);
  const frame = asRecord(payloadRecord?.frame) ?? payloadRecord;
  if (!frame) throw new Error('X-Science returned invalid session data');

  const inputData = asRecord(frame.input_data) ?? {};
  const contextData = asRecord(frame.context_data);
  const originalInput = asRecord(contextData?._original_input) ?? {};
  const delegation = readBooleanMode(originalInput.ultra_mode ?? inputData.ultra_mode);
  const autoReview = readOnOffMode(originalInput.verifier_mode ?? inputData.verifier_mode);
  const memoryOverride = readOnOffMode(originalInput.memory_mode ?? inputData.memory_mode);
  const goalText = nullableString(originalInput.goal_text ?? inputData.goal_text);
  const asRoutine = readBooleanMode(originalInput.as_routine ?? inputData.as_routine);

  return {
    delegation: delegation ?? X_SCIENCE_SESSION_DEFAULTS.delegation,
    autoReview: autoReview ?? X_SCIENCE_SESSION_DEFAULTS.autoReview,
    memory: memoryOverride ?? X_SCIENCE_SESSION_DEFAULTS.memory,
    targetAgent:
      stringValue(originalInput.target_agent) ||
      stringValue(frame.agent_name) ||
      X_SCIENCE_SESSION_DEFAULTS.targetAgent,
    goalText,
    asRoutine: asRoutine ?? X_SCIENCE_SESSION_DEFAULTS.asRoutine,
  };
}

export async function updateXScienceSessionConfig(
  rootFrameId: string,
  patch: XScienceSessionConfigPatch,
  fetchImpl: FetchLike = fetch,
  options: { rootFrameId?: string; signal?: AbortSignal } = {}
): Promise<void> {
  const body: Record<string, 'on' | 'off' | null> = {};
  if (patch.autoReview !== undefined) body.verifier_mode = patch.autoReview ? 'on' : 'off';
  if (patch.memory !== undefined) body.memory_mode = patch.memory ? 'on' : 'off';
  if (patch.goalText === null) body.goal_text = null;
  if (Object.keys(body).length === 0) return;

  const account = getRendererAccountScopeToken();
  const invalidatePreference = () => {
    if ((patch.memory === undefined && patch.autoReview === undefined) || getRendererAccountScopeToken() !== account)
      return;
    invalidateXScienceFrameReads(rootFrameId);
    if (options.rootFrameId) invalidateXScienceFrameReads(options.rootFrameId);
  };
  invalidatePreference();
  try {
    await requestJson(
      `/api/frames/${encodeURIComponent(rootFrameId)}/session-config`,
      {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
      },
      fetchImpl,
      options.signal
    );
    if (getRendererAccountScopeToken() === account) invalidateXScienceFrameReads(rootFrameId);
  } finally {
    // A lost ACK does not mean the write did not happen. Never retain the old cache.
    invalidatePreference();
  }
}

/** Resolve the server's actual root before observing its session preference. */
export async function loadXScienceSessionMemoryPreference(
  frameId: string,
  options: PreferenceReadOptions = {}
): Promise<{ rootFrameId: string; memory: boolean }> {
  const snapshot = await loadSessionPreference(frameId, 'memory', options);
  return { rootFrameId: snapshot.rootFrameId, memory: snapshot.value };
}

export async function loadXScienceSessionAutoReviewPreference(
  frameId: string,
  options: PreferenceReadOptions = {}
): Promise<{ rootFrameId: string; autoReview: boolean }> {
  const snapshot = await loadSessionPreference(frameId, 'autoReview', options);
  return { rootFrameId: snapshot.rootFrameId, autoReview: snapshot.value };
}

type PreferenceReadOptions = {
  expectedRootFrameId?: string;
  projectId?: string;
  signal?: AbortSignal;
  fetchImpl?: FetchLike;
};

async function loadSessionPreference(frameId: string, key: 'memory' | 'autoReview', options: PreferenceReadOptions) {
  const account = getRendererAccountScopeToken();
  const controller = new AbortController();
  const abort = () => controller.abort(options.signal?.reason);
  options.signal?.addEventListener('abort', abort, { once: true });
  if (options.signal?.aborted) abort();
  const timer = setTimeout(() => controller.abort(), SESSION_REQUEST_TIMEOUT_MS);
  const invalid = () =>
    new Error(
      `X-Science returned invalid session ${key === 'memory' ? 'Memory' : 'Auto-review'} identity or preference`
    );
  const checkCurrent = () => {
    if (getRendererAccountScopeToken() !== account || controller.signal.aborted) {
      throw controller.signal.reason ?? new DOMException('Aborted', 'AbortError');
    }
  };
  const read = async (id: string) => {
    checkCurrent();
    const payload = await loadCachedXScienceFrame(id, {
      // Explicit transport intentionally bypasses the account-local frame cache.
      fetchImpl: (input, init) => (options.fetchImpl ?? fetch)(String(input), init),
      signal: controller.signal,
      timeoutMs: SESSION_REQUEST_TIMEOUT_MS,
    });
    const record = asRecord(payload);
    checkCurrent();
    const frame = asRecord(record?.frame) ?? record;
    if (!frame || frame.id !== id || !stringValue(frame.project_id) || !stringValue(frame.root_frame_id))
      throw invalid();
    return frame;
  };
  try {
    const frame = await read(frameId);
    const rootFrameId = stringValue(frame.root_frame_id);
    const projectId = stringValue(frame.project_id);
    if (
      (options.expectedRootFrameId && options.expectedRootFrameId !== rootFrameId) ||
      (options.projectId && options.projectId !== projectId) ||
      (rootFrameId !== frameId && !stringValue(frame.parent_frame_id))
    )
      throw invalid();
    const root = rootFrameId === frameId ? frame : await read(rootFrameId);
    if (root.root_frame_id !== rootFrameId || stringValue(root.parent_frame_id) || root.project_id !== projectId)
      throw invalid();
    const input = asRecord(root.input_data) ?? {};
    const original = asRecord(asRecord(root.context_data)?._original_input) ?? {};
    const field = key === 'memory' ? 'memory_mode' : 'verifier_mode';
    const raw = Object.hasOwn(original, field) ? original[field] : input[field];
    const value =
      raw === undefined
        ? X_SCIENCE_SESSION_DEFAULTS[key]
        : key === 'autoReview'
          ? readFreshAutoReviewMode(raw)
          : raw === 'on'
            ? true
            : raw === 'off'
              ? false
              : null;
    if (value === null) throw invalid();
    checkCurrent();
    return { rootFrameId, value };
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener('abort', abort);
  }
}

export function toXScienceMessageSessionOptions(
  options: XScienceSessionOptions,
  overrides: {
    delegation?: boolean | null;
    memory?: boolean | null;
    autoReview?: boolean | null;
    planMode?: boolean;
    targetBranchId?: string | null;
    expectedBranchId?: string | null;
    expectedGeneration?: number;
    targetAgent?: string | null;
    specialistBinding?: SpecialistInputReference;
  } = {}
) {
  return {
    // Compatibility guidance only; a live policy owner may omit an unknown value.
    ultra_mode: overrides.delegation === null ? undefined : (overrides.delegation ?? options.delegation),
    ...(overrides.autoReview === null
      ? {}
      : { verifier_mode: (overrides.autoReview ?? options.autoReview) ? ('on' as const) : ('off' as const) }),
    ...(overrides.memory === null
      ? {}
      : { memory_mode: (overrides.memory ?? options.memory) ? ('on' as const) : ('off' as const) }),
    ...(overrides.targetAgent === null ? {} : { target_agent: overrides.targetAgent ?? options.targetAgent }),
    ...(overrides.specialistBinding ? { specialist_binding: overrides.specialistBinding } : {}),
    ...(overrides.planMode === undefined ? {} : { plan_mode: overrides.planMode }),
    ...(overrides.targetBranchId ? { target_branch_id: overrides.targetBranchId } : {}),
    ...(overrides.expectedBranchId ? { expected_branch_id: overrides.expectedBranchId } : {}),
    ...(overrides.expectedGeneration === undefined ? {} : { expected_generation: overrides.expectedGeneration }),
  };
}

async function requestJson(
  path: string,
  init: RequestInit,
  fetchImpl: FetchLike,
  signal?: AbortSignal
): Promise<unknown> {
  const controller = new AbortController();
  const abort = () => controller.abort(signal?.reason);
  signal?.addEventListener('abort', abort, { once: true });
  if (signal?.aborted) abort();
  const timeoutId = setTimeout(() => controller.abort('session_request_timeout'), SESSION_REQUEST_TIMEOUT_MS);
  try {
    const response = await fetchImpl(path, {
      ...init,
      headers: {
        Accept: 'application/json',
        ...init.headers,
      },
      signal: controller.signal,
    });
    const text = response.status === 204 ? '' : await response.text();
    if (!response.ok) {
      let decoded: unknown = null;
      try {
        decoded = text ? JSON.parse(text) : null;
      } catch {
        // Preserve the bounded status-only error when the backend returns a non-JSON body.
      }
      const payload = asRecord(decoded);
      const detail = stringValue(payload?.detail) || stringValue(payload?.message) || stringValue(payload?.error);
      throw new Error(`X-Science session request failed: ${response.status}${detail ? ` ${detail}` : ''}`);
    }
    return text ? JSON.parse(text) : undefined;
  } catch (error) {
    if (controller.signal.aborted) throw new Error('X-Science session request timed out', { cause: error });
    throw error;
  } finally {
    clearTimeout(timeoutId);
    signal?.removeEventListener('abort', abort);
  }
}

function readBooleanMode(value: unknown): boolean | null {
  if (typeof value === 'boolean') return value;
  if (value === 'on') return true;
  if (value === 'off') return false;
  return null;
}

function readOnOffMode(value: unknown): boolean | null {
  if (value === 'on' || value === true) return true;
  if (value === 'off' || value === false) return false;
  return null;
}

/** Match the admitted root review preference without loosening Memory's wire contract. */
function readFreshAutoReviewMode(value: unknown): boolean | null {
  if (typeof value === 'boolean') return value;
  if (typeof value !== 'string') return null;
  const mode = value.trim().toLowerCase();
  return mode === 'on' ? true : mode === 'off' ? false : null;
}

function asRecord(value: unknown): RecordValue | null {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as RecordValue) : null;
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value.trim() : '';
}

function nullableString(value: unknown): string | null {
  return stringValue(value) || null;
}

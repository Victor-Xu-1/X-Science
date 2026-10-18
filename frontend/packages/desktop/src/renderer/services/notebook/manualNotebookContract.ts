import type {
  ManualNotebookRequest,
  ManualNotebookScope,
  ManualNotebookTarget,
  NotebookIdentity,
} from './manualNotebookTypes';

export const recordValue = (value: unknown): value is Record<string, unknown> =>
  Boolean(value) && typeof value === 'object' && !Array.isArray(value);
export const safePositive = (value: unknown): value is number => Number.isSafeInteger(value) && Number(value) > 0;
export const validIdentity = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 512 &&
  value === value.trim() &&
  !['/', '\\', '\0', '\r', '\n', '\t'].some((character) => value.includes(character));
export const validRequestId = (value: unknown): value is string =>
  typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
export function sameNotebookIdentity(a: NotebookIdentity, b: NotebookIdentity): boolean {
  return (
    a.frameId === b.frameId &&
    a.rootFrameId === b.rootFrameId &&
    a.frameIncarnationId === b.frameIncarnationId &&
    a.rootFrameIncarnationId === b.rootFrameIncarnationId
  );
}
export function sameNotebookTarget(a: ManualNotebookTarget, b: ManualNotebookTarget): boolean {
  return (
    sameNotebookIdentity(a, b) &&
    a.kernelId === b.kernelId &&
    a.kernelGeneration === b.kernelGeneration &&
    a.language === b.language &&
    a.environment === b.environment
  );
}
function readIdentity(value: Record<string, unknown>): NotebookIdentity | null {
  if (
    ![value.frame_id, value.root_frame_id, value.frame_incarnation_id, value.root_frame_incarnation_id].every(
      validIdentity
    )
  )
    return null;
  return {
    frameId: value.frame_id as string,
    rootFrameId: value.root_frame_id as string,
    frameIncarnationId: value.frame_incarnation_id as string,
    rootFrameIncarnationId: value.root_frame_incarnation_id as string,
  };
}
export function normalizeManualNotebookScope(value: unknown): ManualNotebookScope | null {
  if (!recordValue(value) || value.schema !== 'x-science.manual-kernel-scope.v1') return null;
  const identity = readIdentity(value);
  return identity ? Object.freeze({ ...identity, schema: 'x-science.manual-kernel-scope.v1' as const }) : null;
}
export function normalizeManualNotebookTarget(
  value: unknown,
  kernel?: Record<string, unknown>
): ManualNotebookTarget | null {
  if (!recordValue(value) || value.schema !== 'x-science.manual-kernel-target.v1') return null;
  const identity = readIdentity(value);
  if (
    !identity ||
    !validIdentity(value.kernel_id) ||
    !safePositive(value.kernel_generation) ||
    (value.language !== 'python' && value.language !== 'r') ||
    typeof value.environment !== 'string' ||
    !/^[A-Za-z0-9_.-]{1,100}$/.test(value.environment)
  )
    return null;
  if (
    kernel &&
    (kernel.kind !== 'analysis' ||
      ['frame_id', 'root_frame_id', 'kernel_id', 'language', 'environment'].some((key) => value[key] !== kernel[key]))
  )
    return null;
  return Object.freeze({
    ...identity,
    schema: 'x-science.manual-kernel-target.v1' as const,
    kernelId: value.kernel_id,
    kernelGeneration: value.kernel_generation,
    language: value.language,
    environment: value.environment,
  });
}
export function notebookTargetQuery(target: ManualNotebookTarget): URLSearchParams {
  const query = notebookScopeQuery(target);
  query.set('kernel_id', target.kernelId);
  query.set('expected_kernel_generation', String(target.kernelGeneration));
  query.set('language', target.language);
  query.set('environment', target.environment);
  return query;
}
export function notebookScopeQuery(scope: NotebookIdentity): URLSearchParams {
  return new URLSearchParams({
    expected_frame_incarnation_id: scope.frameIncarnationId,
    expected_root_frame_id: scope.rootFrameId,
    expected_root_frame_incarnation_id: scope.rootFrameIncarnationId,
  });
}
export function freezeManualNotebookRequest(
  target: ManualNotebookTarget,
  code: string,
  timeoutMs: number,
  requestId: string
): ManualNotebookRequest {
  if (
    !validRequestId(requestId) ||
    !code.trim() ||
    new TextEncoder().encode(code).length > 100000 ||
    !safePositive(timeoutMs) ||
    timeoutMs > 600000 ||
    /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/.test(code)
  ) {
    throw new Error('invalid_manual_notebook_request');
  }
  return Object.freeze({
    schema: 'x-science.manual-kernel-request.v1',
    request_id: requestId,
    expected_frame_incarnation_id: target.frameIncarnationId,
    expected_root_frame_id: target.rootFrameId,
    expected_root_frame_incarnation_id: target.rootFrameIncarnationId,
    kernel_id: target.kernelId,
    expected_kernel_generation: target.kernelGeneration,
    language: target.language,
    environment: target.environment,
    code,
    timeout_ms: timeoutMs,
  });
}
export function sameManualNotebookRequest(a: ManualNotebookRequest, b: ManualNotebookRequest): boolean {
  return (
    (Object.keys(a) as Array<keyof ManualNotebookRequest>).every((key) => a[key] === b[key]) &&
    Object.keys(a).length === Object.keys(b).length
  );
}

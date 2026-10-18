import type { ManualNotebookRequest, ManualNotebookTarget } from '@/renderer/services/notebook/manualNotebookTypes';

export const owner = 'notebook-owner';
export const rawTarget = {
  schema: 'x-science.manual-kernel-target.v1',
  frame_id: 'frame-1',
  root_frame_id: 'root-1',
  frame_incarnation_id: 'frame-inc-1',
  root_frame_incarnation_id: 'root-inc-1',
  kernel_id: 'kernel-1',
  kernel_generation: 7,
  language: 'python',
  environment: 'python',
};
export const target: ManualNotebookTarget = {
  schema: 'x-science.manual-kernel-target.v1',
  frameId: 'frame-1',
  rootFrameId: 'root-1',
  frameIncarnationId: 'frame-inc-1',
  rootFrameIncarnationId: 'root-inc-1',
  kernelId: 'kernel-1',
  kernelGeneration: 7,
  language: 'python',
  environment: 'python',
};
export const rawScope = {
  schema: 'x-science.manual-kernel-scope.v1',
  frame_id: 'frame-1',
  root_frame_id: 'root-1',
  frame_incarnation_id: 'frame-inc-1',
  root_frame_incarnation_id: 'root-inc-1',
};
export const request: ManualNotebookRequest = {
  schema: 'x-science.manual-kernel-request.v1',
  request_id: 'c87acdbe-5689-4617-b570-8416ecf541ac',
  expected_frame_incarnation_id: 'frame-inc-1',
  expected_root_frame_id: 'root-1',
  expected_root_frame_incarnation_id: 'root-inc-1',
  kernel_id: 'kernel-1',
  expected_kernel_generation: 7,
  language: 'python',
  environment: 'python',
  code: 'print("unchanged")\n',
  timeout_ms: 600000,
};
export const response = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'content-type': 'application/json' } });
export const inventory = (targets = [rawTarget]) => ({
  has_history: true,
  manual_execution_scope: rawScope,
  kernels: targets.map((item) => ({
    ...item,
    schema: undefined,
    kind: 'analysis',
    project_id: 'project-1',
    agent_name: 'OPERON',
    busy: false,
    starting: false,
    pid_visible: true,
    execution_count: 0,
    cell_count: 0,
    manual_execution_target: item,
  })),
});
export const pendingPage = (pending: unknown[] = [], next = '') => ({
  schema: 'x-science.manual-kernel-pending.v1',
  pending,
  next_cursor: next,
  scan_complete: next === '',
  through_sequence: 42,
});
export function receipt(
  body: ManualNotebookRequest = request,
  state = 'queued',
  kind: 'typed-v1' | 'legacy' = 'typed-v1'
) {
  const terminal = state === 'terminal';
  return {
    schema: 'x-science.manual-kernel-receipt.v1',
    request_kind: kind,
    ...(kind === 'typed-v1' ? { request: body } : {}),
    durable: {
      version: 1,
      origin: 'user',
      execution_id: 'execution-' + body.request_id,
      owner_id: owner,
      project_id: 'project-1',
      frame_id: 'frame-1',
      root_frame_id: body.expected_root_frame_id,
      frame_incarnation_id: body.expected_frame_incarnation_id,
      root_incarnation_id: body.expected_root_frame_incarnation_id,
      kernel_id: body.kernel_id,
      kernel_generation: body.expected_kernel_generation,
      kernel_kind: 'analysis',
      server_boot_id: 'boot-1',
      request: {
        request_id: body.request_id,
        source: body.code,
        language: body.language,
        environment: body.environment,
        timeout_ms: body.timeout_ms,
        ...(kind === 'typed-v1'
          ? { kernel_id: body.kernel_id, expected_kernel_generation: body.expected_kernel_generation }
          : {}),
      },
      request_sha256: 'a'.repeat(64),
      state,
      state_version: terminal ? 3 : state === 'cancel_requested' ? 2 : 1,
      accepted_at: '2026-10-18T01:00:00Z',
      ...(state === 'cancel_requested' ? { cancel_requested_at: '2026-10-18T01:00:01Z' } : {}),
      ...(terminal
        ? { finished_at: '2026-10-18T01:00:02Z', disposition: 'cancelled_before_start', result_sha256: 'b'.repeat(64) }
        : {}),
    },
    physical: { status: terminal ? 'unavailable' : 'queued', sampled_at: '2026-10-18T01:00:02Z' },
  };
}
export function summary(body: ManualNotebookRequest = request, kind: 'typed-v1' | 'legacy' = 'typed-v1') {
  return {
    request_kind: kind,
    request_id: body.request_id,
    execution_id: 'execution-' + body.request_id,
    target: { ...rawTarget, kernel_generation: body.expected_kernel_generation, kernel_id: body.kernel_id },
    state: 'queued',
    state_version: 1,
    accepted_at: '2026-10-18T01:00:00Z',
  };
}

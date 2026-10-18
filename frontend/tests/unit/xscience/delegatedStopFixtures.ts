import type {
  DelegatedStopRequest,
  DelegatedStopSnapshot,
  DelegatedStopReceipt,
} from '@/renderer/services/runtime/delegatedStopTypes';
export const identity = { ownerId: 'stop-owner-a', parentId: 'stop-parent', frameIncarnationId: 'stop-parent-inc' };
export const snapshot: DelegatedStopSnapshot = {
  schema: 'x-science.delegated-stop-snapshot.v1',
  scope: {
    parent_frame_id: identity.parentId,
    parent_frame_incarnation_id: identity.frameIncarnationId,
    root_frame_id: 'root',
    root_frame_incarnation_id: 'root-inc',
  },
  branch: { branch_id: 'br_11111111', generation: 3 },
  parent_input_revision: 4,
  fence_generation: 0,
  can_stop_branch: true,
  targets: [
    {
      frame_id: 'child',
      origin: 'verified',
      in_selected_branch: true,
      can_stop: true,
      status: 'running',
      target: {
        frame_id: 'child',
        frame_incarnation_id: 'child-inc',
        stream_uid: 'stream',
        stream_epoch: 1,
        input_message_id: 'input',
        input_revision: 2,
        runner_attempt: 1,
      },
    },
  ],
};
export const request: DelegatedStopRequest = {
  schema: 'x-science.delegated-stop.v1',
  request_id: '4b43dc3c-45b9-4132-86d3-3436e8d3f631',
  scope: snapshot.scope,
  branch: snapshot.branch,
  parent_input_revision: 4,
  expected_fence_generation: 0,
  mode: 'branch',
};
export function receipt(original = request): DelegatedStopReceipt {
  return {
    schema: 'x-science.delegated-stop-receipt.v1',
    request: original,
    accepted_at: '2026-10-19T01:00:00Z',
    state: 'stopping',
    replayed: false,
    targets: [{ target: snapshot.targets[0].target!, outcome: 'cancel_requested', execution_observation: 'running' }],
  };
}
export function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}
export function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() {
      return values.size;
    },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    setItem: (key, value) => {
      values.set(key, value);
    },
    removeItem: (key) => {
      values.delete(key);
    },
    key: (index) => [...values.keys()][index] ?? null,
  };
}

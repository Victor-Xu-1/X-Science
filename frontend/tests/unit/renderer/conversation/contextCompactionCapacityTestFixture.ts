import { vi } from 'vitest';
import {
  compactionDependencies,
  type ContextCompactionController,
} from '@/renderer/services/compaction/ContextCompactionController';
import type { CompactionIdentity } from '@/renderer/services/compaction/contextCompactionTypes';
import { getContextCompactionController } from '@/renderer/services/compaction/contextCompactionRegistry';
import {
  decodeCompactionReceipt,
  decodeCompactionSnapshot,
} from '@/renderer/services/compaction/contextCompactionContract';
import { manualCompactionSnapshot } from './manualContextCompactionFixtures';

/** Exercise the real bounded registry with unresolved original requests, not a capacity prop stub. */
export async function retainCompactionCapacity(ownerId: string) {
  const read = vi.spyOn(compactionDependencies, 'read').mockImplementation(async (identity) => {
    const original = manualCompactionSnapshot();
    return decodeCompactionSnapshot(
      {
        ...original,
        target: {
          ...original.target,
          frame_incarnation_id: identity.frameIncarnationId,
          root_frame_id: identity.rootFrameId,
          stream_uid: `frame:${identity.frameId}`,
        },
      },
      identity
    );
  });
  const submit = vi.spyOn(compactionDependencies, 'submit').mockRejectedValue(new Error('lost submit ACK'));
  const lookup = vi.spyOn(compactionDependencies, 'lookup').mockRejectedValue(new Error('receipt not found'));
  const observe = vi.spyOn(compactionDependencies, 'observe').mockReturnValue(() => {});
  const sessions: Array<{ identity: CompactionIdentity; controller: ContextCompactionController }> = [];
  for (let index = 0; index < 32; index++) {
    const identity = {
      ownerId,
      frameId: `capacity-${index}`,
      frameIncarnationId: `capacity-inc-${index}`,
      rootFrameId: 'capacity-root',
    };
    const controller = getContextCompactionController(identity);
    if (!controller) throw new Error('Registry exhausted before its actual bound');
    const release = controller.subscribe(() => {});
    await controller.refresh();
    await controller.start();
    release();
    sessions.push({ identity, controller });
  }
  return {
    sessions,
    read,
    submit,
    settleOne: async () => {
      lookup.mockImplementationOnce(async (identity, request) =>
        decodeCompactionReceipt(
          {
            schema: 'x-science.context-compaction-receipt.v1',
            request,
            frame_id: identity.frameId,
            accepted: false,
            state: 'cancelled',
            cancellation_status: 'cancelled_before_acceptance',
            execution_observation: 'not_started',
            can_submit_next_input: true,
            replayed: true,
          },
          identity,
          request
        )
      );
      const controller = sessions[0].controller;
      const release = controller.subscribe(() => {});
      await controller.refresh();
      release();
    },
    dispose: () => {
      read.mockRestore();
      submit.mockRestore();
      lookup.mockRestore();
      observe.mockRestore();
    },
  };
}

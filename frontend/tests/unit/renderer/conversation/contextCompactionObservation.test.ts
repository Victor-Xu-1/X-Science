import { describe, expect, it, vi } from 'vitest';
import { observeContextCompaction } from '@/renderer/services/compaction/contextCompactionObservation';
import { parseRealtimeMessage } from '@/common/adapter/realtimeProtocol';
import {
  manualCompactionIdentity as identity,
  manualCompactionTarget as target,
} from './manualContextCompactionFixtures';

const events = vi.hoisted(() => ({
  event: null as ((value: Record<string, unknown>) => void) | null,
  reconnect: null as (() => void) | null,
  releaseEvent: vi.fn(),
  releaseReconnect: vi.fn(),
}));
vi.mock('@/common/adapter/httpBridge', () => ({
  wsEmitter: (kind: string, name: string) => ({
    on: (callback: (value: Record<string, unknown>) => void) => {
      expect(kind).toBe('durable');
      expect(name).toBe('compaction_status');
      events.event = callback;
      return events.releaseEvent;
    },
  }),
  realtimeReconnectedEmitter: () => ({
    on: (callback: () => void) => {
      events.reconnect = callback;
      return events.releaseReconnect;
    },
  }),
}));
describe('C11 canonical invalidation only', () => {
  it('filters unrelated and malformed events, and uses same-frame changed root incarnation only for GET invalidation', () => {
    const invalidate = vi.fn();
    const release = observeContextCompaction(identity, invalidate);
    const event = {
      schema: 'x-science.context-compaction-event.v1',
      conversation_id: identity.frameId,
      frame_incarnation_id: identity.frameIncarnationId,
      root_frame_id: identity.rootFrameId,
      root_frame_incarnation_id: target.root_frame_incarnation_id,
      request_id: 'only-a-hint',
      state: 'committed',
    };
    events.event?.({ ...event, schema: 'old' });
    events.event?.({ ...event, conversation_id: 'other-frame' });
    events.event?.({ ...event, frame_incarnation_id: 'replaced-frame' });
    events.event?.({ ...event, root_frame_id: 'other-root' });
    expect(invalidate).not.toHaveBeenCalled();
    const wire = parseRealtimeMessage(
      JSON.stringify({
        ...event,
        type: 'compaction_status',
        _event_id: 'compaction-terminal',
        _sequence: 8,
        _kind: 'fanout',
      })
    );
    expect(wire.ok).toBe(true);
    if (!wire.ok || wire.message.kind !== 'durable') throw new Error('Expected the existing durable fanout adapter');
    events.event?.(wire.message.payload);
    events.event?.({ ...event, root_frame_incarnation_id: 'replaced-root' });
    events.reconnect?.();
    expect(invalidate).toHaveBeenCalledTimes(3);
    release();
    expect(events.releaseEvent).toHaveBeenCalledTimes(1);
    expect(events.releaseReconnect).toHaveBeenCalledTimes(1);
  });
});

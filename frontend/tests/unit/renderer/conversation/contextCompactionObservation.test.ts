import { beforeEach, describe, expect, it, vi } from 'vitest';
import { observeContextCompaction } from '@/renderer/services/compaction/contextCompactionObservation';
import { parseRealtimeMessage } from '@/common/adapter/realtimeProtocol';
import {
  manualCompactionIdentity as identity,
  manualCompactionTarget as target,
} from './manualContextCompactionFixtures';

const events = vi.hoisted(() => ({
  event: null as ((value: Record<string, unknown>) => void) | null,
  runtime: null as ((value: Record<string, unknown>) => void) | null,
  reconnect: null as (() => void) | null,
  releaseEvent: vi.fn(),
  releaseRuntime: vi.fn(),
  releaseReconnect: vi.fn(),
}));
vi.mock('@/common/adapter/httpBridge', () => ({
  wsEmitter: (kind: string, name: string) => ({
    on: (callback: (value: Record<string, unknown>) => void) => {
      expect(kind).toBe('durable');
      if (name === 'compaction_status') {
        events.event = callback;
        return events.releaseEvent;
      }
      expect(name).toBe('runtime.statusChanged');
      events.runtime = callback;
      return events.releaseRuntime;
    },
  }),
  realtimeReconnectedEmitter: () => ({
    on: (callback: () => void) => {
      events.reconnect = callback;
      return events.releaseReconnect;
    },
  }),
}));
beforeEach(() => {
  events.event = null;
  events.runtime = null;
  vi.clearAllMocks();
});
const runtime = {
  resource: 'acp_tool',
  scope: { kind: 'conversation', id: identity.frameId },
  phase: 'ready',
  terminal_status: 'completed',
};
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
    expect(events.releaseRuntime).toHaveBeenCalledTimes(1);
    expect(events.releaseReconnect).toHaveBeenCalledTimes(1);
  });
  it.each(['waiting_for_lock', 'waiting_input', 'downloading', 'extracting', 'validating', 'ready', 'failed'])(
    'invalidates the canonical GET for a scoped ordinary lifecycle phase %s',
    (phase) => {
      const invalidate = vi.fn();
      const release = observeContextCompaction(identity, invalidate);
      events.runtime?.({ ...runtime, phase });
      expect(invalidate).toHaveBeenCalledTimes(1);
      release();
    }
  );
  it('accepts a typed attempt boundary as a dirty hint without reading its phase as permission', () => {
    const invalidate = vi.fn();
    const release = observeContextCompaction(identity, invalidate);
    events.runtime?.({ ...runtime, phase: 'waiting_for_lock', boundary_kind: 'attempt' });
    expect(invalidate).toHaveBeenCalledTimes(1);
    release();
  });
  it.each([
    { resource: 'node' },
    { resource: undefined },
    { scope: { kind: 'mcp', id: identity.frameId } },
    { scope: { kind: 'conversation', id: 'other-frame' } },
    { scope: null },
    { frame_id: 'other-frame' },
    { root_frame_id: 'other-root' },
    { phase: 'unknown' },
    { boundary_kind: 'unknown' },
    { boundary_kind: 'tool' },
    { tool_call_id: 'tool-progress' },
    { tool_name: 'bash' },
  ])('ignores foreign, malformed or tool-progress runtime payload %j', (patch) => {
    const invalidate = vi.fn();
    const release = observeContextCompaction(identity, invalidate);
    events.runtime?.({ ...runtime, ...patch });
    expect(invalidate).not.toHaveBeenCalled();
    release();
  });
});

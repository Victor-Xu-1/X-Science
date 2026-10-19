import { describe, expect, it } from 'vitest';

import {
  createXScienceSessionDefaults,
  loadXScienceSessionOptions,
  toXScienceMessageSessionOptions,
} from '../../packages/desktop/src/renderer/services/xScienceSessionOptions';

describe('X-Science session review defaults', () => {
  it('keeps automatic Reviewer and Bookmarker checkpoints opt-in by default', async () => {
    const fetchImpl = async () =>
      new Response(
        JSON.stringify({
          id: 'frame-1',
          agent_name: 'OPERON',
          input_data: {},
          context_data: { _original_input: {} },
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      );

    const options = await loadXScienceSessionOptions('frame-1', fetchImpl);
    expect(options.autoReview).toBe(false);
    expect(toXScienceMessageSessionOptions(options).verifier_mode).toBe('off');
  });

  it('preserves an explicit per-session opt-out', async () => {
    const fetchImpl = async () =>
      new Response(
        JSON.stringify({
          id: 'frame-2',
          agent_name: 'OPERON',
          input_data: { verifier_mode: 'off' },
          context_data: {},
        }),
        { status: 200, headers: { 'Content-Type': 'application/json' } }
      );

    const options = await loadXScienceSessionOptions('frame-2', fetchImpl);
    expect(options.autoReview).toBe(false);
  });
});

describe('X-Science inherited session Memory', () => {
  const load = (input: Record<string, unknown>, original?: Record<string, unknown>) =>
    loadXScienceSessionOptions(
      'frame-memory',
      async () =>
        new Response(
          JSON.stringify({
            id: 'frame-memory',
            agent_name: 'OPERON',
            input_data: input,
            context_data: original ? { _original_input: original } : {},
          })
        )
    );

  it('inherits Memory for fresh drafts without changing opt-in Reviewer or delegation', () => {
    expect(createXScienceSessionDefaults()).toMatchObject({ memory: true, autoReview: false, delegation: false });
  });

  it.each([undefined, {}])('inherits Memory when the stored session has no override: %j', async (original) => {
    const options = await load({}, original);
    expect(options.memory).toBe(true);
    expect(toXScienceMessageSessionOptions(options).memory_mode).toBe('on');
    expect(options.autoReview).toBe(false);
  });

  it.each(['off', false])('preserves an explicit input opt-out: %j', async (memoryMode) => {
    const options = await load({ memory_mode: memoryMode });
    expect(options.memory).toBe(false);
    expect(toXScienceMessageSessionOptions(options).memory_mode).toBe('off');
  });

  it.each(['off', false])('preserves a durable opt-out over older input: %j', async (memoryMode) => {
    const options = await load({ memory_mode: 'on' }, { memory_mode: memoryMode });
    expect(options.memory).toBe(false);
    expect(toXScienceMessageSessionOptions(options).memory_mode).toBe('off');
  });

  it.each(['on', true])('preserves an explicit opt-in over older opt-out: %j', async (memoryMode) => {
    const options = await load({ memory_mode: 'off' }, { memory_mode: memoryMode });
    expect(options.memory).toBe(true);
  });
});

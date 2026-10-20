import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  createXScienceSessionDefaults,
  loadXScienceSessionOptions,
  loadXScienceSessionMemoryPreference,
  X_SCIENCE_SESSION_DEFAULTS,
  updateXScienceSessionConfig,
} from '@/renderer/services/xScienceSessionOptions';
import { clearXScienceFrameReads, loadCachedXScienceFrame } from '@/renderer/services/xScienceFrameReads';
import { resetRendererAccountScopeForTest, setRendererAccountOwner } from '@/renderer/services/rendererAccountScope';

afterEach(() => {
  clearXScienceFrameReads();
  resetRendererAccountScopeForTest();
  vi.restoreAllMocks();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('X-Science session options service', () => {
  const frame = (id: string, memory: unknown = 'off', root = id) => ({
    id,
    project_id: 'project',
    root_frame_id: root,
    parent_frame_id: id === root ? null : root,
    input_data: memory === undefined ? {} : { memory_mode: memory },
  });
  it('Memory406 reads the checked actual root, bypasses cached child preferences and rejects mismatched identity', async () => {
    const transport = vi.fn<typeof fetch>(async (url) =>
      Response.json(String(url).endsWith('/child') ? frame('child', 'on', 'root') : frame('root', 'off'))
    );
    await expect(
      loadXScienceSessionMemoryPreference('child', {
        fetchImpl: transport,
        expectedRootFrameId: 'root',
        projectId: 'project',
      })
    ).resolves.toEqual({ rootFrameId: 'root', memory: false });
    expect(transport.mock.calls.map(([url]) => url)).toEqual(['/api/frames/child', '/api/frames/root']);
    await expect(
      loadXScienceSessionMemoryPreference('child', { fetchImpl: transport, expectedRootFrameId: 'other-root' })
    ).rejects.toThrow('invalid session Memory');
  });
  it.each([false, true, 'OFF', 'disabled', null])(
    'Memory406 rejects present invalid root mode %j instead of inventing a confirmed preference',
    async (mode) => {
      await expect(
        loadXScienceSessionMemoryPreference('root', { fetchImpl: async () => Response.json(frame('root', mode)) })
      ).rejects.toThrow('invalid session Memory');
    }
  );
  it('Memory406 inherits the one shared default only when the root mode is absent', async () => {
    await expect(
      loadXScienceSessionMemoryPreference('root', {
        fetchImpl: async () => Response.json({ ...frame('root'), input_data: {} }),
      })
    ).resolves.toEqual({ rootFrameId: 'root', memory: X_SCIENCE_SESSION_DEFAULTS.memory });
  });
  it('Memory406 never requests the root after the account changes during the child read', async () => {
    let finish!: (response: Response) => void;
    const transport = vi.fn<typeof fetch>(
      () =>
        new Promise<Response>((done) => {
          finish = done;
        })
    );
    setRendererAccountOwner('account-a');
    const request = loadXScienceSessionMemoryPreference('child', { fetchImpl: transport });
    const rejected = expect(request).rejects.toThrow();
    setRendererAccountOwner('account-b');
    finish(Response.json(frame('child', 'on', 'root')));
    await rejected;
    expect(transport).toHaveBeenCalledTimes(1);
  });
  it('Memory406 bounds the complete fresh preference body read and cleans up the timer', async () => {
    vi.useFakeTimers();
    const transport = vi.fn<typeof fetch>((_url, init) =>
      Promise.resolve(
        new Response(
          new ReadableStream({
            start(controller) {
              init?.signal?.addEventListener(
                'abort',
                () => controller.error(new DOMException('Aborted', 'AbortError')),
                { once: true }
              );
            },
          })
        )
      )
    );
    const request = loadXScienceSessionMemoryPreference('root', { fetchImpl: transport });
    const rejected = expect(request).rejects.toThrow();
    await vi.advanceTimersByTimeAsync(8_000);
    await rejected;
    expect(vi.getTimerCount()).toBe(0);
  });
  it('Memory406 invalidates a cached snapshot even when the write acknowledgement is lost', async () => {
    let memory = 'on';
    const transport = vi.fn<typeof fetch>(async (_url, init) => {
      if (init?.method === 'POST') {
        memory = 'off';
        throw new TypeError('synthetic lost acknowledgement');
      }
      return Response.json({ id: 'memory406-cache', input_data: { memory_mode: memory } });
    });
    vi.stubGlobal('fetch', transport);
    await loadCachedXScienceFrame('memory406-cache');
    await expect(updateXScienceSessionConfig('memory406-cache', { memory: false })).rejects.toThrow(
      'lost acknowledgement'
    );
    await expect(loadCachedXScienceFrame('memory406-cache')).resolves.toMatchObject({
      input_data: { memory_mode: 'off' },
    });
    expect(transport).toHaveBeenCalledTimes(3);
  });
  it('owns one immutable default with inherited Memory and returns isolated draft copies', () => {
    const first = createXScienceSessionDefaults('AIDD_EXPERT');
    const second = createXScienceSessionDefaults('AIDD_EXPERT');

    expect(X_SCIENCE_SESSION_DEFAULTS).toMatchObject({
      delegation: false,
      autoReview: false,
      memory: true,
      targetAgent: 'OPERON',
    });
    expect(first).toEqual({
      delegation: false,
      autoReview: false,
      memory: true,
      targetAgent: 'AIDD_EXPERT',
      goalText: null,
      asRoutine: false,
    });
    expect(first).not.toBe(second);
  });

  it('reproduces v1.1 precedence from original input, input data and frame agent', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: 'frame-stat6',
          agent_name: 'OPERON',
          input_data: {
            ultra_mode: true,
            verifier_mode: 'on',
            memory_mode: 'off',
          },
          context_data: {
            _original_input: {
              ultra_mode: false,
              verifier_mode: 'off',
            },
          },
        })
      )
    );

    const options = await loadXScienceSessionOptions('frame-stat6', fetchMock);

    expect(options).toEqual({
      delegation: false,
      autoReview: false,
      memory: false,
      targetAgent: 'OPERON',
      asRoutine: false,
      goalText: null,
    });
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/frames/frame-stat6',
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
  });

  it('uses inherited Memory without a persisted override and retains opt-in Reviewer', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: 'frame-stat6',
          agent_name: 'OPERON',
          input_data: {},
          context_data: { _original_input: {} },
        })
      )
    );

    await expect(loadXScienceSessionOptions('frame-stat6', fetchMock)).resolves.toMatchObject({
      delegation: false,
      autoReview: false,
      memory: true,
      targetAgent: 'OPERON',
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it('loads a conversational next-turn expert switch from durable session metadata', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          id: 'frame-stat6',
          agent_name: 'OPERON',
          input_data: {},
          context_data: { _original_input: { target_agent: 'AIDD_EXPERT' } },
        })
      )
    );

    await expect(loadXScienceSessionOptions('frame-stat6', fetchMock)).resolves.toMatchObject({
      targetAgent: 'AIDD_EXPERT',
    });
  });

  it('persists verifier and memory mode through the exact v1.1 session-config contract', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(new Response(null, { status: 204 }));

    await updateXScienceSessionConfig('frame-stat6', { autoReview: true, memory: false }, fetchMock);

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/frames/frame-stat6/session-config',
      expect.objectContaining({
        method: 'POST',
        body: JSON.stringify({ verifier_mode: 'on', memory_mode: 'off' }),
      })
    );
  });

  it('surfaces backend validation detail for a rejected session update', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValue(
      new Response(JSON.stringify({ detail: 'frame is archived' }), {
        status: 409,
        headers: { 'content-type': 'application/json' },
      })
    );

    await expect(updateXScienceSessionConfig('frame-stat6', { autoReview: false }, fetchMock)).rejects.toThrow(
      'frame is archived'
    );
  });

  it('aborts a stalled session read and exposes the existing retry path', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn<typeof fetch>(
      (_url, init) =>
        new Promise<Response>((_resolve, reject) => {
          init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')), {
            once: true,
          });
        })
    );

    const request = loadXScienceSessionOptions('frame-stalled', fetchMock);
    const rejection = expect(request).rejects.toThrow('X-Science session request timed out');
    await vi.advanceTimersByTimeAsync(8_000);

    await rejection;
  });

  it('keeps the session deadline active while reading the response body', async () => {
    vi.useFakeTimers();
    const fetchMock = vi.fn<typeof fetch>((_url, init) =>
      Promise.resolve(
        new Response(
          new ReadableStream({
            start(controller) {
              init?.signal?.addEventListener(
                'abort',
                () => controller.error(new DOMException('Aborted', 'AbortError')),
                { once: true }
              );
            },
          }),
          { status: 200, headers: { 'content-type': 'application/json' } }
        )
      )
    );

    const request = loadXScienceSessionOptions('frame-stalled-body', fetchMock);
    const rejection = expect(request).rejects.toThrow('X-Science session request timed out');
    await vi.advanceTimersByTimeAsync(8_000);

    await rejection;
  });
});

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  loadXScienceSessionDefaults,
  saveXScienceSessionDefaults,
} from '@/renderer/services/xScienceSessionDefaults';

const settingsPath =
  '/api/settings/client?keys=xScience.session.defaultModelId%2CxScience.session.subagentModelId%2CxScience.session.effort';

afterEach(() => {
  vi.restoreAllMocks();
});

describe('X-Science session defaults service', () => {
  it('loads typed defaults after validating model IDs against the live model catalog', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            data: {
              'xScience.session.defaultModelId': 'model-a',
              'xScience.session.subagentModelId': 'model-b',
              'xScience.session.effort': 'high',
            },
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            default_model_id: 'model-a',
            data: [
              { id: 'model-a', display_name: 'Model A', active: true },
              { id: 'model-b', display_name: 'Model B', active: true },
            ],
          })
        )
      );

    await expect(loadXScienceSessionDefaults(fetchMock)).resolves.toEqual({
      defaultModelId: 'model-a',
      subagentModelId: 'model-b',
      effort: 'high',
    });
    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      settingsPath,
      expect.objectContaining({ headers: { Accept: 'application/json' } })
    );
    expect(fetchMock.mock.calls[1]?.[0]).toBe('/v1/models');
  });

  it('removes stale model IDs and unsupported effort values while loading', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            'xScience.session.defaultModelId': 'removed-model',
            'xScience.session.subagentModelId': '',
            'xScience.session.effort': 'extreme',
          })
        )
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            default_model_id: 'model-a',
            data: [{ id: 'model-a', display_name: 'Model A', active: true }],
          })
        )
      );

    await expect(loadXScienceSessionDefaults(fetchMock)).resolves.toEqual({});
  });

  it('writes values and removes undefined settings in one backend update', async () => {
    const fetchMock = vi.fn<typeof fetch>().mockResolvedValueOnce(new Response(JSON.stringify({ success: true })));

    await saveXScienceSessionDefaults(
      { defaultModelId: ' model-a ', subagentModelId: undefined, effort: 'medium' },
      fetchMock
    );

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/settings/client',
      expect.objectContaining({
        method: 'PUT',
        body: JSON.stringify({
          'xScience.session.defaultModelId': 'model-a',
          'xScience.session.subagentModelId': null,
          'xScience.session.effort': 'medium',
        }),
      })
    );
  });

  it('surfaces a backend read failure', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(new Response('settings unavailable', { status: 503 }))
      .mockResolvedValueOnce(new Response(JSON.stringify({ data: [] })));

    await expect(loadXScienceSessionDefaults(fetchMock)).rejects.toThrow('503 settings unavailable');
  });
});

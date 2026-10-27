import { beforeEach, describe, expect, it, vi } from 'vitest';
import { DEFAULT_LIBRARY_COLUMN_PREFERENCES as defaults } from '@/common/adapter/libraryColumnPreferences';

const mocks = vi.hoisted(() => ({ http: vi.fn() }));
vi.mock('@/common/adapter/httpBridge', () => ({ httpRequest: mocks.http }));

import {
  loadLibraryColumnPreferences,
  saveLibraryColumnPreferences,
} from '@/renderer/services/libraryColumnPreferences';

describe('Library column preference authenticated setting transport', () => {
  beforeEach(() => vi.clearAllMocks());
  it('uses the existing single-key GET/PUT with cancellation and no anonymous bootstrap', async () => {
    const controller = new AbortController();
    mocks.http.mockResolvedValueOnce({ 'xScience.library.columnPrefs': defaults }).mockResolvedValueOnce(undefined);
    expect(await loadLibraryColumnPreferences(controller.signal)).toEqual(defaults);
    await saveLibraryColumnPreferences(defaults, controller.signal);
    expect(mocks.http.mock.calls).toEqual([
      [
        'GET',
        '/api/settings/client?keys=xScience.library.columnPrefs',
        undefined,
        { signal: controller.signal, timeoutMs: 15_000 },
      ],
      [
        'PUT',
        '/api/settings/client',
        { 'xScience.library.columnPrefs': defaults },
        { signal: controller.signal, timeoutMs: 15_000 },
      ],
    ]);
    await expect(
      saveLibraryColumnPreferences({ ...defaults, version: 2 } as unknown as typeof defaults, controller.signal)
    ).rejects.toThrow('Invalid Library');
    expect(mocks.http).toHaveBeenCalledTimes(2);
  });
});

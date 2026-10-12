import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it, vi } from 'vitest';

const source = readFileSync(new URL('../../../public/storage-namespace-upgrade.js', import.meta.url), 'utf8');
const flush = () => new Promise<void>((resolve) => setImmediate(resolve));
const readyMessage = { type: 'x-science-cache-namespace-ready-v1', cache: 'x-science-webui-v2' };
const emptyStorage = { length: 0, key: () => null, getItem: () => null, setItem() {}, removeItem() {} };

function createHarness(
  initialCaches = ['x-science-webui-v2', 'synon-ai-webui-v2', 'synon-biomed-structure-thumbnails-v1', 'unowned-cache']
) {
  const controller = { postMessage: vi.fn() };
  const listeners = new Map<string, (event: unknown) => void>();
  const cacheNames = new Set(initialCaches);
  const serviceWorker = {
    controller,
    addEventListener: (name: string, listener: (event: unknown) => void) => listeners.set(name, listener),
    removeEventListener: (name: string) => listeners.delete(name),
  };
  const caches = {
    keys: vi.fn(async () => [...cacheNames]),
    delete: vi.fn(async (key: string) => cacheNames.delete(key)),
  };
  const window = {
    localStorage: emptyStorage,
    sessionStorage: emptyStorage,
    navigator: { serviceWorker },
    caches,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    __xScienceStorageNamespaceUpgrade: undefined as
      | { failures: Array<{ code: string }>; cachePending: boolean }
      | undefined,
  };
  runInNewContext(source, { window, console: { warn: vi.fn() } });
  const message = (data = readyMessage, source: object | null = serviceWorker.controller) => {
    listeners.get('message')?.({ data, source });
  };
  return { controller, caches, cacheNames, serviceWorker, window, listeners, message };
}

describe('cache namespace retirement', () => {
  it('does not delete old caches based on a worker URL or missing claim response', async () => {
    const harness = createHarness();
    await flush();
    expect(harness.controller.postMessage).toHaveBeenCalledWith({ type: 'x-science-cache-namespace-probe-v1' });
    expect(harness.caches.delete).not.toHaveBeenCalled();
    expect(harness.window.__xScienceStorageNamespaceUpgrade?.cachePending).toBe(true);
  });

  it('rejects spoofed messages, obsolete namespace responses and absent sources', async () => {
    const harness = createHarness();
    harness.message(readyMessage, { postMessage() {} });
    harness.message({ ...readyMessage, cache: 'synon-ai-webui-v2' });
    harness.message(readyMessage, null);
    await flush();
    expect(harness.caches.delete).not.toHaveBeenCalled();
  });

  it('retires only exact legacy caches after the current new worker claims the page', async () => {
    const harness = createHarness();
    harness.message();
    harness.message();
    await flush();
    expect([...harness.cacheNames]).toEqual(['x-science-webui-v2', 'unowned-cache']);
    expect(harness.caches.delete).toHaveBeenCalledTimes(2);
    expect(harness.window.__xScienceStorageNamespaceUpgrade?.cachePending).toBe(false);
    expect(harness.listeners.size).toBe(0);
  });

  it('does not remove the fallback source when the new cache does not exist', async () => {
    const harness = createHarness(['synon-ai-webui-v2', 'synon-biomed-structure-thumbnails-v1']);
    harness.message();
    await flush();
    expect(harness.caches.delete).not.toHaveBeenCalled();
    expect(harness.window.__xScienceStorageNamespaceUpgrade?.failures.map((failure) => failure.code)).toContain(
      'cache_retirement_failed'
    );
  });

  it('rechecks controller identity after asynchronous inventory and never trusts a previous worker', async () => {
    const harness = createHarness();
    harness.caches.keys.mockImplementation(async () => {
      harness.serviceWorker.controller = { postMessage: vi.fn() };
      return [...harness.cacheNames];
    });
    harness.message();
    await flush();
    expect(harness.caches.delete).not.toHaveBeenCalled();
    harness.listeners.get('controllerchange')?.({});
    expect(harness.serviceWorker.controller.postMessage).toHaveBeenCalledTimes(1);
  });

  it('reports failed deletion without clearing the new cache or exposing contents', async () => {
    const harness = createHarness();
    harness.caches.delete.mockResolvedValue(false);
    harness.message();
    await flush();
    expect(harness.window.__xScienceStorageNamespaceUpgrade?.failures.map((failure) => failure.code)).toContain(
      'cache_retirement_failed'
    );
    expect(harness.cacheNames.has('x-science-webui-v2')).toBe(true);
    expect(harness.cacheNames.has('synon-ai-webui-v2')).toBe(true);
  });
});

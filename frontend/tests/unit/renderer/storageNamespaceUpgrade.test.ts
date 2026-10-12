import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { describe, expect, it, vi } from 'vitest';

const startupSource = readFileSync(new URL('../../../public/storage-namespace-upgrade.js', import.meta.url), 'utf8');
type Report = { migrated: number; failures: Array<{ area: string; code: string }>; cachePending: boolean };
class MemoryStorage {
  readonly values = new Map<string, string>();
  get length() {
    return this.values.size;
  }
  key(index: number) {
    return [...this.values.keys()][index] ?? null;
  }
  getItem(key: string): string | null {
    return this.values.get(key) ?? null;
  }
  setItem(key: string, value: string) {
    this.values.set(key, value);
  }
  removeItem(key: string) {
    this.values.delete(key);
  }
}

function upgrade(localStorage = new MemoryStorage(), sessionStorage = new MemoryStorage(), extra = {}) {
  const window = {
    localStorage,
    sessionStorage,
    __xScienceStorageNamespaceUpgrade: undefined as Report | undefined,
    ...extra,
  };
  const warn = vi.fn();
  runInNewContext(startupSource, { window, console: { warn } });
  return { result: window.__xScienceStorageNamespaceUpgrade!, warn };
}
const codes = (report: Report) => report.failures.map((failure) => failure.code);

describe('browser storage namespace upgrade', () => {
  it('runs before appearance restoration and the renderer module', () => {
    const html = readFileSync(new URL('../../../packages/desktop/src/renderer/index.html', import.meta.url), 'utf8');
    const migration = html.indexOf('src="/storage-namespace-upgrade.js"');
    expect(migration).toBeGreaterThan(0);
    expect(migration).toBeLessThan(html.indexOf('src="/theme-init.js"'));
    expect(migration).toBeLessThan(html.indexOf('data-app-entry'));
  });

  it('preserves saved language, preference bytes and user identities', () => {
    const storage = new MemoryStorage();
    storage.setItem('__synon-ai_theme', 'dark');
    storage.setItem('i18nextLng', 'zh-CN');
    const profiles = '{"owner-synon":{"displayName":"my original synon name"}}';
    storage.setItem('synon-biomed:user-profiles:v1', profiles);
    const { result } = upgrade(storage);
    expect(storage.getItem('__x-science_theme')).toBe('dark');
    expect(storage.getItem('__synon-ai_theme')).toBeNull();
    expect(storage.getItem('i18nextLng')).toBe('zh-CN');
    expect(storage.getItem('x-science:user-profiles:v1')).toBe(profiles);
    expect(result.failures).toEqual([]);
  });

  it('restores the saved appearance through the actual following theme-init script', () => {
    const storage = new MemoryStorage();
    storage.setItem('__synon-ai_theme', 'dark');
    storage.setItem('__synon-ai_theme_family', 'cool');
    upgrade(storage);
    const rootAttributes = new Map<string, string>();
    const bodyAttributes = new Map<string, string>();
    const themeSource = readFileSync(new URL('../../../public/theme-init.js', import.meta.url), 'utf8');
    runInNewContext(themeSource, {
      localStorage: storage,
      document: {
        documentElement: { setAttribute: (key: string, value: string) => rootAttributes.set(key, value) },
        body: { setAttribute: (key: string, value: string) => bodyAttributes.set(key, value) },
      },
    });
    expect(rootAttributes.get('data-theme')).toBe('dark');
    expect(rootAttributes.get('data-theme-family')).toBe('cool');
    expect(bodyAttributes.get('arco-theme')).toBe('dark');
  });

  it.each([
    ['synonbiomed.composer.draft.v1:owner%2Fsynon:frame', 'xscience.composer.draft.v1:owner%2Fsynon:frame'],
    ['synonbiomed.onboarding.draft.v1:owner', 'xscience.onboarding.draft.v1:owner'],
    ['synon-biomed.task-status.presentation.v5.owner', 'x-science.task-status.presentation.v5.owner'],
    [
      'synon.realtime.cursor.v1:origin:scope=global:owner=abc',
      'x-science.realtime.cursor.v1:origin:scope=global:owner=abc',
    ],
  ])('migrates scoped key %s without changing its opaque suffix or value', (from, to) => {
    const storage = new MemoryStorage();
    const raw = '{"prompt":"synon, synonym", "file":"synon-original.sdf"}';
    storage.setItem(from, raw);
    upgrade(storage);
    expect(storage.getItem(from)).toBeNull();
    expect(storage.getItem(to)).toBe(raw);
  });

  it('does not migrate unknown namespaces, unscoped queue data or prefix lookalikes', () => {
    const storage = new MemoryStorage();
    for (const key of [
      'synon.custom-plugin',
      'synonbiomed.composer.draft.v10:owner',
      'conversation-command-queue/v2/owner/frame',
    ]) {
      storage.setItem(key, 'original');
    }
    upgrade(storage);
    expect([...storage.values.values()]).toEqual(['original', 'original', 'original']);
  });

  it('migrates session and local namespaces independently', () => {
    const session = new MemoryStorage();
    session.setItem('synonbiomed.auth.returnPath', '/conversation/original');
    upgrade(new MemoryStorage(), session);
    expect(session.getItem('xscience.auth.returnPath')).toBe('/conversation/original');
  });

  it('moves connector storage tokens byte-for-byte without claiming renewed authorization', () => {
    const storage = new MemoryStorage();
    storage.setItem('synon.appSessionToken', 'original.signed-token');
    storage.setItem('synon.link.autoConnect', 'false');
    upgrade(storage);
    expect(storage.getItem('x-science.appSessionToken')).toBe('original.signed-token');
    expect(storage.getItem('x-science.link.autoConnect')).toBe('false');
    expect(storage.getItem('synon.appSessionToken')).toBeNull();
  });

  it('preserves both distinct collision values and exposes only metadata', () => {
    const storage = new MemoryStorage();
    storage.setItem('__synon-ai_theme', 'SECRET_OLD');
    storage.setItem('__x-science_theme', 'SECRET_NEW');
    const { result, warn } = upgrade(storage);
    expect(storage.getItem('__synon-ai_theme')).toBe('SECRET_OLD');
    expect(storage.getItem('__x-science_theme')).toBe('SECRET_NEW');
    expect(codes(result)).toContain('value_conflict');
    expect(JSON.stringify([result, warn.mock.calls])).not.toContain('SECRET');
  });

  it('completes an interrupted equal-value copy and remains idempotent', () => {
    const storage = new MemoryStorage();
    storage.setItem('__synon-ai_theme', 'dark');
    storage.setItem('__x-science_theme', 'dark');
    expect(upgrade(storage).result.migrated).toBe(1);
    expect(storage.getItem('__synon-ai_theme')).toBeNull();
    expect(upgrade(storage).result.migrated).toBe(0);
  });

  it('retains original data after quota, readback or deletion failures', () => {
    for (const operation of ['throw', 'write', 'remove'] as const) {
      const storage = new MemoryStorage();
      storage.setItem('__synon-ai_theme', 'dark');
      if (operation === 'throw')
        storage.setItem = () => {
          throw new Error('quota exceeded');
        };
      if (operation === 'write') storage.setItem = () => {};
      if (operation === 'remove') storage.removeItem = () => {};
      expect(upgrade(storage).result.failures.length).toBeGreaterThan(0);
      expect(storage.getItem('__synon-ai_theme')).toBe('dark');
    }
  });

  it('does not delete a changed source after copying the preceding value', () => {
    const storage = new MemoryStorage();
    storage.setItem('__synon-ai_theme', 'dark');
    const write = storage.setItem.bind(storage);
    storage.setItem = (key, value) => {
      write(key, value);
      write('__synon-ai_theme', 'updated');
    };
    expect(codes(upgrade(storage).result)).toContain('source_changed');
    expect(storage.getItem('__synon-ai_theme')).toBe('updated');
  });

  it('preserves the source if a competing target update follows the first readback', () => {
    const storage = new MemoryStorage();
    storage.setItem('__synon-ai_theme', 'dark');
    const read = storage.getItem.bind(storage);
    let targetReads = 0;
    storage.getItem = (key) => {
      if (key === '__x-science_theme' && ++targetReads === 3) storage.setItem(key, 'newer preference');
      return read(key);
    };
    expect(codes(upgrade(storage).result)).toContain('target_changed');
    expect(storage.getItem('__synon-ai_theme')).toBe('dark');
    expect(storage.getItem('__x-science_theme')).toBe('newer preference');
  });

  it('bounds inventory work and reports inaccessible storage', () => {
    const storage = new MemoryStorage();
    for (let index = 0; index < 4097; index++) storage.setItem(`unowned-${index}`, 'value');
    expect(codes(upgrade(storage).result)).toContain('inventory_limit');
    const extra = Object.defineProperty({}, 'localStorage', {
      enumerable: true,
      get() {
        throw new Error('denied');
      },
    });
    const window = {
      sessionStorage: new MemoryStorage(),
      __xScienceStorageNamespaceUpgrade: undefined as Report | undefined,
    };
    Object.defineProperty(window, 'localStorage', Object.getOwnPropertyDescriptor(extra, 'localStorage')!);
    runInNewContext(startupSource, { window, console: { warn() {} } });
    expect(codes(window.__xScienceStorageNamespaceUpgrade!)).toContain('storage_unavailable');
  });
});

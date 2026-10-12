import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';

// Controlled API-contract fixture, not evidence of an installed browser profile.
function databaseFixture(initial) {
  const databases = new Map(Object.entries(initial).map(([name, stores]) => [name,
    new Map(Object.entries(stores).map(([store, values]) => [store, new Map(values)]))]));
  const opened = [];
  function connection(name) {
    const stores = databases.get(name);
    return {
      objectStoreNames: { contains: store => stores.has(store) }, close() {},
      createObjectStore(store) { stores.set(store, new Map()); },
      transaction(names, mode) {
        const snapshot = new Map([...stores].map(([store, values]) => [store, new Map(values)]));
        let pending = 0;
        let aborted = false;
        const transaction = {
          abort() {
            if (aborted) return;
            aborted = true;
            databases.set(name, snapshot);
            transaction.onabort?.();
          },
          objectStore(store) {
            function request(operation) {
              pending += 1;
              const result = {};
              setImmediate(() => {
                if (aborted) return;
                try {
                  result.result = operation(stores.get(store));
                  result.onsuccess?.();
                } catch (error) {
                  result.error = error;
                  transaction.error = error;
                  transaction.abort();
                }
                pending -= 1;
                if (!pending && !aborted) transaction.oncomplete?.();
              });
              return result;
            }
            return {
              get: key => request(values => values.get(key)),
              getAll: (_query, count) => request(values => [...values.values()].slice(0, count)),
              count: () => request(values => values.size),
              add: (value, key = value.id) => request(values => {
                if (mode !== 'readwrite' || values.has(key)) throw new Error('ConstraintError');
                values.set(key, value);
              }),
            };
          },
        };
        return transaction;
      },
    };
  }
  const api = {
    async databases() { return [...databases.keys()].map(name => ({ name })); },
    open(name, version) {
      opened.push(name);
      const request = {};
      setImmediate(() => {
        const fresh = !databases.has(name);
        if (fresh) databases.set(name, new Map());
        request.result = connection(name);
        request.transaction = { abort() { databases.delete(name); } };
        if (fresh || (version === 2 && !databases.get(name).has('namespaceUpgrades'))) request.onupgradeneeded?.();
        request.onsuccess?.();
      });
      return request;
    },
  };
  return { api, databases, opened };
}

async function load(initial) {
  const fixture = databaseFixture(initial);
  globalThis.indexedDB = fixture.api;
  globalThis.chrome = { runtime: { id: 'owned-connector' } };
  globalThis.location = { protocol: 'chrome-extension:', hostname: 'owned-connector' };
  const source = await readFile(new URL('../../assets/x-science-link/source/local-files-database.js', import.meta.url), 'utf8');
  const module = await import('data:text/javascript;base64,' + Buffer.from(source + '\n// case=' + Math.random()).toString('base64'));
  return { ...fixture, open: module.openLocalFilesDatabase };
}

test('capability upgrade preserves actual handle records and publishes one canonical authority', async () => {
  const handle = { kind: 'directory', async queryPermission() { return 'prompt'; } };
  const record = { id: 'folder_1', name: 'Original', handle, grantedAt: 42 };
  const fixture = await load({ 'synon-link-local-files': { folders: [['folder_1', record]] } });
  (await fixture.open()).close();
  const canonical = fixture.databases.get('x-science-link-local-files');
  assert.equal(canonical.get('folders').get('folder_1'), record);
  assert.equal(canonical.get('namespaceUpgrades').get('local-files-v1'), true);
  assert.equal(fixture.databases.get('synon-link-local-files').get('folders').get('folder_1'), record);
  (await fixture.open()).close();
  assert.equal(fixture.opened.filter(name => name === 'synon-link-local-files').length, 1);
});

test('different canonical inventory fails closed without overwrite or automatic authorization', async () => {
  const handle = { kind: 'directory', async queryPermission() { throw new Error('must not grant'); } };
  const fixture = await load({
    'synon-link-local-files': { folders: [['old', { id: 'old', handle }]] },
    'x-science-link-local-files': { folders: [['current', { id: 'current', handle }]], namespaceUpgrades: [] },
  });
  await assert.rejects(fixture.open(), /conflict/);
  assert.deepEqual([...fixture.databases.get('x-science-link-local-files').get('folders').keys()], ['current']);
  assert.deepEqual([...fixture.databases.get('synon-link-local-files').get('folders').keys()], ['old']);
});

test('invalid legacy capability never creates a success marker', async () => {
  const fixture = await load({ 'synon-link-local-files': { folders: [['bad', { id: 'bad', handle: {} }]] } });
  await assert.rejects(fixture.open(), /invalid/);
  assert.equal(fixture.databases.get('x-science-link-local-files').get('namespaceUpgrades').size, 0);
});

test('foreign origins cannot inspect or migrate extension capabilities', async () => {
  const fixture = await load({});
  globalThis.location = { protocol: 'https:', hostname: 'owned-connector' };
  await assert.rejects(fixture.open(), /extension origin/);
  assert.deepEqual(fixture.opened, []);
});

test('oversized capability inventories are bounded and original records stay recoverable', async () => {
  const handle = { kind: 'directory', async queryPermission() { return 'prompt'; } };
  const records = Array.from({ length: 1201 }, (_item, index) => ['id_' + index, { id: 'id_' + index, handle }]);
  const fixture = await load({ 'synon-link-local-files': { folders: records } });
  await assert.rejects(fixture.open(), /exceeds its bound/);
  assert.equal(fixture.databases.get('synon-link-local-files').get('folders').size, 1201);
  assert.equal(fixture.databases.get('x-science-link-local-files').get('namespaceUpgrades').size, 0);
});

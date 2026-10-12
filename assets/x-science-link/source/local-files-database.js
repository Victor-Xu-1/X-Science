// One extension-origin-owned capability store. Historical names occur only as
// explicit migration inputs; they are never emitted as current product marks.
const DATABASE = 'x-science-link-local-files'
const PREVIOUS_DATABASE = 'synon-link-local-files'
const FOLDERS = 'folders'
const MIGRATIONS = 'namespaceUpgrades'
const MIGRATION = 'local-files-v1'
const MAX_FOLDERS = 1200
const WAIT_MS = 15000
let upgrade

function requireExtensionOrigin() {
  if (globalThis.location?.protocol !== 'chrome-extension:' ||
      globalThis.location.hostname !== globalThis.chrome?.runtime?.id) {
    throw new Error('Local file capabilities require this browser extension origin')
  }
}

function openDatabase(name, version) {
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(name, version)
    let settled = false
    const timer = setTimeout(() => {
      settled = true
      reject(new Error('Local file database upgrade is blocked; close older extension pages and retry'))
    }, WAIT_MS)
    request.onupgradeneeded = () => {
      if (name === PREVIOUS_DATABASE) {
        request.transaction.abort()
        return
      }
      const database = request.result
      if (!database.objectStoreNames.contains(FOLDERS)) database.createObjectStore(FOLDERS, { keyPath: 'id' })
      if (!database.objectStoreNames.contains(MIGRATIONS)) database.createObjectStore(MIGRATIONS)
    }
    request.onsuccess = () => {
      clearTimeout(timer)
      if (settled) request.result.close()
      else resolve(request.result)
    }
    request.onerror = () => {
      clearTimeout(timer)
      reject(request.error || new Error('Local file database is unavailable'))
    }
  })
}

function read(database, store, operation) {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction(store, 'readonly')
    const request = operation(transaction.objectStore(store))
    transaction.oncomplete = () => resolve(request.result)
    transaction.onabort = transaction.onerror = () => reject(transaction.error || request.error)
  })
}

function validateRecords(records) {
  if (!Array.isArray(records) || records.length > MAX_FOLDERS) throw new Error('Previous local file grant inventory exceeds its bound')
  const identities = new Set()
  for (const record of records) {
    if (typeof record?.id !== 'string' || !record.id || record.id.length > 256 ||
        identities.has(record.id) || record.handle?.kind !== 'directory' ||
        typeof record.handle.queryPermission !== 'function') {
      throw new Error('Previous local file grant inventory is invalid; original capabilities were preserved')
    }
    identities.add(record.id)
  }
  return records
}

function commitUpgrade(database, records) {
  return new Promise((resolve, reject) => {
    const transaction = database.transaction([FOLDERS, MIGRATIONS], 'readwrite')
    const marker = transaction.objectStore(MIGRATIONS).get(MIGRATION)
    marker.onsuccess = () => {
      if (marker.result === true) return
      const folders = transaction.objectStore(FOLDERS)
      const existing = folders.count()
      existing.onsuccess = () => {
        if (existing.result && records.length) {
          transaction.abort()
          return
        }
        // add, never put: an identity collision aborts the complete transaction.
        for (const record of records) folders.add(record)
        transaction.objectStore(MIGRATIONS).add(true, MIGRATION)
      }
    }
    transaction.oncomplete = () => resolve()
    transaction.onabort = transaction.onerror = () => reject(transaction.error ||
      new Error('Current and previous file grants conflict; neither inventory was overwritten'))
  })
}

async function upgradeDatabase() {
  requireExtensionOrigin()
  const current = await openDatabase(DATABASE, 2)
  try {
    if (await read(current, MIGRATIONS, store => store.get(MIGRATION)) === true) return
    if (typeof indexedDB.databases !== 'function') throw new Error('This browser cannot safely inspect previous local file grants')
    const databases = await indexedDB.databases()
    let records = []
    if (databases.some(database => database.name === PREVIOUS_DATABASE)) {
      const previous = await openDatabase(PREVIOUS_DATABASE)
      try {
        if (!previous.objectStoreNames.contains(FOLDERS)) throw new Error('Previous local file grant database has no capability store')
        records = validateRecords(await read(previous, FOLDERS, store => store.getAll(undefined, MAX_FOLDERS + 1)))
      } finally {
        previous.close()
      }
    }
    await commitUpgrade(current, records)
    // The old database is a recovery source, never a live fallback authority.
    // Query/requestPermission remains owned by the existing actual operation.
  } finally {
    current.close()
  }
}

export async function openLocalFilesDatabase() {
  requireExtensionOrigin()
  if (!upgrade) {
    upgrade = upgradeDatabase().catch(error => { upgrade = undefined; throw error })
  }
  await upgrade
  return openDatabase(DATABASE, 2)
}

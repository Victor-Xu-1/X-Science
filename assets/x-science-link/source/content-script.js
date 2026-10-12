const BRIDGE_SOURCE = 'x-science-link-extension'
const PAGE_SOURCE = 'x-science-link-page'
const REQUEST_TYPE = 'x-science-link-request'
const RESPONSE_TYPE = 'x-science-link-response'
const APP_SESSION_STORAGE_KEY = 'x-science.appSessionToken'
const AUTO_CONNECT_STORAGE_KEY = 'x-science.link.autoConnect'

let autoConnectStarted = false
let autoConnectPollCount = 0

announce()
window.setTimeout(announce, 300)
window.setTimeout(announce, 1200)
window.setTimeout(announce, 3000)
window.setTimeout(autoConnectFromPageSession, 700)
const autoConnectPoll = window.setInterval(() => {
  autoConnectPollCount += 1
  void autoConnectFromPageSession()
  if (autoConnectStarted || autoConnectPollCount >= 30) {
    window.clearInterval(autoConnectPoll)
  }
}, 1000)

window.addEventListener('message', (event) => {
  if (event.source !== window) return
  const message = event.data
  if (!message || message.source !== PAGE_SOURCE || message.type !== REQUEST_TYPE) return

  void handlePageRequest(message)
})

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === 'status_changed') {
    window.postMessage({
      source: BRIDGE_SOURCE,
      type: 'status_changed',
      status: message.status,
    }, window.location.origin)
  }
})

async function handlePageRequest(message) {
  const { requestId, action, payload } = message
  try {
    const result = await chrome.runtime.sendMessage(toRuntimeMessage(action, payload))
    window.postMessage({
      source: BRIDGE_SOURCE,
      type: RESPONSE_TYPE,
      requestId,
      ok: result?.ok !== false,
      result,
    }, window.location.origin)
  } catch (error) {
    window.postMessage({
      source: BRIDGE_SOURCE,
      type: RESPONSE_TYPE,
      requestId,
      ok: false,
      error: error instanceof Error ? error.message : String(error),
    }, window.location.origin)
  }
}

function toRuntimeMessage(action, payload = {}) {
  if (action === 'status') return { type: 'status' }
  if (action === 'disconnect') return { type: 'disconnect' }
  if (action === 'ensure_workspace') {
    return { type: 'ensure_workspace', active: payload.active !== false }
  }
  if (action === 'local_files_status') return { type: 'local_files_status' }
  if (action === 'bookmarks_status') return { type: 'bookmarks_status' }
  if (action === 'request_bookmarks_permission') {
    return { type: 'request_bookmarks_permission', refresh: payload.refresh !== false }
  }
  if (action === 'bookmarks_profile') {
    return { type: 'refresh_bookmarks_profile', refresh: payload.refresh !== false, prompt: payload.prompt !== false }
  }
  if (action === 'revoke_bookmarks_permission') return { type: 'revoke_bookmarks_permission' }
  if (action === 'clear_local_files') return { type: 'clear_local_files' }
  if (action === 'open_downloads_folder') return { type: 'open_downloads_folder' }
  if (action === 'show_downloaded_file') {
    return {
      type: 'show_downloaded_file',
      downloadId: payload.downloadId,
      filename: payload.filename,
      url: payload.url,
    }
  }
  if (action === 'open_local_files') return { type: 'open_local_files' }
  if (action === 'open_personalization') return { type: 'open_personalization', reason: payload.reason }
  if (action === 'connect') {
    return {
      type: 'connect',
      serverUrl: payload.serverUrl,
      appSession: payload.appSession,
      createWorkspace: payload.createWorkspace !== false,
      requestPersonalization: payload.requestPersonalization === true,
    }
  }
  throw new Error(`Unsupported page bridge action: ${action}`)
}

function announce() {
  window.postMessage({
    source: BRIDGE_SOURCE,
    type: 'available',
    version: chrome.runtime.getManifest().version,
  }, window.location.origin)
}

async function autoConnectFromPageSession() {
  if (autoConnectStarted) return
  const appSession = readLocalStorage(APP_SESSION_STORAGE_KEY)
  if (!appSession) return
  if (readLocalStorage(AUTO_CONNECT_STORAGE_KEY) === '0') return

  autoConnectStarted = true
  try {
    const result = await chrome.runtime.sendMessage({
      type: 'connect',
      serverUrl: resolveServerUrl(),
      appSession,
      createWorkspace: true,
      autoConnect: true,
      requestPersonalization: true,
    })
    if (result?.ok === false) throw new Error('Connector rejected this page origin')
    window.localStorage.setItem(AUTO_CONNECT_STORAGE_KEY, '1')
  } catch {
    autoConnectStarted = false
  }
}

function readLocalStorage(key) {
  try {
    return window.localStorage.getItem(key)?.trim() || ''
  } catch {
    return ''
  }
}

function resolveServerUrl() {
  return window.location.origin
}

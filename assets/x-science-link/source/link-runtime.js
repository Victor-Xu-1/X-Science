export const X_SCIENCE_LINK_PROTOCOL_VERSION = '1'
export const X_SCIENCE_LINK_POLICY_VERSION = '1'
export const EXTENSION_RUNTIME_BUILD = '2026-06-02-info-flow-upgrade'
export const DEFAULT_X_SCIENCE_SERVER_URL = 'http://127.0.0.1:8765'

const PAGE_BRIDGE_ACTIONS = new Set([
  'status', 'connect', 'disconnect', 'ensure_workspace', 'local_files_status',
  'bookmarks_status', 'request_bookmarks_permission', 'refresh_bookmarks_profile',
  'revoke_bookmarks_permission', 'clear_local_files', 'open_downloads_folder',
  'show_downloaded_file', 'open_local_files', 'open_personalization',
])

function httpOrigin(value) {
  try {
    const parsed = new URL(value)
    if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) return ''
    // Both documented fixed localhost entry names identify the same service.
    // Scheme and port remain part of the authority; another local port does not.
    if (parsed.hostname === 'localhost') parsed.hostname = '127.0.0.1'
    return parsed.origin
  } catch {
    return ''
  }
}

export function isTrustedAppPage(pageUrl, configuredServerUrl) {
  const configuredOrigin = httpOrigin(configuredServerUrl)
  return Boolean(configuredOrigin && httpOrigin(pageUrl) === configuredOrigin)
}

function isDisallowedPublicFetchHost(hostname) {
  const host = String(hostname || '').toLowerCase().replace(/^\[|\]$/g, '')
  if (!host || host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.local')) return true
  if (host === '127.0.0.1' || host.startsWith('127.')) return true
  if (host === '::1' || host === '0:0:0:0:0:0:0:1') return true
  if (/^10\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host)) return true
  if (/^192\.168\.\d{1,3}\.\d{1,3}$/.test(host)) return true
  if (/^172\.(1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}$/.test(host)) return true
  if (host === '0.0.0.0' || host === 'metadata.google.internal') return true
  return false
}

export function assertPublicHttpFetchTarget(value) {
  if (typeof value !== 'string' || !/^https?:\/\//i.test(value)) {
    throw new Error('A valid http(s) url is required')
  }
  const parsed = new URL(value)
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('A valid http(s) url is required')
  }
  if (parsed.username || parsed.password) {
    throw new Error('A valid http(s) url is required')
  }
  if (isDisallowedPublicFetchHost(parsed.hostname)) {
    throw new Error('Background fetch is not allowed for private or local hosts')
  }
  const protocol = parsed.protocol === 'https:' ? 'https:' : 'http:'
  return `${protocol}//${parsed.host}${parsed.pathname}${parsed.search}`
}

export async function fetchPublicHttpResource(value) {
  const requestUrl = assertPublicHttpFetchTarget(value)
  // lgtm[js/request-forgery] Only http(s) public hosts after private-network and credential checks.
  return fetch(requestUrl)
}

export function assertRuntimeMessageSender(message, sender, extensionId, configuredServerUrl) {
  if (!extensionId || sender?.id !== extensionId) throw new Error('Untrusted extension message sender')
  let nativePage = false
  try {
    const parsed = new URL(sender.url)
    nativePage = parsed.protocol === 'chrome-extension:' && parsed.hostname === extensionId
  } catch {}
  if (nativePage) return
  if (sender.frameId !== 0 || !isTrustedAppPage(sender.url, configuredServerUrl)) {
    throw new Error('Page is not the configured X-Science server')
  }
  if (!PAGE_BRIDGE_ACTIONS.has(message?.type)) throw new Error('Message requires the native connector UI')
  if (message.type === 'connect' && message.serverUrl !== undefined
      && !isTrustedAppPage(message.serverUrl, configuredServerUrl)) {
    throw new Error('Change the server using the native connector settings')
  }
}

export const BROWSER_SUPPORTED_ACTIONS = Object.freeze([
  'run_task_loop',
  'ensure_workspace',
  'open_local_files',
  'local_files_status',
  'local_files_manifest',
  'local_files_list',
  'local_files_search',
  'local_file_info',
  'local_file_read',
  'local_file_write',
  'local_file_copy',
  'local_file_move',
  'local_file_delete',
  'local_file_mkdir',
  'local_files_organize',
  'bookmarks_status',
  'bookmarks_profile',
  'revoke_bookmarks_permission',
  'open_personalization',
  'search_web',
  'extract_search_results',
  'read_logged_in_page',
  'open_tab',
  'get_active_tab',
  'extract_page',
  'read_page',
  'refresh_tab',
  'scroll_page',
  'wait_for_page',
  'click',
  'click_at',
  'type',
  'screenshot',
  'visual_element_map',
  'find_download_links',
  'download_from_page',
  'download_file',
  'open_downloads_folder',
  'show_downloaded_file',
  'cleanup_tabs',
  'clear_local_files',
  'close_tab',
])

export const BROWSER_CAPABILITIES = Object.freeze([
  'tabs',
  'tabGroups',
  'scripting',
  'activeTab',
  'captureVisibleTab',
  'xScienceLink',
  'taskLoop',
  'browserSearch',
  'humanPageRead',
  'llmMediaCapture',
  'mediaManifest',
  'noLocalOcr',
  'scrollPage',
  'refreshTab',
  'downloads',
  'downloadOpen',
  'downloadDiscovery',
  'visualClick',
  'visualElementMap',
  'pageWait',
  'tabCleanup',
  'localFiles',
  'localFileOperations',
  'localFileAdvanced',
  'fileSystemAccess',
  'permissionReset',
  'bookmarks',
  'personalization',
  'loggedInPageRead',
])

export function buildExtensionRuntimeMetadata({ manifest, startedAt, lastHeartbeatAt }) {
  return {
    protocolVersion: X_SCIENCE_LINK_PROTOCOL_VERSION,
    policyVersion: X_SCIENCE_LINK_POLICY_VERSION,
    version: manifest.version,
    extensionHash: `x-science-link-${manifest.version}-${EXTENSION_RUNTIME_BUILD}`,
    build: EXTENSION_RUNTIME_BUILD,
    manifestVersion: manifest.manifest_version,
    startedAt,
    lastHeartbeatAt,
  }
}

export function buildBrowserRuntimePolicy(input) {
  return {
    maxXScienceTabs: input.maxXScienceTabs,
    defaultSearchReadPages: input.defaultSearchReadPages,
    maxSearchReadPages: input.maxSearchReadPages,
    maxHumanReadScrolls: input.maxHumanReadScrolls,
    maxVisibleElements: input.maxVisibleElements,
    approvalScope: 'conversation',
  }
}

export function buildHelloPayload({ state, browser, userAgent, runtime, runtimePolicy }) {
  return {
    type: 'hello',
    clientId: state.clientId,
    name: state.name || 'X-Science Link',
    browser,
    extensionVersion: runtime.version,
    extensionHash: runtime.extensionHash,
    protocolVersion: X_SCIENCE_LINK_PROTOCOL_VERSION,
    policyVersion: X_SCIENCE_LINK_POLICY_VERSION,
    startedAt: runtime.startedAt,
    lastHeartbeatAt: runtime.lastHeartbeatAt,
    runtime,
    userAgent,
    capabilities: [...BROWSER_CAPABILITIES],
    supportedActions: [...BROWSER_SUPPORTED_ACTIONS],
    runtimePolicy,
  }
}

export function buildLocalDoctorReport({
  statusSnapshot,
  runtime,
  runtimePolicy,
  permissions,
  warnings = [],
}) {
  return {
    status: statusSnapshot.connected ? 'ok' : 'degraded',
    server: {
      url: statusSnapshot.serverUrl,
      connectionStatus: statusSnapshot.connected ? 'online' : 'offline',
      protocolVersion: X_SCIENCE_LINK_PROTOCOL_VERSION,
      policyVersion: X_SCIENCE_LINK_POLICY_VERSION,
    },
    client: {
      clientId: statusSnapshot.clientId,
      clientKind: 'browser',
      runtime,
      supportedActions: [...BROWSER_SUPPORTED_ACTIONS],
      capabilities: [...BROWSER_CAPABILITIES],
      runtimePolicy,
    },
    permissions,
    warnings,
  }
}

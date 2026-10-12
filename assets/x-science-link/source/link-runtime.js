export const X_SCIENCE_LINK_PROTOCOL_VERSION = '1'
export const X_SCIENCE_LINK_POLICY_VERSION = '1'
export const EXTENSION_RUNTIME_BUILD = '2026-06-02-info-flow-upgrade'

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

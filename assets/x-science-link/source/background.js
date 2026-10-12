import {
  buildBrowserRuntimePolicy,
  buildExtensionRuntimeMetadata,
  buildHelloPayload,
  buildLocalDoctorReport,
} from './link-runtime.js'
import { buildPermissionSnapshot } from './permission-snapshot.js'
import { openLocalFilesDatabase } from './local-files-database.js'

const DEFAULT_SERVER_URL = 'http://127.0.0.1:3456'
const DEFAULT_GROUP_TITLE = 'X-Science'
const MAX_X_SCIENCE_TAB_GROUPS = 1
const LOCAL_FILES_STORE_NAME = 'folders'
const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 15000
const MAX_X_SCIENCE_GROUP_TABS = 20
const MAX_VISIBLE_X_SCIENCE_TABS = 3
const DEFAULT_TAB_ACTIVE = false
const DEFAULT_SEARCH_READ_PAGES = 1
const MAX_SEARCH_READ_PAGES = 2
const SEARCH_PAGE_TEXT_LIMIT = 30_000
const SEARCH_RESULT_PAGE_TIMEOUT_MS = 7_000
const DEFAULT_HUMAN_READ_SCROLLS = 6
const MAX_HUMAN_READ_SCROLLS = 18
const HUMAN_READ_TEXT_LIMIT = 120_000
const HUMAN_READ_IMAGE_LIMIT = 120
const MAX_DOWNLOAD_CANDIDATES = 80
const CLICK_WAIT_DEFAULT_MS = 900
const WAIT_FOR_PAGE_DEFAULT_MS = 4_500
const DEFAULT_LLM_MEDIA_CAPTURES = 3
const MAX_LLM_MEDIA_CAPTURES = 6
const MAX_LOCAL_READ_BYTES = 2 * 1024 * 1024
const MAX_LOCAL_LIST_ENTRIES = 500
const MAX_LOCAL_SEARCH_CONTENT_BYTES = 256 * 1024
const MAX_LOCAL_MANIFEST_ENTRIES = 1200
const BOOKMARKS_PERMISSION = { permissions: ['bookmarks'] }
const MAX_PROFILE_BOOKMARKS = 1200
const MAX_PROFILE_RESOURCES = 160
const BOOKMARKS_PROFILE_STALE_MS = 24 * 60 * 60 * 1000
const CONVERSATION_APPROVALS_KEY = 'conversationApprovals'
const MAX_CONVERSATION_APPROVALS = 80
const TASK_LOOP_DEFAULT_MAX_ATTEMPTS = 2
const TASK_LOOP_MAX_ATTEMPTS = 3
const TASK_LOOP_DOWNLOAD_CANDIDATE_LIMIT = 8
const DEFAULT_VISUAL_ELEMENT_LIMIT = 80
const MAX_VISUAL_ELEMENT_LIMIT = 160
const MAX_BACKGROUND_DOWNLOAD_BYTES = 15 * 1024 * 1024
const EXTENSION_STARTED_AT = Date.now()

let ws = null
let reconnectTimer = null
let heartbeatTimer = null
let reconnectAttempt = 0
let allowReconnect = true
let lastHeartbeatAt = null
const pendingApprovals = new Map()
const taskProgressReporters = new WeakMap()
let status = {
  connected: false,
  connecting: false,
  autoConnect: true,
  lastError: '',
  clientId: '',
  serverUrl: DEFAULT_SERVER_URL,
  user: null,
  connectedAt: null,
  workspace: null,
  localFiles: null,
  bookmarks: null,
}

chrome.runtime.onInstalled.addListener(async () => {
  const state = await getStoredState()
  if (!state.clientId) {
    await chrome.storage.local.set({ clientId: createClientId() })
  }
  await enforceSingleXScienceGroup({ source: 'installed' }).catch(() => null)
})

chrome.runtime.onStartup.addListener(() => {
  void enforceSingleXScienceGroup({ source: 'startup' }).catch(() => null)
  connectFromStorage()
})

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  void (async () => {
    try {
      if (message?.type === 'connect') {
        await saveConnectionSettings(message)
        await connectFromStorage(true, { requestPersonalization: message.requestPersonalization === true })
        sendResponse({ ok: true, status })
        return
      }
      if (message?.type === 'disconnect') {
        await chrome.storage.local.set({ autoConnect: false })
        disconnect('User disconnected', true)
        sendResponse({ ok: true, status })
        return
      }
      if (message?.type === 'status') {
        sendResponse({
          ok: true,
          status: await getStatusSnapshot(),
          version: chrome.runtime.getManifest().version,
          runtime: getExtensionRuntimeMetadata(),
        })
        return
      }
      if (message?.type === 'doctor') {
        sendResponse({
          ok: true,
          doctor: await getLocalDoctorReport(),
        })
        return
      }
      if (message?.type === 'local_files_status') {
        const localFiles = await getLocalFilesStatus()
        setStatus({ localFiles })
        sendResponse({ ok: true, result: localFiles, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'bookmarks_status') {
        const bookmarks = await getBookmarksStatus()
        setStatus({ bookmarks })
        sendResponse({ ok: true, result: bookmarks, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'open_personalization') {
        const result = await openPersonalizationPrompt({ active: true, reason: message.reason || 'manual' })
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'request_bookmarks_permission') {
        const result = await requestBookmarksPermissionAndProfile({ refresh: true })
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'refresh_bookmarks_profile') {
        const result = await getBookmarksProfile({ refresh: true, prompt: true })
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'revoke_bookmarks_permission') {
        const result = await revokeBookmarksPermission()
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'clear_local_files') {
        const result = await clearLocalFilesAuthorization()
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'open_downloads_folder') {
        const result = await openDownloadsFolder()
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'show_downloaded_file') {
        const result = await showDownloadedFile(message)
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'dismiss_personalization') {
        await chrome.storage.local.set({ personalizationPromptDismissedAt: Date.now() })
        sendResponse({ ok: true, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'open_local_files') {
        const result = await ensureWorkspace({ active: true })
        sendResponse({ ok: true, result, status: await getStatusSnapshot() })
        return
      }
      if (message?.type === 'ensure_workspace') {
        const result = await ensureWorkspace({ active: message.active === true })
        sendResponse({ ok: true, result, status })
        return
      }
      if (message?.type === 'login') {
        const result = await login(message.serverUrl, message.username, message.password)
        sendResponse({ ok: true, result })
        return
      }
      if (message?.type === 'extract_active_page') {
        const result = await executeCommand('extract_page', {})
        sendResponse({ ok: true, result })
        return
      }
      if (message?.type === 'approval_details') {
        sendResponse({ ok: true, request: pendingApprovals.get(message.requestId)?.request || null })
        return
      }
      if (message?.type === 'approval_decision') {
        const pending = pendingApprovals.get(message.requestId)
        if (!pending) {
          sendResponse({ ok: false, error: 'Approval request expired' })
          return
        }
        pendingApprovals.delete(message.requestId)
        if (message.approved === true && pending.request.approvalKey) {
          await rememberConversationApproval(pending.request.approvalKey, pending.request)
        }
        pending.resolve(message.approved === true)
        sendResponse({ ok: true })
        return
      }
      sendResponse({ ok: false, error: 'Unknown extension message' })
    } catch (error) {
      sendResponse({ ok: false, error: error instanceof Error ? error.message : String(error) })
    }
  })()
  return true
})

async function saveConnectionSettings(message) {
  const next = { autoConnect: message.autoConnect !== false }
  if (typeof message.serverUrl === 'string' && message.serverUrl.trim()) {
    next.serverUrl = normalizeServerUrl(message.serverUrl)
  }
  if (typeof message.appSession === 'string') {
    next.appSession = message.appSession.trim()
  }
  await chrome.storage.local.set(next)
}

async function login(serverUrl, username, password) {
  const normalizedServerUrl = normalizeServerUrl(serverUrl || DEFAULT_SERVER_URL)
  const response = await fetch(`${normalizedServerUrl}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  const body = await response.json().catch(() => ({}))
  if (!response.ok) {
    throw new Error(body?.message || `Login failed (${response.status})`)
  }
  await chrome.storage.local.set({
    serverUrl: normalizedServerUrl,
    appSession: body.token,
    user: body.user,
  })
  return { user: body.user }
}

async function connectFromStorage(force = false, options = {}) {
  const state = await getStoredState()
  if (!force && state.autoConnect === false) {
    setStatus({ connected: false, connecting: false, autoConnect: false, lastError: '' })
    return
  }
  if (!state.appSession) {
    setStatus({ connected: false, connecting: false, lastError: 'Login required' })
    return
  }
  if (!force && ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return
  }
  allowReconnect = true
  connect(state, options)
}

function connect(state, options = {}) {
  clearReconnectTimer()
  clearHeartbeat()
  allowReconnect = state.autoConnect !== false
  if (ws) {
    try { ws.close() } catch {}
  }

  const wsUrl = toWsUrl(state.serverUrl, state.appSession, state.clientId)
  setStatus({
    connected: false,
    connecting: true,
    autoConnect: allowReconnect,
    lastError: '',
    clientId: state.clientId,
    serverUrl: state.serverUrl,
    user: state.user || null,
    connectedAt: null,
  })

  ws = new WebSocket(wsUrl)
  ws.onopen = () => {
    reconnectAttempt = 0
    setStatus({ connected: true, connecting: false, connectedAt: Date.now(), lastError: '' })
    sendHello(state)
    void enforceSingleXScienceGroup({ source: 'connect' }).catch(() => null)
    void ensureWorkspace({ active: false })
      .then((workspace) => setStatus({ workspace }))
      .catch((error) => setStatus({ lastError: error instanceof Error ? error.message : String(error) }))
    if (options.requestPersonalization === true) {
      void maybeOpenPersonalizationOnConnect()
    }
    heartbeatTimer = setInterval(() => {
      lastHeartbeatAt = Date.now()
      sendJson({ type: 'pong' })
    }, 20_000)
  }
  ws.onmessage = (event) => {
    void handleServerMessage(event.data)
  }
  ws.onerror = () => {
    setStatus({ lastError: 'WebSocket error' })
  }
  ws.onclose = () => {
    const shouldReconnect = allowReconnect
    clearHeartbeat()
    setStatus({ connected: false, connecting: false, connectedAt: null })
    if (shouldReconnect) scheduleReconnect()
  }
}

function disconnect(reason, manual = false) {
  clearReconnectTimer()
  clearHeartbeat()
  if (manual) allowReconnect = false
  if (ws) {
    try { ws.close(1000, reason || 'Disconnected') } catch {}
  }
  ws = null
  setStatus({ connected: false, connecting: false, autoConnect: allowReconnect, connectedAt: null })
}

async function handleServerMessage(raw) {
  const message = JSON.parse(raw)
  if (message.type === 'command') {
    await handleCommand(message)
  }
}

async function handleCommand(command) {
  try {
    sendCommandProgress(command.commandId, {
      status: 'started',
      summary: 'X-Science Link 已开始执行任务。',
      progress: { completedSteps: 0, totalSteps: estimateTaskProgressTotal(command.payload || {}), percent: 0 },
      event: {
        stage: 'started',
        ok: true,
        at: Date.now(),
        message: 'X-Science Link 已开始执行任务。',
      },
    })
    const result = await executeCommand(command.name, command.payload || {}, command.commandId)
    sendJson({
      type: 'command_result',
      commandId: command.commandId,
      ok: true,
      result,
    })
  } catch (error) {
    sendJson({
      type: 'command_result',
      commandId: command.commandId,
      ok: false,
      error: error instanceof Error ? error.message : String(error),
    })
  }
}

async function executeCommand(name, payload, commandId) {
  switch (name) {
    case 'run_task_loop':
      return runTaskLoop(payload, commandId)
    case 'ensure_workspace':
      return ensureWorkspace(payload)
    case 'open_local_files':
      return openLocalFilesWorkspace(payload)
    case 'local_files_status':
      return getLocalFilesStatus()
    case 'local_files_manifest':
      return buildLocalFilesManifest(payload)
    case 'local_files_list':
      return listLocalFiles(payload)
    case 'local_files_search':
      return searchLocalFiles(payload)
    case 'local_file_info':
      return getLocalFileInfo(payload)
    case 'local_file_read':
      return withLocalOperationFeedback('read', await readLocalFile(payload), { applied: true })
    case 'local_file_write':
      return withLocalOperationFeedback('write', await writeLocalFile(payload), { applied: true })
    case 'local_file_copy':
      return withLocalOperationFeedback('copy', await copyLocalFileCommand(payload))
    case 'local_file_move':
      return withLocalOperationFeedback('move', await moveLocalFileCommand(payload))
    case 'local_file_delete':
      return withLocalOperationFeedback('delete', await deleteLocalFileCommand(payload))
    case 'local_file_mkdir':
      return withLocalOperationFeedback('mkdir', await makeLocalDirectory(payload))
    case 'local_files_organize':
      return withLocalOperationFeedback('organize', await organizeLocalFiles(payload))
    case 'bookmarks_status':
      return getBookmarksStatus()
    case 'bookmarks_profile':
      return getBookmarksProfile(payload)
    case 'revoke_bookmarks_permission':
      return revokeBookmarksPermission()
    case 'open_personalization':
      return openPersonalizationPrompt({ active: payload.active === true, reason: 'command' })
    case 'search_web':
      return searchWeb(payload)
    case 'extract_search_results':
      return extractSearchResults(payload)
    case 'read_logged_in_page':
      return readLoggedInPage(payload)
    case 'open_tab':
      return openTab(payload)
    case 'get_active_tab':
      return getActiveTab()
    case 'extract_page':
      return extractPage(payload)
    case 'read_page':
      return readPageLikeHuman(payload)
    case 'refresh_tab':
      return refreshTab(payload)
    case 'scroll_page':
      return scrollPage(payload)
    case 'wait_for_page':
      return waitForPage(payload)
    case 'click':
      return clickElement(payload)
    case 'click_at':
      return clickAt(payload)
    case 'type':
      return typeIntoElement(payload)
    case 'screenshot':
      return screenshot(payload)
    case 'visual_element_map':
      return buildVisualElementMap(payload)
    case 'find_download_links':
      return findDownloadLinks(payload)
    case 'download_from_page':
      return downloadFromPage(payload)
    case 'download_file':
      return downloadFile(payload)
    case 'open_downloads_folder':
      return openDownloadsFolder()
    case 'show_downloaded_file':
      return showDownloadedFile(payload)
    case 'cleanup_tabs':
      return cleanupXScienceTabs(payload)
    case 'clear_local_files':
      return clearLocalFilesAuthorization()
    case 'close_tab':
      return closeTab(payload)
    case 'desktop_task_loop':
      return rejectDesktopCommand(name)
    case 'desktop_status':
      return rejectDesktopCommand(name)
    case 'desktop_request_access':
      return rejectDesktopCommand(name)
    case 'desktop_screenshot':
      return rejectDesktopCommand(name)
    case 'desktop_click':
      return rejectDesktopCommand(name)
    case 'desktop_mouse_move':
      return rejectDesktopCommand(name)
    case 'desktop_mouse_down':
      return rejectDesktopCommand(name)
    case 'desktop_mouse_up':
      return rejectDesktopCommand(name)
    case 'desktop_drag':
      return rejectDesktopCommand(name)
    case 'desktop_cursor_position':
      return rejectDesktopCommand(name)
    case 'desktop_type':
      return rejectDesktopCommand(name)
    case 'desktop_key':
      return rejectDesktopCommand(name)
    case 'desktop_scroll':
      return rejectDesktopCommand(name)
    case 'desktop_displays':
      return rejectDesktopCommand(name)
    case 'desktop_frontmost_app':
      return rejectDesktopCommand(name)
    case 'desktop_app_under_point':
      return rejectDesktopCommand(name)
    case 'desktop_list_apps':
      return rejectDesktopCommand(name)
    case 'desktop_list_running_apps':
      return rejectDesktopCommand(name)
    case 'desktop_window_list':
      return rejectDesktopCommand(name)
    case 'desktop_window_focus':
      return rejectDesktopCommand(name)
    case 'desktop_window_minimize':
      return rejectDesktopCommand(name)
    case 'desktop_window_maximize':
      return rejectDesktopCommand(name)
    case 'desktop_window_restore':
      return rejectDesktopCommand(name)
    case 'desktop_window_move_resize':
      return rejectDesktopCommand(name)
    case 'desktop_window_close':
      return rejectDesktopCommand(name)
    case 'desktop_window_elements':
      return rejectDesktopCommand(name)
    case 'desktop_open_app':
      return rejectDesktopCommand(name)
    case 'desktop_file_list':
      return rejectDesktopCommand(name)
    case 'desktop_file_info':
      return rejectDesktopCommand(name)
    case 'desktop_file_read':
      return rejectDesktopCommand(name)
    case 'desktop_file_write':
      return rejectDesktopCommand(name)
    case 'desktop_file_copy':
      return rejectDesktopCommand(name)
    case 'desktop_file_move':
      return rejectDesktopCommand(name)
    case 'desktop_file_mkdir':
      return rejectDesktopCommand(name)
    case 'desktop_file_delete':
      return rejectDesktopCommand(name)
    case 'desktop_file_open':
      return rejectDesktopCommand(name)
    case 'desktop_read_clipboard':
      return rejectDesktopCommand(name)
    case 'desktop_write_clipboard':
      return rejectDesktopCommand(name)
    case 'desktop_shell':
      return rejectDesktopCommand(name)
    case 'desktop_batch':
      return rejectDesktopCommand(name)
    default:
      throw new Error(`Unsupported browser command: ${name}`)
  }
}

function rejectDesktopCommand(name) {
  throw new Error(`Desktop command ${name} requires a X-Science Link Desktop Agent client.`)
}

async function runTaskLoop(payload = {}, commandId) {
  const startedAt = Date.now()
  const steps = []
  setTaskProgressReporter(steps, createTaskProgressReporter(commandId, payload))
  const task = cleanTaskText(payload.task || payload.query || payload.url || payload.path || 'X-Science Link task')
  const goal = normalizeTaskGoal(payload)
  const runBrowser = goal === 'browser_research' || goal === 'browser_download' || goal === 'mixed'
  const runLocalFiles = goal === 'local_files' || goal === 'mixed'
  const llmMedia = []
  let workspace = null
  let browser = null
  let localFiles = null
  let cleanup = null

  try {
    workspace = await ensureWorkspace({ active: payload.active === true })
    pushTaskStep(steps, 'workspace_ready', true, {
      tabId: workspace?.tab?.id,
      groupId: workspace?.groupId,
    })
  } catch (error) {
    pushTaskStep(steps, 'workspace_ready', false, { error: compactError(error) })
  }

  try {
    cleanup = await cleanupXScienceTabs({
      maxTabs: payload.maxTabs || MAX_X_SCIENCE_GROUP_TABS,
      tabId: workspace?.tab?.id,
    })
    pushTaskStep(steps, 'preflight_tab_cleanup', true, {
      closed: cleanup?.closed?.length || 0,
      remaining: cleanup?.remaining?.length || 0,
    })
  } catch (error) {
    pushTaskStep(steps, 'preflight_tab_cleanup', false, { error: compactError(error) })
  }

  if (runBrowser) {
    browser = await runBrowserTaskLoop(payload, steps)
    if (Array.isArray(browser.llmMedia)) llmMedia.push(...browser.llmMedia)
  }

  if (runLocalFiles) {
    localFiles = await runLocalFilesTaskLoop(payload, steps)
  }

  let finalCleanup = null
  try {
    const keepTabIds = [
      workspace?.tab?.id,
      browser?.activeTab?.id,
    ].filter((id) => typeof id === 'number')
    finalCleanup = await cleanupXScienceTabs({
      maxTabs: payload.maxTabs || MAX_X_SCIENCE_GROUP_TABS,
      keepTabIds,
    })
    pushTaskStep(steps, 'final_tab_cleanup', true, {
      closed: finalCleanup?.closed?.length || 0,
      remaining: finalCleanup?.remaining?.length || 0,
    })
  } catch (error) {
    pushTaskStep(steps, 'final_tab_cleanup', false, { error: compactError(error) })
  }

  const downloadRequested = wantsTaskDownload(payload)
  const completed = Boolean(
    (downloadRequested && browser?.downloads?.some(isVerifiedDownload))
    || (!downloadRequested && browser?.readComplete)
    || localFiles?.completed,
  )
  const needsUserAuthorization = localFiles?.needsAuthorization === true
  const needsVision = browser?.needsVision === true
  const statusText = completed
    ? 'completed'
    : needsUserAuthorization
      ? 'needs_user_authorization'
      : needsVision
        ? 'needs_x_science_llm_vision'
        : 'needs_follow_up'
  const taskState = buildTaskState({
    task,
    goal,
    statusText,
    completed,
    steps,
    workspace,
    browser,
    localFiles,
    downloadRequested,
    startedAt,
  })
  sendCommandProgress(commandId, {
    status: statusText,
    summary: taskState.summary || buildTaskFeedbackMessage({ completed, statusText, browser, localFiles, downloadRequested }),
    progress: taskState.progress,
    events: taskState.events,
    artifacts: taskState.artifacts,
    recoveryActions: taskState.recoveryActions,
  })

  return {
    task,
    goal,
    status: statusText,
    completed,
    taskState,
    events: taskState.events,
    artifacts: taskState.artifacts,
    workspace,
    browser,
    localFiles,
    cleanup: finalCleanup || cleanup,
    llmMedia,
    mediaPolicy: 'x_science_llm_no_local_ocr',
    steps,
    nextActions: buildTaskNextActions({ completed, needsUserAuthorization, needsVision, browser, localFiles, downloadRequested }),
    recovery: buildTaskRecovery({ browser, localFiles, downloadRequested }),
    recoveryActions: taskState.recoveryActions,
    integrations: taskState.integrations,
    limits: {
      maxGroupTabs: MAX_X_SCIENCE_GROUP_TABS,
      maxVisibleTabs: MAX_VISIBLE_X_SCIENCE_TABS,
      maxAttempts: TASK_LOOP_MAX_ATTEMPTS,
      noLocalOcr: true,
    },
    feedback: {
      message: buildTaskFeedbackMessage({ completed, statusText, browser, localFiles, downloadRequested }),
      durationMs: Date.now() - startedAt,
    },
  }
}

async function runBrowserTaskLoop(payload, steps) {
  const searchTerms = cleanTaskText(payload.searchTerms || payload.query || payload.task || payload.url || '')
  const maxAttempts = clampNumber(payload.maxAttempts, 1, TASK_LOOP_MAX_ATTEMPTS, TASK_LOOP_DEFAULT_MAX_ATTEMPTS)
  const downloadRequested = wantsTaskDownload(payload)
  const fileTypes = normalizeFileTypes(payload.fileTypes)
  const searches = []
  const reads = []
  const downloads = []
  const failures = []
  const candidates = []
  const llmMedia = []
  let visualMap = null
  let activeTab = null

  if (payload.url) {
    try {
      const read = await readPageLikeHuman({
        url: payload.url,
        active: payload.active === true,
        maxScrolls: payload.maxScrolls ?? 8,
        includeImages: payload.includeImages !== false,
        includeMediaForLLM: payload.includeMediaForLLM === true || payload.useVision === true,
        maxMediaCaptures: payload.maxMediaCaptures,
        searchTerms,
        fileTypes,
      })
      reads.push(summarizeTaskRead(read))
      activeTab = read.tab || activeTab
      if (Array.isArray(read.llmMedia)) llmMedia.push(...read.llmMedia)
      candidates.push(...collectTaskDownloadCandidates({ read }, payload))
      pushTaskStep(steps, 'direct_page_read', true, {
        url: read?.tab?.url || payload.url,
        textLength: read?.page?.textLength || 0,
        candidates: read?.page?.downloadCandidates?.length || 0,
      })
      if (downloadRequested && candidates.length) {
        const download = await tryTaskDownloadCandidates(candidates, payload, steps)
        if (download) downloads.push(download)
      }
    } catch (error) {
      failures.push({ stage: 'direct_page_read', error: compactError(error) })
      pushTaskStep(steps, 'direct_page_read', false, { error: compactError(error) })
    }
  }

  const searchQueries = buildTaskSearchQueries(payload).slice(0, maxAttempts)
  for (const query of searchQueries) {
    if (downloadRequested && downloads.some(isVerifiedDownload)) break
    try {
      const search = await searchWeb({
        ...payload,
        query,
        active: payload.active === true,
        readPages: true,
        maxPages: payload.maxPages || (downloadRequested ? 2 : 1),
        maxScrolls: payload.maxScrolls ?? 6,
        includeImages: payload.includeImages !== false,
        closeAfterRead: payload.closeAfterRead !== false,
        closeSearchTab: payload.closeSearchTab !== false,
        searchTerms,
        fileTypes,
      })
      searches.push(summarizeTaskSearch(search))
      activeTab = search.tab || activeTab
      candidates.push(...collectTaskDownloadCandidates({ search }, payload))
      pushTaskStep(steps, 'browser_search_read', true, {
        query,
        results: search?.structured?.resultCount || 0,
        pagesRead: search?.feedback?.pagesRead || 0,
        candidates: candidates.length,
      })
    } catch (error) {
      failures.push({ stage: 'browser_search_read', query, error: compactError(error) })
      pushTaskStep(steps, 'browser_search_read', false, { query, error: compactError(error) })
    }

    if (downloadRequested && candidates.length) {
      const download = await tryTaskDownloadCandidates(candidates, payload, steps)
      if (download) {
        downloads.push(download)
        break
      }
    }
  }

  if (downloadRequested && !downloads.some(isVerifiedDownload)) {
    const pageUrls = collectTaskPageUrls(searches).slice(0, Math.max(1, maxAttempts))
    for (const url of pageUrls) {
      try {
        const downloaded = await downloadFromPage({
          ...payload,
          url,
          active: false,
          closeAfterRead: true,
          searchTerms,
          fileTypes,
          maxResults: TASK_LOOP_DOWNLOAD_CANDIDATE_LIMIT,
        })
        downloads.push(downloaded.download)
        pushTaskStep(steps, 'download_from_result_page', isVerifiedDownload(downloaded?.download), {
          url,
          filename: downloaded?.download?.filename,
          state: downloaded?.download?.state,
          verified: downloaded?.download?.downloadVerification?.ok === true,
          userMessage: downloaded?.download?.userMessage,
        })
        if (isVerifiedDownload(downloaded?.download)) break
      } catch (error) {
        failures.push({ stage: 'download_from_result_page', url, error: compactError(error) })
        pushTaskStep(steps, 'download_from_result_page', false, { url, error: compactError(error) })
      }
    }
  }

  const needsVision = (
    (payload.useVision === true || payload.includeMediaForLLM === true || downloadRequested)
    && !downloads.some(isVerifiedDownload)
    && searches.length > 0
  )

  if (needsVision) {
    const visualUrl = collectTaskPageUrls(searches)[0]
    if (visualUrl) {
      try {
        const read = await readPageLikeHuman({
          url: visualUrl,
          active: payload.active === true,
          maxScrolls: payload.maxScrolls ?? 8,
          includeImages: true,
          includeMediaForLLM: true,
          maxMediaCaptures: payload.maxMediaCaptures || 3,
          searchTerms,
          fileTypes,
        })
        reads.push(summarizeTaskRead(read))
        activeTab = read.tab || activeTab
        if (Array.isArray(read.llmMedia)) llmMedia.push(...read.llmMedia)
        visualMap = await buildVisualElementMap({
          tabId: read.tab?.id,
          maxElements: payload.maxElements,
        })
        pushTaskStep(steps, 'vision_snapshot_ready', true, {
          url: read?.tab?.url || visualUrl,
          llmMediaCount: read?.llmMedia?.length || 0,
        })
        pushTaskStep(steps, 'visual_element_map_ready', true, {
          url: visualMap?.tab?.url || visualUrl,
          elementCount: visualMap?.elementCount || 0,
        })
      } catch (error) {
        failures.push({ stage: 'vision_snapshot_ready', url: visualUrl, error: compactError(error) })
        pushTaskStep(steps, 'vision_snapshot_ready', false, { url: visualUrl, error: compactError(error) })
      }
    }
  }

  if (!visualMap && payload.includeElementMap === true && activeTab?.id) {
    try {
      visualMap = await buildVisualElementMap({
        tabId: activeTab.id,
        maxElements: payload.maxElements,
      })
      pushTaskStep(steps, 'visual_element_map_ready', true, {
        url: visualMap?.tab?.url || activeTab.url,
        elementCount: visualMap?.elementCount || 0,
      })
    } catch (error) {
      failures.push({ stage: 'visual_element_map_ready', error: compactError(error) })
      pushTaskStep(steps, 'visual_element_map_ready', false, { error: compactError(error) })
    }
  }

  return {
    readComplete: searches.length > 0 || reads.length > 0,
    downloadRequested,
    searches,
    reads,
    candidates: rankDownloadCandidates(candidates, searchTerms, fileTypes).slice(0, TASK_LOOP_DOWNLOAD_CANDIDATE_LIMIT),
    downloads,
    failures,
    llmMedia,
    visualMap,
    activeTab,
    needsVision,
  }
}

async function runLocalFilesTaskLoop(payload, steps) {
  const local = {
    completed: false,
    needsAuthorization: false,
    status: null,
    manifest: null,
    search: null,
    info: null,
    operation: null,
    failures: [],
  }

  try {
    local.status = await getLocalFilesStatus()
    pushTaskStep(steps, 'local_files_status', true, {
      authorizedFolders: local.status?.authorizedFolders || 0,
    })
  } catch (error) {
    local.failures.push({ stage: 'local_files_status', error: compactError(error) })
    pushTaskStep(steps, 'local_files_status', false, { error: compactError(error) })
    return local
  }

  if (!local.status?.authorizedFolders) {
    local.needsAuthorization = true
    try {
      await openLocalFilesWorkspace({ active: true })
      pushTaskStep(steps, 'local_authorization_prompt_opened', true)
    } catch (error) {
      local.failures.push({ stage: 'local_authorization_prompt_opened', error: compactError(error) })
      pushTaskStep(steps, 'local_authorization_prompt_opened', false, { error: compactError(error) })
    }
    return local
  }

  try {
    local.manifest = await buildLocalFilesManifest({
      folderId: payload.folderId,
      path: payload.path && !payload.fileOperation ? payload.path : '',
      recursive: payload.recursive !== false,
      maxEntries: payload.maxEntries || 500,
    })
    pushTaskStep(steps, 'local_inventory', true, {
      files: local.manifest?.totals?.files || 0,
      directories: local.manifest?.totals?.directories || 0,
    })
  } catch (error) {
    local.failures.push({ stage: 'local_inventory', error: compactError(error) })
    pushTaskStep(steps, 'local_inventory', false, { error: compactError(error) })
  }

  if (payload.query || payload.extensions?.length || payload.fileTypes?.length) {
    try {
      local.search = await searchLocalFiles({
        folderId: payload.folderId,
        path: payload.path && !payload.fileOperation ? payload.path : '',
        query: payload.query || payload.searchTerms || payload.task || '',
        recursive: payload.recursive !== false,
        includeContent: payload.includeContent === true,
        matchCase: payload.matchCase === true,
        extensions: payload.extensions || payload.fileTypes,
        maxEntries: payload.maxEntries || 80,
      })
      pushTaskStep(steps, 'local_search', true, { resultCount: local.search?.resultCount || 0 })
    } catch (error) {
      local.failures.push({ stage: 'local_search', error: compactError(error) })
      pushTaskStep(steps, 'local_search', false, { error: compactError(error) })
    }
  }

  if (payload.path && !['mkdir'].includes(String(payload.fileOperation || ''))) {
    try {
      local.info = await getLocalFileInfo({ folderId: payload.folderId, path: payload.path })
      pushTaskStep(steps, 'local_file_info', true, {
        path: local.info?.path,
        kind: local.info?.kind,
      })
    } catch (error) {
      local.failures.push({ stage: 'local_file_info', error: compactError(error) })
      pushTaskStep(steps, 'local_file_info', false, { error: compactError(error) })
    }
  }

  if (payload.fileOperation) {
    local.operation = await runLocalFileOperationPreview(payload, steps)
  }

  local.completed = Boolean(local.manifest || local.search || local.info || local.operation)
  return local
}

async function runLocalFileOperationPreview(payload, steps) {
  const operation = String(payload.fileOperation || '').trim()
  const apply = payload.allowApply === true && payload.dryRun === false
  const common = {
    ...payload,
    dryRun: !apply,
  }
  try {
    let result
    if (operation === 'copy') result = await copyLocalFileCommand(common)
    else if (operation === 'move' || operation === 'rename') result = await moveLocalFileCommand(common)
    else if (operation === 'delete') result = await deleteLocalFileCommand(common)
    else if (operation === 'mkdir') result = await makeLocalDirectory(common)
    else if (operation === 'organize') result = await organizeLocalFiles(common)
    else if (operation === 'read') result = await readLocalFile(common)
    else if (operation === 'write') {
      if (!apply) {
        result = {
          dryRun: true,
          path: payload.path,
          bytes: String(payload.content || '').length,
          impact: 'X-Science would write this text file after user approval.',
        }
      } else {
        result = await writeLocalFile(common)
      }
    } else {
      throw new Error(`Unsupported local file operation: ${operation}`)
    }
    const feedback = buildLocalOperationFeedback(operation, result, { applied: apply })
    pushTaskStep(steps, 'local_file_operation', true, {
      operation,
      applied: apply,
      dryRun: result?.dryRun === true,
      userMessage: feedback.userMessage,
      checks: feedback.operationVerification?.checks || [],
      recoverySuggestions: feedback.operationVerification?.recoverySuggestions || [],
    })
    return { operation, applied: apply, result, ...feedback }
  } catch (error) {
    const feedback = buildLocalOperationFeedback(operation, null, { error })
    pushTaskStep(steps, 'local_file_operation', false, {
      operation,
      error: compactError(error),
      userMessage: feedback.userMessage,
      recoverySuggestions: feedback.operationVerification?.recoverySuggestions || [],
    })
    return { operation, applied: false, error: compactError(error), ...feedback }
  }
}

function buildLocalOperationFeedback(operation, result, context = {}) {
  const localOperationPlan = buildLocalOperationPlan(operation, result)
  const operationVerification = verifyLocalOperationResult(operation, result, context)
  const dryRun = isLocalOperationPreview(operation, result, context)
  const userMessage = context.error
    ? `本地文件操作没有完成：${compactError(context.error)}`
    : dryRun
      ? '已生成预览，还没有改动文件。需要用户确认后再执行。'
      : operation === 'read'
        ? '已读取文件内容，并返回给 X-Science；媒体文件会交给 X-Science LLM 识别。'
        : '已完成操作，并做了基础校验。'
  const nextActions = dryRun
    ? [
      '把预览内容告诉用户，确认后再用 allowApply=true 且 dryRun=false 执行。',
      '如果影响范围不对，先调整 path、targetPath 或整理规则，再重新预览。',
    ]
    : operation === 'read'
      ? ['把读取结果或媒体资源交给 X-Science 回答用户；不要在本地做 OCR、ASR 或媒体识别。']
      : operationVerification.ok
      ? ['把结果、保存位置或变更数量反馈给用户。']
      : operationVerification.recoverySuggestions

  return {
    userMessage,
    localOperationPlan,
    operationVerification,
    nextActions,
    preview: localOperationPlan.preview,
  }
}

function withLocalOperationFeedback(operation, result, context = {}) {
  const applied = context.applied ?? (!isLocalMutationOperation(operation) || result?.dryRun !== true)
  const feedback = buildLocalOperationFeedback(operation, result, { ...context, applied })
  return {
    operation,
    applied,
    ...result,
    ...feedback,
  }
}

function buildLocalOperationPlan(operation, result = {}) {
  const preview = result?.preview || buildLocalOperationPreviewFromResult(operation, result)
  const dryRun = isLocalOperationPreview(operation, result)
  const affectedCount = estimateLocalOperationAffectedCount(operation, result, preview)
  const folder = result?.folder || {}
  const steps = []
  if (operation === 'copy') {
    steps.push(`复制：${preview.from || result.from || ''}`)
    steps.push(`保存到：${preview.to || result.to || ''}`)
    if (preview.overwrite || result.overwrite) steps.push('如果目标已存在，将覆盖目标文件。')
  } else if (operation === 'move' || operation === 'rename') {
    steps.push(`移动/重命名：${preview.from || result.from || ''}`)
    steps.push(`新位置：${preview.to || result.to || ''}`)
    if (preview.overwrite || result.overwrite) steps.push('如果目标已存在，将覆盖目标文件。')
  } else if (operation === 'delete') {
    steps.push(`删除：${preview.path || result.path || ''}`)
    steps.push(preview.kind === 'directory' || result.kind === 'directory'
      ? `目标是文件夹${preview.recursive || result.recursive ? '，会包含里面的内容' : ''}。`
      : '目标是文件。')
  } else if (operation === 'mkdir') {
    steps.push(`新建文件夹：${preview.path || result.path || ''}`)
  } else if (operation === 'organize') {
    steps.push(`整理范围：${result.path || '/'}`)
    steps.push(`按类型移动 ${Array.isArray(result.moves) ? result.moves.length : 0} 个文件。`)
    steps.push('每个文件会移动到 Tables、Documents、Images 等类型文件夹。')
  } else if (operation === 'write') {
    steps.push(`写入文件：${preview.path || result.path || ''}`)
    steps.push(`写入大小：${preview.bytes || result.size || result.bytes || 0} 字节。`)
  } else if (operation === 'read') {
    steps.push(`读取文件：${result.path || preview.path || ''}`)
    steps.push(result.mediaPolicy === 'x_science_llm_no_local_ocr'
      ? '媒体/PDF 文件交给 X-Science LLM 识别，不在本地做 OCR 或转写。'
      : '只读取当前任务需要的文本内容。')
  }

  return {
    operation,
    mode: dryRun ? 'preview' : 'applied',
    folder: folder.name || '',
    affectedCount,
    requiresApproval: isLocalMutationOperation(operation) && dryRun,
    userImpact: dryRun
      ? '这是预览，不会改动用户文件；需要用户确认后再执行。'
      : operation === 'read'
        ? '已读取授权文件夹内的文件，媒体/PDF 等交给 X-Science LLM 处理。'
        : '操作已经执行，X-Science Link 返回了基础校验结果。',
    steps: steps.filter(Boolean),
    preview,
    examples: operation === 'organize' && Array.isArray(result?.moves)
      ? result.moves.slice(0, 12)
      : undefined,
    categorySummary: operation === 'organize' ? summarizeLocalMoveCategories(result?.moves) : undefined,
  }
}

function buildLocalOperationPreviewFromResult(operation, result = {}) {
  if (operation === 'copy') return { operation, from: result.from || '', to: result.to || '', overwrite: result.overwrite === true }
  if (operation === 'move' || operation === 'rename') return { operation, from: result.from || '', to: result.to || '', overwrite: result.overwrite === true }
  if (operation === 'delete') return { operation, path: result.path || '', kind: result.kind || '', recursive: result.recursive === true }
  if (operation === 'mkdir') return { operation, path: result.path || '', created: result.created === true }
  if (operation === 'organize') {
    return {
      operation,
      path: result.path || '',
      mode: result.mode || 'by_type',
      moveCount: Array.isArray(result.moves) ? result.moves.length : 0,
      moves: Array.isArray(result.moves) ? result.moves.slice(0, 12) : [],
    }
  }
  if (operation === 'write') return { operation, path: result.path || '', bytes: result.bytes || result.size || 0, existed: result.existed === true }
  if (operation === 'read') return { operation, path: result.path || '', size: result.size || 0, truncated: result.truncated === true }
  return { operation }
}

function verifyLocalOperationResult(operation, result = {}, context = {}) {
  if (context.error) {
    return {
      ok: false,
      status: 'failed',
      checks: [{
        id: 'operation_finished',
        label: '操作完成',
        ok: false,
        detail: compactError(context.error),
      }],
      userMessage: `操作失败：${compactError(context.error)}`,
      recoverySuggestions: [
        '重新打开 X-Science Link 权限中心，确认本地文件夹仍然授权。',
        '先执行 dryRun=true 生成预览，确认路径和目标位置无误。',
        '如果是文件被占用、重名或浏览器权限过期，处理后再重试。',
      ],
    }
  }

  const dryRun = isLocalOperationPreview(operation, result, context)
  if (dryRun) {
    return {
      ok: true,
      status: 'preview',
      checks: [
        { id: 'preview_ready', label: '预览已生成', ok: Boolean(result), detail: operation },
        { id: 'no_file_changed', label: '没有改动用户文件', ok: true, detail: 'dryRun=true' },
        { id: 'needs_confirmation', label: '需要用户确认后再执行', ok: true, detail: 'allowApply=true + dryRun=false' },
      ],
      userMessage: '已生成预览，还没有改动文件。需要用户确认后再执行。',
      recoverySuggestions: [],
    }
  }

  const checks = []
  if (operation === 'copy') {
    checks.push({ id: 'copied', label: '文件已复制', ok: result?.copied === true, detail: result?.to || '' })
  } else if (operation === 'move' || operation === 'rename') {
    checks.push({ id: 'moved', label: '文件已移动或重命名', ok: result?.moved === true, detail: result?.to || '' })
  } else if (operation === 'delete') {
    checks.push({ id: 'deleted', label: '目标已删除', ok: result?.deleted === true, detail: result?.path || '' })
  } else if (operation === 'mkdir') {
    checks.push({ id: 'created', label: '文件夹已创建', ok: result?.created === true, detail: result?.path || '' })
  } else if (operation === 'organize') {
    checks.push({ id: 'organized', label: '整理动作已执行', ok: Number(result?.applied || 0) === (Array.isArray(result?.moves) ? result.moves.length : 0), detail: `${result?.applied || 0} moved` })
  } else if (operation === 'write') {
    checks.push({ id: 'written', label: '文件已写入', ok: typeof result?.size === 'number', detail: `${result?.size || 0} bytes` })
  } else if (operation === 'read') {
    checks.push({ id: 'readable', label: '文件内容已返回', ok: typeof result?.content === 'string' || Array.isArray(result?.mediaResources), detail: result?.path || '' })
  }
  checks.push({ id: 'inside_authorized_folder', label: '操作限定在授权文件夹内', ok: true, detail: result?.folder?.name || '' })
  const ok = checks.every((check) => check.ok)
  return {
    ok,
    status: ok ? 'verified' : 'needs_recovery',
    checks,
    userMessage: ok
      ? operation === 'read'
        ? '已读取文件内容，并返回给 X-Science；媒体文件会交给 X-Science LLM 识别。'
        : '已完成操作，并做了基础校验。'
      : '操作返回了结果，但校验没有全部通过。',
    recoverySuggestions: ok ? [] : [
      '重新执行 local_file_info 或 local_files_manifest 检查目标文件状态。',
      '如果目标不存在或重名，调整 targetPath 后先 dryRun 预览。',
      '必要时让用户重新授权文件夹，然后重试。',
    ],
  }
}

function isLocalMutationOperation(operation) {
  return ['copy', 'move', 'rename', 'delete', 'mkdir', 'organize', 'write'].includes(operation)
}

function isLocalOperationPreview(operation, result = {}, context = {}) {
  if (!isLocalMutationOperation(operation)) return false
  return result?.dryRun === true || context.applied === false
}

function estimateLocalOperationAffectedCount(operation, result = {}, preview = {}) {
  if (operation === 'organize') return Array.isArray(result?.moves) ? result.moves.length : Number(preview.moveCount || 0)
  if (operation === 'read') return result?.path || preview.path ? 1 : 0
  if (operation === 'write') return result?.path || preview.path ? 1 : 0
  if (operation === 'copy' || operation === 'move' || operation === 'rename') return preview.from || result?.from ? 1 : 0
  if (operation === 'delete' || operation === 'mkdir') return preview.path || result?.path ? 1 : 0
  return 0
}

function summarizeLocalMoveCategories(moves = []) {
  const summary = {}
  for (const move of Array.isArray(moves) ? moves : []) {
    const category = move?.category || 'Other'
    summary[category] = (summary[category] || 0) + 1
  }
  return summary
}

async function openTab(payload) {
  const url = requireUrl(payload.url)
  const active = payload.active === true
  const tab = await chrome.tabs.create({ url, active })
  const groupId = await addToXScienceGroup(tab.id, { active })
  await waitForTabComplete(tab.id)
  await enforceXScienceGroupLimits(groupId, tab.id)
  return tabInfo(await chrome.tabs.get(tab.id))
}

async function searchWeb(payload) {
  const query = typeof payload.query === 'string' ? payload.query.trim() : ''
  if (!query) throw new Error('Search query is required')

  const active = payload.active === true
  const searchUrl = `https://www.bing.com/search?${new URLSearchParams({ q: query }).toString()}`
  const tab = await chrome.tabs.create({ url: searchUrl, active })
  const groupId = await addToXScienceGroup(tab.id, { active })
  await waitForTabComplete(tab.id)
  await enforceXScienceGroupLimits(groupId, tab.id)
  const freshTab = await chrome.tabs.get(tab.id)
  const page = await extractPage({ tabId: freshTab.id })
  const structured = await extractSearchResults({ tabId: freshTab.id, maxResults: payload.maxResults })
  const readPages = payload.readPages !== false
  const maxPages = readPages
    ? clampNumber(payload.maxPages, 1, MAX_SEARCH_READ_PAGES, DEFAULT_SEARCH_READ_PAGES)
    : 0
  const pages = readPages
    ? await readSearchResultPages(structured.results || [], {
      maxPages,
      maxScrolls: payload.maxScrolls,
      includeImages: payload.includeImages,
      closeAfterRead: payload.closeAfterRead,
      searchTerms: payload.searchTerms || payload.query,
    })
    : []
  const pagesRead = pages.filter((entry) => entry.ok).length
  const pagesFailed = pages.filter((entry) => !entry.ok).length
  const imagesFound = pages.reduce((total, entry) => total + (entry?.page?.images?.length || 0), 0)
  const closeSearchTab = payload.closeSearchTab !== false && !active
  let searchTabClosed = false
  if (closeSearchTab && typeof freshTab.id === 'number') {
    await chrome.tabs.remove(freshTab.id).then(() => {
      searchTabClosed = true
    }).catch(() => null)
  }
  return {
    query,
    searchUrl,
    tab: tabInfo(freshTab),
    searchTabClosed,
    page: page.page,
    structured,
    pages,
    feedback: {
      message: `X-Science Link opened the search page, found ${structured.resultCount || 0} result(s), and read ${pagesRead} result page(s).`,
      pagesRead,
      pagesFailed,
      imagesFound,
      steps: [
        { stage: 'search_page_opened', ok: true, url: searchUrl },
        { stage: 'results_extracted', ok: true, resultCount: structured.resultCount || 0 },
        { stage: 'pages_read', ok: pagesFailed === 0, pagesRead, pagesFailed, imagesFound },
        { stage: 'search_tab_closed', ok: searchTabClosed, enabled: closeSearchTab },
      ],
      note: 'search_web reads visible page text and image metadata from the user browser, scrolls pages like a human reader, and returns feedback with result and page-read counts; use read_logged_in_page for pages that need explicit logged-in or paid-database approval.',
    },
    limits: {
      maxGroupTabs: MAX_X_SCIENCE_GROUP_TABS,
      maxVisibleTabs: MAX_VISIBLE_X_SCIENCE_TABS,
      defaultSearchReadPages: DEFAULT_SEARCH_READ_PAGES,
      maxSearchReadPages: MAX_SEARCH_READ_PAGES,
    },
  }
}

async function readSearchResultPages(results, options = {}) {
  const maxPages = clampNumber(options.maxPages, 1, MAX_SEARCH_READ_PAGES, DEFAULT_SEARCH_READ_PAGES)
  const targets = []
  const seen = new Set()
  for (const result of results || []) {
    if (targets.length >= maxPages) break
    const url = typeof result?.url === 'string' ? result.url.trim() : ''
    if (!isReadableSearchResultUrl(url) || seen.has(url)) continue
    seen.add(url)
    targets.push(result)
  }

  return Promise.all(targets.map((result, index) => readSearchResultPage(result, index, options)))
}

async function readSearchResultPage(result, index, options = {}) {
  const startedAt = Date.now()
  const url = result.url
  let tab = null
  try {
    tab = await chrome.tabs.create({ url, active: false })
    const groupId = await addToXScienceGroup(tab.id, { active: false })
    await waitForTabComplete(tab.id, SEARCH_RESULT_PAGE_TIMEOUT_MS)
    if (groupId) await enforceXScienceGroupLimits(groupId, tab.id)
    const freshTab = await chrome.tabs.get(tab.id)
    const extracted = await readPageLikeHuman({
      tabId: freshTab.id,
      refresh: false,
      maxScrolls: options.maxScrolls,
      includeImages: options.includeImages,
      closeAfterRead: options.closeAfterRead !== false,
    })
    return {
      ok: true,
      index,
      source: result,
      tab: extracted.tab || tabInfo(freshTab),
      page: trimPageForSearchResult(extracted.page),
      feedback: extracted.feedback,
      durationMs: Date.now() - startedAt,
    }
  } catch (error) {
    if (tab?.id && options.closeAfterRead !== false) {
      await chrome.tabs.remove(tab.id).catch(() => null)
    }
    return {
      ok: false,
      index,
      source: result,
      tab: tab ? tabInfo(tab) : null,
      error: error instanceof Error ? error.message : String(error),
      durationMs: Date.now() - startedAt,
    }
  }
}

function hostnameMatchesDomain(hostname, domain) {
  const host = String(hostname || '').toLowerCase().replace(/\.$/, '')
  return host === domain || host.endsWith(`.${domain}`)
}

function isReadableSearchResultUrl(value) {
  if (!/^https?:\/\//i.test(value)) return false
  try {
    const url = new URL(value)
    const host = url.hostname.toLowerCase()
    if (hostnameMatchesDomain(host, 'bing.com') || hostnameMatchesDomain(host, 'google.com')) return false
    return true
  } catch {
    return false
  }
}

function trimPageForSearchResult(page) {
  if (!page || typeof page !== 'object') return null
  const text = String(page.text || '')
  return {
    title: page.title || '',
    url: page.url || '',
    text: text.slice(0, SEARCH_PAGE_TEXT_LIMIT),
    textLength: text.length,
    truncated: text.length > SEARCH_PAGE_TEXT_LIMIT,
    links: Array.isArray(page.links) ? page.links.slice(0, 30) : [],
    images: Array.isArray(page.images) ? page.images.slice(0, 20) : [],
    downloadCandidates: Array.isArray(page.downloadCandidates) ? page.downloadCandidates.slice(0, 12) : [],
  }
}

function buildDownloadCandidatesFromPage(page) {
  if (!page || typeof page !== 'object') return []
  const candidates = []
  const seen = new Set()
  const add = (record) => {
    const url = typeof record.url === 'string' ? record.url.trim() : ''
    if (!/^https?:\/\//i.test(url) || seen.has(url)) return
    if (isSearchOrNavigationUrl(url)) return
    const extension = detectFileExtension(url, record.mimeType || record.type || record.kind)
    const label = cleanCandidateText(record.label || record.title || record.text || record.alt || record.caption || '')
    const likelyDownload = isLikelyDownloadableUrl(url, extension, label)
    if (!likelyDownload && record.source !== 'image') return
    seen.add(url)
    candidates.push({
      url,
      label,
      filename: sanitizeDownloadFilename(record.filename || inferFilenameFromUrl(url)),
      extension,
      mimeType: record.mimeType || record.type || '',
      source: record.source || 'link',
      kind: record.kind || extension || '',
      width: record.width || 0,
      height: record.height || 0,
      naturalWidth: record.naturalWidth || 0,
      naturalHeight: record.naturalHeight || 0,
    })
  }

  for (const link of Array.isArray(page.links) ? page.links : []) {
    add({
      url: link.href || link.url,
      label: link.text || link.title,
      source: 'link',
    })
  }
  for (const doc of Array.isArray(page.media?.documents) ? page.media.documents : []) {
    add({
      url: doc.url,
      label: doc.title,
      mimeType: doc.mimeType,
      kind: doc.kind,
      source: 'document',
    })
  }
  for (const image of Array.isArray(page.images) ? page.images : []) {
    add({
      url: image.src || image.url,
      label: [image.alt, image.title, image.caption].filter(Boolean).join(' '),
      source: 'image',
      kind: 'image',
      width: image.width,
      height: image.height,
      naturalWidth: image.naturalWidth,
      naturalHeight: image.naturalHeight,
    })
  }
  return candidates
}

function rankDownloadCandidates(candidates, searchTerms = '', fileTypes) {
  const terms = tokenizeDownloadIntent(searchTerms)
  const desiredExtensions = normalizeFileTypes(fileTypes)
  const currentYear = new Date().getFullYear()
  const strongWords = ['poster', 'download', 'view poster', 'presentation', 'slides', 'pdf', 'abstract', 'resource', 'resources']
  const badWords = ['privacy', 'terms', 'contact', 'login', 'cookie', 'linkedin', 'twitter', 'facebook', 'youtube']
  return (Array.isArray(candidates) ? candidates : [])
    .map((candidate) => {
      const haystack = `${candidate.label || ''} ${candidate.filename || ''} ${candidate.url || ''}`.toLowerCase()
      let score = 0
      const reasons = []

      if (candidate.source === 'document') {
        score += 24
        reasons.push('document')
      }
      if (candidate.source === 'link') score += 8
      if (candidate.source === 'image') {
        const area = Math.max(candidate.width || 0, candidate.naturalWidth || 0)
          * Math.max(candidate.height || 0, candidate.naturalHeight || 0)
        if (area >= 240_000) {
          score += 14
          reasons.push('large_image')
        }
      }

      if (candidate.extension === 'pdf') {
        score += 36
        reasons.push('pdf')
      } else if (['ppt', 'pptx', 'doc', 'docx', 'xls', 'xlsx', 'csv'].includes(candidate.extension)) {
        score += 28
        reasons.push(candidate.extension)
      } else if (['png', 'jpg', 'jpeg', 'webp', 'gif', 'svg'].includes(candidate.extension)) {
        score += 12
        reasons.push('image')
      }

      for (const word of strongWords) {
        if (haystack.includes(word)) {
          score += word === 'poster' || word === 'view poster' ? 42 : 14
          reasons.push(word.replace(/\s+/g, '_'))
        }
      }
      for (const term of terms) {
        if (haystack.includes(term)) {
          score += ['poster', 'pdf', 'download'].includes(term) ? 20 : 8
          reasons.push(`matches_${term}`)
        }
      }
      const years = [...haystack.matchAll(/\b(20\d{2})\b/g)].map((match) => Number(match[1]))
      if (years.length) {
        const bestYear = Math.max(...years)
        score += Math.max(0, 18 - Math.max(0, currentYear - bestYear) * 4)
        reasons.push(`year_${bestYear}`)
      }
      if (desiredExtensions.length && !desiredExtensions.includes(candidate.extension)) {
        score -= 30
      }
      if (desiredExtensions.includes(candidate.extension)) {
        score += 25
        reasons.push('requested_type')
      }
      for (const word of badWords) {
        if (haystack.includes(word)) score -= 35
      }

      return { ...candidate, score, reasons: [...new Set(reasons)] }
    })
    .filter((candidate) => candidate.score > 0)
    .sort((a, b) => b.score - a.score || String(a.filename).localeCompare(String(b.filename)))
}

function tokenizeDownloadIntent(value) {
  return String(value || '')
    .toLowerCase()
    .replace(/[_./:?=&#+-]+/g, ' ')
    .split(/\s+/)
    .map((term) => term.trim())
    .filter((term) => term.length >= 2 && !['the', 'and', 'for', 'with', 'from', 'latest', 'newest'].includes(term))
    .slice(0, 16)
}

function normalizeFileTypes(value) {
  const raw = Array.isArray(value) ? value : (typeof value === 'string' ? value.split(/[,\s]+/) : [])
  return raw
    .map((item) => String(item || '').toLowerCase().replace(/^\./, '').trim())
    .map((item) => (item === 'jpg' ? 'jpeg' : item))
    .filter(Boolean)
}

function detectFileExtension(url, hint = '') {
  const hinted = String(hint || '').toLowerCase()
  if (/^(pdf|application\/pdf)$/.test(hinted)) return 'pdf'
  if (/^(ppt|pptx|application\/vnd\.ms-powerpoint|application\/vnd\.openxmlformats-officedocument\.presentationml\.presentation)$/.test(hinted)) return 'pptx'
  if (/^(xls|xlsx|application\/vnd\.ms-excel|application\/vnd\.openxmlformats-officedocument\.spreadsheetml\.sheet)$/.test(hinted)) return 'xlsx'
  if (/^(doc|docx|application\/msword|application\/vnd\.openxmlformats-officedocument\.wordprocessingml\.document)$/.test(hinted)) return 'docx'
  try {
    const pathname = new URL(url).pathname.toLowerCase()
    const match = pathname.match(/\.([a-z0-9]{2,6})$/)
    return match?.[1] || ''
  } catch {
    return ''
  }
}

function isLikelyDownloadableUrl(url, extension, label = '') {
  const haystack = `${url} ${label}`.toLowerCase()
  if (isSearchOrNavigationUrl(url)) return false
  if (['pdf', 'ppt', 'pptx', 'doc', 'docx', 'xls', 'xlsx', 'csv', 'zip', 'png', 'jpg', 'jpeg', 'webp', 'gif', 'svg'].includes(extension)) return true
  if (/wp-content\/uploads|\/uploads\//i.test(haystack)) return true
  if (/\/(download|downloads|attachment|attachments|file|files|media|asset|assets)\b/i.test(url)) return true
  return /\b(pdf|poster|presentation|slides|abstract)\b/i.test(label)
    && /\/(poster|presentation|slides|abstract|pdf|resource|resources)\b/i.test(url)
}

function isSearchOrNavigationUrl(url) {
  try {
    const parsed = new URL(url)
    const host = parsed.hostname.toLowerCase()
    const parts = parsed.pathname.toLowerCase().split('/').filter(Boolean)
    const basename = parts[parts.length - 1] || ''
    if (/\.[a-z0-9]{2,6}$/.test(basename)) return false
    const isSearchHost = /(bing|google|duckduckgo|yahoo|baidu)\./i.test(host)
    if (!basename) return true
    if (/^(search|results?|query|find|browse|home|index|login|signin|account|profile)$/.test(basename)) return true
    if (/(^|[-_])(search|results?|query|find)([-_]|$)/.test(basename)) return true
    if (
      isSearchHost
      && (parsed.searchParams.has('q') || parsed.searchParams.has('query') || parsed.searchParams.has('search'))
    ) return true
    return false
  } catch {
    return false
  }
}

function cleanCandidateText(value) {
  return String(value || '').replace(/\s+/g, ' ').trim().slice(0, 240)
}

async function readLoggedInPage(payload) {
  if (payload.url) {
    const tab = await openTab({
      url: payload.url,
      active: payload.active === true,
    })
    await requireLoggedInPageReadApproval(tab, payload)
    const page = await readPageLikeHuman({
      tabId: tab.id,
      refresh: payload.refresh === true,
      maxScrolls: payload.maxScrolls,
      initialWaitMs: payload.initialWaitMs,
      textLimit: payload.textLimit,
      includeImages: payload.includeImages,
      closeAfterRead: payload.closeAfterRead === true,
      feedReadMode: payload.feedReadMode === true,
      feedCompletionMode: payload.feedCompletionMode || '',
      onlyToday: payload.onlyToday === true,
      maxCards: payload.maxCards,
      maxNoNewScrolls: payload.maxNoNewScrolls,
      socialFeedSurfaces: payload.socialFeedSurfaces,
      sourceTarget: payload.sourceTarget || null,
    })
    return {
      mode: 'logged_in_page',
      note: 'Read only the page content visible to this user in their own browser session.',
      ...page,
    }
  }

  const tab = await chrome.tabs.get(payload.tabId)
  await requireLoggedInPageReadApproval(tabInfo(tab), payload)
  const page = await readPageLikeHuman({
    tabId: payload.tabId,
    refresh: payload.refresh === true,
    maxScrolls: payload.maxScrolls,
    initialWaitMs: payload.initialWaitMs,
    textLimit: payload.textLimit,
    includeImages: payload.includeImages,
    closeAfterRead: payload.closeAfterRead === true,
    feedReadMode: payload.feedReadMode === true,
    feedCompletionMode: payload.feedCompletionMode || '',
    onlyToday: payload.onlyToday === true,
    maxCards: payload.maxCards,
    maxNoNewScrolls: payload.maxNoNewScrolls,
    socialFeedSurfaces: payload.socialFeedSurfaces,
    sourceTarget: payload.sourceTarget || null,
  })
  return {
    mode: 'logged_in_page',
    note: 'Read only the page content visible to this user in their own browser session.',
    ...page,
  }
}

async function ensureWorkspace(payload = {}) {
  const workspaceUrl = chrome.runtime.getURL('workspace.html')
  const active = payload.active === true || DEFAULT_TAB_ACTIVE
  await enforceSingleXScienceGroup({ source: 'workspace' }).catch(() => null)
  const tabs = await chrome.tabs.query({})
  let tab = tabs.find((candidate) => (
    candidate.url === workspaceUrl || candidate.pendingUrl === workspaceUrl
  ))
  const created = !tab

  if (!tab) {
    tab = await chrome.tabs.create({ url: workspaceUrl, active })
  } else if (typeof tab.id === 'number' && active) {
    await chrome.tabs.update(tab.id, { active: true })
    if (typeof tab.windowId === 'number') {
      await chrome.windows.update(tab.windowId, { focused: true })
    }
  }

  const groupId = await addToXScienceGroup(tab.id, { active })
  await enforceXScienceGroupLimits(groupId, tab.id)
  const result = {
    created,
    groupId,
    groupTitle: DEFAULT_GROUP_TITLE,
    tab: tabInfo(tab),
    workspaceUrl,
  }
  setStatus({ workspace: result })
  return result
}

async function openLocalFilesWorkspace(payload = {}) {
  const workspace = await ensureWorkspace({ active: payload.active === true })
  const localFiles = await getLocalFilesStatus()
  setStatus({ localFiles })
  return { workspace, localFiles }
}

async function getActiveTab() {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true })
  if (!tab) throw new Error('No active tab')
  return tabInfo(tab)
}

async function extractPage(payload) {
  const tab = await resolveTab(payload.tabId)
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: extractPageInTab,
  })
  return {
    tab: tabInfo(tab),
    page: result?.result || null,
  }
}

async function extractSearchResults(payload) {
  const tab = await resolveTab(payload.tabId)
  const maxResults = clampNumber(payload.maxResults, 1, 20, 10)
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: extractSearchResultsInTab,
    args: [{ maxResults }],
  })
  return {
    tab: tabInfo(tab),
    ...(result?.result || { results: [] }),
  }
}

async function readPageLikeHuman(payload = {}) {
  const startedAt = Date.now()
  const openedTab = payload.url
    ? await openTab({ url: payload.url, active: payload.active === true })
    : null
  let tab = openedTab?.id ? await chrome.tabs.get(openedTab.id) : await resolveTab(payload.tabId)
  if (payload.refresh === true) {
    await chrome.tabs.reload(tab.id)
    await waitForTabComplete(tab.id)
    tab = await chrome.tabs.get(tab.id)
  }

  const maxScrolls = clampNumber(payload.maxScrolls, 0, MAX_HUMAN_READ_SCROLLS, DEFAULT_HUMAN_READ_SCROLLS)
  const initialWaitMs = clampNumber(payload.initialWaitMs, 0, 10_000, 0)
  if (initialWaitMs > 0) {
    await new Promise((resolve) => setTimeout(resolve, initialWaitMs))
  }
  const includeImages = payload.includeImages !== false
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: readPageLikeHumanInTab,
    args: [{
      maxScrolls,
      textOffset: payload.textOffset,
      textLimit: payload.textLimit,
      includeImages,
      defaultTextLimit: HUMAN_READ_TEXT_LIMIT,
      imageLimit: HUMAN_READ_IMAGE_LIMIT,
      feedReadMode: payload.feedReadMode === true,
      feedCompletionMode: payload.feedCompletionMode || '',
      onlyToday: payload.onlyToday === true,
      maxCards: payload.maxCards,
      maxNoNewScrolls: payload.maxNoNewScrolls,
      socialFeedSurfaces: payload.socialFeedSurfaces,
      sourceTarget: payload.sourceTarget || null,
    }],
  })
  const freshTab = await chrome.tabs.get(tab.id).catch(() => tab)
  const readResult = result?.result || {}
  const downloadCandidates = rankDownloadCandidates(
    buildDownloadCandidatesFromPage(readResult.page),
    payload.searchTerms || payload.query || payload.text || '',
    payload.fileTypes,
  ).slice(0, MAX_DOWNLOAD_CANDIDATES)
  const page = readResult.page
    ? { ...readResult.page, downloadCandidates }
    : null
  const llmMedia = payload.includeMediaForLLM === true
    ? await captureViewportMediaForLLM(freshTab, {
      maxCaptures: payload.maxMediaCaptures,
      reason: 'read_page',
    })
    : []
  const visualElementMap = payload.includeElementMap === true
    ? await buildVisualElementMap({
      tabId: freshTab.id,
      maxElements: payload.maxElements,
    })
    : null
  const output = {
    tab: tabInfo(freshTab),
    page,
    llmMedia,
    ...(visualElementMap ? { visualElementMap } : {}),
    mediaPolicy: 'x_science_llm_no_local_ocr',
    feedback: {
      ...(readResult.feedback || {}),
      llmMediaCount: llmMedia.length,
      durationMs: Date.now() - startedAt,
    },
  }
  if (payload.closeAfterRead === true && openedTab?.id) {
    await chrome.tabs.remove(freshTab.id).catch(() => null)
    output.closed = true
  }
  return output
}

async function refreshTab(payload = {}) {
  const tab = await resolveTab(payload.tabId)
  await chrome.tabs.reload(tab.id)
  await waitForTabComplete(tab.id)
  return { tab: tabInfo(await chrome.tabs.get(tab.id)) }
}

async function scrollPage(payload = {}) {
  const tab = await resolveTab(payload.tabId)
  const direction = ['up', 'top', 'bottom'].includes(payload.direction) ? payload.direction : 'down'
  const amount = clampNumber(payload.amount, 1, 20_000, 0)
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: scrollPageInTab,
    args: [{ direction, amount }],
  })
  return {
    tab: tabInfo(await chrome.tabs.get(tab.id)),
    feedback: result?.result || null,
  }
}

async function clickElement(payload) {
  const tab = await resolveTab(payload.tabId)
  const selector = typeof payload.selector === 'string' ? payload.selector : ''
  const text = typeof payload.text === 'string' ? payload.text : ''
  const waitAfterClickMs = clampNumber(payload.waitMs, 0, 20_000, CLICK_WAIT_DEFAULT_MS)
  const downloadIdsBefore = await getRecentDownloadIds()
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: clickInTab,
    args: [{ selector, text }],
  })
  return {
    clicked: result?.result || null,
    afterClick: await waitForPageFeedback(tab.id, { timeoutMs: waitAfterClickMs, downloadIdsBefore }),
    waitAfterClickMs,
  }
}

async function clickAt(payload) {
  const tab = await resolveTab(payload.tabId)
  const x = clampNumber(payload.x, 0, 100_000, Number.NaN)
  const y = clampNumber(payload.y, 0, 100_000, Number.NaN)
  if (!Number.isFinite(x) || !Number.isFinite(y)) {
    throw new Error('click_at requires viewport x and y coordinates')
  }
  const waitAfterClickMs = clampNumber(payload.waitMs, 0, 20_000, CLICK_WAIT_DEFAULT_MS)
  const downloadIdsBefore = await getRecentDownloadIds()
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: clickAtInTab,
    args: [{
      x,
      y,
      coordinateSpace: payload.coordinateSpace,
      screenshotWidth: payload.screenshotWidth,
      screenshotHeight: payload.screenshotHeight,
    }],
  })
  return {
    tab: tabInfo(await chrome.tabs.get(tab.id)),
    clicked: result?.result || null,
    afterClick: await waitForPageFeedback(tab.id, { timeoutMs: waitAfterClickMs, downloadIdsBefore }),
    waitAfterClickMs,
  }
}

async function typeIntoElement(payload) {
  const tab = await resolveTab(payload.tabId)
  const selector = typeof payload.selector === 'string' ? payload.selector : ''
  const text = typeof payload.text === 'string' ? payload.text : ''
  const value = typeof payload.value === 'string' ? payload.value : ''
  const clear = payload.clear !== false
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: typeInTab,
    args: [{ selector, text, value, clear }],
  })
  return result?.result
}

async function buildVisualElementMap(payload = {}) {
  const tab = await resolveTab(payload.tabId)
  const maxElements = clampNumber(
    payload.maxElements,
    1,
    MAX_VISUAL_ELEMENT_LIMIT,
    DEFAULT_VISUAL_ELEMENT_LIMIT,
  )
  const [result] = await chrome.scripting.executeScript({
    target: { tabId: tab.id },
    func: collectVisualElementMapInTab,
    args: [{ maxElements }],
  })
  const freshTab = await chrome.tabs.get(tab.id).catch(() => tab)
  const map = result?.result || { elements: [], elementCount: 0 }
  return {
    tab: tabInfo(freshTab),
    ...map,
    guidance: {
      message: 'X-Science Link 已把当前页面可点击、可输入的位置整理成元素地图，X-Science 可以更稳地选择下一步。',
      coordinateSpace: 'viewport',
      useWith: 'click_at',
      noviceHint: '优先选择 friendlyLabel 清楚、confidence 高、suggestedAction 匹配任务的元素。',
      ...(map.guidance || {}),
    },
  }
}

async function screenshot(payload) {
  const tab = await resolveTab(payload.tabId)
  const visibleTab = await focusTabForCapture(tab)
  const viewport = await getViewportSnapshotMeta(visibleTab.id)
  const dataUrl = await chrome.tabs.captureVisibleTab(visibleTab.windowId, { format: 'png' })
  const visualElementMap = payload.includeElementMap === true
    ? await buildVisualElementMap({
      tabId: visibleTab.id,
      maxElements: payload.maxElements,
    })
    : null
  return {
    tab: tabInfo(visibleTab),
    viewport,
    dataUrl,
    ...(visualElementMap ? { visualElementMap } : {}),
    llmMedia: [{
      kind: 'viewport_screenshot',
      mimeType: 'image/png',
      dataUrl,
      index: 0,
      reason: 'screenshot',
      policy: 'x_science_llm_no_local_ocr',
    }],
    mediaPolicy: 'x_science_llm_no_local_ocr',
  }
}

async function waitForPage(payload = {}) {
  const tab = await resolveTab(payload.tabId)
  return waitForPageFeedback(tab.id, {
    timeoutMs: payload.waitMs || payload.timeoutMs,
    expectedText: payload.expectedText,
    expectedUrl: payload.expectedUrl,
  })
}

async function findDownloadLinks(payload = {}) {
  const searchTerms = payload.searchTerms || payload.query || payload.text || ''
  const read = await readPageLikeHuman({
    url: payload.url,
    tabId: payload.tabId,
    active: payload.active === true,
    maxScrolls: payload.maxScrolls ?? 3,
    textLimit: payload.textLimit || 60_000,
    includeImages: true,
    searchTerms,
    fileTypes: payload.fileTypes,
  })
  const candidates = rankDownloadCandidates(
    buildDownloadCandidatesFromPage(read.page),
    searchTerms,
    payload.fileTypes,
  ).slice(0, clampNumber(payload.maxResults, 1, MAX_DOWNLOAD_CANDIDATES, 20))

  if (payload.closeAfterRead === true && read.tab?.id && !read.tab.active) {
    await chrome.tabs.remove(read.tab.id).catch(() => null)
    read.closed = true
  }

  return {
    tab: read.tab,
    page: read.page ? {
      title: read.page.title,
      url: read.page.url,
      textLength: read.page.textLength,
      linkCount: read.page.links?.length || 0,
      imageCount: read.page.images?.length || 0,
    } : null,
    candidates,
    candidateCount: candidates.length,
    feedback: {
      message: `X-Science Link found ${candidates.length} downloadable candidate(s) on this page.`,
      source: 'page_links_images_and_media',
      searchTerms,
    },
  }
}

async function downloadFromPage(payload = {}) {
  const discovery = await findDownloadLinks({
    ...payload,
    closeAfterRead: false,
    maxResults: payload.maxResults || 12,
  })
  const [candidate] = discovery.candidates || []
  if (!candidate?.url) {
    throw new Error('No downloadable file matched this page. Try read_page or click_at to reveal more links.')
  }
  const download = await downloadFile({
    url: candidate.url,
    filename: payload.filename || candidate.filename,
    saveAs: payload.saveAs,
    conflictAction: payload.conflictAction,
  })
  const shouldCloseDiscoveryTab = payload.url && payload.active !== true && payload.closeAfterRead !== false
  if (shouldCloseDiscoveryTab && discovery.tab?.id && !discovery.tab.active) {
    await chrome.tabs.remove(discovery.tab.id).catch(() => null)
    discovery.closed = true
  }
  return {
    selected: candidate,
    download,
    discovery,
    feedback: {
      message: download.userMessage || `X-Science Link selected "${candidate.label || candidate.filename || candidate.url}" and downloaded it.`,
      state: download.state,
      filename: download.filename,
      verified: download.downloadVerification?.ok === true,
      savedLocationLabel: download.savedLocationLabel,
      recoverySuggestions: download.downloadVerification?.recoverySuggestions || [],
    },
  }
}

async function downloadFile(payload) {
  const url = requireUrl(payload.url)
  const filename = sanitizeDownloadFilename(payload.filename || inferFilenameFromUrl(url))
  const conflictAction = ['uniquify', 'overwrite', 'prompt'].includes(payload.conflictAction)
    ? payload.conflictAction
    : 'uniquify'

  if (!shouldUseBrowserDownloadManager(payload)) {
    try {
      return await downloadFileInBackground({ url, filename })
    } catch (error) {
      if (payload.backgroundOnly === true) throw error
      if (isNonDownloadResponseError(error)) throw error
    }
  }

  return downloadFileWithBrowserDownloadManager({ url, filename, saveAs: payload.saveAs, conflictAction })
}

function isNonDownloadResponseError(error) {
  return /does not look like a downloadable file|Refusing to download a search or navigation page/i.test(compactError(error))
}

function shouldUseBrowserDownloadManager(payload = {}) {
  return payload.backgroundTransfer !== true
    && payload.delivery !== 'background_transfer'
    && payload.backgroundOnly !== true
}

async function downloadFileInBackground({ url, filename }) {
  const response = await fetch(url)
  if (!response.ok) {
    throw new Error(`Background download failed (${response.status})`)
  }

  const mimeType = normalizeDownloadedMimeType(response.headers.get('content-type'), filename)
  const dispositionFilename = inferFilenameFromContentDisposition(response.headers.get('content-disposition'))
  const resolvedFilename = sanitizeDownloadFilename(dispositionFilename || filename || inferFilenameFromUrl(url))
  const extension = detectFileExtension(url, mimeType || resolvedFilename)
  const contentLength = Number(response.headers.get('content-length') || 0)

  if (!isLikelyBackgroundDownloadFile({ url, filename: resolvedFilename, mimeType, extension, response })) {
    throw new Error('Background response does not look like a downloadable file')
  }
  if (contentLength > MAX_BACKGROUND_DOWNLOAD_BYTES) {
    throw new Error(`Background download is too large (${contentLength} bytes)`)
  }

  const blob = await response.blob()
  if (blob.size > MAX_BACKGROUND_DOWNLOAD_BYTES) {
    throw new Error(`Background download is too large (${blob.size} bytes)`)
  }
  const dataUrl = await blobToDataUrl(blob, mimeType)
  const savedLocationLabel = `X-Science Link 后台传输 / ${resolvedFilename}`
  const downloadVerification = verifyBackgroundTransfer({
    filename: resolvedFilename,
    url,
    mimeType,
    bytesReceived: blob.size,
    savedLocationLabel,
  })

  return {
    downloadId: null,
    delivery: 'background_transfer',
    url,
    filename: resolvedFilename,
    mimeType,
    dataUrl,
    state: 'complete',
    bytesReceived: blob.size,
    totalBytes: contentLength || blob.size,
    danger: '',
    exists: true,
    verified: downloadVerification.ok,
    savedLocationLabel,
    userMessage: downloadVerification.userMessage,
    downloadVerification,
  }
}

async function downloadFileWithBrowserDownloadManager({ url, filename, saveAs, conflictAction }) {
  if (isSearchOrNavigationUrl(url)) {
    throw new Error('Refusing to download a search or navigation page')
  }
  const downloadId = await chrome.downloads.download({
    url,
    filename: `X-Science/${filename}`,
    saveAs: saveAs === true,
    conflictAction,
  })
  let item = null
  let waitError = null
  try {
    item = await waitForDownloadComplete(downloadId)
  } catch (error) {
    waitError = error
    const [fallback] = await chrome.downloads.search({ id: downloadId }).catch(() => [])
    item = fallback || null
  }
  const downloadVerification = verifyDownloadItem(item, {
    requestedFilename: filename,
    url,
    error: waitError,
  })
  return {
    downloadId,
    delivery: 'browser_download_manager',
    url,
    filename: item?.filename || `Downloads/X-Science/${filename}`,
    state: item?.state || (waitError ? 'interrupted' : 'complete'),
    bytesReceived: item?.bytesReceived || 0,
    totalBytes: item?.totalBytes || 0,
    danger: item?.danger || '',
    exists: item?.exists !== false,
    verified: downloadVerification.ok,
    savedLocationLabel: downloadVerification.savedLocationLabel,
    userMessage: downloadVerification.userMessage,
    downloadVerification,
  }
}

function normalizeDownloadedMimeType(value, filename = '') {
  const mimeType = String(value || '').split(';')[0].trim().toLowerCase()
  return mimeType || guessMimeType(filename) || 'application/octet-stream'
}

function inferFilenameFromContentDisposition(value) {
  const text = String(value || '')
  const encoded = text.match(/filename\*=UTF-8''([^;]+)/i)
  if (encoded?.[1]) {
    try {
      return decodeURIComponent(encoded[1].trim().replace(/^"|"$/g, ''))
    } catch {
      return encoded[1].trim().replace(/^"|"$/g, '')
    }
  }
  const plain = text.match(/filename="?([^";]+)"?/i)
  return plain?.[1]?.trim() || ''
}

function isLikelyBackgroundDownloadFile({ url, filename, mimeType, extension, response }) {
  const contentDisposition = String(response.headers.get('content-disposition') || '').toLowerCase()
  if (contentDisposition.includes('attachment')) return true
  if (['pdf', 'ppt', 'pptx', 'doc', 'docx', 'xls', 'xlsx', 'csv', 'zip', 'png', 'jpg', 'jpeg', 'webp', 'gif', 'svg'].includes(extension)) return true
  if (/^(application\/pdf|application\/zip|image\/|text\/csv|application\/vnd\.|application\/msword)/i.test(mimeType)) return true
  return isLikelyDownloadableUrl(url, extension, filename)
}

async function blobToDataUrl(blob, mimeType) {
  const bytes = new Uint8Array(await blob.arrayBuffer())
  let binary = ''
  const chunkSize = 0x8000
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + chunkSize))
  }
  return `data:${mimeType || blob.type || 'application/octet-stream'};base64,${btoa(binary)}`
}

function verifyBackgroundTransfer({ filename, url, mimeType, bytesReceived, savedLocationLabel }) {
  const checks = [
    {
      id: 'delivery_background_transfer',
      label: '文件已通过后台传回 X-Science',
      ok: true,
      detail: 'background_transfer',
    },
    {
      id: 'file_type_ready',
      label: '响应看起来是可下载文件',
      ok: Boolean(mimeType),
      detail: mimeType,
    },
    {
      id: 'filename_ready',
      label: '文件名可识别',
      ok: Boolean(filename),
      detail: filename,
    },
    {
      id: 'bytes_ready',
      label: '文件大小看起来正常',
      ok: bytesReceived > 0,
      detail: `${bytesReceived} bytes`,
    },
  ]
  const ok = checks.every((check) => check.ok)
  return {
    ok,
    state: ok ? 'complete' : 'interrupted',
    filename,
    savedLocationLabel,
    bytesReceived,
    totalBytes: bytesReceived,
    danger: '',
    exists: true,
    checks,
    userMessage: ok
      ? `下载完成，文件已通过 X-Science Link 后台传回：${filename}`
      : `后台下载还没有确认完成。请重试 ${url}`,
    recoverySuggestions: ok ? [] : ['使用 saveAs=true 回退到浏览器下载管理器。'],
  }
}

async function openDownloadsFolder() {
  await chrome.downloads.showDefaultFolder()
  return {
    opened: true,
    userMessage: '已打开浏览器下载目录。X-Science 下载的文件通常在 Downloads/X-Science 文件夹里。',
    savedLocationLabel: 'Downloads/X-Science',
  }
}

async function showDownloadedFile(payload = {}) {
  const item = await findDownloadItem(payload)
  if (item?.id && item.exists !== false) {
    chrome.downloads.show(item.id)
    return {
      opened: true,
      downloadId: item.id,
      filename: item.filename,
      state: item.state,
      exists: item.exists !== false,
      userMessage: `已在文件管理器中定位：${basenameFromPath(item.filename || '下载文件')}`,
      savedLocationLabel: item.filename || 'Downloads/X-Science',
    }
  }

  await chrome.downloads.showDefaultFolder()
  return {
    opened: true,
    found: false,
    userMessage: '没有精确定位到这个下载文件，已打开下载目录。请查看 Downloads/X-Science。',
    savedLocationLabel: 'Downloads/X-Science',
  }
}

async function findDownloadItem(payload = {}) {
  if (typeof payload.downloadId === 'number') {
    const [item] = await chrome.downloads.search({ id: payload.downloadId }).catch(() => [])
    if (item) return item
  }

  const query = [
    payload.filename,
    payload.url,
  ].filter((value) => typeof value === 'string' && value.trim())
  if (query.length) {
    const items = await chrome.downloads.search({
      query: query.map((value) => String(value).trim()),
      limit: 20,
      orderBy: ['-startTime'],
    }).catch(() => [])
    if (items.length) return items[0]
  }

  const recent = await chrome.downloads.search({
    limit: 20,
    orderBy: ['-startTime'],
  }).catch(() => [])
  const filename = String(payload.filename || '').trim().toLowerCase()
  const url = String(payload.url || '').trim()
  return recent.find((item) => (
    (filename && String(item.filename || '').toLowerCase().includes(filename))
    || (url && item.url === url)
  )) || null
}

function normalizeTaskGoal(payload = {}) {
  const raw = typeof payload.goal === 'string' ? payload.goal.trim() : ''
  if (['browser_research', 'browser_download', 'local_files', 'mixed'].includes(raw)) return raw
  const hasLocalSignal = Boolean(payload.folderId || payload.path || payload.fileOperation)
  const hasBrowserSignal = Boolean(payload.url || payload.query || wantsTaskDownload(payload))
  if (hasLocalSignal && hasBrowserSignal) return 'mixed'
  if (hasLocalSignal) return 'local_files'
  if (wantsTaskDownload(payload)) return 'browser_download'
  return 'browser_research'
}

function wantsTaskDownload(payload = {}) {
  if (payload.download === false && payload.verifyDownload === false) return false
  if (payload.download === true || payload.verifyDownload === true) return true
  if (Array.isArray(payload.fileTypes) && payload.fileTypes.length > 0) return true
  const task = `${payload.task || ''} ${payload.query || ''} ${payload.searchTerms || ''}`.toLowerCase()
  return /下载|保存|海报|附件|poster|pdf|ppt|slides|download|save|attachment|report|whitepaper|paper|file/.test(task)
}

function buildTaskSearchQueries(payload = {}) {
  const base = cleanTaskText(payload.query || payload.task || '')
  if (!base) return []
  const searchTerms = cleanTaskText(payload.searchTerms || '')
  const fileTypes = normalizeFileTypes(payload.fileTypes)
  const queries = []
  const add = (value) => {
    const query = cleanTaskText(value)
    if (query && !queries.includes(query)) queries.push(query)
  }
  add(base)
  if (searchTerms && !base.toLowerCase().includes(searchTerms.toLowerCase())) {
    add(`${base} ${searchTerms}`)
  }
  if (wantsTaskDownload(payload)) {
    add(`${base} download ${searchTerms}`.trim())
    add(`${base} poster PDF ${searchTerms}`.trim())
    for (const type of fileTypes.slice(0, 2)) {
      add(`${base} filetype:${type}`)
    }
  }
  return queries
}

function collectTaskDownloadCandidates(artifact, payload = {}) {
  const searchTerms = payload.searchTerms || payload.query || payload.task || ''
  const fileTypes = payload.fileTypes
  const candidates = []
  const addPage = (page, source) => {
    if (!page) return
    for (const candidate of rankDownloadCandidates(
      buildDownloadCandidatesFromPage(page),
      searchTerms,
      fileTypes,
    )) {
      candidates.push({
        ...candidate,
        sourcePageUrl: page.url || source?.url || '',
        sourcePageTitle: page.title || source?.title || '',
      })
    }
    for (const candidate of Array.isArray(page.downloadCandidates) ? page.downloadCandidates : []) {
      if (!candidate?.url) continue
      candidates.push({
        ...candidate,
        sourcePageUrl: page.url || source?.url || candidate.sourcePageUrl || '',
        sourcePageTitle: page.title || source?.title || candidate.sourcePageTitle || '',
      })
    }
  }

  if (artifact?.read?.page) addPage(artifact.read.page, artifact.read.tab)
  if (artifact?.search?.page) addPage(artifact.search.page, artifact.search.tab)
  for (const entry of Array.isArray(artifact?.search?.pages) ? artifact.search.pages : []) {
    addPage(entry.page, entry.source)
  }

  const seen = new Set()
  return rankDownloadCandidates(candidates, searchTerms, fileTypes)
    .filter((candidate) => {
      if (!candidate?.url || seen.has(candidate.url)) return false
      seen.add(candidate.url)
      return true
    })
}

async function tryTaskDownloadCandidates(candidates, payload = {}, steps = []) {
  const ranked = rankDownloadCandidates(
    candidates,
    payload.searchTerms || payload.query || payload.task || '',
    payload.fileTypes,
  ).slice(0, TASK_LOOP_DOWNLOAD_CANDIDATE_LIMIT)

  for (const candidate of ranked) {
    if (isSearchOrNavigationDownloadCandidate(candidate)) {
      pushTaskStep(steps, 'download_candidate_skipped', true, {
        url: candidate.url,
        filename: candidate.filename,
        reason: 'search_or_navigation_page',
      })
      continue
    }
    try {
      const download = await downloadFile({
        url: candidate.url,
        filename: payload.filename || candidate.filename,
        saveAs: payload.saveAs,
        conflictAction: payload.conflictAction,
      })
      const verified = isVerifiedDownload(download)
      pushTaskStep(steps, 'download_verified', verified, {
        url: candidate.url,
        filename: download.filename,
        state: download.state,
        userMessage: download.userMessage,
        savedLocationLabel: download.savedLocationLabel,
        checks: download.downloadVerification?.checks || [],
        recoverySuggestions: download.downloadVerification?.recoverySuggestions || [],
      })
      if (verified) return download
    } catch (error) {
      pushTaskStep(steps, 'download_verified', false, {
        url: candidate.url,
        error: compactError(error),
      })
    }
  }
  return null
}

function isSearchOrNavigationDownloadCandidate(candidate) {
  if (!candidate?.url) return true
  if (isSearchOrNavigationUrl(candidate.url)) return true
  if (candidate.extension) return false
  if (candidate.source === 'document') return false
  const filename = sanitizeDownloadFilename(candidate.filename || inferFilenameFromUrl(candidate.url)).toLowerCase()
  if (/\.[a-z0-9]{2,6}$/.test(filename)) return false
  if (/^(search|results?|query|find|browse|home|index|login|signin|account|profile)$/.test(filename)) return true
  if (/(^|[-_])(search|results?|query|find)([-_]|$)/.test(filename)) return true
  return isSearchOrNavigationUrl(candidate.url)
}

function summarizeTaskSearch(search) {
  return {
    query: search?.query || '',
    searchUrl: search?.searchUrl || '',
    resultCount: search?.structured?.resultCount || 0,
    pagesRead: search?.feedback?.pagesRead || 0,
    pagesFailed: search?.feedback?.pagesFailed || 0,
    results: Array.isArray(search?.structured?.results)
      ? search.structured.results.slice(0, 8)
      : [],
    pages: Array.isArray(search?.pages)
      ? search.pages.map((entry) => ({
        ok: entry.ok,
        url: entry.page?.url || entry.source?.url || '',
        title: entry.page?.title || entry.source?.title || '',
        textLength: entry.page?.textLength || 0,
        candidates: entry.page?.downloadCandidates || [],
        error: entry.error || '',
      }))
      : [],
  }
}

function summarizeTaskRead(read) {
  return {
    url: read?.page?.url || read?.tab?.url || '',
    title: read?.page?.title || read?.tab?.title || '',
    textLength: read?.page?.textLength || 0,
    imageCount: read?.page?.images?.length || 0,
    candidateCount: read?.page?.downloadCandidates?.length || 0,
    truncated: read?.page?.truncated === true,
    nextTextOffset: read?.page?.nextTextOffset ?? null,
  }
}

function collectTaskPageUrls(searches = []) {
  const urls = []
  const add = (url) => {
    if (/^https?:\/\//i.test(String(url || '')) && !urls.includes(url)) urls.push(url)
  }
  for (const search of searches) {
    for (const page of Array.isArray(search.pages) ? search.pages : []) add(page.url)
    for (const result of Array.isArray(search.results) ? search.results : []) add(result.url)
  }
  return urls
}

function buildTaskNextActions({ completed, needsUserAuthorization, needsVision, browser, localFiles, downloadRequested }) {
  if (completed) {
    if (localFiles?.operation?.nextActions?.length) return localFiles.operation.nextActions
    return ['Report the result to the user with downloaded filenames, pages read, or file changes verified.']
  }
  if (needsUserAuthorization) {
    return ['Ask the user to authorize a local folder in the X-Science Link workspace, then rerun run_task_loop.']
  }
  if (needsVision) {
    return [
      'Use the returned llmMedia screenshot(s) to identify the correct button or link.',
      'Call click_at with screenshot coordinates, then wait_for_page and download_from_page.',
    ]
  }
  if (downloadRequested && !browser?.downloads?.length) {
    return ['Try a narrower query, open the likely result page, then use screenshot plus click_at if the download link is only visible visually.']
  }
  if (localFiles?.failures?.length) {
    return ['Inspect the local file error, ask for authorization if needed, and retry with a dryRun preview.']
  }
  return ['Continue with the most specific single-step X-Science Link action for the remaining task.']
}

function buildTaskRecovery({ browser, localFiles, downloadRequested }) {
  const failures = [
    ...(Array.isArray(browser?.failures) ? browser.failures : []),
    ...(Array.isArray(localFiles?.failures) ? localFiles.failures : []),
  ]
  return {
    failureCount: failures.length,
    failures: failures.slice(0, 8),
    strategy: downloadRequested
      ? 'retry_with_refined_query_or_visual_click_then_verify_download'
      : 'retry_smallest_failed_step_then_verify_state',
  }
}

function buildTaskFeedbackMessage({ completed, statusText, browser, localFiles, downloadRequested }) {
  if (completed && downloadRequested) {
    const download = browser?.downloads?.find(isVerifiedDownload)
    return download?.userMessage || `X-Science Link completed the loop and verified the download: ${download?.filename || 'downloaded file'}.`
  }
  if (completed && localFiles?.completed) {
    return localFiles.operation?.userMessage || 'X-Science Link completed the local file inspection or approved file operation loop.'
  }
  if (completed) return 'X-Science Link completed the browser reading loop.'
  if (statusText === 'needs_x_science_llm_vision') {
    return 'X-Science Link reached a page that needs X-Science LLM visual judgment before the next click.'
  }
  if (statusText === 'needs_user_authorization') {
    return 'X-Science Link needs the user to authorize a local folder before continuing.'
  }
  return 'X-Science Link collected partial progress and returned recovery steps.'
}

function buildTaskState({
  task,
  goal,
  statusText,
  completed,
  steps,
  workspace,
  browser,
  localFiles,
  downloadRequested,
  startedAt,
}) {
  const events = (Array.isArray(steps) ? steps : []).map((step, index) => ({
    id: `event_${String(index + 1).padStart(3, '0')}`,
    stage: step.stage || 'unknown',
    status: step.ok ? 'completed' : 'failed',
    ok: Boolean(step.ok),
    at: step.at || Date.now(),
    message: describeTaskEvent(step),
    detail: redactTaskEventDetail(step),
    recoveryActions: step.ok ? [] : buildStepRecoveryActions(step, { downloadRequested }),
  }))
  const artifacts = buildTaskArtifacts({ browser, localFiles, workspace })
  const recoveryActions = [
    ...new Set([
      ...events.flatMap((event) => event.recoveryActions || []),
      ...buildTaskStateRecoveryActions({ statusText, completed, browser, localFiles, downloadRequested }),
    ]),
  ].slice(0, 8)

  return {
    protocol: 'x_science_link_os',
    version: chrome.runtime.getManifest().version,
    task,
    goal,
    status: statusText,
    completed: Boolean(completed),
    currentStage: events.at(-1)?.stage || 'not_started',
    summary: buildTaskStateSummary({ completed, statusText, artifacts, browser, localFiles }),
    events,
    artifacts,
    recoveryActions,
    integrations: {
      knowledgeReady: artifacts.length > 0 || Boolean(browser?.readComplete || localFiles?.completed),
      dailyTaskReady: true,
      suggestedKnowledgeTitle: buildSuggestedKnowledgeTitle(task, artifacts),
      suggestedDailyTaskName: buildSuggestedDailyTaskName(task),
      suggestedDailyTaskPrompt: buildSuggestedDailyTaskPrompt(task, goal),
    },
    durationMs: Date.now() - startedAt,
  }
}

function describeTaskEvent(step) {
  const stage = step?.stage || 'unknown'
  if (stage === 'workspace_ready') return step.ok ? 'X-Science Link 工作区已准备好。' : 'X-Science Link 工作区准备失败。'
  if (stage === 'preflight_tab_cleanup') return step.ok ? `已清理旧标签页，保留 ${step.remaining ?? 0} 个相关页面。` : '清理旧标签页失败。'
  if (stage === 'browser_search_read') return step.ok ? `已搜索并读取网页，候选资源 ${step.candidates ?? 0} 个。` : `搜索或读取网页失败：${step.error || ''}`
  if (stage === 'direct_page_read') return step.ok ? `已读取指定网页，文本长度 ${step.textLength ?? 0}。` : `读取指定网页失败：${step.error || ''}`
  if (stage === 'vision_snapshot_ready') return step.ok ? `已准备视觉截图 ${step.llmMediaCount ?? 0} 张。` : `视觉截图准备失败：${step.error || ''}`
  if (stage === 'visual_element_map_ready') return step.ok ? `已生成视觉元素地图，目标 ${step.elementCount ?? 0} 个。` : `视觉元素地图生成失败：${step.error || ''}`
  if (stage === 'download_verified') return step.ok ? (step.userMessage || `下载已验证：${step.filename || ''}`) : `下载未完成：${step.error || step.state || ''}`
  if (stage === 'download_from_result_page') return step.ok ? (step.userMessage || `已从结果页下载：${step.filename || ''}`) : `从结果页下载失败：${step.error || ''}`
  if (stage === 'local_files_status') return step.ok ? `本地授权文件夹 ${step.authorizedFolders ?? 0} 个。` : `检查本地授权失败：${step.error || ''}`
  if (stage === 'local_inventory') return step.ok ? `已生成本地文件清单：${step.files ?? 0} 个文件，${step.directories ?? 0} 个文件夹。` : `生成本地清单失败：${step.error || ''}`
  if (stage === 'local_search') return step.ok ? `本地文件搜索命中 ${step.resultCount ?? 0} 条。` : `本地文件搜索失败：${step.error || ''}`
  if (stage === 'local_file_operation') return step.ok ? `本地文件操作已${step.applied ? '执行' : '预览'}：${step.operation || ''}` : `本地文件操作失败：${step.error || ''}`
  if (stage === 'final_tab_cleanup') return step.ok ? `任务结束后已清理 ${step.closed ?? 0} 个旧标签页。` : '任务结束清理标签页失败。'
  return step.ok ? `完成步骤：${stage}` : `步骤失败：${stage}`
}

function redactTaskEventDetail(step) {
  const detail = { ...(step || {}) }
  delete detail.at
  return detail
}

function buildStepRecoveryActions(step, { downloadRequested }) {
  const stage = step?.stage || ''
  if (stage.includes('download')) {
    return step.recoverySuggestions?.length
      ? step.recoverySuggestions
      : ['重新扫描页面下载链接，必要时用 visual_element_map 找到下载按钮后点击。']
  }
  if (stage.includes('visual')) return ['重新截图并生成 visual_element_map，再选择高置信度目标点击。']
  if (stage.includes('local')) return ['让用户在 X-Science Link 权限中心重新授权文件夹，然后重试本地文件步骤。']
  if (stage.includes('browser') || stage.includes('page') || stage.includes('search')) {
    return downloadRequested
      ? ['换更具体的搜索词，读取最相关页面，然后执行 download_from_page。']
      : ['刷新页面、继续滚动读取，或换一个搜索结果页面。']
  }
  return ['查看该步骤的 detail，按最小失败步骤重试。']
}

function buildTaskStateRecoveryActions({ statusText, completed, browser, localFiles, downloadRequested }) {
  if (completed) return []
  if (statusText === 'needs_user_authorization') return ['打开 X-Science Link 权限中心，授权本地文件或个性化权限后重试。']
  if (statusText === 'needs_x_science_llm_vision') return ['查看 llmMedia 截图和 visualMap，选择正确按钮坐标后调用 click_at。']
  if (downloadRequested && !browser?.downloads?.some(isVerifiedDownload)) return ['再次执行 find_download_links/download_from_page，直到 downloadVerification.ok=true。']
  if (localFiles?.operation?.operationVerification?.recoverySuggestions?.length) return localFiles.operation.operationVerification.recoverySuggestions
  if (localFiles?.failures?.length) return ['先 dryRun 预览本地文件操作，用户确认后再执行。']
  return ['继续执行最小下一步，并记录新的事件。']
}

function buildTaskArtifacts({ browser, localFiles, workspace }) {
  const artifacts = []
  if (workspace?.tab) {
    artifacts.push({
      kind: 'workspace',
      title: workspace.groupTitle || DEFAULT_GROUP_TITLE,
      url: workspace.workspaceUrl || workspace.tab.url || '',
      summary: 'X-Science Link dedicated workspace/tab group.',
    })
  }
  for (const download of Array.isArray(browser?.downloads) ? browser.downloads : []) {
    artifacts.push({
      kind: 'download',
      title: basenameFromPath(download.filename || download.savedLocationLabel || 'download'),
      downloadId: download.downloadId,
      delivery: download.delivery || 'browser_download_manager',
      filename: download.filename,
      mimeType: download.mimeType || '',
      dataUrl: download.dataUrl || '',
      sizeBytes: download.bytesReceived || download.totalBytes || 0,
      url: download.url || '',
      path: download.savedLocationLabel || download.filename || '',
      verified: isVerifiedDownload(download),
      summary: download.userMessage || download.downloadVerification?.userMessage || '',
      checks: download.downloadVerification?.checks || [],
    })
  }
  for (const read of Array.isArray(browser?.reads) ? browser.reads : []) {
    artifacts.push({
      kind: 'web_page',
      title: read.title || read.url || 'Read page',
      url: read.url || '',
      summary: `${read.textLength || 0} characters, ${read.imageCount || 0} image(s), ${read.candidateCount || 0} candidate(s).`,
      truncated: read.truncated === true,
    })
  }
  for (const search of Array.isArray(browser?.searches) ? browser.searches : []) {
    artifacts.push({
      kind: 'web_search',
      title: search.query || 'Browser search',
      url: search.searchUrl || '',
      summary: `${search.resultCount || 0} result(s), ${search.pagesRead || 0} page(s) read.`,
      results: Array.isArray(search.results) ? search.results.slice(0, 5) : [],
    })
  }
  if (browser?.visualMap) {
    artifacts.push({
      kind: 'visual_map',
      title: browser.visualMap.title || browser.visualMap.tab?.title || 'Visual element map',
      url: browser.visualMap.url || browser.visualMap.tab?.url || '',
      summary: `${browser.visualMap.elementCount || 0} visible target(s) mapped.`,
    })
  }
  if (localFiles?.manifest) {
    artifacts.push({
      kind: 'local_manifest',
      title: localFiles.manifest.folder?.name || 'Authorized local folder',
      path: localFiles.manifest.path || '',
      summary: `${localFiles.manifest.totals?.files || 0} file(s), ${localFiles.manifest.totals?.directories || 0} folder(s).`,
    })
  }
  if (localFiles?.search) {
    artifacts.push({
      kind: 'local_search',
      title: localFiles.search.query || 'Local file search',
      path: localFiles.search.path || '',
      summary: `${localFiles.search.resultCount || 0} local file match(es).`,
      results: Array.isArray(localFiles.search.results) ? localFiles.search.results.slice(0, 12) : [],
    })
  }
  if (localFiles?.operation) {
    artifacts.push({
      kind: 'local_operation',
      title: localFiles.operation.operation || 'Local file operation',
      summary: localFiles.operation.error || localFiles.operation.userMessage || JSON.stringify(localFiles.operation.result || {}).slice(0, 500),
      applied: localFiles.operation.applied === true,
      plan: localFiles.operation.localOperationPlan,
      verification: localFiles.operation.operationVerification,
      preview: localFiles.operation.preview,
      nextActions: localFiles.operation.nextActions || [],
    })
  }
  return artifacts.slice(0, 80)
}

function buildTaskStateSummary({ completed, statusText, artifacts, browser, localFiles }) {
  const downloads = artifacts.filter((artifact) => artifact.kind === 'download')
  if (downloads.length) {
    const verified = downloads.filter((artifact) => artifact.verified).length
    return `下载产物 ${downloads.length} 个，已验证 ${verified} 个；状态：${statusText}。`
  }
  if (localFiles?.operation?.userMessage) return `${localFiles.operation.userMessage} 状态：${statusText}。`
  if (localFiles?.completed) return `本地文件流程已返回 ${artifacts.length} 个产物；状态：${statusText}。`
  if (browser?.readComplete) return `浏览器读取流程已返回 ${artifacts.length} 个产物；状态：${statusText}。`
  return completed ? '任务已完成。' : `任务需要继续处理：${statusText}。`
}

function buildSuggestedKnowledgeTitle(task, artifacts) {
  const download = artifacts.find((artifact) => artifact.kind === 'download')
  const page = artifacts.find((artifact) => artifact.kind === 'web_page')
  return cleanTaskText(download?.title || page?.title || task || 'X-Science Link task artifact').slice(0, 120)
}

function buildSuggestedDailyTaskName(task) {
  const cleaned = cleanTaskText(task || 'X-Science Link recurring workflow').slice(0, 60)
  return cleaned || 'X-Science Link recurring workflow'
}

function buildSuggestedDailyTaskPrompt(task, goal) {
  return [
    `Run this recurring X-Science workflow using XScienceLink first: ${cleanTaskText(task || 'recurring workflow')}.`,
    'Call XScienceLink action=list_clients, then use run_task_loop with the relevant browser, file, and personalization context.',
    'Return important browser findings, verified downloads, and user-approved local file summaries to the active conversation when useful.',
    'Use approved browser and local-device context to personalize the result.',
    `Workflow goal: ${goal || 'auto'}.`,
  ].join('\n')
}

function isVerifiedDownload(download) {
  if (!download) return false
  return download.state === 'complete' && download.downloadVerification?.ok !== false
}

function pushTaskStep(steps, stage, ok, detail = {}) {
  const step = {
    stage,
    ok: Boolean(ok),
    at: Date.now(),
    ...detail,
  }
  steps.push(step)
  const reporter = taskProgressReporters.get(steps)
  if (reporter) reporter(step, steps)
}

function setTaskProgressReporter(steps, reporter) {
  if (!steps || !reporter) return
  taskProgressReporters.set(steps, reporter)
}

function createTaskProgressReporter(commandId, payload = {}) {
  const totalSteps = estimateTaskProgressTotal(payload)
  return (step, steps) => {
    const completedSteps = steps.length
    const percent = Math.max(1, Math.min(95, Math.round((completedSteps / totalSteps) * 100)))
    const event = buildTaskProgressEvent(step)
    sendCommandProgress(commandId, {
      status: step.stage,
      summary: event.message,
      progress: {
        completedSteps,
        totalSteps,
        percent,
      },
      event,
    })
  }
}

function estimateTaskProgressTotal(payload = {}) {
  const goal = normalizeTaskGoal(payload)
  const runBrowser = goal === 'browser_research' || goal === 'browser_download' || goal === 'mixed'
  const runLocalFiles = goal === 'local_files' || goal === 'mixed'
  const downloadRequested = wantsTaskDownload(payload)
  const attempts = clampNumber(payload.maxAttempts, 1, TASK_LOOP_MAX_ATTEMPTS, TASK_LOOP_DEFAULT_MAX_ATTEMPTS)
  let total = 3
  if (runBrowser) total += attempts + 1
  if (downloadRequested) total += 2
  if (payload.useVision === true || payload.includeMediaForLLM === true || downloadRequested) total += 2
  if (runLocalFiles) total += 4
  return Math.max(4, Math.min(18, total))
}

function buildTaskProgressEvent(step) {
  const stage = step?.stage || 'running'
  const error = step?.error
  const message = error
    ? `遇到问题：${String(error).slice(0, 160)}`
    : step?.userMessage || TASK_PROGRESS_MESSAGES[stage] || 'X-Science Link 正在继续处理任务。'
  return sanitizeProgressValue({
    stage,
    ok: step?.ok !== false,
    at: step?.at || Date.now(),
    message,
    detail: buildTaskProgressDetail(step),
  })
}

function buildTaskProgressDetail(step) {
  if (!step || typeof step !== 'object') return ''
  const detail = []
  if (step.query) detail.push(`搜索：${step.query}`)
  if (step.url) detail.push(`页面：${step.url}`)
  if (step.filename) detail.push(`文件：${step.filename}`)
  if (typeof step.pagesRead === 'number') detail.push(`已读 ${step.pagesRead} 个页面`)
  if (typeof step.candidates === 'number') detail.push(`发现 ${step.candidates} 个候选`)
  if (typeof step.elementCount === 'number') detail.push(`识别 ${step.elementCount} 个可点击元素`)
  if (typeof step.closed === 'number') detail.push(`关闭 ${step.closed} 个多余标签页`)
  return detail.join('；')
}

const TASK_PROGRESS_MESSAGES = {
  started: 'X-Science Link 已开始执行任务。',
  workspace_ready: '已准备浏览器工作区。',
  preflight_tab_cleanup: '正在整理 X-Science 浏览器标签页。',
  direct_page_read: '正在读取网页全文、图片和下载线索。',
  browser_search_read: '正在检索网页并读取结果页。',
  download_from_result_page: '正在从候选页面尝试下载文件。',
  download_verified: '正在校验下载文件。',
  vision_snapshot_ready: '已为 X-Science LLM 准备页面截图。',
  visual_element_map_ready: '已生成可点击元素地图。',
  local_files_status: '正在检查本地文件授权。',
  local_authorization_prompt_opened: '已打开本地文件授权弹窗。',
  local_inventory: '正在读取已授权文件夹清单。',
  local_search: '正在检索本地文件。',
  local_file_info: '正在读取本地文件信息。',
  local_file_operation: '正在准备本地文件操作。',
  final_tab_cleanup: '正在收尾并关闭多余标签页。',
}

function sendCommandProgress(commandId, payload = {}) {
  if (!commandId) return
  sendJson(sanitizeProgressValue({
    type: 'command_progress',
    commandId,
    ...payload,
  }))
}

function sanitizeProgressValue(value, depth = 0) {
  if (depth > 4) return '[omitted]'
  if (typeof value === 'string') {
    if (/^data:(image|audio|video|application)\//i.test(value)) {
      return `[media data omitted, ${value.length} chars]`
    }
    return value.length > 1200 ? `${value.slice(0, 1197)}...` : value
  }
  if (!value || typeof value !== 'object') return value
  if (Array.isArray(value)) return value.slice(0, 12).map((item) => sanitizeProgressValue(item, depth + 1))
  const output = {}
  for (const [key, item] of Object.entries(value)) {
    if (key === 'dataUrl' || key === 'text' || key === 'html') {
      output[key] = typeof item === 'string' ? `[omitted, ${item.length} chars]` : '[omitted]'
      continue
    }
    output[key] = sanitizeProgressValue(item, depth + 1)
  }
  return output
}

function cleanTaskText(value) {
  return String(value || '').replace(/\s+/g, ' ').trim().slice(0, 500)
}

function compactError(error) {
  return error instanceof Error ? error.message : String(error)
}

async function focusTabForCapture(tab) {
  if (typeof tab.windowId === 'number') {
    await chrome.windows.update(tab.windowId, { focused: true }).catch(() => null)
  }
  if (typeof tab.id === 'number' && !tab.active) {
    await chrome.tabs.update(tab.id, { active: true })
    await new Promise((resolve) => setTimeout(resolve, 180))
    return chrome.tabs.get(tab.id)
  }
  return tab
}

async function getViewportSnapshotMeta(tabId) {
  if (typeof tabId !== 'number') return null
  const [result] = await chrome.scripting.executeScript({
    target: { tabId },
    func: () => ({
      viewportWidth: window.innerWidth || 0,
      viewportHeight: window.innerHeight || 0,
      devicePixelRatio: window.devicePixelRatio || 1,
      scrollX: Math.round(window.scrollX || 0),
      scrollY: Math.round(window.scrollY || 0),
      screenshotCoordinateSpace: 'screenshot',
      viewportCoordinateSpace: 'viewport',
      normalizedCoordinateSpace: 'normalized',
    }),
  }).catch(() => [{ result: null }])
  return result?.result || null
}

async function waitForPageFeedback(tabId, options = {}) {
  const startedAt = Date.now()
  const timeoutMs = clampNumber(options.timeoutMs, 300, 60_000, WAIT_FOR_PAGE_DEFAULT_MS)
  const before = await chrome.tabs.get(tabId).then(tabInfo).catch(() => null)
  await waitForTabComplete(tabId, timeoutMs)
  const expectedText = typeof options.expectedText === 'string' ? options.expectedText.trim() : ''
  const expectedUrl = typeof options.expectedUrl === 'string' ? options.expectedUrl.trim() : ''
  let textMatched = expectedText ? false : null
  let urlMatched = expectedUrl ? false : null

  if (expectedText) {
    const deadline = Date.now() + timeoutMs
    while (Date.now() < deadline) {
      textMatched = await chrome.scripting.executeScript({
        target: { tabId },
        func: (needle) => (document.body?.innerText || '').toLowerCase().includes(String(needle || '').toLowerCase()),
        args: [expectedText],
      }).then(([result]) => Boolean(result?.result)).catch(() => false)
      if (textMatched) break
      await delay(250)
    }
  }

  const after = await chrome.tabs.get(tabId).then(tabInfo).catch(() => null)
  if (expectedUrl && after?.url) {
    urlMatched = after.url.includes(expectedUrl)
  }
  const newDownloads = await getNewDownloads(options.downloadIdsBefore)

  return {
    before,
    after,
    status: after?.status || 'unknown',
    textMatched,
    urlMatched,
    newDownloads,
    durationMs: Date.now() - startedAt,
    message: 'X-Science Link waited for the page after the browser action and captured the current tab state.',
  }
}

async function getRecentDownloadIds() {
  const downloads = await chrome.downloads.search({
    limit: 10,
    orderBy: ['-startTime'],
  }).catch(() => [])
  return downloads.map((item) => item.id).filter((id) => typeof id === 'number')
}

async function getNewDownloads(idsBefore = []) {
  const before = new Set(Array.isArray(idsBefore) ? idsBefore : [])
  const downloads = await chrome.downloads.search({
    limit: 10,
    orderBy: ['-startTime'],
  }).catch(() => [])
  return downloads
    .filter((item) => !before.has(item.id))
    .map((item) => ({
      id: item.id,
      filename: item.filename,
      url: item.url,
      state: item.state,
      bytesReceived: item.bytesReceived,
      totalBytes: item.totalBytes,
      danger: item.danger,
      exists: item.exists,
    }))
}

async function captureViewportMediaForLLM(tab, options = {}) {
  const maxCaptures = clampNumber(
    options.maxCaptures,
    1,
    MAX_LLM_MEDIA_CAPTURES,
    DEFAULT_LLM_MEDIA_CAPTURES,
  )
  const visibleTab = await focusTabForCapture(tab)
  const captures = []
  const reason = options.reason || 'read_page'

  try {
    await chrome.scripting.executeScript({
      target: { tabId: visibleTab.id },
      func: () => window.scrollTo({ top: 0, left: 0, behavior: 'auto' }),
    })
    await new Promise((resolve) => setTimeout(resolve, 180))
  } catch {
    // Capture the current viewport when the page cannot be scripted.
  }

  for (let index = 0; index < maxCaptures; index += 1) {
    const dataUrl = await chrome.tabs.captureVisibleTab(visibleTab.windowId, {
      format: 'jpeg',
      quality: 82,
    })
    captures.push({
      kind: 'viewport_screenshot',
      mimeType: 'image/jpeg',
      dataUrl,
      index,
      reason,
      policy: 'x_science_llm_no_local_ocr',
    })

    const [scrollResult] = await chrome.scripting.executeScript({
      target: { tabId: visibleTab.id },
      func: scrollPageInTab,
      args: [{ direction: 'down', amount: 0 }],
    }).catch(() => [{ result: { reachedBottom: true } }])
    if (scrollResult?.result?.reachedBottom) break
  }

  return captures
}

async function closeTab(payload) {
  const tab = await resolveTab(payload.tabId)
  await chrome.tabs.remove(tab.id)
  return { closed: true, tabId: tab.id }
}

async function cleanupXScienceTabs(payload = {}) {
  const group = await getPrimaryXScienceGroup()
  if (!group) {
    return { groupFound: false, closed: [], remaining: [] }
  }
  const maxTabs = clampNumber(payload.maxTabs, 1, MAX_X_SCIENCE_GROUP_TABS, MAX_X_SCIENCE_GROUP_TABS)
  const keepTabIds = new Set(
    [
      payload.tabId,
      ...(Array.isArray(payload.keepTabIds) ? payload.keepTabIds : []),
    ].filter((id) => typeof id === 'number'),
  )
  const workspaceUrl = chrome.runtime.getURL('workspace.html')
  const tabs = (await chrome.tabs.query({ groupId: group.id }))
    .sort((a, b) => (a.index ?? 0) - (b.index ?? 0))
  const removable = tabs
    .filter((tab) => !keepTabIds.has(tab.id))
    .filter((tab) => (tab.url || tab.pendingUrl || '') !== workspaceUrl)
    .filter((tab) => !tab.pinned)
  const closeCount = tabs.length > maxTabs ? removable.length : 0
  // Once the X-Science group crosses the limit, close all old tabs instead of only trimming the overflow.
  const toClose = removable.slice(0, closeCount)
  const ids = toClose.map((tab) => tab.id).filter((id) => typeof id === 'number')
  if (ids.length) {
    await chrome.tabs.remove(ids).catch(() => null)
  }
  const remaining = await chrome.tabs.query({ groupId: group.id }).catch(() => [])
  await moveXScienceGroupToLeft(group.id)
  return {
    groupFound: true,
    groupId: group.id,
    maxTabs,
    closed: toClose.map(tabInfo),
    remaining: remaining.map(tabInfo),
    feedback: {
      message: `X-Science Link cleaned up ${toClose.length} old tab(s) in the X-Science group.`,
      maxGroupTabs: MAX_X_SCIENCE_GROUP_TABS,
      maxVisibleTabs: MAX_VISIBLE_X_SCIENCE_TABS,
    },
  }
}

async function resolveTab(tabId) {
  if (typeof tabId === 'number') {
    return chrome.tabs.get(tabId)
  }
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true })
  if (!tab?.id) throw new Error('No active tab')
  return tab
}

async function addToXScienceGroup(tabId, options = {}) {
  if (typeof tabId !== 'number') return null
  try {
    const tab = await chrome.tabs.get(tabId)
    const reconciliation = await enforceSingleXScienceGroup({
      source: 'add_tab',
      preferredWindowId: tab.windowId,
    }).catch(() => null)
    const existingGroup = reconciliation?.primary || null
    if (existingGroup && tab.windowId !== existingGroup.windowId) {
      await chrome.tabs.move(tabId, { windowId: existingGroup.windowId, index: -1 })
    }
    const groupId = existingGroup
      ? await chrome.tabs.group({ tabIds: [tabId], groupId: existingGroup.id })
      : await chrome.tabs.group({ tabIds: [tabId] })
    await chrome.tabGroups.update(groupId, {
      title: DEFAULT_GROUP_TITLE,
      color: 'blue',
      collapsed: options.active !== true,
    })
    await moveXScienceGroupToLeft(groupId)
    return groupId
  } catch {
    // Grouping can fail in browsers that do not support tabGroups; command still succeeds.
    return null
  }
}

async function getPrimaryXScienceGroup() {
  return (await enforceSingleXScienceGroup())?.primary || null
}

async function enforceSingleXScienceGroup(options = {}) {
  try {
    if (!chrome?.tabGroups?.query || !chrome?.tabs?.query) return null
    const groups = (await chrome.tabGroups.query({}))
      .filter(isXScienceTabGroup)
      .sort(compareXScienceTabGroups(options.preferredWindowId))
    if (!groups.length) return null
    const [primary, ...duplicates] = groups
    const duplicateGroupIds = duplicates
      .map((group) => group.id)
      .filter((id) => typeof id === 'number')
    const groupCountBefore = duplicateGroupIds.length + MAX_X_SCIENCE_TAB_GROUPS
    let mergedTabCount = 0

    for (const duplicate of duplicates) {
      mergedTabCount += await mergeXScienceGroupIntoPrimary(primary, duplicate)
    }

    await chrome.tabGroups.update(primary.id, {
      title: DEFAULT_GROUP_TITLE,
      color: 'blue',
    }).catch(() => null)
    await moveXScienceGroupToLeft(primary.id)
    return {
      primary,
      source: options.source || '',
      groupCountBefore,
      groupCountAfter: MAX_X_SCIENCE_TAB_GROUPS,
      duplicateGroupIds,
      mergedTabCount,
    }
  } catch {
    return null
  }
}

async function mergeXScienceGroupIntoPrimary(primary, duplicate) {
  if (!primary || !duplicate || primary.id === duplicate.id) return 0
  try {
    const tabs = await chrome.tabs.query({ groupId: duplicate.id })
    const tabIds = tabs.map((tab) => tab.id).filter((id) => typeof id === 'number')
    if (!tabIds.length) return 0
    if (typeof primary.windowId === 'number' && duplicate.windowId !== primary.windowId) {
      await chrome.tabs.move(tabIds, { windowId: primary.windowId, index: -1 }).catch(() => null)
    }
    await chrome.tabs.group({ tabIds, groupId: primary.id })
    return tabIds.length
  } catch {
    // Best effort: future commands will still use the primary group.
    return 0
  }
}

function isXScienceTabGroup(group) {
  return String(group?.title || '').trim() === DEFAULT_GROUP_TITLE
}

function compareXScienceTabGroups(preferredWindowId) {
  return (a, b) => {
    const aPreferred = typeof preferredWindowId === 'number' && a.windowId === preferredWindowId
    const bPreferred = typeof preferredWindowId === 'number' && b.windowId === preferredWindowId
    if (aPreferred !== bPreferred) return aPreferred ? -1 : 1
    return (a.id ?? 0) - (b.id ?? 0)
  }
}

async function moveXScienceGroupToLeft(groupId) {
  if (typeof groupId !== 'number') return
  try {
    await chrome.tabGroups.move(groupId, { index: 0 })
    return
  } catch {
    // Some Chromium builds are stricter about tabGroups.move. Moving the tabs
    // gives the same user-facing result while keeping commands resilient.
  }

  try {
    const tabs = await chrome.tabs.query({ groupId })
    const tabIds = tabs
      .sort((a, b) => (a.index ?? 0) - (b.index ?? 0))
      .map((tab) => tab.id)
      .filter((id) => typeof id === 'number')
    if (tabIds.length) {
      await chrome.tabs.move(tabIds, { index: 0 })
    }
  } catch {
    // Positioning is best-effort; the browser action itself should still work.
  }
}

async function enforceXScienceGroupLimits(groupId, protectedTabId) {
  if (typeof groupId !== 'number') return
  try {
    const tabs = await chrome.tabs.query({ groupId })
    if (tabs.length <= MAX_X_SCIENCE_GROUP_TABS) return
    const workspaceUrl = chrome.runtime.getURL('workspace.html')
    const removable = tabs
      .filter((tab) => tab.id !== protectedTabId)
      .filter((tab) => (tab.url || tab.pendingUrl || '') !== workspaceUrl)
      .filter((tab) => !tab.pinned)
      .sort((a, b) => (a.index ?? 0) - (b.index ?? 0))
    // Once the X-Science group crosses the limit, close all old tabs instead of only trimming the overflow.
    const removeCount = removable.length
    const tabIds = removable.slice(0, removeCount).map((tab) => tab.id).filter((id) => typeof id === 'number')
    if (tabIds.length) await chrome.tabs.remove(tabIds)
  } catch {
    // Limits are defensive; never fail the user's requested action only because cleanup failed.
  }
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function waitForTabComplete(tabId, timeoutMs = 12_000) {
  if (typeof tabId !== 'number') return Promise.resolve()
  return new Promise((resolve) => {
    let done = false
    const finish = () => {
      if (done) return
      done = true
      clearTimeout(timeout)
      chrome.tabs.onUpdated.removeListener(listener)
      resolve()
    }
    const timeout = setTimeout(finish, timeoutMs)
    const listener = (updatedTabId, changeInfo) => {
      if (updatedTabId === tabId && changeInfo.status === 'complete') {
        finish()
      }
    }
    chrome.tabs.onUpdated.addListener(listener)
    chrome.tabs.get(tabId).then((tab) => {
      if (tab.status === 'complete') finish()
    }).catch(finish)
  })
}

function extractPageInTab() {
  const visibleText = document.body?.innerText || ''
  const links = [...document.querySelectorAll('a[href]')].slice(0, 80).map((link) => ({
    text: (link.innerText || link.getAttribute('aria-label') || '').trim().slice(0, 200),
    href: link.href,
  }))
  const forms = [...document.querySelectorAll('input, textarea, select, button')].slice(0, 120).map((el) => ({
    tag: el.tagName.toLowerCase(),
    type: el.getAttribute('type') || '',
    name: el.getAttribute('name') || '',
    id: el.id || '',
    placeholder: el.getAttribute('placeholder') || '',
    ariaLabel: el.getAttribute('aria-label') || '',
    text: (el.innerText || el.value || '').trim().slice(0, 120),
  }))
  return {
    title: document.title,
    url: location.href,
    selection: String(window.getSelection?.() || ''),
    text: visibleText.trim().slice(0, 60_000),
    links,
    controls: forms,
  }
}

async function readPageLikeHumanInTab(options = {}) {
  const startedAt = Date.now()
  const maxScrolls = Math.max(0, Math.min(30, Math.floor(Number(options.maxScrolls) || 12)))
  const defaultTextLimit = Math.max(10_000, Math.min(200_000, Math.floor(Number(options.defaultTextLimit) || 120_000)))
  const textOffset = Math.max(0, Math.floor(Number(options.textOffset) || 0))
  const textLimit = Math.max(10_000, Math.min(defaultTextLimit, Math.floor(Number(options.textLimit) || defaultTextLimit)))
  const imageLimit = Math.max(0, Math.min(200, Math.floor(Number(options.imageLimit) || 120)))
  const includeImages = options.includeImages !== false
  const feedReadMode = options.feedReadMode === true
  const feedCompletionMode = String(options.feedCompletionMode || '')
  const onlyToday = options.onlyToday === true
  const maxCards = Math.max(20, Math.min(180, Math.floor(Number(options.maxCards) || 80)))
  const maxNoNewScrolls = Math.max(1, Math.min(8, Math.floor(Number(options.maxNoNewScrolls) || 3)))
  const sourceTarget = options.sourceTarget && typeof options.sourceTarget === 'object' ? options.sourceTarget : null
  const socialFeedSurfaces = normalizeSocialFeedSurfaces(options.socialFeedSurfaces, sourceTarget)
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
  const cleanText = (value) => String(value || '')
    .replace(/\u00a0/g, ' ')
    .replace(/[ \t]+\n/g, '\n')
    .replace(/\n{3,}/g, '\n\n')
    .trim()
  const scrollMetrics = () => ({
    x: Math.round(window.scrollX || 0),
    y: Math.round(window.scrollY || 0),
    viewportWidth: window.innerWidth || 0,
    viewportHeight: window.innerHeight || 0,
    scrollHeight: Math.max(
      document.documentElement?.scrollHeight || 0,
      document.body?.scrollHeight || 0,
    ),
    scrollWidth: Math.max(
      document.documentElement?.scrollWidth || 0,
      document.body?.scrollWidth || 0,
    ),
  })
  const isAtBottom = () => {
    const metrics = scrollMetrics()
    return metrics.y + metrics.viewportHeight >= metrics.scrollHeight - 8
  }

  const snapshots = []
  const seenTextBlocks = new Set()
  const accumulatedBlocks = []
  const rememberVisibleText = () => {
    const text = cleanText(document.body?.innerText || '')
    for (const block of text.split(/\n{2,}/)) {
      const normalized = cleanText(block)
      if (normalized.length < 2 || seenTextBlocks.has(normalized)) continue
      seenTextBlocks.add(normalized)
      accumulatedBlocks.push(normalized)
    }
  }

  window.scrollTo({ top: 0, left: 0, behavior: 'auto' })
  await wait(250)
  await waitForImagesToSettle(350)
  if (feedReadMode) expandFeedCardText()

  let scrolls = 0
  let surfaceReports = []
  let socialCards = []
  if (feedReadMode && socialFeedSurfaces.length) {
    const socialRead = await readSocialFeedSurfaces()
    scrolls = socialRead.scrolls
    surfaceReports = socialRead.surfaces
    socialCards = socialRead.cards
  } else {
    for (let index = 0; index <= maxScrolls; index += 1) {
      if (feedReadMode) expandFeedCardText()
      rememberVisibleText()
      snapshots.push(scrollMetrics())
      if (isAtBottom()) break
      const before = scrollMetrics()
      window.scrollBy({
        top: Math.max(320, Math.floor((window.innerHeight || 800) * 0.86)),
        left: 0,
        behavior: 'smooth',
      })
      await wait(360)
      await waitForImagesToSettle(180)
      const after = scrollMetrics()
      if (after.y <= before.y + 4 && after.scrollHeight <= before.scrollHeight + 4) break
      scrolls += 1
    }
  }

  rememberVisibleText()
  const domText = cleanText(document.body?.innerText || '')
  const accumulatedText = cleanText(accumulatedBlocks.join('\n\n'))
  const text = domText.length >= accumulatedText.length ? domText : accumulatedText
  const textEnd = Math.min(text.length, textOffset + textLimit)
  const textSlice = text.slice(textOffset, textEnd)
  const nextTextOffset = textEnd < text.length ? textEnd : null
  const links = collectPageLinks()
  const controls = collectPageControls()
  const images = includeImages ? collectPageImages(imageLimit) : []
  const mediaResources = collectPageMediaResources()
  const cards = socialCards.length ? socialCards : collectFeedCards(maxCards, sourceTarget)
  const finalMetrics = scrollMetrics()
  const truncated = nextTextOffset !== null

  return {
    page: {
      title: document.title || '',
      url: location.href,
      selection: String(window.getSelection?.() || ''),
      text: textSlice,
      textLength: text.length,
      textOffset,
      nextTextOffset,
      truncated,
      links,
      controls,
      images,
      cards,
      media: mediaResources,
      mediaPolicy: 'x_science_llm_no_local_ocr',
      metrics: finalMetrics,
      snapshots,
    },
    feedback: {
      mode: 'human_like_scroll',
      feedReadMode,
      feedCompletionMode,
      onlyToday,
      socialFeedSurfaces,
      maxNoNewScrolls,
      surfaceReports,
      scrolls,
      reachedBottom: isAtBottom(),
      textLength: text.length,
      truncated,
      imageCount: images.length,
      audioCount: mediaResources.audio.length,
      videoCount: mediaResources.video.length,
      documentCount: mediaResources.documents.length,
      cardCount: cards.length,
      linkCount: links.length,
      controlCount: controls.length,
      durationMs: Date.now() - startedAt,
      viewportHeight: finalMetrics.viewportHeight,
      scrollHeight: finalMetrics.scrollHeight,
    },
  }

  function normalizeSocialFeedSurfaces(value, target) {
    const category = String(target?.category || '').toLowerCase()
    if (!feedReadMode || !['x', 'linkedin'].includes(category)) return []
    const raw = Array.isArray(value) ? value : []
    const surfaces = raw
      .map((entry) => String(entry || '').toLowerCase())
      .filter((entry) => ['recommended', 'following'].includes(entry))
    return [...new Set(surfaces.length ? surfaces : ['recommended', 'following'])]
  }

  async function readSocialFeedSurfaces() {
    const cardsByKey = new Map()
    const reports = []
    let totalScrolls = 0

    for (const surface of socialFeedSurfaces) {
      const clicked = await clickSocialFeedSurface(surface, sourceTarget)
      window.scrollTo({ top: 0, left: 0, behavior: 'auto' })
      await wait(clicked ? 900 : 300)
      await waitForImagesToSettle(350)

      let noNewScrolls = 0
      let surfaceScrolls = 0
      let olderSeen = false
      let stoppedBy = 'max_scrolls'
      const beforeSurfaceCount = cardsByKey.size

      for (let index = 0; index <= maxScrolls; index += 1) {
        expandFeedCardText()
        rememberVisibleText()
        const visibleCards = collectFeedCards(maxCards, sourceTarget, surface)
        let added = 0
        let olderCards = 0

        for (const card of visibleCards) {
          if (onlyToday && !isTodayFeedCard(card)) {
            olderCards += 1
            continue
          }
          const key = feedCardKey(card)
          if (!key || cardsByKey.has(key)) continue
          cardsByKey.set(key, card)
          added += 1
        }

        if (added === 0) {
          noNewScrolls += 1
        } else {
          noNewScrolls = 0
        }
        if (olderCards > 0 && index > 0) olderSeen = true

        snapshots.push({
          ...scrollMetrics(),
          surface,
          cards: cardsByKey.size,
          visibleCards: visibleCards.length,
          addedCards: added,
        })

        if (cardsByKey.size >= maxCards) {
          stoppedBy = 'max_cards'
          break
        }
        if (onlyToday && olderSeen && noNewScrolls >= 1) {
          stoppedBy = 'older_than_today'
          break
        }
        if (noNewScrolls >= maxNoNewScrolls) {
          stoppedBy = 'no_new_cards'
          break
        }
        if (isAtBottom()) {
          stoppedBy = 'bottom'
          break
        }

        const progress = await performSocialFeedScroll()
        if (!progress.moved && !progress.heightIncreased) {
          if (added === 0) {
            stoppedBy = 'scroll_exhausted'
            break
          }
          await wait(500)
          continue
        }
        totalScrolls += 1
        surfaceScrolls += 1
      }

      reports.push({
        surface,
        clicked,
        scrolls: surfaceScrolls,
        cardsAdded: cardsByKey.size - beforeSurfaceCount,
        noNewScrolls,
        stoppedBy,
      })
    }

    return {
      cards: [...cardsByKey.values()].slice(0, maxCards),
      surfaces: reports,
      scrolls: totalScrolls,
    }
  }

  async function performSocialFeedScroll() {
    const before = scrollMetrics()
    const distance = Math.max(420, Math.floor((window.innerHeight || 800) * 0.92))
    const scroller = document.scrollingElement || document.documentElement || document.body
    const main = document.querySelector?.('main, [role="main"]') || document.body

    try { main?.focus?.({ preventScroll: true }) } catch {}
    try {
      window.scrollBy({
        top: distance,
        left: 0,
        behavior: 'smooth',
      })
    } catch {}
    try {
      if (scroller && scroller.scrollTop <= before.y + 4) {
        scroller.scrollTop = before.y + distance
      }
    } catch {}
    try {
      main?.dispatchEvent(new WheelEvent('wheel', { deltaY: distance, bubbles: true, cancelable: true }))
      window.dispatchEvent(new WheelEvent('wheel', { deltaY: distance, bubbles: true, cancelable: true }))
    } catch {}
    try {
      main?.dispatchEvent(new KeyboardEvent('keydown', { key: 'PageDown', code: 'PageDown', bubbles: true }))
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'PageDown', code: 'PageDown', bubbles: true }))
    } catch {}

    await wait(650)
    await waitForImagesToSettle(250)
    const after = scrollMetrics()
    return {
      before,
      after,
      moved: after.y > before.y + 4,
      heightIncreased: after.scrollHeight > before.scrollHeight + 4,
    }
  }

  async function clickSocialFeedSurface(surface, target) {
    const sourceCategory = String(target?.category || '').toLowerCase()
    const patterns = surface === 'following'
      ? [/^following$/i, /^关注$/, /^正在关注$/, /^关注中$/]
      : [/^for you$/i, /^推荐$/, /^为你推荐$/, /^首页$/, /^top$/i]
    const candidates = [
      ...document.querySelectorAll(
        'a[role="tab"], button[role="tab"], [role="tablist"] a, [role="tablist"] button, nav a, nav button, [data-testid="ScrollSnap-SwipeableList"] a, [data-testid="ScrollSnap-SwipeableList"] button'
      ),
    ]

    for (const candidate of candidates) {
      const label = cleanText(candidate.innerText || candidate.getAttribute('aria-label') || candidate.textContent || '')
      if (!label || !patterns.some((pattern) => pattern.test(label))) continue
      const role = String(candidate.getAttribute('role') || '').toLowerCase()
      const href = candidate.getAttribute('href') || ''
      const inNavigation = Boolean(candidate.closest('nav, [role="tablist"], [data-testid="ScrollSnap-SwipeableList"]'))
      const safeNavigationLink = sourceCategory === 'x'
        ? /\/home(?:\?|#|$)/i.test(href) || role === 'tab' || inNavigation
        : /\/feed(?:\?|#|$)/i.test(href) || role === 'tab' || inNavigation
      if (!safeNavigationLink) continue
      try {
        candidate.scrollIntoView({ block: 'center', inline: 'center' })
        candidate.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
        candidate.click()
        await wait(900)
        return true
      } catch {
        return false
      }
    }

    return surface === 'recommended'
  }

  function feedCardKey(card) {
    const url = cleanText(card?.url || '')
    const title = cleanText(card?.title || '')
    const summary = cleanText(card?.summary || card?.text || '')
    return `${url || title}:${summary.slice(0, 180)}`
  }

  function expandFeedCardText() {
    const buttons = [...document.querySelectorAll('button, [role="button"]')]
      .filter((button) => {
        const label = cleanText(button.innerText || button.getAttribute('aria-label') || '')
        return /^(see more|show more|more|查看更多|显示更多|展开|更多)$/i.test(label)
          || /…more|see more|show more|查看更多|显示更多/i.test(label)
      })
      .slice(0, 12)
    for (const button of buttons) {
      try {
        button.scrollIntoView({ block: 'center', inline: 'nearest' })
        button.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
        button.click()
      } catch {
        // Expanding post text is best effort and should never stop a read.
      }
    }
  }

  function collectFeedCards(limit, target, surface = 'current') {
    const sourceCategory = String(target?.category || '').toLowerCase()
    const selectors = [
      'article[data-testid="tweet"]',
      '[data-testid="cellInnerDiv"] article',
      '.feed-shared-update-v2',
      '.occludable-update',
      '[data-urn*="urn:li:activity"]',
      '[data-id*="urn:li:activity"]',
      '[role="article"]',
      'article',
    ]
    const containers = selectors
      .flatMap((selector) => [...document.querySelectorAll(selector)])
      .filter((element, index, all) => all.indexOf(element) === index)
      .filter((element) => {
        const text = cleanText(element.innerText || '')
        if (text.length < 40) return false
        const rect = element.getBoundingClientRect()
        return rect.width > 120 && rect.height > 40
      })

    const records = []
    const seen = new Set()
    for (const element of containers) {
      if (records.length >= limit) break
      const text = cleanText(element.innerText || '')
      if (!text || isFeedChromeText(text)) continue
      const title = inferFeedCardTitle(element, text)
      if (!title || isFeedChromeText(title)) continue
      const detailLinks = collectElementLinks(element)
        .filter((link) => isUsefulFeedDetailLink(link.href, sourceCategory))
        .slice(0, 8)
      const url = findFeedCardUrl(element, sourceCategory, detailLinks)
      const publishedAt = element.querySelector?.('time[datetime]')?.getAttribute('datetime')
        || inferFeedCardRelativeTime(element, text)
      const author = inferFeedCardAuthor(element, text)
      const images = collectElementImages(element).slice(0, 8)
      const key = `${url || title}:${text.slice(0, 120)}`
      if (seen.has(key)) continue
      seen.add(key)
      records.push({
        title,
        text: text.slice(0, 1400),
        summary: text.slice(0, 800),
        url,
        author,
        publishedAt,
        surface,
        images,
        detailLinks,
        links: collectElementLinks(element).slice(0, 20),
      })
    }
    if (sourceCategory === 'linkedin' && records.length < limit) {
      for (const card of collectLinkedInFeedCardsFromLinks(limit - records.length, surface, seen)) {
        records.push(card)
      }
    }
    return records
  }

  function collectLinkedInFeedCardsFromLinks(limit, surface = 'current', seen = new Set()) {
    const records = []
    const links = [...document.querySelectorAll('a[href]')]
      .map((link) => ({ link, href: absolutizeUrl(link.getAttribute('href') || link.href) }))
      .filter((entry) => isLinkedInFeedStoryLink(entry.href))

    for (const { link, href } of links) {
      if (records.length >= limit) break
      const element = findLinkedInFeedContainer(link)
      const linkText = cleanText(link.innerText || link.getAttribute?.('aria-label') || '')
      const containerText = cleanText(element?.innerText || link.closest?.('article, section, li, div')?.innerText || '')
      const text = isLinkedInNewsStoryLink(href) && linkText.length >= 20
        ? linkText
        : (containerText || linkText)
      if (text.length < 40 || isFeedChromeText(text)) continue
      const title = isLinkedInNewsStoryLink(href)
        ? inferLinkedInStoryTitle(linkText || text)
        : inferFeedCardTitle(element || link, text)
      if (!title || isFeedChromeText(title)) continue
      const detailLinks = collectElementLinks(element || link)
        .filter((candidate) => isUsefulFeedDetailLink(candidate.href, 'linkedin'))
        .slice(0, 8)
      const url = href || findFeedCardUrl(element || link, 'linkedin', detailLinks)
      const key = `${url || title}:${text.slice(0, 120)}`
      if (seen.has(key)) continue
      seen.add(key)
      records.push({
        title,
        text: text.slice(0, 1400),
        summary: text.slice(0, 800),
        url,
        author: inferFeedCardAuthor(element || link, text),
        publishedAt: element?.querySelector?.('time[datetime]')?.getAttribute('datetime')
          || inferFeedCardRelativeTime(element || link, text),
        surface,
        images: collectElementImages(element || link).slice(0, 8),
        detailLinks,
        links: collectElementLinks(element || link).slice(0, 20),
      })
    }
    return records
  }

  function isLinkedInFeedStoryLink(href) {
    return /linkedin\.com\/(?:feed\/update|posts|news\/story)\//i.test(String(href || ''))
  }

  function isLinkedInNewsStoryLink(href) {
    return /linkedin\.com\/news\/story\//i.test(String(href || ''))
  }

  function inferLinkedInStoryTitle(text) {
    return cleanText(text)
      .split('\n')
      .map((line) => cleanText(line))
      .find((line) => line.length >= 18 && !isFeedChromeLine(line) && !/^\d+\s*(位读者|readers?)$/i.test(line))
      || cleanText(text).slice(0, 160)
  }

  function findLinkedInFeedContainer(link) {
    const closest = link.closest?.(
      '.feed-shared-update-v2, .occludable-update, [data-urn*="urn:li:activity"], [data-id*="urn:li:activity"], [role="article"], article'
    )
    if (closest) return closest

    let current = link.parentElement
    for (let depth = 0; current && depth < 8; depth += 1) {
      const text = cleanText(current.innerText || '')
      const rect = current.getBoundingClientRect?.() || { width: 0, height: 0 }
      if (text.length >= 80 && rect.width > 120 && rect.height > 40) return current
      current = current.parentElement
    }
    return link
  }

  function inferFeedCardRelativeTime(element, text) {
    const timeElement = element.querySelector?.('time')
    const explicit = cleanText(
      timeElement?.getAttribute?.('datetime')
      || timeElement?.innerText
      || timeElement?.getAttribute?.('aria-label')
      || ''
    )
    if (explicit) return explicit

    const lines = cleanText(text).split('\n').map((line) => cleanText(line)).filter(Boolean)
    return lines.find((line) => (
      line.length <= 36
      && /^(now|today|yesterday|just now|刚刚|今天|昨天|昨日|前天|\d+\s*(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks|mo|month|months|y|yr|year|years)|\d+\s*(秒|分钟|小时|天|周|个月|年)前)$/i.test(line)
    )) || ''
  }

  function isTodayFeedCard(card) {
    const value = cleanText(card?.publishedAt || '')
    if (!value) return true
    const lower = value.toLowerCase()
    if (/^(now|today|just now|刚刚|今天)$/i.test(value)) return true
    if (/(^\d+\s*(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours)$)|(\d+\s*(秒|分钟|小时)前)/i.test(lower)) return true
    if (/yesterday|昨天|昨日|前天|(^\d+\s*(d|day|days|w|week|weeks|mo|month|months|y|yr|year|years)$)|(\d+\s*(天|周|个月|年)前)/i.test(lower)) return false
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime())) return true
    const now = new Date()
    return parsed.getFullYear() === now.getFullYear()
      && parsed.getMonth() === now.getMonth()
      && parsed.getDate() === now.getDate()
  }

  function inferFeedCardTitle(element, text) {
    const preferred = cleanText(
      element.querySelector?.('[data-testid="tweetText"], .feed-shared-update-v2__description, .update-components-text, h1, h2, h3')?.innerText || ''
    )
    const source = preferred || text
    return source
      .split(/\n|(?<=[.!?。！？])\s+/)
      .map((line) => cleanText(line).slice(0, 220))
      .find((line) => line.length >= 18 && !isFeedChromeLine(line))
      || cleanText(source).slice(0, 160)
  }

  function inferFeedCardAuthor(element, text) {
    const profileLink = element.querySelector?.('a[href*="/in/"], a[href*="/company/"], a[href^="/"][href*="/status/"]')
    const label = cleanText(profileLink?.innerText || profileLink?.getAttribute?.('aria-label') || '')
    if (label && !isFeedChromeLine(label)) return label.slice(0, 160)
    return text
      .split('\n')
      .map((line) => cleanText(line))
      .find((line) => line.length >= 2 && line.length <= 80 && !isFeedChromeLine(line))
      || ''
  }

  function findFeedCardUrl(element, category, detailLinks = []) {
    if (detailLinks.length) return detailLinks[0].href
    const links = collectElementLinks(element)
    const preferred = links.find((link) => isUsefulFeedDetailLink(link.href, category))
      || links.find((link) => category === 'linkedin' && /linkedin\.com\/company\/[^/]+\/posts/i.test(link.href))
      || links[0]
    return preferred?.href || location.href
  }

  // Page functions are serialized by chrome.scripting, so their small URL
  // predicate must live in this execution scope rather than an outer closure.
  function hostnameMatchesDomain(hostname, domain) {
    const host = String(hostname || '').toLowerCase().replace(/\.$/, '')
    return host === domain || host.endsWith(`.${domain}`)
  }

  function isUsefulFeedDetailLink(href, category) {
    const value = String(href || '')
    if (!/^https?:\/\//i.test(value)) return false
    if (category === 'x') {
      return /(?:x|twitter)\.com\/[^/?#]+\/status\/\d+/i.test(value)
        && !/\/(analytics|photo|video)(?:\/|$)/i.test(value)
    }
    if (category === 'linkedin') {
      if (/linkedin\.com\/(?:feed\/update|posts|news\/story)\//i.test(value)) return true
      if (/linkedin\.com\/safety\/go/i.test(value)) return true
      if (/^https:\/\/lnkd\.in\//i.test(value)) return true
      try {
        const url = new URL(value)
        return !hostnameMatchesDomain(url.hostname, 'linkedin.com')
          && !/(accounts|login|signup|help|support|privacy|terms)\./i.test(url.hostname)
      } catch {
        return false
      }
    }
    if (category === 'github') {
      return /^https:\/\/github\.com\/[^/?#]+\/[^/?#]+\/?$/i.test(value)
        && !/github\.com\/(?:features|topics|collections|events|sponsors|settings|notifications|pulls|issues|repos)(?:\/|$)/i.test(value)
    }
    return true
  }

  function collectElementLinks(element) {
    return [...element.querySelectorAll('a[href]')]
      .map((link) => ({
        text: cleanText(link.innerText || link.getAttribute('aria-label') || '').slice(0, 220),
        href: absolutizeUrl(link.getAttribute('href') || link.href),
      }))
      .filter((link) => /^https?:\/\//i.test(link.href))
  }

  function collectElementImages(element) {
    return [...element.querySelectorAll('img')]
      .map((img) => ({
        src: img.currentSrc || img.src || '',
        alt: cleanText(img.alt || img.getAttribute('aria-label') || ''),
      }))
      .filter((image) => /^https?:\/\//i.test(image.src))
  }

  function absolutizeUrl(value) {
    try {
      return new URL(value, location.href).href
    } catch {
      return ''
    }
  }

  function isFeedChromeText(value) {
    const text = cleanText(value).toLowerCase()
    if (!text || text.length < 8) return true
    return /^(home|search|notifications|messages|profile|bookmarks|skip to|首页|搜索|通知|消息|书签)/.test(text)
  }

  function isFeedChromeLine(value) {
    const text = cleanText(value).toLowerCase()
    return /^(home|search|notifications|messages|profile|bookmarks|follow|following|post|repost|like|reply|comment|share|analytics|skip to|首页|搜索|通知|消息|书签|关注|评论|分享|赞|发送)$/.test(text)
      || /^@\w+$/.test(text)
      || /^\d+(\.\d+)?[km]?$/.test(text)
  }

  function collectPageLinks() {
    return [...document.querySelectorAll('a[href]')].slice(0, 160).map((link) => ({
      text: cleanText(link.innerText || link.getAttribute('aria-label') || '').slice(0, 220),
      href: link.href,
    }))
  }

  function collectPageControls() {
    return [...document.querySelectorAll('input, textarea, select, button, [role="button"], [contenteditable="true"]')]
      .slice(0, 160)
      .map((el) => ({
        tag: el.tagName.toLowerCase(),
        type: el.getAttribute('type') || '',
        name: el.getAttribute('name') || '',
        id: el.id || '',
        placeholder: el.getAttribute('placeholder') || '',
        ariaLabel: el.getAttribute('aria-label') || '',
        text: cleanText(el.innerText || el.value || '').slice(0, 160),
      }))
  }

  function collectPageImages(limit) {
    const records = []
    const seen = new Set()
    const addImage = (record) => {
      const src = String(record.src || '').trim()
      if (!src || seen.has(src) || records.length >= limit) return
      seen.add(src)
      records.push(record)
    }

    for (const img of [...document.images]) {
      const rect = img.getBoundingClientRect()
      const figure = img.closest('figure, picture, article, section')
      addImage({
        kind: 'image',
        src: img.currentSrc || img.src || '',
        alt: cleanText(img.alt || img.getAttribute('aria-label') || ''),
        title: cleanText(img.title || ''),
        caption: cleanText(figure?.querySelector?.('figcaption, [class*="caption"], [class*="Caption"]')?.innerText || '').slice(0, 300),
        width: Math.round(rect.width || img.width || 0),
        height: Math.round(rect.height || img.height || 0),
        naturalWidth: img.naturalWidth || 0,
        naturalHeight: img.naturalHeight || 0,
        visible: rect.bottom > 0 && rect.right > 0 && rect.top < window.innerHeight && rect.left < window.innerWidth,
        loading: img.loading || '',
      })
    }

    for (const element of [...document.querySelectorAll('[style], figure, article, section, div')]) {
      if (records.length >= limit) break
      const background = getComputedStyle(element).backgroundImage || ''
      const match = background.match(/url\(["']?([^"')]+)["']?\)/)
      if (!match?.[1]) continue
      const rect = element.getBoundingClientRect()
      addImage({
        kind: 'background',
        src: new URL(match[1], location.href).href,
        alt: cleanText(element.getAttribute('aria-label') || element.textContent || '').slice(0, 220),
        title: cleanText(element.getAttribute('title') || ''),
        caption: '',
        width: Math.round(rect.width || 0),
        height: Math.round(rect.height || 0),
        naturalWidth: 0,
        naturalHeight: 0,
        visible: rect.bottom > 0 && rect.right > 0 && rect.top < window.innerHeight && rect.left < window.innerWidth,
        loading: '',
      })
    }
    return records
  }

  function collectPageMediaResources() {
    const media = {
      audio: [],
      video: [],
      documents: [],
      embeds: [],
      policy: 'x_science_llm_no_local_ocr',
    }
    const seen = new Set()
    const add = (collection, record) => {
      const url = String(record.url || '').trim()
      const key = `${collection}:${url}:${record.type || ''}`
      if (!url || seen.has(key) || media[collection].length >= 80) return
      seen.add(key)
      media[collection].push(record)
    }
    const absoluteUrl = (value) => {
      try {
        return new URL(value, location.href).href
      } catch {
        return ''
      }
    }

    for (const element of [...document.querySelectorAll('audio, audio source')]) {
      const url = absoluteUrl(element.currentSrc || element.src || element.getAttribute('src') || '')
      add('audio', {
        url,
        type: element.getAttribute('type') || '',
        title: cleanText(element.getAttribute('title') || element.closest('[aria-label]')?.getAttribute('aria-label') || ''),
        controls: Boolean(element.controls),
      })
    }

    for (const element of [...document.querySelectorAll('video, video source, track')]) {
      const url = absoluteUrl(element.currentSrc || element.src || element.getAttribute('src') || '')
      add('video', {
        url,
        type: element.getAttribute('type') || '',
        title: cleanText(element.getAttribute('title') || element.closest('[aria-label]')?.getAttribute('aria-label') || ''),
        controls: Boolean(element.controls),
      })
    }

    for (const element of [...document.querySelectorAll('a[href], iframe[src], embed[src], object[data]')]) {
      const raw = element.href || element.src || element.data || element.getAttribute('href') || element.getAttribute('src') || element.getAttribute('data') || ''
      const url = absoluteUrl(raw)
      if (!url) continue
      const lower = url.toLowerCase().split(/[?#]/)[0]
      const label = cleanText(element.innerText || element.getAttribute('aria-label') || element.getAttribute('title') || '')
      if (lower.endsWith('.pdf')) {
        add('documents', { url, mimeType: 'application/pdf', title: label, kind: 'pdf' })
      } else if (/\.(mp3|wav|m4a|aac|ogg|flac)$/i.test(lower)) {
        add('audio', { url, type: guessMediaMime(lower, 'audio'), title: label })
      } else if (/\.(mp4|webm|mov|m4v)$/i.test(lower)) {
        add('video', { url, type: guessMediaMime(lower, 'video'), title: label })
      } else if (element.tagName && ['IFRAME', 'EMBED', 'OBJECT'].includes(element.tagName)) {
        add('embeds', { url, title: label, tag: element.tagName.toLowerCase() })
      }
    }
    return media
  }

  function guessMediaMime(value, family) {
    if (value.endsWith('.pdf')) return 'application/pdf'
    if (value.endsWith('.mp3')) return 'audio/mpeg'
    if (value.endsWith('.wav')) return 'audio/wav'
    if (value.endsWith('.m4a')) return 'audio/mp4'
    if (value.endsWith('.aac')) return 'audio/aac'
    if (value.endsWith('.ogg')) return `${family}/ogg`
    if (value.endsWith('.flac')) return 'audio/flac'
    if (value.endsWith('.mp4') || value.endsWith('.m4v')) return 'video/mp4'
    if (value.endsWith('.webm')) return 'video/webm'
    if (value.endsWith('.mov')) return 'video/quicktime'
    return ''
  }

  async function waitForImagesToSettle(timeoutMs) {
    const pending = [...document.images]
      .filter((img) => !img.complete)
      .slice(0, 20)
      .map((img) => new Promise((resolve) => {
        const done = () => resolve()
        const timer = setTimeout(done, timeoutMs)
        img.addEventListener('load', () => {
          clearTimeout(timer)
          done()
        }, { once: true })
        img.addEventListener('error', () => {
          clearTimeout(timer)
          done()
        }, { once: true })
      }))
    await Promise.race([
      Promise.all(pending),
      wait(timeoutMs),
    ])
  }
}

async function scrollPageInTab({ direction, amount }) {
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
  const before = {
    x: Math.round(window.scrollX || 0),
    y: Math.round(window.scrollY || 0),
    viewportHeight: window.innerHeight || 0,
    scrollHeight: Math.max(document.documentElement?.scrollHeight || 0, document.body?.scrollHeight || 0),
  }
  const step = amount || Math.max(320, Math.floor((window.innerHeight || 800) * 0.86))
  if (direction === 'top') {
    window.scrollTo({ top: 0, behavior: 'smooth' })
  } else if (direction === 'bottom') {
    window.scrollTo({ top: before.scrollHeight, behavior: 'smooth' })
  } else {
    window.scrollBy({ top: direction === 'up' ? -step : step, behavior: 'smooth' })
  }
  await wait(320)
  const after = {
    x: Math.round(window.scrollX || 0),
    y: Math.round(window.scrollY || 0),
    viewportHeight: window.innerHeight || 0,
    scrollHeight: Math.max(document.documentElement?.scrollHeight || 0, document.body?.scrollHeight || 0),
  }
  return {
    direction,
    amount: step,
    before,
    after,
    reachedBottom: after.y + after.viewportHeight >= after.scrollHeight - 8,
  }
}

function clickInTab({ selector, text }) {
  const element = findElement(selector, text)
  if (!element) throw new Error('Element not found')
  element.scrollIntoView({ block: 'center', inline: 'center' })
  element.dispatchEvent(new MouseEvent('mouseover', { bubbles: true }))
  element.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }))
  element.dispatchEvent(new MouseEvent('mouseup', { bubbles: true }))
  element.click()
  return describeElement(element)
}

function clickAtInTab({ x, y, coordinateSpace, screenshotWidth, screenshotHeight }) {
  const point = normalizeClickPoint({ x, y, coordinateSpace, screenshotWidth, screenshotHeight })
  const element = document.elementFromPoint(point.x, point.y)
  if (!element) throw new Error(`No element at viewport point ${point.x}, ${point.y}`)
  const rect = element.getBoundingClientRect()
  const clickX = Math.max(rect.left + 1, Math.min(point.x, rect.right - 1))
  const clickY = Math.max(rect.top + 1, Math.min(point.y, rect.bottom - 1))
  const eventInit = {
    bubbles: true,
    cancelable: true,
    view: window,
    clientX: clickX,
    clientY: clickY,
    screenX: window.screenX + clickX,
    screenY: window.screenY + clickY,
    button: 0,
    buttons: 1,
  }
  element.dispatchEvent(new PointerEvent('pointerover', { ...eventInit, pointerId: 1, pointerType: 'mouse' }))
  element.dispatchEvent(new PointerEvent('pointerdown', { ...eventInit, pointerId: 1, pointerType: 'mouse' }))
  element.dispatchEvent(new MouseEvent('mouseover', eventInit))
  element.dispatchEvent(new MouseEvent('mousedown', eventInit))
  element.dispatchEvent(new PointerEvent('pointerup', { ...eventInit, pointerId: 1, pointerType: 'mouse', buttons: 0 }))
  element.dispatchEvent(new MouseEvent('mouseup', { ...eventInit, buttons: 0 }))
  element.dispatchEvent(new MouseEvent('click', { ...eventInit, buttons: 0 }))
  return {
    ...describeElement(element),
    x: clickX,
    y: clickY,
    requestedX: x,
    requestedY: y,
    coordinateSpace: point.coordinateSpace,
    viewportWidth: point.viewportWidth,
    viewportHeight: point.viewportHeight,
    devicePixelRatio: point.devicePixelRatio,
  }
}

function normalizeClickPoint({ x, y, coordinateSpace, screenshotWidth, screenshotHeight }) {
  const viewportWidth = window.innerWidth || 0
  const viewportHeight = window.innerHeight || 0
  const devicePixelRatio = window.devicePixelRatio || 1
  let nextX = Number(x)
  let nextY = Number(y)
  let space = coordinateSpace || 'viewport'

  if (space === 'normalized' || (nextX > 0 && nextX <= 1 && nextY > 0 && nextY <= 1)) {
    nextX *= viewportWidth
    nextY *= viewportHeight
    space = 'normalized'
  } else if (
    space === 'screenshot'
    && Number(screenshotWidth) > 0
    && Number(screenshotHeight) > 0
  ) {
    nextX = nextX * (viewportWidth / Number(screenshotWidth))
    nextY = nextY * (viewportHeight / Number(screenshotHeight))
  } else if (
    devicePixelRatio > 1
    && (nextX > viewportWidth || nextY > viewportHeight)
    && nextX / devicePixelRatio <= viewportWidth + 8
    && nextY / devicePixelRatio <= viewportHeight + 8
  ) {
    nextX /= devicePixelRatio
    nextY /= devicePixelRatio
    space = 'screenshot'
  }

  return {
    x: Math.max(0, Math.min(viewportWidth - 1, nextX)),
    y: Math.max(0, Math.min(viewportHeight - 1, nextY)),
    coordinateSpace: space,
    viewportWidth,
    viewportHeight,
    devicePixelRatio,
  }
}

function typeInTab({ selector, text, value, clear }) {
  const element = findElement(selector, text) || document.activeElement
  if (!element) throw new Error('Input element not found')
  const target = getEditableTarget(element)
  if (!target) throw new Error('Target is not editable')
  target.scrollIntoView({ block: 'center', inline: 'center' })
  target.focus()
  applyTextValue(target, value, clear)
  return describeElement(target)
}

function getEditableTarget(element) {
  if (
    element instanceof HTMLInputElement
    || element instanceof HTMLTextAreaElement
    || element instanceof HTMLSelectElement
    || element.isContentEditable
  ) {
    return element
  }
  return element.querySelector?.('input, textarea, select, [contenteditable="true"]') || null
}

function applyTextValue(element, value, clear) {
  if (element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement) {
    const nextValue = clear ? value : `${element.value || ''}${value}`
    setNativeValue(element, nextValue)
    element.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: value }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
    return
  }
  if (element instanceof HTMLSelectElement) {
    element.value = value
    element.dispatchEvent(new Event('input', { bubbles: true }))
    element.dispatchEvent(new Event('change', { bubbles: true }))
    return
  }
  element.textContent = clear ? value : `${element.textContent || ''}${value}`
  element.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: value }))
}

function setNativeValue(element, value) {
  const prototype = Object.getPrototypeOf(element)
  const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set
  if (setter) {
    setter.call(element, value)
  } else {
    element.value = value
  }
}

function findElement(selector, text) {
  if (selector) {
    const selected = document.querySelector(selector)
    if (selected) return selected
  }
  const needle = String(text || '').trim().toLowerCase()
  if (!needle) return null
  const candidates = [...document.querySelectorAll('button, a, input, textarea, select, [role=\"button\"], [aria-label]')]
  return candidates.find((el) => {
    const haystack = [
      el.innerText,
      el.value,
      el.getAttribute('aria-label'),
      el.getAttribute('placeholder'),
      el.getAttribute('title'),
      el.getAttribute('name'),
    ].filter(Boolean).join(' ').toLowerCase()
    return haystack.includes(needle)
  }) || null
}

function describeElement(element) {
  return {
    tag: element.tagName.toLowerCase(),
    id: element.id || '',
    name: element.getAttribute('name') || '',
    text: (element.innerText || element.value || '').trim().slice(0, 200),
    ariaLabel: element.getAttribute('aria-label') || '',
  }
}

function collectVisualElementMapInTab(options = {}) {
  const maxElements = Math.max(1, Math.min(160, Math.floor(Number(options.maxElements) || 80)))
  const selectors = [
    'a[href]',
    'button',
    'input',
    'textarea',
    'select',
    'summary',
    '[role="button"]',
    '[role="link"]',
    '[role="menuitem"]',
    '[role="tab"]',
    '[role="checkbox"]',
    '[role="radio"]',
    '[onclick]',
    '[tabindex]:not([tabindex="-1"])',
    '[contenteditable="true"]',
  ].join(',')
  const candidates = [...document.querySelectorAll(selectors)]
  const elements = []
  const seen = new Set()

  for (const element of candidates) {
    if (elements.length >= maxElements) break
    if (!(element instanceof HTMLElement) && !(element instanceof SVGElement)) continue
    const rect = element.getBoundingClientRect()
    const visible = isVisualElementVisible(element, rect)
    if (!visible) continue
    const center = {
      x: Math.round(rect.left + rect.width / 2),
      y: Math.round(rect.top + rect.height / 2),
    }
    const key = `${Math.round(rect.left)}:${Math.round(rect.top)}:${Math.round(rect.width)}:${Math.round(rect.height)}:${getVisualElementLabel(element)}`
    if (seen.has(key)) continue
    seen.add(key)

    const tag = element.tagName.toLowerCase()
    const role = element.getAttribute('role') || ''
    const type = element.getAttribute('type') || ''
    const friendlyLabel = getVisualElementLabel(element) || `${role || tag} ${center.x},${center.y}`
    const suggestedAction = getVisualSuggestedAction(element, tag, role, type)
    const confidence = getVisualElementConfidence({ friendlyLabel, rect, tag, role, type })
    elements.push({
      tag,
      role,
      type,
      text: cleanVisualText(element.innerText || element.textContent || element.value || ''),
      friendlyLabel,
      selector: buildVisualSelector(element),
      rect: {
        x: Math.round(rect.left),
        y: Math.round(rect.top),
        width: Math.round(rect.width),
        height: Math.round(rect.height),
      },
      center,
      confidence,
      suggestedAction,
      noviceHint: buildVisualNoviceHint(suggestedAction, friendlyLabel),
      isVisible: true,
      href: getVisualHref(element),
      placeholder: element.getAttribute('placeholder') || '',
      ariaLabel: element.getAttribute('aria-label') || '',
      title: element.getAttribute('title') || '',
    })
  }

  const sorted = elements
    .sort((a, b) => b.confidence - a.confidence || a.rect.y - b.rect.y || a.rect.x - b.rect.x)
    .map((element, index) => ({ index: index + 1, ...element }))

  return {
    title: document.title || '',
    url: location.href,
    viewport: {
      width: window.innerWidth || 0,
      height: window.innerHeight || 0,
      scrollX: Math.round(window.scrollX || 0),
      scrollY: Math.round(window.scrollY || 0),
      devicePixelRatio: window.devicePixelRatio || 1,
    },
    elements: sorted,
    elementCount: sorted.length,
    guidance: {
      coordinateSpace: 'viewport',
      suggestedUse: 'Use center.x and center.y with click_at after checking the friendlyLabel and screenshot.',
    },
  }

  function isVisualElementVisible(element, rect) {
    if (!rect || rect.width < 6 || rect.height < 6) return false
    if (rect.bottom < 0 || rect.right < 0 || rect.top > window.innerHeight || rect.left > window.innerWidth) return false
    const style = window.getComputedStyle(element)
    if (style.visibility === 'hidden' || style.display === 'none' || Number(style.opacity) === 0) return false
    const disabled = element.disabled || element.getAttribute('aria-disabled') === 'true'
    return !disabled
  }

  function getVisualElementLabel(element) {
    const ariaLabel = element.getAttribute('aria-label')
    const labelledBy = element.getAttribute('aria-labelledby')
    const labelledByText = labelledBy
      ? labelledBy.split(/\s+/).map((id) => document.getElementById(id)?.innerText || '').join(' ')
      : ''
    const title = element.getAttribute('title')
    const placeholder = element.getAttribute('placeholder')
    const name = element.getAttribute('name')
    const alt = element.getAttribute('alt') || element.querySelector?.('img[alt]')?.getAttribute('alt')
    const value = element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement
      ? element.value
      : ''
    const hrefLabel = getVisualHref(element) ? lastUrlPart(getVisualHref(element)) : ''
    return cleanVisualText(
      ariaLabel
      || labelledByText
      || element.innerText
      || element.textContent
      || value
      || placeholder
      || title
      || alt
      || name
      || hrefLabel
    ).slice(0, 180)
  }

  function getVisualSuggestedAction(element, tag, role, type) {
    const normalizedType = String(type || '').toLowerCase()
    if (tag === 'textarea' || element.isContentEditable) return 'type'
    if (tag === 'input') {
      if (['checkbox', 'radio'].includes(normalizedType)) return 'toggle'
      if (['button', 'submit', 'reset', 'image'].includes(normalizedType)) return 'click'
      return 'type'
    }
    if (tag === 'select') return 'select'
    if (role === 'checkbox' || role === 'radio') return 'toggle'
    if (tag === 'a' || role === 'link') return 'open_link'
    return 'click'
  }

  function getVisualElementConfidence({ friendlyLabel, rect, tag, role, type }) {
    let score = 0.45
    if (friendlyLabel && !/^\w+ \d+,\d+$/.test(friendlyLabel)) score += 0.25
    if (['button', 'a', 'input', 'textarea', 'select'].includes(tag)) score += 0.12
    if (role) score += 0.06
    if (type) score += 0.03
    if (rect.width >= 24 && rect.height >= 18) score += 0.08
    return Math.max(0.1, Math.min(0.98, Number(score.toFixed(2))))
  }

  function buildVisualNoviceHint(action, label) {
    const name = label ? `“${label}”` : '这个元素'
    if (action === 'type') return `可以在 ${name} 输入文字。`
    if (action === 'select') return `可以展开 ${name} 选择一个选项。`
    if (action === 'toggle') return `可以切换 ${name} 的勾选状态。`
    if (action === 'open_link') return `可以打开 ${name} 这个链接。`
    return `可以点击 ${name}。`
  }

  function buildVisualSelector(element) {
    const tag = element.tagName.toLowerCase()
    if (element.id) return `#${cssEscape(element.id)}`
    for (const attr of ['data-testid', 'aria-label', 'name', 'title', 'placeholder']) {
      const value = element.getAttribute(attr)
      if (value && value.length <= 80) {
        return `${tag}[${attr}="${cssAttrEscape(value)}"]`
      }
    }
    const path = []
    let current = element
    while (current && current.nodeType === Node.ELEMENT_NODE && path.length < 5) {
      const currentTag = current.tagName.toLowerCase()
      if (current.id) {
        path.unshift(`#${cssEscape(current.id)}`)
        break
      }
      const parent = current.parentElement
      if (!parent) {
        path.unshift(currentTag)
        break
      }
      const siblings = [...parent.children].filter((child) => child.tagName === current.tagName)
      const index = siblings.indexOf(current) + 1
      path.unshift(siblings.length > 1 ? `${currentTag}:nth-of-type(${index})` : currentTag)
      current = parent
    }
    return path.join(' > ')
  }

  function cleanVisualText(value) {
    return String(value || '')
      .replace(/\u00a0/g, ' ')
      .replace(/\s+/g, ' ')
      .trim()
  }

  function lastUrlPart(value) {
    try {
      const url = new URL(value, location.href)
      const part = decodeURIComponent(url.pathname.split('/').filter(Boolean).pop() || url.hostname)
      return part.replace(/[-_]+/g, ' ')
    } catch {
      return ''
    }
  }

  function getVisualHref(element) {
    const href = element.href
    if (typeof href === 'string') return href
    const animatedHref = href?.baseVal
    if (typeof animatedHref === 'string') return animatedHref
    return element.getAttribute('href') || ''
  }

  function cssEscape(value) {
    if (window.CSS?.escape) return window.CSS.escape(value)
    return String(value).replace(/[^a-zA-Z0-9_-]/g, (char) => `\\${char}`)
  }

  function cssAttrEscape(value) {
    return String(value).replace(/\\/g, '\\\\').replace(/"/g, '\\"')
  }
}

function sendHello(state) {
  const runtime = getExtensionRuntimeMetadata()
  sendJson(buildHelloPayload({
    state,
    browser: navigator.userAgent.includes('Edg/') ? 'Edge' : 'Chrome',
    runtime,
    userAgent: navigator.userAgent,
    runtimePolicy: getBrowserRuntimePolicy(),
  }))
}

function getExtensionRuntimeMetadata() {
  return buildExtensionRuntimeMetadata({
    manifest: chrome.runtime.getManifest(),
    startedAt: EXTENSION_STARTED_AT,
    lastHeartbeatAt,
  })
}

function getBrowserRuntimePolicy() {
  return buildBrowserRuntimePolicy({
    maxXScienceTabs: MAX_X_SCIENCE_GROUP_TABS,
    defaultSearchReadPages: DEFAULT_SEARCH_READ_PAGES,
    maxSearchReadPages: MAX_SEARCH_READ_PAGES,
    maxHumanReadScrolls: MAX_HUMAN_READ_SCROLLS,
    maxVisibleElements: MAX_VISUAL_ELEMENT_LIMIT,
  })
}

async function getLocalDoctorReport() {
  const statusSnapshot = await getStatusSnapshot()
  return buildLocalDoctorReport({
    statusSnapshot,
    runtime: getExtensionRuntimeMetadata(),
    runtimePolicy: getBrowserRuntimePolicy(),
    permissions: await getPermissionSnapshot(),
    warnings: statusSnapshot.lastError ? [statusSnapshot.lastError] : [],
  })
}

async function getPermissionSnapshot() {
  const bookmarks = await chrome.permissions.contains(BOOKMARKS_PERMISSION).catch(() => false)
  return buildPermissionSnapshot({
    bookmarks,
    localFiles: status.localFiles || {},
  })
}

function sendJson(payload) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return
  ws.send(JSON.stringify(payload))
}

function scheduleReconnect() {
  if (!allowReconnect) return
  clearReconnectTimer()
  reconnectAttempt += 1
  const delay = Math.min(RECONNECT_MAX_MS, RECONNECT_BASE_MS * 2 ** Math.min(5, reconnectAttempt))
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    connectFromStorage()
  }, delay)
}

function clearReconnectTimer() {
  if (reconnectTimer) clearTimeout(reconnectTimer)
  reconnectTimer = null
}

function clearHeartbeat() {
  if (heartbeatTimer) clearInterval(heartbeatTimer)
  heartbeatTimer = null
}

async function getStoredState() {
  const stored = await chrome.storage.local.get(['serverUrl', 'appSession', 'clientId', 'name', 'user', 'autoConnect'])
  const clientId = stored.clientId || createClientId()
  if (!stored.clientId) await chrome.storage.local.set({ clientId })
  return {
    serverUrl: normalizeServerUrl(stored.serverUrl || DEFAULT_SERVER_URL),
    appSession: stored.appSession || '',
    clientId,
    name: stored.name || '',
    user: stored.user || null,
    autoConnect: stored.autoConnect !== false,
  }
}

function setStatus(next) {
  status = { ...status, autoConnect: allowReconnect, ...next }
  chrome.runtime.sendMessage({ type: 'status_changed', status }).catch(() => {})
  void broadcastStatusToXScienceTabs(status)
}

async function broadcastStatusToXScienceTabs(nextStatus) {
  try {
    const tabs = await chrome.tabs.query({})
    await Promise.all(tabs
      .filter((tab) => shouldNotifyXScienceTab(tab.url || tab.pendingUrl || ''))
      .map((tab) => (
        typeof tab.id === 'number'
          ? chrome.tabs.sendMessage(tab.id, { type: 'status_changed', status: nextStatus }).catch(() => null)
          : Promise.resolve(null)
      )))
  } catch {
    // Status broadcasts are best-effort; direct command responses still carry the latest state.
  }
}

function shouldNotifyXScienceTab(url) {
  try {
    const parsed = new URL(url)
    return (
      parsed.hostname === '115.190.116.251'
      || parsed.hostname === '127.0.0.1'
      || parsed.hostname === 'localhost'
    )
  } catch {
    return false
  }
}

async function getStatusSnapshot() {
  const localFiles = await getLocalFilesStatus()
  const bookmarks = await getBookmarksStatus()
  status = { ...status, localFiles, bookmarks }
  return status
}

async function maybeOpenPersonalizationOnConnect() {
  const bookmarks = await getBookmarksStatus()
  if (bookmarks.granted) {
    const needsRefresh = !bookmarks.updatedAt || Date.now() - bookmarks.updatedAt > BOOKMARKS_PROFILE_STALE_MS
    if (needsRefresh) {
      try {
        await buildBookmarksProfile()
        setStatus({ bookmarks: await getBookmarksStatus() })
      } catch (error) {
        setStatus({ lastError: error instanceof Error ? error.message : String(error) })
      }
    }
    return
  }
  const stored = await chrome.storage.local.get(['personalizationPromptedAt', 'personalizationPromptDismissedAt'])
  if (stored.personalizationPromptedAt || stored.personalizationPromptDismissedAt) return
  await chrome.storage.local.set({ personalizationPromptedAt: Date.now() })
  await openPersonalizationPrompt({ active: true, reason: 'first_connect' })
}

async function openPersonalizationPrompt({ active = true, reason = 'manual' } = {}) {
  const url = chrome.runtime.getURL(`personalization.html?reason=${encodeURIComponent(reason)}`)
  const tabs = await chrome.tabs.query({})
  let tab = tabs.find((candidate) => (
    (candidate.url || candidate.pendingUrl || '').startsWith(chrome.runtime.getURL('personalization.html'))
  ))
  const created = !tab
  if (!tab) {
    tab = await chrome.tabs.create({ url, active })
  } else if (typeof tab.id === 'number' && active) {
    await chrome.tabs.update(tab.id, { active: true, url })
    if (typeof tab.windowId === 'number') {
      await chrome.windows.update(tab.windowId, { focused: true })
    }
  }
  return { created, tab: tabInfo(tab), url }
}

async function getBookmarksStatus() {
  if (!chrome.permissions?.contains) {
    return { supported: false, granted: false, bookmarkCount: 0, folderCount: 0 }
  }
  const granted = await chrome.permissions.contains(BOOKMARKS_PERMISSION)
  const stored = await chrome.storage.local.get(['bookmarksProfile'])
  const profile = stored.bookmarksProfile && typeof stored.bookmarksProfile === 'object'
    ? stored.bookmarksProfile
    : null
  return {
    supported: true,
    granted,
    bookmarkCount: granted ? profile?.bookmarkCount || 0 : 0,
    folderCount: granted ? profile?.folderCount || 0 : 0,
    updatedAt: granted ? profile?.updatedAt || 0 : 0,
    topDomains: granted ? profile?.topDomains?.slice?.(0, 8) || [] : [],
  }
}

async function requestBookmarksPermissionAndProfile(options = {}) {
  if (!chrome.permissions?.request) {
    return { supported: false, granted: false, error: 'Bookmarks permission is not supported in this browser.' }
  }
  const granted = await chrome.permissions.request(BOOKMARKS_PERMISSION)
  if (!granted) {
    const bookmarks = await getBookmarksStatus()
    setStatus({ bookmarks })
    return { supported: true, granted: false }
  }
  const profile = await buildBookmarksProfile({ refresh: options.refresh !== false })
  setStatus({ bookmarks: await getBookmarksStatus() })
  return { supported: true, granted: true, profile }
}

async function getBookmarksProfile(payload = {}) {
  const granted = await chrome.permissions.contains(BOOKMARKS_PERMISSION)
  if (!granted) {
    if (payload.prompt === true) {
      await openPersonalizationPrompt({ active: true, reason: 'command' })
    }
    throw new Error('Bookmark personalization requires user authorization in X-Science Link.')
  }
  const stored = await chrome.storage.local.get(['bookmarksProfile'])
  const current = stored.bookmarksProfile && typeof stored.bookmarksProfile === 'object'
    ? stored.bookmarksProfile
    : null
  if (current && payload.refresh !== true) return current
  return buildBookmarksProfile({ refresh: true })
}

async function revokeBookmarksPermission() {
  if (chrome.permissions?.remove) {
    await chrome.permissions.remove(BOOKMARKS_PERMISSION).catch(() => false)
  }
  await chrome.storage.local.remove([
    'bookmarksProfile',
    'bookmarksProfileUpdatedAt',
    'personalizationPromptedAt',
    'personalizationPromptDismissedAt',
  ])
  const bookmarks = await getBookmarksStatus()
  setStatus({ bookmarks })
  return {
    revoked: true,
    bookmarks,
    userMessage: '已关闭书签画像授权，并清除了 X-Science Link 保存的画像摘要。',
  }
}

async function buildBookmarksProfile() {
  const tree = await chrome.bookmarks.getTree()
  const flattened = flattenBookmarks(tree)
  const bookmarks = flattened.filter((item) => item.url).slice(0, MAX_PROFILE_BOOKMARKS)
  const folders = flattened.filter((item) => !item.url)
  const domains = countDomains(bookmarks)
  const terms = extractTopicTerms(flattened)
  const paidDatabaseCandidates = findPaidDatabaseCandidates(bookmarks)
  const profile = {
    updatedAt: Date.now(),
    bookmarkCount: bookmarks.length,
    folderCount: folders.length,
    topDomains: domains.slice(0, 30),
    topTerms: terms.slice(0, 40),
    inferredInterests: inferInterests(terms, domains, paidDatabaseCandidates),
    paidDatabaseCandidates,
    resources: bookmarks.slice(0, MAX_PROFILE_RESOURCES).map((bookmark) => ({
      title: bookmark.title,
      url: stripUrlForProfile(bookmark.url),
      domain: safeDomain(bookmark.url),
      folderPath: bookmark.folderPath,
    })),
    privacy: {
      source: 'browser_bookmarks',
      note: 'Generated locally by X-Science Link from bookmark titles, folder names, and URLs after user approval.',
    },
  }
  await chrome.storage.local.set({ bookmarksProfile: profile, bookmarksProfileUpdatedAt: profile.updatedAt })
  return profile
}

function flattenBookmarks(nodes, folderPath = []) {
  const output = []
  for (const node of nodes || []) {
    const title = String(node.title || '').trim()
    const nextPath = node.url ? folderPath : [...folderPath, title].filter(Boolean)
    output.push({
      id: node.id,
      title,
      url: node.url || '',
      dateAdded: node.dateAdded || 0,
      folderPath: folderPath.join(' / '),
    })
    if (node.children?.length) {
      output.push(...flattenBookmarks(node.children, nextPath))
    }
  }
  return output
}

function countDomains(bookmarks) {
  const counts = new Map()
  for (const bookmark of bookmarks) {
    const domain = safeDomain(bookmark.url)
    if (!domain) continue
    counts.set(domain, (counts.get(domain) || 0) + 1)
  }
  return [...counts.entries()]
    .map(([domain, count]) => ({ domain, count }))
    .sort((a, b) => b.count - a.count || a.domain.localeCompare(b.domain))
}

function extractTopicTerms(items) {
  const stopWords = new Set([
    'http', 'https', 'www', 'com', 'org', 'net', 'html', 'login', 'home', 'index',
    'the', 'and', 'for', 'with', 'from', 'this', 'that', 'you', 'your', 'into',
    '收藏夹', '书签栏', '其他书签', '未命名', '首页', '登录', '注册',
  ])
  const counts = new Map()
  for (const item of items) {
    const text = `${item.title || ''} ${item.folderPath || ''} ${safeDomain(item.url || '')}`
      .toLowerCase()
      .replace(/[_./:?=&#+-]+/g, ' ')
    for (const term of text.match(/[\p{Script=Han}]{2,}|[a-z][a-z0-9]{2,}/gu) || []) {
      if (stopWords.has(term) || /^\d+$/.test(term)) continue
      counts.set(term, (counts.get(term) || 0) + 1)
    }
  }
  return [...counts.entries()]
    .map(([term, count]) => ({ term, count }))
    .sort((a, b) => b.count - a.count || a.term.localeCompare(b.term))
}

function inferInterests(terms, domains, paidDatabaseCandidates) {
  const haystack = [
    ...terms.slice(0, 80).map((item) => item.term),
    ...domains.slice(0, 50).map((item) => item.domain),
    ...paidDatabaseCandidates.map((item) => item.domain),
  ].join(' ')
  const rules = [
    { label: '生物医药与药物研发', keys: ['pubmed', 'ncbi', 'drug', 'protein', 'compound', 'patent', 'clinical', 'biotech', 'chem', '药物', '蛋白', '化合物', '专利'] },
    { label: '科研文献与数据库检索', keys: ['scholar', 'scopus', 'webofscience', 'reaxys', 'scifinder', 'springer', 'elsevier', 'wiley', '文献', '数据库'] },
    { label: '代码与工程工具', keys: ['github', 'gitlab', 'stackoverflow', 'developer', 'docs', 'api', 'python', 'javascript'] },
    { label: '商业与行业情报', keys: ['market', 'finance', 'news', 'report', 'industry', 'business', '资讯', '报告'] },
  ]
  return rules
    .filter((rule) => rule.keys.some((key) => haystack.includes(key)))
    .map((rule) => rule.label)
}

function findPaidDatabaseCandidates(bookmarks) {
  const patterns = [
    'scifinder', 'reaxys', 'webofscience', 'scopus', 'embase', 'clarivate',
    'dimensions.ai', 'drugbank', 'cortellis', 'integrity', 'patbase',
    'questel', 'derwent', 'springer', 'sciencedirect', 'wiley', 'nature',
    'acs.org', 'rsc.org', 'pubs.acs', 'clinicalkey',
  ]
  const matches = []
  for (const bookmark of bookmarks) {
    const domain = safeDomain(bookmark.url)
    const text = `${domain} ${bookmark.title}`.toLowerCase()
    if (!patterns.some((pattern) => text.includes(pattern))) continue
    matches.push({
      title: bookmark.title || domain,
      domain,
      url: stripUrlForProfile(bookmark.url),
      folderPath: bookmark.folderPath,
    })
  }
  return matches.slice(0, 60)
}

function stripUrlForProfile(value) {
  try {
    const url = new URL(value)
    url.hash = ''
    url.search = ''
    return url.href
  } catch {
    return String(value || '')
  }
}

async function requireLoggedInPageReadApproval(tab, payload = {}) {
  const url = tab?.url || tab?.pendingUrl || ''
  await requireUserApproval({
    title: '读取已登录网页',
    action: 'read_page',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    targetLabel: '网页',
    pathLabel: '网址',
    actionLabel: '操作',
    target: { name: tab?.title || safeDomain(url) || '当前页面' },
    path: url,
    impact: 'X-Science 会读取这个页面当前可见的文字、链接和控件，用于当前任务；不会绕过登录、付费限制或批量下载内容。',
  })
}

function safeDomain(value) {
  try {
    return new URL(value).hostname.replace(/^www\./, '')
  } catch {
    return ''
  }
}

async function getLocalFilesStatus() {
  if (typeof indexedDB === 'undefined') {
    return { supported: false, authorizedFolders: 0, folders: [] }
  }

  try {
    const folders = await getStoredLocalFolders()
    const enriched = []
    for (const folder of folders) {
      let permission = 'unknown'
      try {
        if (folder.handle?.queryPermission) {
          permission = await folder.handle.queryPermission({ mode: 'readwrite' })
        }
      } catch {
        permission = 'unknown'
      }
      enriched.push({
        id: folder.id,
        name: folder.name || folder.handle?.name || 'Folder',
        grantedAt: folder.grantedAt || 0,
        permission,
      })
    }
    return {
      supported: true,
      capabilities: ['manifest', 'searchName', 'searchText', 'info', 'list', 'readText', 'previewCsv', 'writeText', 'copy', 'move', 'rename', 'mkdir', 'deleteDryRun', 'organizeByType', 'operationPlan', 'operationVerification', 'noviceFeedback'],
      authorizedFolders: enriched.filter((folder) => folder.permission === 'granted').length,
      folders: enriched,
    }
  } catch (error) {
    return {
      supported: true,
      authorizedFolders: 0,
      folders: [],
      error: error instanceof Error ? error.message : String(error),
    }
  }
}

async function listLocalFiles(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const basePath = normalizeLocalPath(payload.path || '', { allowEmpty: true })
  const recursive = payload.recursive === true
  const maxEntries = clampNumber(payload.maxEntries, 1, MAX_LOCAL_LIST_ENTRIES, 100)
  const baseHandle = basePath
    ? await getDirectoryByPath(folder.handle, basePath)
    : folder.handle
  const entries = []
  await collectLocalEntries(baseHandle, basePath, entries, { recursive, maxEntries })
  return {
    folder: publicFolderInfo(folder),
    path: basePath,
    recursive,
    entries,
    truncated: entries.length >= maxEntries,
  }
}

async function buildLocalFilesManifest(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const basePath = normalizeLocalPath(payload.path || '', { allowEmpty: true })
  const recursive = payload.recursive !== false
  const maxEntries = clampNumber(payload.maxEntries, 1, MAX_LOCAL_MANIFEST_ENTRIES, 500)
  const baseHandle = basePath
    ? await getDirectoryByPath(folder.handle, basePath)
    : folder.handle
  const entries = []
  await collectLocalEntries(baseHandle, basePath, entries, { recursive, maxEntries })
  const files = entries.filter((entry) => entry.kind === 'file')
  const directories = entries.filter((entry) => entry.kind === 'directory')
  const byCategory = {}
  const byExtension = {}
  let totalBytes = 0
  for (const file of files) {
    const category = categorizeLocalFile(file.name)
    const extension = getFileExtension(file.name) || '(none)'
    const size = Number(file.size) || 0
    totalBytes += size
    byCategory[category] = (byCategory[category] || 0) + 1
    byExtension[extension] = (byExtension[extension] || 0) + 1
  }
  const largest = [...files]
    .sort((a, b) => (b.size || 0) - (a.size || 0))
    .slice(0, 20)
  const recent = [...files]
    .sort((a, b) => (b.lastModified || 0) - (a.lastModified || 0))
    .slice(0, 20)
  const duplicateNameGroups = findDuplicateNameGroups(files)
  return {
    folder: publicFolderInfo(folder),
    path: basePath,
    recursive,
    truncated: entries.length >= maxEntries,
    totals: {
      entries: entries.length,
      files: files.length,
      directories: directories.length,
      bytes: totalBytes,
    },
    byCategory,
    byExtension,
    largest,
    recent,
    duplicateNameGroups,
  }
}

async function searchLocalFiles(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const basePath = normalizeLocalPath(payload.path || '', { allowEmpty: true })
  const query = String(payload.query || '').trim()
  const recursive = payload.recursive !== false
  const includeContent = payload.includeContent === true
  const matchCase = payload.matchCase === true
  const maxEntries = clampNumber(payload.maxEntries, 1, MAX_LOCAL_LIST_ENTRIES, 100)
  const maxBytes = clampNumber(payload.maxBytes, 1, MAX_LOCAL_SEARCH_CONTENT_BYTES, MAX_LOCAL_SEARCH_CONTENT_BYTES)
  const extensions = normalizeExtensionFilter(payload.extensions || payload.fileTypes)
  const baseHandle = basePath
    ? await getDirectoryByPath(folder.handle, basePath)
    : folder.handle
  const entries = []
  await collectLocalEntries(baseHandle, basePath, entries, { recursive, maxEntries: Math.max(maxEntries * 4, maxEntries) })
  const needle = matchCase ? query : query.toLowerCase()
  const results = []

  for (const entry of entries) {
    if (results.length >= maxEntries) break
    if (entry.kind !== 'file') {
      if (!query && extensions.length) continue
      if (matchesLocalQuery(entry, needle, matchCase)) {
        results.push({ ...entry, match: { type: 'name' } })
      }
      continue
    }
    const extension = getFileExtension(entry.name)
    if (extensions.length && !extensions.includes(extension)) continue
    if (matchesLocalQuery(entry, needle, matchCase)) {
      results.push({ ...entry, match: { type: 'name' } })
      continue
    }
    if (!includeContent || !query || !isTextLikeLocalFile(entry)) continue
    const fileHandle = await getFileByPath(folder.handle, entry.path).catch(() => null)
    if (!fileHandle) continue
    const file = await fileHandle.getFile()
    const content = await file.slice(0, maxBytes).text()
    const haystack = matchCase ? content : content.toLowerCase()
    const index = haystack.indexOf(needle)
    if (index === -1) continue
    results.push({
      ...entry,
      match: {
        type: 'content',
        offset: index,
        snippet: content.slice(Math.max(0, index - 120), Math.min(content.length, index + query.length + 220)),
        truncated: file.size > maxBytes,
      },
    })
  }

  return {
    folder: publicFolderInfo(folder),
    path: basePath,
    query,
    recursive,
    includeContent,
    results,
    resultCount: results.length,
    truncated: results.length >= maxEntries,
  }
}

async function getLocalFileInfo(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const path = normalizeLocalPath(payload.path)
  const handle = await getHandleByPath(folder.handle, path)
  if (handle.kind === 'directory') {
    const entries = []
    await collectLocalEntries(handle, path, entries, { recursive: false, maxEntries: 200 })
    return {
      folder: publicFolderInfo(folder),
      path,
      kind: 'directory',
      entriesPreview: entries.slice(0, 50),
      entryCountPreviewed: entries.length,
      truncated: entries.length >= 200,
    }
  }
  const file = await handle.getFile()
  const info = {
    folder: publicFolderInfo(folder),
    path,
    kind: 'file',
    name: file.name,
    type: file.type || guessMimeType(file.name),
    category: categorizeLocalFile(file.name),
    extension: getFileExtension(file.name),
    size: file.size,
    lastModified: file.lastModified,
    textLike: isTextLikeName(file.name, file.type),
    mediaPolicy: 'x_science_llm_no_local_ocr',
  }
  if (isTextLikeName(file.name, file.type)) {
    const maxBytes = clampNumber(payload.maxBytes, 1, MAX_LOCAL_READ_BYTES, 64 * 1024)
    const content = await file.slice(0, maxBytes).text()
    return {
      ...info,
      preview: content.slice(0, 12_000),
      truncated: file.size > maxBytes,
      table: buildTablePreview(path, content) || undefined,
    }
  }
  return {
    ...info,
    preview: null,
    note: 'Binary/media files should be passed to X-Science LLM media/document handling, not locally OCR/transcribed by X-Science Link.',
  }
}

async function readLocalFile(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const path = normalizeLocalPath(payload.path)
  await requireUserApproval({
    title: '读取本地文件',
    action: 'read',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    folder: publicFolderInfo(folder),
    path,
    impact: 'X-Science 会读取这个文件的文本内容，用于当前任务。',
  })
  const fileHandle = await getFileByPath(folder.handle, path)
  const file = await fileHandle.getFile()
  const maxBytes = clampNumber(payload.maxBytes, 1, MAX_LOCAL_READ_BYTES, MAX_LOCAL_READ_BYTES)
  if (!isTextLikeName(file.name, file.type)) {
    return readLocalBinaryForLLM(folder, path, file, {
      includeMediaForLLM: payload.includeMediaForLLM === true,
      maxBytes,
    })
  }
  const blob = file.size > maxBytes ? file.slice(0, maxBytes) : file
  const content = await blob.text()
  const result = {
    folder: publicFolderInfo(folder),
    path,
    name: file.name,
    type: file.type || guessMimeType(file.name),
    size: file.size,
    lastModified: file.lastModified,
    truncated: file.size > maxBytes,
    content,
  }
  const table = buildTablePreview(path, content)
  return table ? { ...result, table } : result
}

async function readLocalBinaryForLLM(folder, path, file, options = {}) {
  const type = file.type || guessMimeType(file.name) || 'application/octet-stream'
  const category = categorizeLocalFile(file.name)
  const canInline = options.includeMediaForLLM === true && file.size <= options.maxBytes
  const mediaResource = {
    kind: 'local_file_media',
    path,
    name: file.name,
    type,
    category,
    size: file.size,
    lastModified: file.lastModified,
    mediaPolicy: 'x_science_llm_no_local_ocr',
    note: 'Binary/media files are returned as LLM media resources; X-Science Link does not OCR or transcribe locally.',
  }
  const llmMedia = canInline
    ? [{
      ...mediaResource,
      dataUrl: await fileToDataUrl(file, type),
    }]
    : []

  return {
    folder: publicFolderInfo(folder),
    path,
    name: file.name,
    type,
    category,
    extension: getFileExtension(file.name),
    size: file.size,
    lastModified: file.lastModified,
    textLike: false,
    content: null,
    preview: null,
    truncated: !canInline && options.includeMediaForLLM === true && file.size > options.maxBytes,
    mediaPolicy: 'x_science_llm_no_local_ocr',
    mediaResources: [mediaResource],
    llmMedia,
    userMessage: canInline
      ? '本地媒体文件已作为 X-Science LLM 输入返回；X-Science Link 没有在本地做 OCR 或转写。'
      : '本地媒体文件已识别为需要 X-Science LLM 处理；文件较大时只返回元数据，避免在浏览器端本地识别。',
    note: 'Binary/media files are returned as LLM media resources; X-Science Link does not OCR or transcribe locally.',
  }
}

async function fileToDataUrl(file, type) {
  const buffer = await file.arrayBuffer()
  const bytes = new Uint8Array(buffer)
  let binary = ''
  const chunkSize = 0x8000
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.slice(offset, offset + chunkSize))
  }
  return `data:${type || 'application/octet-stream'};base64,${btoa(binary)}`
}

async function writeLocalFile(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const path = normalizeLocalPath(payload.path)
  const content = typeof payload.content === 'string' ? payload.content : ''
  const overwrite = payload.overwrite !== false
  await requireUserApproval({
    title: overwrite ? '写入本地文件' : '新建本地文件',
    action: 'write',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    folder: publicFolderInfo(folder),
    path,
    impact: overwrite
      ? 'X-Science 会写入这个文件；如果文件已存在，将覆盖原内容。'
      : 'X-Science 会新建这个文件；如果文件已存在，本次操作会停止。',
  })
  const { directory, fileName } = await getParentDirectory(folder.handle, path, { create: true })
  let existed = true
  try {
    await directory.getFileHandle(fileName)
  } catch {
    existed = false
  }
  if (existed && !overwrite) {
    throw new Error(`File already exists: ${path}`)
  }
  const fileHandle = await directory.getFileHandle(fileName, { create: true })
  const writable = await fileHandle.createWritable()
  await writable.write(content)
  await writable.close()
  const file = await fileHandle.getFile()
  return {
    folder: publicFolderInfo(folder),
    path,
    name: file.name,
    size: file.size,
    existed,
    overwritten: existed,
  }
}

async function copyLocalFileCommand(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const from = normalizeLocalPath(payload.path)
  const to = normalizeLocalPath(payload.targetPath)
  if (from === to) throw new Error('Source and target paths must be different')
  const overwrite = payload.overwrite === true
  const preview = {
    operation: 'copy',
    from,
    to,
    overwrite,
  }
  if (payload.dryRun !== false) {
    return { folder: publicFolderInfo(folder), dryRun: true, preview }
  }
  await requireUserApproval({
    title: '复制本地文件',
    action: 'copy',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    folder: publicFolderInfo(folder),
    path: from,
    targetPath: to,
    impact: overwrite
      ? 'X-Science 会复制这个文件；如果目标已存在，将覆盖目标文件。'
      : 'X-Science 会复制这个文件；如果目标已存在，本次操作会停止。',
    preview,
  })
  await copyLocalFile(folder.handle, from, to, { overwrite })
  return { folder: publicFolderInfo(folder), dryRun: false, copied: true, from, to, overwrite }
}

async function moveLocalFileCommand(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const from = normalizeLocalPath(payload.path)
  const to = normalizeLocalPath(payload.targetPath)
  if (from === to) throw new Error('Source and target paths must be different')
  const overwrite = payload.overwrite === true
  const preview = {
    operation: 'move',
    from,
    to,
    overwrite,
  }
  if (payload.dryRun !== false) {
    return { folder: publicFolderInfo(folder), dryRun: true, preview }
  }
  await requireUserApproval({
    title: '移动或重命名本地文件',
    action: 'move',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    folder: publicFolderInfo(folder),
    path: from,
    targetPath: to,
    impact: overwrite
      ? 'X-Science 会移动或重命名这个文件；如果目标已存在，将覆盖目标文件。'
      : 'X-Science 会移动或重命名这个文件；如果目标已存在，本次操作会停止。',
    preview,
  })
  await copyLocalFile(folder.handle, from, to, { overwrite })
  await removeLocalEntry(folder.handle, from, { recursive: false })
  return { folder: publicFolderInfo(folder), dryRun: false, moved: true, from, to, overwrite }
}

async function deleteLocalFileCommand(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const path = normalizeLocalPath(payload.path)
  const recursive = payload.recursive === true
  const handle = await getHandleByPath(folder.handle, path)
  const preview = {
    operation: 'delete',
    path,
    kind: handle.kind,
    recursive,
  }
  if (payload.dryRun !== false) {
    return { folder: publicFolderInfo(folder), dryRun: true, preview }
  }
  await requireUserApproval({
    title: handle.kind === 'directory' ? '删除本地文件夹' : '删除本地文件',
    action: 'delete',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    folder: publicFolderInfo(folder),
    path,
    impact: handle.kind === 'directory'
      ? `X-Science 会删除这个文件夹${recursive ? '及其中所有内容' : ''}。这个操作不可撤销。`
      : 'X-Science 会删除这个文件。这个操作不可撤销。',
    preview,
  })
  await removeLocalEntry(folder.handle, path, { recursive })
  return { folder: publicFolderInfo(folder), dryRun: false, deleted: true, path, kind: handle.kind, recursive }
}

async function makeLocalDirectory(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const path = normalizeLocalPath(payload.path)
  if (payload.dryRun !== false) {
    return {
      folder: publicFolderInfo(folder),
      dryRun: true,
      preview: { operation: 'mkdir', path },
    }
  }
  await requireUserApproval({
    title: '新建本地文件夹',
    action: 'mkdir',
    approvalContext: payload.approvalContext,
    approvalScope: payload.approvalScope,
    folder: publicFolderInfo(folder),
    path,
    impact: 'X-Science 会在授权文件夹内新建这个文件夹。',
  })
  await getDirectoryByPath(folder.handle, path, { create: true })
  return { folder: publicFolderInfo(folder), dryRun: false, created: true, path }
}

async function organizeLocalFiles(payload = {}) {
  const folder = await getAuthorizedLocalFolder(payload.folderId)
  const basePath = normalizeLocalPath(payload.path || '', { allowEmpty: true })
  const dryRun = payload.dryRun !== false
  const maxEntries = clampNumber(payload.maxEntries, 1, 200, 100)
  const baseHandle = basePath
    ? await getDirectoryByPath(folder.handle, basePath)
    : folder.handle
  const entries = []
  await collectLocalEntries(baseHandle, basePath, entries, { recursive: false, maxEntries })
  const files = entries.filter((entry) => entry.kind === 'file')
  const moves = files
    .map((entry) => {
      const category = categorizeLocalFile(entry.name)
      const from = entry.path
      const to = joinLocalPath(basePath, category, entry.name)
      return from === to ? null : { from, to, category }
    })
    .filter(Boolean)

  if (!dryRun) {
    const existingTargets = []
    for (const move of moves) {
      if (await localFileExists(folder.handle, move.to)) existingTargets.push(move.to)
    }
    if (existingTargets.length) {
      throw new Error(`Target file already exists: ${existingTargets.slice(0, 5).join(', ')}`)
    }
    await requireUserApproval({
      title: '整理本地文件',
      action: 'organize',
      approvalContext: payload.approvalContext,
      approvalScope: payload.approvalScope,
      folder: publicFolderInfo(folder),
      path: basePath || '/',
      impact: `X-Science 会把 ${moves.length} 个文件按类型移动到子文件夹。`,
      preview: moves.slice(0, 8),
    })
    for (const move of moves) {
      await copyLocalFile(folder.handle, move.from, move.to, { overwrite: false })
      await removeLocalFile(folder.handle, move.from)
    }
  }

  return {
    folder: publicFolderInfo(folder),
    path: basePath,
    dryRun,
    mode: 'by_type',
    totalFiles: files.length,
    moves,
    applied: dryRun ? 0 : moves.length,
  }
}

async function requireUserApproval(request) {
  const approvalKey = getConversationApprovalKey(request)
  if (approvalKey && await hasConversationApproval(approvalKey)) {
    return true
  }

  const requestId = `approval_${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`
  const payload = {
    ...request,
    approvalKey,
    approvalMode: approvalKey ? 'conversation' : 'operation',
    requestId,
    createdAt: Date.now(),
  }
  const url = chrome.runtime.getURL(`approval.html?requestId=${encodeURIComponent(requestId)}`)
  return new Promise((resolve, reject) => {
    const timeout = setTimeout(() => {
      pendingApprovals.delete(requestId)
      reject(new Error('User approval timed out'))
    }, 120_000)
    pendingApprovals.set(requestId, {
      request: payload,
      resolve: (approved) => {
        clearTimeout(timeout)
        if (approved) {
          resolve(true)
        } else {
          reject(new Error('User denied the local file operation'))
        }
      },
    })
    chrome.windows.create({
      url,
      type: 'popup',
      width: 460,
      height: 620,
      focused: true,
    }).catch((error) => {
      clearTimeout(timeout)
      pendingApprovals.delete(requestId)
      reject(error)
    })
  })
}

function getConversationApprovalKey(request) {
  const context = sanitizeApprovalPart(request.approvalContext)
  if (!context) return ''
  const scope = sanitizeApprovalPart(request.approvalScope) || 'x_science_link_sensitive'
  return `${context}:${scope}`
}

function sanitizeApprovalPart(value) {
  if (typeof value !== 'string') return ''
  const text = value.trim()
  return /^[a-zA-Z0-9_-]{6,160}$/.test(text) ? text : ''
}

async function hasConversationApproval(key) {
  const stored = await chrome.storage.local.get([CONVERSATION_APPROVALS_KEY])
  const approvals = stored[CONVERSATION_APPROVALS_KEY]
  return Boolean(approvals && typeof approvals === 'object' && approvals[key]?.approvedAt)
}

async function rememberConversationApproval(key, request) {
  const stored = await chrome.storage.local.get([CONVERSATION_APPROVALS_KEY])
  const approvals = stored[CONVERSATION_APPROVALS_KEY] && typeof stored[CONVERSATION_APPROVALS_KEY] === 'object'
    ? stored[CONVERSATION_APPROVALS_KEY]
    : {}
  approvals[key] = {
    approvedAt: Date.now(),
    title: request.title || '',
    scope: request.approvalScope || '',
  }
  const entries = Object.entries(approvals)
    .sort(([, a], [, b]) => (b?.approvedAt || 0) - (a?.approvedAt || 0))
    .slice(0, MAX_CONVERSATION_APPROVALS)
  await chrome.storage.local.set({ [CONVERSATION_APPROVALS_KEY]: Object.fromEntries(entries) })
}

async function getAuthorizedLocalFolder(folderId) {
  const folders = await getStoredLocalFolders()
  const folder = typeof folderId === 'string' && folderId.trim()
    ? folders.find((candidate) => candidate.id === folderId.trim())
    : folders[0]
  if (!folder?.handle) {
    throw new Error('No authorized local folder. Open X-Science Link and authorize a folder first.')
  }
  const permission = folder.handle.queryPermission
    ? await folder.handle.queryPermission({ mode: 'readwrite' })
    : 'unknown'
  if (permission !== 'granted') {
    throw new Error(`Folder permission is ${permission}. Open X-Science Link and authorize the folder again.`)
  }
  return folder
}

function publicFolderInfo(folder) {
  return {
    id: folder.id,
    name: folder.name || folder.handle?.name || 'Folder',
    grantedAt: folder.grantedAt || 0,
  }
}

async function collectLocalEntries(directoryHandle, basePath, entries, options) {
  for await (const [name, handle] of directoryHandle.entries()) {
    if (entries.length >= options.maxEntries) return
    const path = joinLocalPath(basePath, name)
    const item = {
      name,
      path,
      kind: handle.kind,
    }
    if (handle.kind === 'file') {
      try {
        const file = await handle.getFile()
        item.size = file.size
        item.type = file.type || guessMimeType(file.name)
        item.lastModified = file.lastModified
      } catch {
        // Metadata is best-effort.
      }
    }
    entries.push(item)
    if (options.recursive && handle.kind === 'directory') {
      await collectLocalEntries(handle, path, entries, options)
      if (entries.length >= options.maxEntries) return
    }
  }
}

async function getDirectoryByPath(rootHandle, path, options = {}) {
  const parts = splitLocalPath(path)
  let current = rootHandle
  for (const part of parts) {
    current = await current.getDirectoryHandle(part, { create: options.create === true })
  }
  return current
}

async function getFileByPath(rootHandle, path) {
  const { directory, fileName } = await getParentDirectory(rootHandle, path)
  return directory.getFileHandle(fileName)
}

async function getHandleByPath(rootHandle, path) {
  const parts = splitLocalPath(path)
  if (!parts.length) return rootHandle
  const name = parts.pop()
  const directory = parts.length ? await getDirectoryByPath(rootHandle, parts.join('/')) : rootHandle
  try {
    return await directory.getFileHandle(name)
  } catch {
    return directory.getDirectoryHandle(name)
  }
}

async function getParentDirectory(rootHandle, path, options = {}) {
  const parts = splitLocalPath(path)
  if (!parts.length) throw new Error('File path is required')
  const fileName = parts.pop()
  let directory = rootHandle
  for (const part of parts) {
    directory = await directory.getDirectoryHandle(part, { create: options.create === true })
  }
  return { directory, fileName }
}

async function copyLocalFile(rootHandle, fromPath, toPath, options = {}) {
  const sourceHandle = await getFileByPath(rootHandle, fromPath)
  const file = await sourceHandle.getFile()
  const { directory, fileName } = await getParentDirectory(rootHandle, toPath, { create: true })
  if (!options.overwrite) {
    let targetExists = true
    try {
      await directory.getFileHandle(fileName)
    } catch {
      targetExists = false
    }
    if (targetExists) {
      throw new Error(`Target already exists: ${toPath}`)
    }
  }
  const targetHandle = await directory.getFileHandle(fileName, { create: true })
  const writable = await targetHandle.createWritable()
  await writable.write(await file.arrayBuffer())
  await writable.close()
}

async function removeLocalFile(rootHandle, path) {
  const { directory, fileName } = await getParentDirectory(rootHandle, path)
  await directory.removeEntry(fileName)
}

async function removeLocalEntry(rootHandle, path, options = {}) {
  const { directory, fileName } = await getParentDirectory(rootHandle, path)
  await directory.removeEntry(fileName, { recursive: options.recursive === true })
}

async function localFileExists(rootHandle, path) {
  try {
    await getFileByPath(rootHandle, path)
    return true
  } catch {
    return false
  }
}

function normalizeLocalPath(value, options = {}) {
  const text = String(value || '').replace(/\\/g, '/').trim()
  if (!text) {
    if (options.allowEmpty) return ''
    throw new Error('Local file path is required')
  }
  if (text.startsWith('/') || /^[a-zA-Z]:/.test(text) || text.includes('\0')) {
    throw new Error('Local file path must stay inside the authorized folder')
  }
  const parts = text.split('/').filter(Boolean)
  if (parts.some((part) => part === '.' || part === '..')) {
    throw new Error('Local file path must not contain dot segments')
  }
  return parts.join('/')
}

function splitLocalPath(path) {
  return normalizeLocalPath(path, { allowEmpty: true }).split('/').filter(Boolean)
}

function joinLocalPath(...parts) {
  return parts
    .filter((part) => typeof part === 'string' && part.length > 0)
    .join('/')
    .replace(/\/+/g, '/')
}

function getFileExtension(name) {
  const text = String(name || '')
  if (!text.includes('.')) return ''
  return text.split('.').pop().toLowerCase()
}

function categorizeLocalFile(name) {
  const ext = getFileExtension(name)
  if (['csv', 'tsv', 'xls', 'xlsx', 'numbers'].includes(ext)) return 'Tables'
  if (['doc', 'docx', 'txt', 'md', 'pdf', 'rtf'].includes(ext)) return 'Documents'
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'heic'].includes(ext)) return 'Images'
  if (['mp4', 'mov', 'avi', 'mkv', 'webm'].includes(ext)) return 'Videos'
  if (['zip', 'rar', '7z', 'tar', 'gz'].includes(ext)) return 'Archives'
  if (['json', 'js', 'ts', 'tsx', 'py', 'html', 'css', 'xml', 'yml', 'yaml'].includes(ext)) return 'Code'
  return 'Other'
}

function normalizeExtensionFilter(value) {
  const raw = Array.isArray(value) ? value : (typeof value === 'string' ? value.split(/[,\s]+/) : [])
  return raw
    .map((item) => String(item || '').trim().toLowerCase().replace(/^\./, ''))
    .filter(Boolean)
}

function matchesLocalQuery(entry, needle, matchCase) {
  if (!needle) return true
  const haystack = [
    entry.name,
    entry.path,
    entry.type,
    categorizeLocalFile(entry.name || ''),
  ].filter(Boolean).join(' ')
  return (matchCase ? haystack : haystack.toLowerCase()).includes(needle)
}

function isTextLikeLocalFile(entry) {
  return isTextLikeName(entry.name, entry.type)
}

function isTextLikeName(name, type = '') {
  const ext = getFileExtension(name)
  if (String(type || '').startsWith('text/')) return true
  if (String(type || '').includes('json') || String(type || '').includes('xml')) return true
  return [
    'txt', 'md', 'markdown', 'csv', 'tsv', 'json', 'jsonl', 'xml', 'html', 'htm',
    'css', 'js', 'jsx', 'ts', 'tsx', 'py', 'r', 'sql', 'yaml', 'yml', 'toml',
    'ini', 'log', 'bib', 'sdf', 'mol', 'pdb', 'fasta', 'fa', 'gb', 'gff',
  ].includes(ext)
}

function findDuplicateNameGroups(files) {
  const groups = new Map()
  for (const file of files) {
    const key = String(file.name || '').toLowerCase()
    if (!key) continue
    if (!groups.has(key)) groups.set(key, [])
    groups.get(key).push(file)
  }
  return [...groups.entries()]
    .filter(([, items]) => items.length > 1)
    .map(([name, items]) => ({ name, count: items.length, files: items.slice(0, 8) }))
    .slice(0, 20)
}

function buildTablePreview(path, content) {
  const lower = path.toLowerCase()
  const delimiter = lower.endsWith('.tsv') ? '\t' : lower.endsWith('.csv') ? ',' : ''
  if (!delimiter) return null
  const rows = content
    .split(/\r?\n/)
    .filter((line) => line.trim().length > 0)
    .slice(0, 21)
    .map((line) => parseDelimitedLine(line, delimiter))
  const headers = rows[0] || []
  return {
    format: delimiter === '\t' ? 'tsv' : 'csv',
    columns: headers.length,
    rowsPreviewed: Math.max(0, rows.length - 1),
    headers,
    rows: rows.slice(1, 21),
  }
}

function parseDelimitedLine(line, delimiter) {
  const cells = []
  let current = ''
  let quoted = false
  for (let index = 0; index < line.length; index += 1) {
    const char = line[index]
    const next = line[index + 1]
    if (char === '"' && quoted && next === '"') {
      current += '"'
      index += 1
    } else if (char === '"') {
      quoted = !quoted
    } else if (char === delimiter && !quoted) {
      cells.push(current)
      current = ''
    } else {
      current += char
    }
  }
  cells.push(current)
  return cells.map((cell) => cell.trim())
}

function guessMimeType(name) {
  const ext = String(name || '').split('.').pop()?.toLowerCase()
  if (ext === 'csv') return 'text/csv'
  if (ext === 'tsv') return 'text/tab-separated-values'
  if (ext === 'json') return 'application/json'
  if (ext === 'md') return 'text/markdown'
  if (ext === 'txt') return 'text/plain'
  if (ext === 'pdf') return 'application/pdf'
  if (ext === 'png') return 'image/png'
  if (ext === 'jpg' || ext === 'jpeg') return 'image/jpeg'
  if (ext === 'webp') return 'image/webp'
  if (ext === 'gif') return 'image/gif'
  if (ext === 'svg') return 'image/svg+xml'
  if (ext === 'mp3') return 'audio/mpeg'
  if (ext === 'wav') return 'audio/wav'
  if (ext === 'mp4') return 'video/mp4'
  if (ext === 'webm') return 'video/webm'
  return ''
}

function extractSearchResultsInTab({ maxResults }) {
  const host = location.hostname.toLowerCase()
  const resultSelectors = [
    '#b_results .b_algo',
    'li.b_algo',
    '[data-testid="result"]',
    '.g',
    'article',
  ]
  const containers = resultSelectors
    .flatMap((selector) => [...document.querySelectorAll(selector)])
    .filter((element, index, all) => all.indexOf(element) === index)

  const candidates = containers.length
    ? containers
    : [...document.querySelectorAll('a[href]')]
      .map((link) => link.closest('li, article, div') || link)
      .filter((element, index, all) => all.indexOf(element) === index)

  const results = []
  for (const container of candidates) {
    if (results.length >= maxResults) break
    const link = container.matches?.('a[href]')
      ? container
      : container.querySelector?.('a[href]')
    if (!link?.href) continue
    const href = normalizeResultUrl(link.href)
    if (!href || !/^https?:\/\//i.test(href)) continue
    const title = cleanResultText(
      link.innerText
      || link.getAttribute('aria-label')
      || container.querySelector?.('h1,h2,h3')?.innerText
      || ''
    )
    if (!title || isNavigationResult(title, href)) continue
    const snippet = cleanResultText(
      container.querySelector?.('.b_caption p, .VwiC3b, [data-content-feature="1"], p')?.innerText
      || container.innerText
      || ''
    ).replace(title, '').trim().slice(0, 500)
    const displayUrl = cleanResultText(
      container.querySelector?.('cite, .b_attribution, .TbwUpd')?.innerText
      || new URL(href).hostname
    )
    if (results.some((result) => result.url === href)) continue
    results.push({
      title: title.slice(0, 220),
      url: href,
      displayUrl: displayUrl.slice(0, 180),
      snippet,
    })
  }

  return {
    engine: hostnameMatchesDomain(host, 'bing.com') ? 'bing' : hostnameMatchesDomain(host, 'google.com') ? 'google' : host,
    query: new URLSearchParams(location.search).get('q') || '',
    url: location.href,
    results,
    resultCount: results.length,
  }

  // Keep all helpers inside the serialized extractor. Browser page execution
  // cannot access the extension worker's top-level declarations.
  function hostnameMatchesDomain(hostname, domain) {
    const host = String(hostname || '').toLowerCase().replace(/\.$/, '')
    return host === domain || host.endsWith(`.${domain}`)
  }

  function normalizeResultUrl(href) {
    try {
      const url = new URL(href)
      if (hostnameMatchesDomain(url.hostname, 'bing.com') && url.pathname === '/ck/a') {
        const target = url.searchParams.get('u')
        if (target) {
          const decoded = decodeBingRedirectTarget(target, url.origin)
          if (decoded) return decoded
        }
      }
      return url.href
    } catch {
      return ''
    }
  }

  function normalizeDecodedResultTarget(value, origin = location.origin) {
    try {
      const relative = value.startsWith('/')
      if (!relative && !/^https?:\/\//i.test(value)) return ''
      const url = relative ? new URL(value, origin) : new URL(value)
      if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''
      // URL parsing normalizes protocol-relative and backslash paths. Only a
      // genuinely same-origin path may use the relative-target branch.
      if (relative && url.origin !== origin) return ''
      return url.href
    } catch {
      return ''
    }
  }

  function decodeBingRedirectTarget(target, origin = location.origin) {
    const raw = String(target || '')
    const withoutPrefix = raw.replace(/^a1/i, '')
    let base64 = withoutPrefix.replace(/-/g, '+').replace(/_/g, '/')
    const padding = base64.length % 4
    if (padding) base64 += '='.repeat(4 - padding)

    try {
      const decoded = normalizeDecodedResultTarget(atob(base64), origin)
      if (decoded) return decoded
    } catch {
      // Some Bing links are not encoded target URLs; use the fallback below.
    }

    try {
      return normalizeDecodedResultTarget(decodeURIComponent(raw), origin)
    } catch {
      return ''
    }
  }

  function cleanResultText(value) {
    return String(value || '').replace(/\s+/g, ' ').trim()
  }

  function isNavigationResult(title, href) {
    const lower = title.toLowerCase()
    if (lower === 'cached' || lower === 'translate this page') return true
    try {
      const url = new URL(href)
      return url.protocol !== 'http:' && url.protocol !== 'https:'
    } catch {
      return true
    }
  }
}

function clampNumber(value, min, max, fallback) {
  const number = Number(value)
  if (!Number.isFinite(number)) return fallback
  return Math.max(min, Math.min(max, Math.floor(number)))
}

function waitForDownloadComplete(downloadId, timeoutMs = 120_000) {
  return new Promise((resolve, reject) => {
    const startedAt = Date.now()
    const finish = async () => {
      chrome.downloads.onChanged.removeListener(listener)
      clearTimeout(timeout)
      const [item] = await chrome.downloads.search({ id: downloadId }).catch(() => [])
      resolve(item || null)
    }
    const timeout = setTimeout(() => {
      chrome.downloads.onChanged.removeListener(listener)
      reject(new Error(`Download timed out after ${Date.now() - startedAt}ms`))
    }, timeoutMs)
    const listener = (delta) => {
      if (delta.id !== downloadId) return
      if (delta.error?.current) {
        chrome.downloads.onChanged.removeListener(listener)
        clearTimeout(timeout)
        reject(new Error(`Download failed: ${delta.error.current}`))
        return
      }
      if (delta.state?.current === 'complete') {
        void finish()
      }
    }
    chrome.downloads.onChanged.addListener(listener)
    chrome.downloads.search({ id: downloadId }).then(([item]) => {
      if (item?.state === 'complete') void finish()
      if (item?.state === 'interrupted') {
        chrome.downloads.onChanged.removeListener(listener)
        clearTimeout(timeout)
        reject(new Error(`Download interrupted: ${item.error || 'unknown error'}`))
      }
    }).catch(() => null)
  })
}

function verifyDownloadItem(item, context = {}) {
  const requestedFilename = sanitizeDownloadFilename(context.requestedFilename || inferFilenameFromUrl(context.url || 'download'))
  const filename = item?.filename || `Downloads/X-Science/${requestedFilename}`
  const state = item?.state || (context.error ? 'interrupted' : 'complete')
  const bytesReceived = Number(item?.bytesReceived || 0)
  const totalBytes = Number(item?.totalBytes || 0)
  const danger = String(item?.danger || '')
  const exists = item?.exists !== false
  const savedLocationLabel = getDownloadSavedLocationLabel(filename, requestedFilename)
  const safeDanger = !danger || ['safe', 'accepted', 'allowlisted'].includes(danger)
  const checks = [
    {
      id: 'state_complete',
      label: '下载已经完成',
      ok: state === 'complete',
      detail: state,
    },
    {
      id: 'file_exists',
      label: '文件还在下载目录中',
      ok: exists,
      detail: savedLocationLabel,
    },
    {
      id: 'filename_ready',
      label: '文件名可识别',
      ok: Boolean(filename),
      detail: basenameFromPath(filename),
    },
    {
      id: 'bytes_ready',
      label: '文件大小看起来正常',
      ok: totalBytes > 0 ? bytesReceived >= totalBytes : bytesReceived >= 0,
      detail: totalBytes > 0 ? `${bytesReceived}/${totalBytes} bytes` : `${bytesReceived} bytes`,
    },
    {
      id: 'browser_safety',
      label: '浏览器没有标记危险',
      ok: safeDanger,
      detail: danger || 'safe',
    },
  ]
  if (context.error) {
    checks.unshift({
      id: 'download_event',
      label: '浏览器下载事件正常返回',
      ok: false,
      detail: compactError(context.error),
    })
  }
  const ok = checks.every((check) => check.ok)
  const recoverySuggestions = ok ? [] : buildDownloadRecoverySuggestions(item, context)
  const userMessage = ok
    ? `下载完成，文件已保存到：${savedLocationLabel}`
    : `下载还没有确认完成。请按提示检查：${recoverySuggestions[0] || '打开浏览器下载列表查看失败原因。'}`
  return {
    ok,
    state,
    filename,
    savedLocationLabel,
    bytesReceived,
    totalBytes,
    danger,
    exists,
    checks,
    userMessage,
    recoverySuggestions,
  }
}

function buildDownloadRecoverySuggestions(item, context = {}) {
  const suggestions = []
  if (context.error) suggestions.push(`重试下载：${compactError(context.error)}`)
  if (item?.state === 'interrupted') suggestions.push(`浏览器中断了下载：${item.error || '未知原因'}，可以重新点击下载或换一个下载链接。`)
  if (item?.exists === false) suggestions.push('文件可能被移动或删除了，请打开浏览器下载列表确认保存位置。')
  if (item?.danger && !['safe', 'accepted', 'allowlisted'].includes(item.danger)) suggestions.push(`浏览器安全检查标记为 ${item.danger}，需要用户在下载列表中确认是否保留。`)
  if (!suggestions.length) suggestions.push('重新执行 download_file；如果页面需要点击后才生成文件，先用 visual_element_map 找到下载按钮再点击。')
  suggestions.push('打开 Chrome/Edge 下载列表，确认文件是否在“X-Science”文件夹里。')
  return [...new Set(suggestions)].slice(0, 4)
}

function getDownloadSavedLocationLabel(filename, requestedFilename) {
  const basename = basenameFromPath(filename) || requestedFilename || 'download'
  const normalized = String(filename || '').replace(/\\/g, '/')
  if (normalized.includes('/X-Science/')) return `浏览器下载目录 / X-Science / ${basename}`
  if (normalized) return `浏览器下载目录 / ${basename}`
  return `浏览器下载目录 / X-Science / ${basename}`
}

function basenameFromPath(value) {
  return String(value || '')
    .replace(/\\/g, '/')
    .split('/')
    .filter(Boolean)
    .pop() || ''
}

function inferFilenameFromUrl(value) {
  try {
    const url = new URL(value)
    const pathname = decodeURIComponent(url.pathname || '')
    const name = pathname.split('/').filter(Boolean).pop()
    return name || 'download'
  } catch {
    return 'download'
  }
}

function sanitizeDownloadFilename(value) {
  const text = String(value || 'download')
    .replace(/\\/g, '/')
    .split('/')
    .filter(Boolean)
    .pop()
    ?.replace(/[<>:"|?*\x00-\x1F]/g, '_')
    .replace(/\s+/g, ' ')
    .trim()
  return (text || 'download').slice(0, 180)
}

async function getStoredLocalFolders() {
  const db = await openLocalFilesDb()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(LOCAL_FILES_STORE_NAME, 'readonly')
    const request = tx.objectStore(LOCAL_FILES_STORE_NAME).getAll()
    request.onsuccess = () => resolve(request.result || [])
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
}

async function clearStoredLocalFolders() {
  const db = await openLocalFilesDb()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(LOCAL_FILES_STORE_NAME, 'readwrite')
    const request = tx.objectStore(LOCAL_FILES_STORE_NAME).clear()
    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
}

async function clearLocalFilesAuthorization() {
  await clearStoredLocalFolders()
  const localFiles = await getLocalFilesStatus()
  setStatus({ localFiles })
  return {
    cleared: true,
    localFiles,
    userMessage: '已清空 X-Science Link 保存的本地文件夹授权；下次处理本地文件时会重新弹窗选择文件夹。',
  }
}

function openLocalFilesDb() {
  return openLocalFilesDatabase()
}

function normalizeServerUrl(url) {
  return String(url || DEFAULT_SERVER_URL).trim().replace(/\/+$/, '')
}

function toWsUrl(serverUrl, appSession, clientId) {
  const base = normalizeServerUrl(serverUrl).replace(/^http:/, 'ws:').replace(/^https:/, 'wss:')
  const query = new URLSearchParams({ appSession, clientId })
  return `${base}/x-science-link/ws?${query.toString()}`
}

function createClientId() {
  const bytes = crypto.getRandomValues(new Uint8Array(12))
  return `sl_${[...bytes].map((byte) => byte.toString(16).padStart(2, '0')).join('')}`
}

function requireUrl(value) {
  if (typeof value !== 'string' || !/^https?:\/\//i.test(value)) {
    throw new Error('A valid http(s) url is required')
  }
  return value
}

function tabInfo(tab) {
  return {
    tabId: tab.id,
    id: tab.id,
    windowId: tab.windowId,
    title: tab.title,
    url: tab.url,
    active: tab.active,
    status: tab.status,
  }
}

connectFromStorage()

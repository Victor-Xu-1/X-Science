import { DEFAULT_X_SCIENCE_SERVER_URL } from './link-runtime.js'
const DEFAULT_X_SCIENCE_APP_URL = DEFAULT_X_SCIENCE_SERVER_URL

const stateEl = document.querySelector('#state')
const userEl = document.querySelector('#user')
const versionEl = document.querySelector('#version')
const localFilesEl = document.querySelector('#localFiles')
const bookmarksEl = document.querySelector('#bookmarks')
const openXScienceButton = document.querySelector('#openXScience')
const connectButton = document.querySelector('#connect')
const workspaceButton = document.querySelector('#workspace')
const personalizationButton = document.querySelector('#personalization')
const doctorButton = document.querySelector('#doctor')
const disconnectButton = document.querySelector('#disconnect')
const optionsButton = document.querySelector('#options')
const doctorPanel = document.querySelector('#doctorPanel')
const doctorStateEl = document.querySelector('#doctorState')
const doctorProtocolEl = document.querySelector('#doctorProtocol')
const doctorPolicyEl = document.querySelector('#doctorPolicy')
const doctorActionsEl = document.querySelector('#doctorActions')
const doctorWarningsEl = document.querySelector('#doctorWarnings')
let lastStatus = null

openXScienceButton.addEventListener('click', async () => {
  await openOrFocusXScienceApp()
  window.close()
})

connectButton.addEventListener('click', async () => {
  setBusy(true)
  try {
    const result = await chrome.runtime.sendMessage({ type: 'connect' })
    renderStatus(result?.status)
  } finally {
    setBusy(false)
  }
})

workspaceButton.addEventListener('click', async () => {
  setBusy(true)
  try {
    const result = await chrome.runtime.sendMessage({ type: 'open_local_files' })
    renderStatus(result?.status)
    window.close()
  } finally {
    setBusy(false)
  }
})

personalizationButton.addEventListener('click', async () => {
  setBusy(true)
  try {
    const result = await chrome.runtime.sendMessage({ type: 'open_personalization', reason: 'popup' })
    renderStatus(result?.status)
    window.close()
  } finally {
    setBusy(false)
  }
})

doctorButton.addEventListener('click', async () => {
  setBusy(true)
  try {
    const result = await chrome.runtime.sendMessage({ type: 'doctor' })
    renderDoctorPanel(result?.doctor)
  } finally {
    setBusy(false)
  }
})

disconnectButton.addEventListener('click', async () => {
  setBusy(true)
  try {
    const result = await chrome.runtime.sendMessage({ type: 'disconnect' })
    renderStatus(result?.status)
  } finally {
    setBusy(false)
  }
})

optionsButton.addEventListener('click', () => {
  chrome.runtime.openOptionsPage()
  window.close()
})

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === 'status_changed') renderStatus(message.status)
})

const result = await chrome.runtime.sendMessage({ type: 'status' })
renderStatus(result?.status, result?.version)

function renderStatus(status, explicitVersion) {
  if (status) lastStatus = status
  versionEl.textContent = explicitVersion || chrome.runtime.getManifest().version || '-'
  userEl.textContent = status?.user?.username || '-'
  const count = status?.localFiles?.authorizedFolders ?? 0
  localFilesEl.textContent = `${count} 个文件夹`
  bookmarksEl.textContent = status?.bookmarks?.granted
    ? `${status.bookmarks.bookmarkCount || 0} 条书签`
    : '未授权'

  if (status?.connected) {
    stateEl.textContent = '已连接。X-Science 可以按需调用当前浏览器。'
    stateEl.className = 'state success-state'
    disconnectButton.disabled = false
    return
  }

  disconnectButton.disabled = true
  if (status?.connecting) {
    stateEl.textContent = '正在连接 X-Science...'
    stateEl.className = 'state'
    return
  }

  if (status?.lastError === 'Login required') {
    stateEl.textContent = '未连接。请先打开 X-Science 并登录，然后在页面点击连接。'
  } else {
    stateEl.textContent = status?.lastError
      ? `未连接：${status.lastError}`
      : '未连接。打开 X-Science 后点击连接即可。'
  }
  stateEl.className = 'state'
}

function renderDoctorPanel(doctor) {
  doctorPanel.hidden = false
  const runtime = doctor?.client?.runtime ?? {}
  doctorStateEl.textContent = doctor?.status || '-'
  doctorProtocolEl.textContent = doctor?.server?.protocolVersion || runtime.protocolVersion || '-'
  doctorPolicyEl.textContent = doctor?.server?.policyVersion || runtime.policyVersion || '-'
  doctorActionsEl.textContent = Array.isArray(doctor?.client?.supportedActions)
    ? `${doctor.client.supportedActions.length} 个 action`
    : '-'
  doctorWarningsEl.textContent = Array.isArray(doctor?.warnings) && doctor.warnings.length
    ? doctor.warnings.join(' / ')
    : '0'
}

function setBusy(isBusy) {
  connectButton.disabled = isBusy
  workspaceButton.disabled = isBusy
  personalizationButton.disabled = isBusy
  doctorButton.disabled = isBusy
  if (isBusy) disconnectButton.disabled = true
}

async function getXScienceAppUrl() {
  const statusUrl = normalizeXScienceAppUrl(lastStatus?.serverUrl)
  if (statusUrl) return statusUrl

  try {
    const result = await chrome.runtime.sendMessage({ type: 'status' })
    if (result?.status) lastStatus = result.status
    const runtimeUrl = normalizeXScienceAppUrl(result?.status?.serverUrl)
    if (runtimeUrl) return runtimeUrl
  } catch {
    // The popup should still open the public X-Science entry even if the status call is unavailable.
  }

  return DEFAULT_X_SCIENCE_APP_URL
}

function normalizeXScienceAppUrl(value) {
  if (typeof value !== 'string' || !value.trim()) return ''

  try {
    const url = new URL(value.trim())
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return ''
    if (!url.pathname) url.pathname = '/'
    return url.href
  } catch {
    return ''
  }
}

async function openOrFocusXScienceApp() {
  const url = await getXScienceAppUrl()
  const origin = new URL(url).origin
  const tabs = await chrome.tabs.query({})
  const existingTab = tabs.find((tab) => {
    const candidate = tab.url || tab.pendingUrl || ''
    return candidate === url || candidate.startsWith(`${origin}/`)
  })

  if (existingTab?.id) {
    await chrome.tabs.update(existingTab.id, { active: true })
    if (typeof existingTab.windowId === 'number') {
      await chrome.windows.update(existingTab.windowId, { focused: true })
    }
    return existingTab
  }

  return chrome.tabs.create({ url, active: true })
}

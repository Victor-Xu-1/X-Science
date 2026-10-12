const DEFAULT_PUBLIC_SERVER_URL = 'http://115.190.116.251:3456'

const serverUrlInput = document.querySelector('#serverUrl')
const usernameInput = document.querySelector('#username')
const passwordInput = document.querySelector('#password')
const statusEl = document.querySelector('#status')
const loginConnectButton = document.querySelector('#loginConnect')
const disconnectButton = document.querySelector('#disconnect')

init()

async function init() {
  const stored = await chrome.storage.local.get(['serverUrl', 'user'])
  serverUrlInput.value = stored.serverUrl || DEFAULT_PUBLIC_SERVER_URL
  usernameInput.value = stored.user?.username || ''
  await refreshStatus()
}

loginConnectButton.addEventListener('click', async () => {
  setStatus('正在登录并连接...')
  const serverUrl = serverUrlInput.value.trim()
  const username = usernameInput.value.trim()
  const password = passwordInput.value

  try {
    const login = await chrome.runtime.sendMessage({
      type: 'login',
      serverUrl,
      username,
      password,
    })
    if (!login?.ok) {
      setStatus(`登录失败：${login?.error || '未知错误'}`, true)
      return
    }

    const connected = await chrome.runtime.sendMessage({
      type: 'connect',
      serverUrl,
    })
    renderStatus(connected?.status)
  } catch (error) {
    setStatus(`登录失败：${error instanceof Error ? error.message : String(error)}`, true)
  }
})

disconnectButton.addEventListener('click', async () => {
  const result = await chrome.runtime.sendMessage({ type: 'disconnect' })
  renderStatus(result?.status)
})

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === 'status_changed') renderStatus(message.status)
})

async function refreshStatus() {
  const result = await chrome.runtime.sendMessage({ type: 'status' })
  renderStatus(result?.status)
}

function renderStatus(status) {
  if (status?.connected) {
    setStatus(`已连接：${status.user?.username || '当前账号'} · ${status.serverUrl || 'X-Science'}`)
    disconnectButton.disabled = false
    return
  }
  disconnectButton.disabled = true
  if (status?.connecting) {
    setStatus(`正在连接：${status.serverUrl || 'X-Science'}`)
    return
  }
  if (status?.lastError === 'Login required') {
    setStatus('未连接。建议回到 X-Science 页面点击“连接”，这里不需要手动填写。')
    return
  }
  setStatus(status?.lastError ? `未连接：${status.lastError}` : '未连接。')
}

function setStatus(text, isError = false) {
  statusEl.textContent = text
  statusEl.className = isError ? 'state error-state' : 'state'
}

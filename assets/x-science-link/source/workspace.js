import { openLocalFilesDatabase } from './local-files-database.js'

const stateEl = document.querySelector('#state')
const connectButton = document.querySelector('#connect')
const optionsButton = document.querySelector('#options')
const localFilesStateEl = document.querySelector('#localFilesState')
const localFilesBadgeEl = document.querySelector('#localFilesBadge')
const chooseFolderButton = document.querySelector('#chooseFolder')
const refreshFoldersButton = document.querySelector('#refreshFolders')
const clearFoldersButton = document.querySelector('#clearFolders')
const folderListEl = document.querySelector('#folderList')
const bookmarksStateEl = document.querySelector('#bookmarksState')
const bookmarksBadgeEl = document.querySelector('#bookmarksBadge')
const authorizeBookmarksButton = document.querySelector('#authorizeBookmarks')
const refreshBookmarksButton = document.querySelector('#refreshBookmarks')

const STORE_NAME = 'folders'

connectButton.addEventListener('click', async () => {
  const result = await chrome.runtime.sendMessage({ type: 'connect' })
  renderStatus(result?.status)
})

optionsButton.addEventListener('click', () => {
  chrome.runtime.openOptionsPage()
})

chooseFolderButton.addEventListener('click', async () => {
  if (!('showDirectoryPicker' in window)) {
    renderLocalFilesError('当前浏览器不支持在这里选择文件夹。请使用新版 Chrome 或 Edge。')
    return
  }

  chooseFolderButton.disabled = true
  try {
    const handle = await window.showDirectoryPicker({ mode: 'readwrite' })
    let permission = 'granted'
    if (handle.requestPermission) {
      permission = await handle.requestPermission({ mode: 'readwrite' })
    }
    if (permission !== 'granted') {
      renderLocalFilesError('没有获得文件夹权限。请重新选择并允许访问。')
      return
    }
    await saveFolderHandle(handle)
    await renderLocalFiles()
    await notifyLocalFilesChanged()
  } catch (error) {
    if (error?.name !== 'AbortError') {
      renderLocalFilesError(error instanceof Error ? error.message : String(error))
    }
  } finally {
    chooseFolderButton.disabled = false
  }
})

refreshFoldersButton.addEventListener('click', async () => {
  await renderLocalFiles()
  await notifyLocalFilesChanged()
})

authorizeBookmarksButton.addEventListener('click', async () => {
  authorizeBookmarksButton.disabled = true
  try {
    const response = await chrome.runtime.sendMessage({ type: 'open_personalization', reason: 'workspace' })
    renderBookmarks(response?.status?.bookmarks)
  } finally {
    authorizeBookmarksButton.disabled = false
  }
})

refreshBookmarksButton.addEventListener('click', async () => {
  refreshBookmarksButton.disabled = true
  try {
    const response = await chrome.runtime.sendMessage({ type: 'refresh_bookmarks_profile' })
    renderBookmarks(response?.status?.bookmarks)
  } catch (error) {
    bookmarksStateEl.textContent = error instanceof Error ? error.message : String(error)
  } finally {
    refreshBookmarksButton.disabled = false
  }
})

clearFoldersButton.addEventListener('click', async () => {
  if (!confirm('清空 X-Science Link 中保存的所有本地文件夹授权？')) return
  const db = await openDb()
  await new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite')
    const request = tx.objectStore(STORE_NAME).clear()
    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
  await renderLocalFiles()
  await notifyLocalFilesChanged()
})

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === 'status_changed') renderStatus(message.status)
})

const result = await chrome.runtime.sendMessage({ type: 'status' })
renderStatus(result?.status)
await renderLocalFiles()
await renderBookmarksStatus()

function renderStatus(status) {
  if (status?.bookmarks) renderBookmarks(status.bookmarks)
  if (status?.connected) {
    stateEl.textContent = `已连接：${status.user?.username || '当前账号'}`
    connectButton.textContent = '重新连接'
    return
  }

  if (status?.connecting) {
    stateEl.textContent = '正在连接 X-Science...'
    connectButton.textContent = '连接中'
    return
  }

  stateEl.textContent = status?.lastError
    ? `未连接：${translateStatusError(status.lastError)}`
    : '未连接。请先回到 X-Science 页面登录并点击连接。'
  connectButton.textContent = '连接 / 重新连接'
}

async function renderBookmarksStatus() {
  const response = await chrome.runtime.sendMessage({ type: 'bookmarks_status' })
  renderBookmarks(response?.result || response?.status?.bookmarks)
}

function renderBookmarks(bookmarks) {
  if (!bookmarks?.supported) {
    bookmarksBadgeEl.textContent = '不可用'
    bookmarksStateEl.textContent = '当前浏览器不支持书签授权。'
    authorizeBookmarksButton.disabled = true
    refreshBookmarksButton.disabled = true
    return
  }
  if (!bookmarks.granted) {
    bookmarksBadgeEl.textContent = '未授权'
    bookmarksStateEl.textContent = '首次连接时会自动弹出个性化授权。你也可以在这里手动开启。'
    authorizeBookmarksButton.textContent = '授权书签画像'
    refreshBookmarksButton.disabled = true
    return
  }
  const count = bookmarks.bookmarkCount || 0
  bookmarksBadgeEl.textContent = `${count} 条书签`
  bookmarksStateEl.textContent = count
    ? `已生成个性化画像。X-Science 可据此识别你的常用网站、付费数据库和新闻偏好。`
    : '已获得书签权限，但还没有生成画像。点击刷新画像。'
  authorizeBookmarksButton.textContent = '重新授权'
  refreshBookmarksButton.disabled = false
}

async function renderLocalFiles() {
  if (!('showDirectoryPicker' in window)) {
    renderLocalFilesError('当前浏览器不支持文件夹授权。请使用新版 Chrome 或 Edge。')
    chooseFolderButton.disabled = true
    return
  }

  const folders = await getFolders()
  const rows = []
  for (const folder of folders) {
    let permission = 'unknown'
    try {
      permission = folder.handle?.queryPermission
        ? await folder.handle.queryPermission({ mode: 'readwrite' })
        : 'unknown'
    } catch {
      permission = 'unknown'
    }
    rows.push({ ...folder, permission })
  }

  localFilesBadgeEl.textContent = `${rows.filter((folder) => folder.permission === 'granted').length} 个文件夹`
  localFilesStateEl.textContent = rows.length
    ? '这些授权只保存在你的浏览器本地。X-Science 真正操作文件前还会再次请求确认。'
    : '还没有授权文件夹。需要处理本地文件时，请先选择一个文件夹。'
  folderListEl.innerHTML = ''

  for (const folder of rows) {
    const item = document.createElement('div')
    item.className = 'folder-item'

    const text = document.createElement('div')
    text.className = 'folder-text'
    const name = document.createElement('strong')
    name.textContent = folder.name || folder.handle?.name || '文件夹'
    const meta = document.createElement('span')
    meta.textContent = `${formatPermission(folder.permission)} · ${new Date(folder.grantedAt || Date.now()).toLocaleString()}`
    text.append(name, meta)

    const removeButton = document.createElement('button')
    removeButton.className = 'secondary small'
    removeButton.textContent = '移除'
    removeButton.addEventListener('click', async () => {
      await removeFolder(folder.id)
      await renderLocalFiles()
      await notifyLocalFilesChanged()
    })

    item.append(text, removeButton)
    folderListEl.append(item)
  }
}

function renderLocalFilesError(message) {
  localFilesStateEl.textContent = message
  localFilesBadgeEl.textContent = '不可用'
}

function translateStatusError(message) {
  if (message === 'Login required') return '请先在 X-Science 页面登录并连接'
  return message
}

function formatPermission(permission) {
  if (permission === 'granted') return '已授权'
  if (permission === 'prompt') return '需要重新确认'
  if (permission === 'denied') return '已拒绝'
  return '未知状态'
}

async function saveFolderHandle(handle) {
  const db = await openDb()
  const record = {
    id: `folder_${Date.now().toString(36)}_${Math.random().toString(36).slice(2)}`,
    name: handle.name,
    handle,
    grantedAt: Date.now(),
  }
  await new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite')
    const request = tx.objectStore(STORE_NAME).put(record)
    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
}

async function removeFolder(id) {
  const db = await openDb()
  await new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readwrite')
    const request = tx.objectStore(STORE_NAME).delete(id)
    request.onsuccess = () => resolve()
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
}

async function getFolders() {
  const db = await openDb()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(STORE_NAME, 'readonly')
    const request = tx.objectStore(STORE_NAME).getAll()
    request.onsuccess = () => resolve(request.result || [])
    request.onerror = () => reject(request.error)
    tx.oncomplete = () => db.close()
    tx.onerror = () => {
      db.close()
      reject(tx.error)
    }
  })
}

function openDb() {
  return openLocalFilesDatabase()
}

async function notifyLocalFilesChanged() {
  await chrome.runtime.sendMessage({ type: 'local_files_status' }).catch(() => null)
}

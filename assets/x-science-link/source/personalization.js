const stateEl = document.querySelector('#state')
const allowButton = document.querySelector('#allow')
const denyButton = document.querySelector('#deny')
let completed = false

allowButton.addEventListener('click', async () => {
  if (completed) {
    window.close()
    return
  }
  setBusy(true, '正在请求浏览器书签权限...')
  try {
    if (!chrome.permissions?.request) {
      setBusy(false, '当前浏览器不支持书签授权。请使用新版 Chrome 或 Edge。')
      return
    }
    const granted = await chrome.permissions.request({ permissions: ['bookmarks'] })
    if (!granted) {
      setBusy(false, '未获得书签权限。你可以稍后在 X-Science Link 中重新开启。')
      return
    }
    const response = await chrome.runtime.sendMessage({ type: 'refresh_bookmarks_profile' })
    const result = response?.result
    const count = result?.bookmarkCount || result?.profile?.bookmarkCount || 0
    stateEl.textContent = `已生成个性化画像：读取 ${count} 条书签。你可以关闭此页面。`
    allowButton.textContent = '完成'
    allowButton.disabled = false
    denyButton.hidden = true
    completed = true
  } catch (error) {
    setBusy(false, `授权失败：${error instanceof Error ? error.message : String(error)}`)
  }
})

denyButton.addEventListener('click', async () => {
  await chrome.runtime.sendMessage({ type: 'dismiss_personalization' }).catch(() => null)
  window.close()
})

function setBusy(isBusy, text) {
  allowButton.disabled = isBusy
  denyButton.disabled = isBusy
  stateEl.textContent = text
}

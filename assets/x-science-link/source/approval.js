const params = new URLSearchParams(location.search)
const requestId = params.get('requestId') || ''

const titleEl = document.querySelector('#title')
const impactEl = document.querySelector('#impact')
const targetLabelEl = document.querySelector('#targetLabel')
const pathLabelEl = document.querySelector('#pathLabel')
const actionLabelEl = document.querySelector('#actionLabel')
const targetEl = document.querySelector('#target')
const pathEl = document.querySelector('#path')
const actionEl = document.querySelector('#action')
const previewEl = document.querySelector('#preview')
const approvalNoteEl = document.querySelector('#approvalNote')
const allowButton = document.querySelector('#allow')
const denyButton = document.querySelector('#deny')

const actionText = {
  read: '读取文件',
  write: '写入文件',
  organize: '整理文件',
  read_page: '读取网页',
}

let request = null

init().catch((error) => {
  titleEl.textContent = '请求已失效'
  impactEl.textContent = error instanceof Error ? error.message : String(error)
  allowButton.disabled = true
})

allowButton.addEventListener('click', () => decide(true))
denyButton.addEventListener('click', () => decide(false))

async function init() {
  if (!requestId) throw new Error('没有找到权限请求。')
  const response = await chrome.runtime.sendMessage({ type: 'approval_details', requestId })
  request = response?.request
  if (!request) throw new Error('这个请求已经过期，请回到 X-Science 后重试。')

  titleEl.textContent = request.title || '需要你的确认'
  impactEl.textContent = request.impact || 'X-Science 想进行一次本地操作。'
  targetLabelEl.textContent = request.targetLabel || '文件夹'
  pathLabelEl.textContent = request.pathLabel || '路径'
  actionLabelEl.textContent = request.actionLabel || '操作'
  targetEl.textContent = request.target?.name || request.folder?.name || '已授权文件夹'
  pathEl.textContent = request.path || '/'
  actionEl.textContent = actionText[request.action] || request.action || '本地操作'
  if (request.approvalScope === 'logged_in_sources_read') {
    approvalNoteEl.textContent = '允许后，后续信息流刷新会默认读取这些已登录来源，不再重复弹窗；只读取可见内容，不提交表单或下载文件。'
    allowButton.textContent = '允许并不再提示'
  } else if (request.approvalMode === 'conversation') {
    approvalNoteEl.textContent = '允许后，本次对话里 X-Science Link 的本地文件和已登录网页能力不再重复弹窗；新建会话会重新确认。'
    allowButton.textContent = '允许本次对话'
  } else {
    approvalNoteEl.textContent = '只在你点击允许后，X-Science 才会继续。你可以随时拒绝，本次任务会停止该操作。'
    allowButton.textContent = '允许本次操作'
  }

  if (Array.isArray(request.preview) && request.preview.length) {
    previewEl.hidden = false
    previewEl.innerHTML = ''
    const title = document.createElement('strong')
    title.textContent = '将要处理的示例'
    previewEl.append(title)
    for (const item of request.preview.slice(0, 8)) {
      const row = document.createElement('span')
      row.textContent = `${item.from || ''} -> ${item.to || ''}`
      previewEl.append(row)
    }
  }
}

async function decide(approved) {
  allowButton.disabled = true
  denyButton.disabled = true
  await chrome.runtime.sendMessage({
    type: 'approval_decision',
    requestId,
    approved,
  }).catch(() => null)
  window.close()
}

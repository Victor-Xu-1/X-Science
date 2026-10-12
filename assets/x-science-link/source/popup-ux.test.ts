import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const popup = readFileSync(join(scriptDir, 'popup.js'), 'utf8')

function bodyOf(functionName: string) {
  const markers = [`function ${functionName}(`, `async function ${functionName}(`]
  const start = markers
    .map((marker) => popup.indexOf(marker))
    .filter((index) => index >= 0)
    .sort((a, b) => a - b)[0] ?? -1
  expect(start).toBeGreaterThanOrEqual(0)

  let depth = 0
  let parenDepth = 0
  let bodyStart = -1
  for (let index = start; index < popup.length; index += 1) {
    const char = popup[index]
    if (bodyStart < 0) {
      if (char === '(') {
        parenDepth += 1
        continue
      }
      if (char === ')') {
        parenDepth = Math.max(0, parenDepth - 1)
        continue
      }
      if (char === '{' && parenDepth === 0) {
        depth = 1
        bodyStart = index + 1
      }
      continue
    }
    if (char === '{') {
      depth += 1
    } else if (char === '}') {
      depth -= 1
      if (depth === 0) return popup.slice(bodyStart, index)
    }
  }
  throw new Error(`Could not parse ${functionName}`)
}

describe('X-Science Link popup user experience', () => {
  test('opens or focuses the configured X-Science app instead of spawning duplicate hard-coded tabs', () => {
    expect(popup).not.toContain("const X_SCIENCE_APP_URL = 'http://115.190.116.251/'")
    expect(popup).toContain('const DEFAULT_X_SCIENCE_APP_URL')
    expect(popup).toContain('let lastStatus')

    const openHandler = popup.slice(
      popup.indexOf("openXScienceButton.addEventListener('click'"),
      popup.indexOf("connectButton.addEventListener('click'"),
    )
    expect(openHandler).toContain('openOrFocusXScienceApp')
    expect(openHandler).not.toContain('chrome.tabs.create({ url: X_SCIENCE_APP_URL')

    const getUrlBody = bodyOf('getXScienceAppUrl')
    expect(getUrlBody).toContain('lastStatus?.serverUrl')
    expect(getUrlBody).toContain("chrome.runtime.sendMessage({ type: 'status' })")

    const openBody = bodyOf('openOrFocusXScienceApp')
    expect(openBody).toContain('chrome.tabs.query')
    expect(openBody).toContain('chrome.tabs.update')
    expect(openBody).toContain('chrome.windows.update')
    expect(openBody).toContain('chrome.tabs.create')
  })
})

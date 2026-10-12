import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const background = readFileSync(join(scriptDir, 'background.js'), 'utf8')

function bodyOf(functionName: string) {
  const markers = [`function ${functionName}(`, `async function ${functionName}(`]
  const start = markers
    .map((marker) => background.indexOf(marker))
    .filter((index) => index >= 0)
    .sort((a, b) => a - b)[0] ?? -1
  expect(start).toBeGreaterThanOrEqual(0)
  let depth = 0
  let parenDepth = 0
  let bodyStart = -1
  for (let index = start; index < background.length; index += 1) {
    const char = background[index]
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
      if (bodyStart >= 0 && depth === 0) return background.slice(bodyStart, index)
    }
  }
  throw new Error(`Could not parse ${functionName}`)
}

describe('X-Science Link tab group policy', () => {
  test('declares one X-Science tab group as the hard browser rule', () => {
    expect(background).toContain('const MAX_X_SCIENCE_TAB_GROUPS = 1')
  })

  test('enforces the single X-Science group rule at lifecycle and workspace boundaries', () => {
    const installedListener = background.slice(
      background.indexOf('chrome.runtime.onInstalled.addListener'),
      background.indexOf('chrome.runtime.onStartup.addListener'),
    )
    const startupListener = background.slice(
      background.indexOf('chrome.runtime.onStartup.addListener'),
      background.indexOf('chrome.runtime.onMessage.addListener'),
    )
    const connectBody = bodyOf('connect')
    const ensureWorkspaceBody = bodyOf('ensureWorkspace')

    expect(installedListener).toContain('enforceSingleXScienceGroup')
    expect(startupListener).toContain('enforceSingleXScienceGroup')
    expect(connectBody).toContain('enforceSingleXScienceGroup')
    expect(ensureWorkspaceBody.indexOf('await enforceSingleXScienceGroup')).toBeLessThan(
      ensureWorkspaceBody.indexOf('chrome.tabs.query({})'),
    )
  })

  test('merges duplicate X-Science groups into the primary group before new tabs are grouped', () => {
    const enforceSingleXScienceGroup = bodyOf('enforceSingleXScienceGroup')
    const addToXScienceGroup = bodyOf('addToXScienceGroup')
    const getPrimaryXScienceGroup = bodyOf('getPrimaryXScienceGroup')

    expect(enforceSingleXScienceGroup).toContain('.filter(isXScienceTabGroup)')
    expect(enforceSingleXScienceGroup).toContain('duplicateGroupIds')
    expect(enforceSingleXScienceGroup).toContain('await mergeXScienceGroupIntoPrimary(primary, duplicate)')
    expect(enforceSingleXScienceGroup).toContain('duplicateGroupIds.length + MAX_X_SCIENCE_TAB_GROUPS')
    expect(addToXScienceGroup.indexOf('await enforceSingleXScienceGroup')).toBeLessThan(
      addToXScienceGroup.indexOf('chrome.tabs.group'),
    )
    expect(getPrimaryXScienceGroup).toContain('return (await enforceSingleXScienceGroup())?.primary || null')
  })
})

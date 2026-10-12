import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const background = readFileSync(join(scriptDir, 'background.js'), 'utf8')

function bodyOf(functionName: string) {
  const marker = `function ${functionName}`
  const start = background.indexOf(marker)
  expect(start).toBeGreaterThanOrEqual(0)
  let depth = 0
  let parenDepth = 0
  let bodyStart = -1
  for (let index = start; index < background.length; index += 1) {
    const char = background[index]
    if (bodyStart < 0 && char === '(') {
      parenDepth += 1
      continue
    }
    if (bodyStart < 0 && char === ')') {
      parenDepth = Math.max(0, parenDepth - 1)
      continue
    }
    if (char === '{' && parenDepth === 0) {
      depth += 1
      if (bodyStart < 0) bodyStart = index + 1
    } else if (char === '}') {
      depth -= 1
      if (bodyStart >= 0 && depth === 0) return background.slice(bodyStart, index)
    }
  }
  throw new Error(`Could not parse ${functionName}`)
}

describe('X-Science Link download guards', () => {
  test('filters search and navigation URLs before treating hints as downloadable files', () => {
    const buildCandidates = bodyOf('buildDownloadCandidatesFromPage')
    const likelyDownloadable = bodyOf('isLikelyDownloadableUrl')
    const candidateGuard = bodyOf('isSearchOrNavigationDownloadCandidate')

    expect(buildCandidates).toContain('if (isSearchOrNavigationUrl(url)) return')
    expect(likelyDownloadable.indexOf('if (isSearchOrNavigationUrl(url)) return false')).toBeLessThan(
      likelyDownloadable.indexOf("['pdf', 'ppt', 'pptx'"),
    )
    expect(candidateGuard.indexOf('if (isSearchOrNavigationUrl(candidate.url)) return true')).toBeLessThan(
      candidateGuard.indexOf('if (candidate.extension) return false'),
    )
  })

  test('does not fall back to Chrome downloads when a fetched URL is just an HTML page', () => {
    const downloadFile = bodyOf('downloadFile')
    const browserDownload = bodyOf('downloadFileWithBrowserDownloadManager')

    expect(downloadFile).toContain('if (isNonDownloadResponseError(error)) throw error')
    expect(browserDownload).toContain('if (isSearchOrNavigationUrl(url))')
    expect(browserDownload).toContain('Refusing to download a search or navigation page')
  })

  test('uses the Chrome download manager by default instead of background blob transfer', () => {
    const shouldUseBrowserDownloadManager = bodyOf('shouldUseBrowserDownloadManager')

    expect(shouldUseBrowserDownloadManager).toContain('payload.backgroundTransfer !== true')
    expect(shouldUseBrowserDownloadManager).toContain("payload.delivery !== 'background_transfer'")
    expect(shouldUseBrowserDownloadManager).toContain('payload.backgroundOnly !== true')
  })

  test('rejects private-network fetch targets before background transfer', () => {
    const requireUrlForFetch = bodyOf('requireUrlForFetch')
    const assertPublicFetchHostname = bodyOf('assertPublicFetchHostname')

    expect(requireUrlForFetch).toContain('assertPublicFetchHostname(parsed.hostname)')
    expect(assertPublicFetchHostname).toContain('Refusing to fetch a local or private URL')
    expect(assertPublicFetchHostname).toContain('localhost')
  })

  test('bounds human-read timers before scheduling setTimeout in the injected page script', () => {
    const readPageLikeHumanInTab = bodyOf('readPageLikeHumanInTab')
    const waitForImagesToSettle = bodyOf('waitForImagesToSettle')

    expect(readPageLikeHumanInTab).toMatch(/const wait = \(ms\) => \{[\s\S]*Math\.min\(60_000/)
    expect(waitForImagesToSettle).toContain('boundedTimeoutMs')
    expect(waitForImagesToSettle).toContain('Math.min(60_000')
  })
})

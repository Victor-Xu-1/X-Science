import { describe, expect, test } from 'bun:test'
import { readFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const background = readFileSync(join(scriptDir, 'background.js'), 'utf8')
const fetchPolicy = readFileSync(join(scriptDir, 'public-fetch-sanitizer.js'), 'utf8')

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
    const downloadFileInBackground = bodyOf('downloadFileInBackground')

    expect(downloadFileInBackground).toContain('sanitizeBackgroundDownloadRequest(url)')
    expect(downloadFileInBackground).toContain('fetchBackgroundDownloadResponse(sanitizedRequest)')
    expect(fetchPolicy).toContain('assertPublicFetchHostname')
    expect(fetchPolicy).toContain('Refusing to fetch a local or private URL')
  })

  test('uses fixed human-read delays instead of user-controlled timer durations', () => {
    const readPageLikeHumanInTab = bodyOf('readPageLikeHumanInTab')
    const waitForImagesToSettle = bodyOf('waitForImagesToSettle')

    expect(readPageLikeHumanInTab).not.toMatch(/const wait = \(ms\)/)
    expect(readPageLikeHumanInTab).toContain('setTimeout(resolve, 250)')
    expect(waitForImagesToSettle).toContain('timeoutMs === 180 ? 180 : timeoutMs === 250 ? 250 : 350')
  })
})

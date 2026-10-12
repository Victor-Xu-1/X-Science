import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import vm from 'node:vm'

const background = readFileSync(new URL('./background.js', import.meta.url), 'utf8')

// Evaluate actual production declarations, including declarations local to a
// serialized page function; the fixtures do not reimplement URL policy.
function functionSources(name) {
  const marker = `function ${name}(`
  const sources = []
  for (let start = background.indexOf(marker); start >= 0; start = background.indexOf(marker, start + marker.length)) {
    let parentheses = 0
    let braces = 0
    let body = false
    for (let index = start; index < background.length; index += 1) {
      const character = background[index]
      if (!body) {
        if (character === '(') parentheses += 1
        else if (character === ')') parentheses -= 1
        else if (character === '{' && parentheses === 0) {
          body = true
          braces = 1
        }
      } else if (character === '{') braces += 1
      else if (character === '}' && --braces === 0) {
        sources.push(background.slice(start, index + 1))
        break
      }
    }
  }
  assert.ok(sources.length, `missing production function ${name}`)
  return sources
}

const location = { origin: 'https://www.bing.com', hostname: 'www.bing.com', href: 'https://www.bing.com/search?q=science', search: '?q=science' }
function productionFunction(name, additions = {}) {
  return new vm.Script(`(${functionSources(name)[0]})`).runInNewContext({ URL, URLSearchParams, atob, location, ...additions })
}

function searchFunctions() {
  const hostnameMatchesDomain = productionFunction('hostnameMatchesDomain')
  const normalizeDecodedResultTarget = productionFunction('normalizeDecodedResultTarget')
  const decodeBingRedirectTarget = productionFunction('decodeBingRedirectTarget', { normalizeDecodedResultTarget })
  const normalizeResultUrl = productionFunction('normalizeResultUrl', { hostnameMatchesDomain, decodeBingRedirectTarget })
  return { hostnameMatchesDomain, decodeBingRedirectTarget, normalizeResultUrl }
}

function bingRedirect(host, target) {
  return `https://${host}/ck/a?u=${encodeURIComponent('a1' + Buffer.from(target).toString('base64'))}`
}

test('every execution scope uses exact domain or a dotted subdomain boundary', () => {
  for (const source of functionSources('hostnameMatchesDomain')) {
    const matches = new vm.Script(`(${source})`).runInNewContext()
    for (const domain of ['bing.com', 'google.com', 'linkedin.com']) {
      for (const host of [domain, `www.${domain}`, `research.www.${domain}`, domain.toUpperCase(), `${domain}.`]) assert.equal(matches(host, domain), true, host)
      for (const host of [`evil${domain}`, `${domain}.example`, `evil.${domain}.example`]) assert.equal(matches(host, domain), false, host)
    }
  }
  const { hostnameMatchesDomain } = searchFunctions()
  const readable = productionFunction('isReadableSearchResultUrl', { hostnameMatchesDomain })
  for (const host of ['bing.com', 'www.bing.com', 'google.com', 'www.google.com']) assert.equal(readable(`https://${host}/paper`), false, host)
  for (const host of ['evilbing.com', 'bing.com.example', 'evilgoogle.com', 'google.com.example']) assert.equal(readable(`https://${host}/paper`), true, host)
})

test('LinkedIn feed classification does not exclude unrelated suffix lookalikes', () => {
  const { hostnameMatchesDomain } = searchFunctions()
  const useful = productionFunction('isUsefulFeedDetailLink', { hostnameMatchesDomain })
  for (const host of ['linkedin.com', 'www.linkedin.com']) assert.equal(useful(`https://${host}/company/example`, 'linkedin'), false, host)
  for (const host of ['notlinkedin.com', 'linkedin.com.example', 'papers.example']) assert.equal(useful(`https://${host}/paper`, 'linkedin'), true, host)
})

test('only real Bing hosts decode redirects and relative targets stay same-origin', () => {
  const { normalizeResultUrl, decodeBingRedirectTarget } = searchFunctions()
  const external = 'https://papers.example/result.pdf'
  for (const host of ['bing.com', 'www.bing.com']) assert.equal(normalizeResultUrl(bingRedirect(host, external)), external)
  assert.equal(normalizeResultUrl(bingRedirect('bing.com', '/papers/result.pdf')), 'https://bing.com/papers/result.pdf')
  for (const host of ['evilbing.com', 'bing.com.example']) {
    const original = bingRedirect(host, external)
    assert.equal(normalizeResultUrl(original), original)
  }
  for (const target of ['http://papers.example/result.pdf', external, '/papers/result.pdf']) {
    const encoded = 'a1' + Buffer.from(target).toString('base64')
    assert.equal(decodeBingRedirectTarget(encoded), new URL(target, location.origin).href, target)
  }
  for (const target of ['//outside.example/result.pdf', '/\\outside.example/result.pdf', '///outside.example/result.pdf', 'javascript:alert(1)', 'data:text/plain,unsafe']) {
    assert.equal(decodeBingRedirectTarget('a1' + Buffer.from(target).toString('base64')), '', target)
  }
  assert.equal(decodeBingRedirectTarget(encodeURIComponent(external)), external)
})

test('navigation helper itself allows only HTTP(S), including direct callers', () => {
  const navigation = productionFunction('isNavigationResult')
  for (const value of ['https://papers.example/', 'http://papers.example/']) assert.equal(navigation('Paper', value), false)
  for (const value of ['javascript:alert(1)', 'chrome://settings', 'file:///tmp/file', 'data:text/plain,unsafe', 'ftp://papers.example/']) assert.equal(navigation('Paper', value), true)
})

test('the serialized search extractor executes without outer helper closures', () => {
  const resultLink = {
    href: bingRedirect('www.bing.com', 'https://papers.example/result.pdf'),
    innerText: 'Actual matched paper',
    matches: () => true,
    getAttribute: () => '',
    querySelector: () => null,
  }
  const document = { querySelectorAll: (selector) => selector === '#b_results .b_algo' ? [resultLink] : [] }
  const extract = productionFunction('extractSearchResultsInTab', { document })
  const result = extract({ maxResults: 2 })
  assert.equal(result.engine, 'bing')
  assert.equal(result.resultCount, 1)
  assert.equal(result.results[0].url, 'https://papers.example/result.pdf')
  assert.equal(result.results[0].title, 'Actual matched paper')
})

test('background transfer URL parser accepts only complete HTTP(S) URLs', () => {
  const requireUrl = productionFunction('requireUrl')
  const parse = productionFunction('requireUrlForFetch', { requireUrl })
  for (const value of ['https://papers.example/a.pdf', 'http://papers.example/a.pdf']) assert.equal(parse(value), value)
  for (const value of ['file:///tmp/private', 'data:text/plain,private', 'javascript:alert(1)', 'ftp://papers.example/a', '/relative']) assert.throws(() => parse(value))
})

test('human-read delays and tab completion timers remain bounded and release listeners', async () => {
  const clampNumber = productionFunction('clampNumber')
  const timers = []
  const sleep = productionFunction('sleepBoundedMs', {
    clampNumber,
    setTimeout: (callback, ms) => { timers.push(ms); callback(); return timers.length },
  })
  for (const value of [Number.MAX_VALUE, Infinity, NaN, -1, '8000']) await sleep(value, 10_000)
  assert.deepEqual(timers, [10_000, 0, 0, 0, 8000])
  let removed = 0
  let cleared = 0
  const wait = productionFunction('waitForTabComplete', {
    clampNumber,
    setTimeout: (_callback, ms) => { timers.push(ms); return 1 },
    clearTimeout: () => { cleared += 1 },
    chrome: { tabs: { onUpdated: { addListener: () => {}, removeListener: () => { removed += 1 } }, get: async () => ({ status: 'complete' }) } },
  })
  for (const value of [Number.MAX_VALUE, Infinity, -1]) await wait(1, value)
  assert.deepEqual(timers.slice(-3), [60_000, 12_000, 300])
  assert.equal(removed, 3)
  assert.equal(cleared, 3)
})

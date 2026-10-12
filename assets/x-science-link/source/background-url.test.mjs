import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { test } from 'node:test'
import vm from 'node:vm'

const background = readFileSync(new URL('./background.js', import.meta.url), 'utf8')
const runtimeSource = readFileSync(new URL('./link-runtime.js', import.meta.url), 'utf8')
const runtimePolicy = await import('data:text/javascript;base64,' + Buffer.from(runtimeSource).toString('base64'))

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
        const declarationStart = background.slice(start - 6, start) === 'async ' ? start - 6 : start
        sources.push(background.slice(declarationStart, index + 1))
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
  for (const maximum of [Infinity, NaN, -1, 120_000]) assert.throws(() => sleep(100_000, maximum))
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

test('Chrome sender authority is origin-bound and cannot be supplied by page payloads', () => {
  const { assertRuntimeMessageSender: guard, isTrustedAppPage: trusted } = runtimePolicy
  const id = 'owned-connector'
  const page = { id, url: 'http://localhost:8765/#/guid', frameId: 0 }
  const server = runtimePolicy.DEFAULT_X_SCIENCE_SERVER_URL
  assert.equal(server, 'http://127.0.0.1:8765')
  for (const type of ['connect', 'status', 'disconnect', 'local_files_status', 'show_downloaded_file']) {
    assert.doesNotThrow(() => guard({ type, serverUrl: server }, page, id, server))
  }
  for (const sender of [
    undefined, { ...page, id: 'other-extension' }, { ...page, url: undefined },
    { ...page, url: 'about:blank' }, { ...page, frameId: 1 },
    { ...page, origin: 'null' }, { ...page, origin: 'http://localhost:18885' },
    { ...page, url: 'http://localhost:18885' }, { ...page, url: 'https://localhost:8765' },
    { ...page, url: 'http://localhost.example:8765' },
    { ...page, url: 'http://localhost:8765@other.example' },
  ]) assert.throws(() => guard({ type: 'connect', sender: page, serverUrl: server }, sender, id, server))
  for (const type of ['login', 'approval_decision', 'extract_active_page']) {
    assert.throws(() => guard({ type }, page, id, server))
  }
  assert.throws(() => guard({ type: 'connect', serverUrl: 'http://127.0.0.1:18885' }, page, id, server))
  assert.doesNotThrow(() => guard({ type: 'login', serverUrl: 'https://explicit.example' },
    { id, url: `chrome-extension://${id}/options.html` }, id, server))
  assert.throws(() => guard({ type: 'login' }, { id, url: 'chrome-extension://other/options.html' }, id, server))
  assert.equal(trusted('https://explicit.example/task', 'https://explicit.example/'), true)
  assert.equal(trusted('http://localhost:8765/', 'https://explicit.example'), false)
  assert.equal(trusted('https://explicit.example/', ''), false)
})

test('actual message listener rejects untrusted connects before settings or WebSocket effects', async () => {
  const start = background.indexOf('chrome.runtime.onMessage.addListener(')
  const end = background.indexOf('\nasync function saveConnectionSettings(', start)
  assert.ok(start >= 0 && end > start)
  let listener
  const stored = {}
  const writes = []
  let connections = 0
  const chrome = {
    runtime: { id: 'owned-connector', onMessage: { addListener: value => { listener = value } } },
    storage: { local: {
      get: async () => ({ ...stored }),
      set: async values => { writes.push(values); Object.assign(stored, values) },
    } },
  }
  const context = {
    chrome, DEFAULT_SERVER_URL: runtimePolicy.DEFAULT_X_SCIENCE_SERVER_URL,
    assertRuntimeMessageSender: runtimePolicy.assertRuntimeMessageSender,
    normalizeServerUrl: productionFunction('normalizeServerUrl'),
    connectFromStorage: async () => { connections += 1 }, status: {},
  }
  context.saveConnectionSettings = productionFunction('saveConnectionSettings', context)
  new vm.Script(background.slice(start, end)).runInNewContext(context)
  const request = (message, sender) => new Promise(resolve => {
    assert.equal(listener(message, sender, resolve), true)
  })
  const page = { id: chrome.runtime.id, url: 'http://localhost:8765/#/guid', frameId: 0 }
  const message = { type: 'connect', serverUrl: 'http://127.0.0.1:8765', appSession: 'fixture-session' }
  for (const sender of [{ ...page, url: 'http://localhost:18885' }, { ...page, frameId: 1 }]) {
    assert.equal((await request(message, sender)).ok, false)
  }
  assert.equal((await request({ ...message, serverUrl: 'https://outside.example' }, page)).ok, false)
  assert.equal((await request({ type: 'approval_decision', approved: true }, page)).ok, false)
  assert.equal(writes.length, 0)
  assert.equal(connections, 0)
  assert.equal((await request(message, page)).ok, true)
  assert.equal(writes.length, 1)
  assert.equal(connections, 1)
  const native = { id: chrome.runtime.id, url: 'chrome-extension://owned-connector/options.html' }
  assert.equal((await request({ ...message, serverUrl: 'https://explicit.example' }, native)).ok, true)
  assert.equal((await request(message, page)).ok, false)
  assert.equal(connections, 2)
})

test('status broadcasts follow the same configured-origin authority as commands', async () => {
  const shouldNotifyXScienceTab = productionFunction('shouldNotifyXScienceTab', {
    isTrustedAppPage: runtimePolicy.isTrustedAppPage,
    DEFAULT_SERVER_URL: runtimePolicy.DEFAULT_X_SCIENCE_SERVER_URL,
  })
  const sent = []
  const broadcast = productionFunction('broadcastStatusToXScienceTabs', {
    shouldNotifyXScienceTab,
    chrome: { tabs: {
      query: async () => [
        { id: 1, url: 'http://localhost:8765/#/guid' },
        { id: 2, url: 'http://127.0.0.1:8765/#/settings' },
        { id: 3, url: 'http://localhost:18885' },
        { id: 4, url: 'https://explicit.example/task' },
      ],
      sendMessage: async (id, message) => { sent.push({ id, message }) },
    } },
  })
  const status = { serverUrl: runtimePolicy.DEFAULT_X_SCIENCE_SERVER_URL, localFiles: { count: 2 } }
  await broadcast(status)
  assert.deepEqual(sent.map(item => item.id), [1, 2])
  assert.ok(sent.every(item => item.message.status === status))
  sent.length = 0
  await broadcast({ ...status, serverUrl: 'https://explicit.example' })
  assert.deepEqual(sent.map(item => item.id), [4])
})
